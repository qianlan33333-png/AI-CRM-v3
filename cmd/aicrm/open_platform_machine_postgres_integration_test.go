package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	openplatformhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/http"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// TestOpenPlatformMachineManagementPostgreSQLJourney exercises the exact
// composition boundary against an isolated, randomly named database. It proves
// the list read model can load grants after multi-row scans on one pgx Tx, and
// that lifecycle state and its audit are atomic in the owner store.
func TestOpenPlatformMachineManagementPostgreSQLJourney(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := openPlatformMachineTestDatabase(t, ctx)
	defer cleanup()

	native, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	if err = openPlatformMachineMigrate(ctx, native); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repository := accessstore.NewPostgreSQL()
	passwords := credential.PasswordHasher{}
	service, err := accessapp.NewMachineService(repository, unit, passwords, accessapp.MachineConfig{
		SigningKey: []byte("01234567890123456789012345678901"), CorpID: "open-platform-pg", Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	passwordHash, err := passwords.Hash("open-platform-postgres-admin")
	if err != nil {
		t.Fatal(err)
	}
	var adminUser accessdomain.User
	err = unit.Within(ctx, func(txContext context.Context) error {
		var createErr error
		adminUser, createErr = repository.CreateUser(txContext, accessdomain.User{Username: "open-platform-admin", PasswordHash: passwordHash, DisplayName: "Open Platform Admin", Active: true, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}})
		return createErr
	})
	if err != nil {
		t.Fatal(err)
	}
	admin := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: adminUser.ID, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}

	external, err := service.Create(ctx, admin, accessapp.CreateMachineClientInput{
		ClientID: "postgres.external", DisplayName: "PostgreSQL external", Purpose: "external_agent",
		Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"}, Capabilities: []string{"external_read", "external_write"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mcp, err := service.Create(ctx, admin, accessapp.CreateMachineClientInput{
		ClientID: "postgres.mcp", DisplayName: "PostgreSQL MCP", Purpose: "mcp",
		Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"}, Capabilities: []string{"mcp_read", "mcp_execute"},
	})
	if err != nil {
		t.Fatal(err)
	}

	clients, err := service.List(ctx, admin)
	if err != nil || len(clients) != 2 {
		t.Fatalf("machine list clients=%+v err=%v", clients, err)
	}
	assertMachineCapabilities(t, clients, external.Client.ClientID, []string{"external_read", "external_write"})
	assertMachineCapabilities(t, clients, mcp.Client.ClientID, []string{"mcp_execute", "mcp_read"})

	handler, err := openplatformhttp.NewHandler(openplatformhttp.Config{
		MachineAuthentication: service, AdminAuthentication: openPlatformMachineAdmin{}, Management: service,
		Executor: openPlatformMachineExecutor{}, SessionCookieName: "session", CSRFCookieName: "csrf", PublicOrigin: "https://crm.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://crm.example.test/api/admin/config/api-clients", nil)
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("legacy management list status=%d body=%s", response.Code, response.Body.String())
	}
	var page struct {
		OK         bool `json:"ok"`
		APIClients struct {
			Rows []struct {
				ClientID     string   `json:"client_id"`
				Capabilities []string `json:"capabilities"`
			} `json:"rows"`
		} `json:"api_clients"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &page); err != nil || !page.OK || len(page.APIClients.Rows) != 2 {
		t.Fatalf("legacy management page=%s err=%v", response.Body.String(), err)
	}

	if _, err = service.Activate(ctx, admin, external.Client.ClientID, external.Secret, true); err != nil {
		t.Fatal(err)
	}
	issued, err := service.IssueClientCredentialsToken(ctx, accessapp.ClientCredentialsInput{
		ClientID: external.Client.ClientID, ClientSecret: external.Secret, Audience: "external_integration", RequestedScopes: []string{"read"}, SourceIP: mustOpenPlatformAddr(t, "203.0.113.5"),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Both operations lock the same client. Whichever wins, rotation leaves a
	// regular client disabled and every old bearer must fail immediately.
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		<-start
		_, rotateErr := service.Rotate(ctx, admin, external.Client.ClientID)
		results <- rotateErr
	}()
	go func() {
		defer workers.Done()
		<-start
		_, disableErr := service.SetEnabled(ctx, admin, external.Client.ClientID, false)
		results <- disableErr
	}()
	close(start)
	workers.Wait()
	close(results)
	for result := range results {
		if result != nil {
			t.Fatalf("concurrent rotate/disable=%v", result)
		}
	}
	if _, err = service.AuthenticateBearer(ctx, issued.AccessToken, "external_integration", mustOpenPlatformAddr(t, "203.0.113.5")); err == nil {
		t.Fatal("old bearer remained valid after concurrent lifecycle changes")
	}

	historical := accessapp.HistoricalMachineImportInput{ImportRunID: "open-platform-history-pg", SourceRowID: "legacy-identity-1", SourceRowDigest: [32]byte{9, 6}, ClientID: "historic.identity", DisplayName: "Historic identity", Purpose: "identity", TokenTTLSeconds: 1800}
	imported, err := service.ImportHistorical(ctx, historical)
	if err != nil || imported.Replayed || imported.Client.Enabled || !imported.Client.ReissueRequired || imported.Client.Purpose != "identity" {
		t.Fatalf("historical import=%+v err=%v", imported, err)
	}
	verified, err := service.VerifyHistorical(ctx, historical)
	if err != nil || verified.Outcome != "reissue_required" || verified.Client.Enabled || !verified.Client.ReissueRequired {
		t.Fatalf("historical verification=%+v err=%v", verified, err)
	}
	replayed, err := service.ImportHistorical(ctx, historical)
	if err != nil || !replayed.Replayed || replayed.Outcome != "replayed" {
		t.Fatalf("historical replay=%+v err=%v", replayed, err)
	}
	var historicalAudits int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM access_machine_audit WHERE action='machine_client_imported'`).Scan(&historicalAudits); err != nil || historicalAudits != 1 {
		t.Fatalf("historical audit count=%d err=%v", historicalAudits, err)
	}
	clients, err = service.List(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range clients {
		if client.ClientID == external.Client.ClientID && client.Enabled {
			t.Fatal("rotated external client remained enabled")
		}
	}

	rollback := errors.New("machine audit rollback")
	err = unit.Within(ctx, func(txContext context.Context) error {
		client, lookupErr := repository.MachineClientByID(txContext, external.Client.ClientID, true)
		if lookupErr != nil {
			return lookupErr
		}
		if auditErr := repository.AppendMachineAudit(txContext, accessdomain.MachineAudit{MachineClientID: client.ID, Action: "machine_audit_rollback", Outcome: "failed", Details: []byte(`{}`), CreatedAt: time.Now().UTC()}); auditErr != nil {
			return auditErr
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("audit rollback error=%v", err)
	}
	var rolledBack int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM access_machine_audit WHERE action='machine_audit_rollback'`).Scan(&rolledBack); err != nil || rolledBack != 0 {
		t.Fatalf("rolled-back audit count=%d err=%v", rolledBack, err)
	}
}

type openPlatformMachineAdmin struct{}

func (openPlatformMachineAdmin) Authenticate(context.Context, string) (accessdomain.Principal, error) {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}, nil
}
func (openPlatformMachineAdmin) AuthorizeCSRF(context.Context, string, string, string) (accessdomain.Principal, error) {
	return openPlatformMachineAdmin{}.Authenticate(context.Background(), "")
}

type openPlatformMachineExecutor struct{}

func (openPlatformMachineExecutor) Execute(context.Context, openplatformport.Request) (openplatformport.Response, error) {
	return openplatformport.Response{Status: http.StatusOK, Body: map[string]any{"ok": true}}, nil
}

func assertMachineCapabilities(t *testing.T, clients []accessapp.MachineClientSummary, clientID string, want []string) {
	t.Helper()
	for _, client := range clients {
		if client.ClientID == clientID {
			if len(client.Capabilities) != len(want) {
				t.Fatalf("client=%s capabilities=%v want=%v", clientID, client.Capabilities, want)
			}
			for index := range want {
				if client.Capabilities[index] != want[index] {
					t.Fatalf("client=%s capabilities=%v want=%v", clientID, client.Capabilities, want)
				}
			}
			return
		}
	}
	t.Fatalf("missing listed client %s", clientID)
}

func mustOpenPlatformAddr(t *testing.T, value string) netip.Addr {
	t.Helper()
	address, err := netip.ParseAddr(value)
	if err != nil {
		t.Fatal(err)
	}
	return address
}

func openPlatformMachineTestDatabase(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping Open Platform PostgreSQL journey")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		t.Fatal("parse AICRM_DATABASE_URL")
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	database := "aicrm_open_platform_" + hex.EncodeToString(random[:])
	adminURL := *parsed
	adminURL.Path = "/postgres"
	adminURL.RawPath = ""
	admin, err := pgx.Connect(ctx, adminURL.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{database}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	testURL := *parsed
	testURL.Path = "/" + database
	testURL.RawPath = ""
	return testURL.String(), func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{database}.Sanitize()+" WITH (FORCE)")
		admin.Close(cleanup)
	}
}

func openPlatformMachineMigrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return os.ErrNotExist
	}
	root := filepath.Join(filepath.Dir(source), "..", "..")
	for _, name := range []string{"0003_access.sql", "0096_open_platform.sql"} {
		sql, err := os.ReadFile(filepath.Join(root, "migrations", name))
		if err != nil {
			return err
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			return err
		}
	}
	return nil
}
