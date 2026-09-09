package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	config "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Real rendered Host, session/CSRF, database, edits and atomic approval. The
// preparation provider is a deterministic fixture; Python XLSX parsing and
// observation calculation have separate restart/format/window tests.
func TestPostgreSQLExcelBatchesChromiumJourney(t *testing.T) {
	if !config.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	runExcelCompositionJourney(t, true)
}
func TestPostgreSQLExcelHTTPCompositionJourney(t *testing.T) { runExcelCompositionJourney(t, false) }
func runExcelCompositionJourney(t *testing.T, browser bool) {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(source), "..", "..")
	t.Chdir(root)
	prepareProductExternalPushChromiumArtifacts(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	token := strings.Repeat("x", 32)
	now := time.Now().UTC()
	cover, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
	coverHash := effect.Hash("fixture-cover")
	card := map[string]any{"appid": "fixture-app", "path": "pages/article/article?lesson_id=1", "title": "标准案例", "cover_digest": ""}
	component := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		var value any = map[string]any{"ok": true}
		switch {
		case r.URL.Path == "/imports":
			value = map[string]any{"batch_key": "browser-fixture", "file_digest": effect.Hash("file"), "created_at": now, "rows": []any{map[string]any{"unionid": "browser-user-one", "text": "第一条待审核话术", "sender_userid": "staff-one", "card": card}, map[string]any{"unionid": "browser-user-two", "text": "第二条待审核话术", "sender_userid": "staff-two", "card": card}}}
		case r.URL.Path == "/covers":
			value = map[string]any{"cover_digest": coverHash}
		case strings.HasPrefix(r.URL.Path, "/covers/"):
			w.Header().Set("Content-Type", "image/png")
			w.Write(cover)
			return
		case strings.HasPrefix(r.URL.Path, "/reports/"):
			value = map[string]any{"pending": true}
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
	defer component.Close()
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	defer server.Close()
	origin := "https://" + server.Listener.Addr().String()
	key := base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	application, err := compose(ctx, config.Runtime{Role: config.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: origin, ReleaseSHA: "excel-browser", WorkerOwner: "excel-browser", WorkerLimit: 1,
		Effects: config.Effects{ProviderEnabled: true}, WeCom: config.WeCom{Enabled: true, CorpID: "fixture-corp", AgentID: "fixture-agent", Secret: "fixture-secret", ContactSecret: "fixture-contact", ContextSigningKey: strings.Repeat("z", 32), APIBase: component.URL, HTTPClient: component.Client()},
		GroupOps: config.GroupOps{WebhookSecret: "excel-fixture-webhook"}, Survey: config.Survey{DataKey: key, IdentityPhoneDataKey: key, OAuthOpenPlatformID: "fixture"},
		AIAssistant: config.AIAssistant{UIEnabled: true, DispatchEnabled: true, ProviderPermission: "private-message-authorized", ExcelBatchURL: component.URL, ExcelBatchToken: token},
		Bootstrap:   config.Bootstrap{Enabled: true, Username: "excel-browser", Password: "excel-browser-password", DisplayName: "Excel Browser"}})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, config.Bootstrap{Enabled: true, Username: "excel-browser", Password: "excel-browser-password", DisplayName: "Excel Browser"}); err != nil {
		t.Fatal(err)
	}
	session, csrf := adminAccessLogin(t, application.handler, "excel-browser", "excel-browser-password")
	request := httptest.NewRequest("POST", "/api/admin/operation-batches/imports", bytes.NewReader([]byte("fixture")))
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	request.Header.Set("X-CSRF-Token", csrf)
	request.Header.Set("Idempotency-Key", "http-composition-import")
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("full HTTP import: %d %s", response.Code, response.Body.String())
	}
	var imported struct {
		Plan struct {
			ID      int64 `json:"id"`
			Version int64 `json:"version"`
		} `json:"plan"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &imported); err != nil || imported.Plan.ID < 1 {
		t.Fatal("missing native plan", err)
	}
	detail := authenticatedAdminGet(t, application.handler, session, fmt.Sprintf("/api/admin/operation-batches/%d", imported.Plan.ID))
	if detail.Code != 200 {
		t.Fatalf("full HTTP detail: %d %s", detail.Code, detail.Body.String())
	}
	if !browser {
		for _, attempt := range []struct {
			body []byte
			want int
		}{{[]byte("not an image"), 400}, {cover, 200}, {cover, 200}} {
			req := httptest.NewRequest("POST", fmt.Sprintf("/api/admin/operation-batches/%d/cover?expected_version=%d", imported.Plan.ID, imported.Plan.Version), bytes.NewReader(attempt.body))
			req.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
			req.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
			req.Header.Set("X-CSRF-Token", csrf)
			req.Header.Set("Idempotency-Key", "http-cover-upload")
			res := httptest.NewRecorder()
			application.handler.ServeHTTP(res, req)
			if res.Code != attempt.want {
				t.Fatalf("full HTTP cover: %d %s", res.Code, res.Body.String())
			}
		}
		return
	}
	server.Config.Handler = application.handler
	server.StartTLS()
	cmd := exec.CommandContext(ctx, "node", filepath.Join(root, "cmd/aicrm/excel_batches_chromium_journey.mjs"))
	cmd.Env = append(os.Environ(), "AICRM_EXCEL_TEST_URL="+server.URL, "AICRM_EXCEL_TEST_USERNAME=excel-browser", "AICRM_EXCEL_TEST_PASSWORD=excel-browser-password")
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "excel_batches_chromium: PASS") {
		t.Fatalf("browser: %v %s", err, output)
	}
	var queued, excluded int
	err = application.pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM outbound_private_message_intents),(SELECT count(*) FROM ai_assistant_plan_recipients WHERE review_state='rejected')`).Scan(&queued, &excluded)
	if err != nil || queued != 1 || excluded != 1 {
		t.Fatalf("queued=%d excluded=%d err=%v", queued, excluded, err)
	}
}
