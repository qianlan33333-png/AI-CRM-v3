package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLAdminAccessCompatibilityJourney uses the production
// composition root and a real PostgreSQL schema. It intentionally does not
// substitute an auth, CSRF, management, or repository mock: the frozen
// AdminOps request must exercise the same session and transaction boundary as
// the deployed application.
func TestPostgreSQLAdminAccessCompatibilityJourney(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal(err)
	}
	application, err := compose(ctx, platformconfig.Runtime{
		Role:         platformconfig.RoleAPI,
		DatabaseURL:  databaseURL,
		PublicOrigin: "https://id-dev.example.test",
		ReleaseSHA:   "admin-access-integration",
		WorkerOwner:  "admin-access-integration",
		WorkerLimit:  1,
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "admin-access-integration-webhook-secret"},
		Survey:       platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(dataKey)},
		Bootstrap: platformconfig.Bootstrap{
			Enabled: true, Username: "owner", Password: "admin-access-owner-password", DisplayName: "Owner",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{
		Enabled: true, Username: "owner", Password: "admin-access-owner-password", DisplayName: "Owner",
	}); err != nil {
		t.Fatal(err)
	}
	owner := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}
	target, err := application.management.AddUser(ctx, owner, accessapp.AddUserInput{
		Username: "operator", Password: "admin-access-operator-password", DisplayName: "Operator", Roles: []accessdomain.Role{accessdomain.RoleAdmin},
	})
	if err != nil {
		t.Fatal(err)
	}
	targetSession, _ := adminAccessLogin(t, application.handler, "operator", "admin-access-operator-password")

	session, csrf := adminAccessLogin(t, application.handler, "owner", "admin-access-owner-password")
	before := adminAccessRequest(t, application.handler, http.MethodGet, nil, session, "")
	if before.Code != http.StatusOK || !strings.Contains(before.Body.String(), `"admin_user_id":`+itoa(target.ID)) {
		t.Fatalf("initial read=%d body=%s", before.Code, before.Body.String())
	}

	payload := `{"members":[{"admin_user_id":` + itoa(target.ID) + `,"login_enabled":false}]}`
	saved := adminAccessMutation(t, application.handler, payload, session, csrf, "admin-access-journey-save")
	if saved.Code != http.StatusOK || !strings.Contains(saved.Body.String(), `"login_enabled":false`) {
		t.Fatalf("save=%d body=%s", saved.Code, saved.Body.String())
	}
	refreshed := adminAccessRequest(t, application.handler, http.MethodGet, nil, targetSession, "")
	if refreshed.Code != http.StatusUnauthorized || !strings.Contains(refreshed.Body.String(), "authentication_required") {
		t.Fatalf("disabled target session must be fenced: status=%d body=%s", refreshed.Code, refreshed.Body.String())
	}

	var active bool
	var audits, receipts int
	if err = application.pool.Native().QueryRow(ctx, `SELECT is_active FROM admin_users WHERE id=$1`, target.ID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM admin_access_audit WHERE actor_admin_user_id=1 AND target_admin_user_id=$1 AND action='set_login_enabled'`, target.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM admin_access_login_compat_receipts WHERE actor_admin_user_id=1 AND idempotency_key='admin-access-journey-save'`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if active || audits != 1 || receipts != 1 {
		t.Fatalf("commit state active=%v audits=%d receipts=%d", active, audits, receipts)
	}

	// The owner remains active; an exact replay must not create another
	// audit/session mutation after the target's existing session was fenced.
	replay := adminAccessMutation(t, application.handler, payload, session, csrf, "admin-access-journey-save")
	if replay.Code != http.StatusOK {
		t.Fatalf("exact replay=%d body=%s", replay.Code, replay.Body.String())
	}
	drift := adminAccessMutation(t, application.handler, `{"members":[{"admin_user_id":`+itoa(target.ID)+`,"login_enabled":true}]}`, session, csrf, "admin-access-journey-save")
	if drift.Code != http.StatusConflict || !strings.Contains(drift.Body.String(), "idempotency_conflict") {
		t.Fatalf("payload drift=%d body=%s", drift.Code, drift.Body.String())
	}
	if err = application.pool.Native().QueryRow(ctx, `SELECT is_active FROM admin_users WHERE id=$1`, target.ID).Scan(&active); err != nil || active {
		t.Fatalf("drift changed active=%v err=%v", active, err)
	}
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM admin_access_audit WHERE target_admin_user_id=$1 AND action='set_login_enabled'`, target.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("replay/drift audits=%d err=%v", audits, err)
	}

	rollback := adminAccessMutation(t, application.handler, `{"members":[{"admin_user_id":`+itoa(target.ID)+`,"login_enabled":true},{"admin_user_id":999999,"login_enabled":false}]}`, session, csrf, "admin-access-journey-rollback")
	if rollback.Code != http.StatusBadRequest {
		t.Fatalf("rollback request=%d body=%s", rollback.Code, rollback.Body.String())
	}
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM admin_access_login_compat_receipts WHERE actor_admin_user_id=1 AND idempotency_key='admin-access-journey-rollback'`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("failed request left receipt count=%d err=%v", receipts, err)
	}
	if err = application.pool.Native().QueryRow(ctx, `SELECT is_active FROM admin_users WHERE id=$1`, target.ID).Scan(&active); err != nil || active {
		t.Fatalf("failed request changed target active=%v err=%v", active, err)
	}
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM admin_access_audit WHERE target_admin_user_id=$1 AND action='set_login_enabled'`, target.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("failed request changed audit count=%d err=%v", audits, err)
	}
}

func adminAccessLogin(t *testing.T, handler http.Handler, username, password string) (session, csrf string) {
	t.Helper()
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/login", nil))
	loginCookie := adminAccessCookie(t, page.Result().Cookies(), accesshttp.LoginCSRFCookieName)
	match := regexp.MustCompile(`name="login_csrf_token" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if len(match) != 2 {
		t.Fatalf("login page missing csrf token: %s", page.Body.String())
	}
	form := url.Values{"username": {username}, "password": {password}, "login_csrf_token": {match[1]}}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(loginCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("login=%d body=%s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	return adminAccessCookie(t, cookies, accesshttp.SessionCookieName).Value, adminAccessCookie(t, cookies, accesshttp.CSRFCookieName).Value
}

func adminAccessMutation(t *testing.T, handler http.Handler, body, session, csrf, key string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPut, "/api/admin/admin-access", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	request.Header.Set("X-CSRF-Token", csrf)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func adminAccessRequest(t *testing.T, handler http.Handler, method string, body *strings.Reader, session, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(method, "/api/admin/admin-access", nil)
	} else {
		request = httptest.NewRequest(method, "/api/admin/admin-access", body)
	}
	if session != "" {
		request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
		request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func adminAccessCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("missing %s cookie", name)
	return nil
}

func adminAccessCompositionDatabase(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping admin-access composition PostgreSQL Journey")
	}
	adminConfig, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "admin_access_composition_" + hex.EncodeToString(random[:])
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config := adminConfig.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if err = adminAccessMigrateCompositionSchema(ctx, native); err != nil {
		native.Close()
		admin.Close()
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		native.Close()
		admin.Close()
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String(), func() {
		native.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}

func adminAccessMigrateCompositionSchema(ctx context.Context, pool *pgxpool.Pool) error {
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return err
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return err
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return os.ErrNotExist
	}
	base := filepath.Join(filepath.Dir(source), "..", "..", "migrations")
	entries, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		sql, readErr := os.ReadFile(filepath.Join(base, name))
		if readErr != nil {
			return readErr
		}
		if _, execErr := pool.Exec(ctx, string(sql)); execErr != nil {
			return execErr
		}
	}
	return nil
}
