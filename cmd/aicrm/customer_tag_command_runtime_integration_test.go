package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/http"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// TestCustomerTagCommandCompositionHTTPPostgreSQL mounts the exact cmd/aicrm
// compatibility route and drives its actual Customer app, store, EER receipt,
// River insert, and durable history read through HTTP. Provider is disabled:
// local acceptance is intentionally not presented as a completed WeCom write.
func TestCustomerTagCommandCompositionHTTPPostgreSQL(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, clean := customerTagRuntimePool(t, ctx, url)
	defer clean()
	native := pool.Native()
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,session_version) VALUES('runtime-tag-admin','$argon2id$test','Runtime tag admin',true,1); INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active')`); err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	insert, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, insert)
	if err != nil {
		t.Fatal(err)
	}
	commands, err := customerapp.NewTagCommandService(uow, customerstore.TagCommandPostgreSQL{}, effects, runtimeTagGate{}, platformaudit.NewPostgreSQLStore(), platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	security := runtimeTagSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	handler, err := customerhttp.NewHandler(customerhttp.Config{
		UnitOfWork: uow, Auth: security, CSRF: security,
		Directory: customerapp.Directory{Store: runtimeTagDirectory{}, SigningKey: []byte("0123456789abcdef0123456789abcdef")}, Store: runtimeTagDirectory{},
		Identities: runtimeTagIdentities{}, Audit: runtimeTagAudit{}, Canonical: runtimeTagCanonical{}, Owners: runtimeTagOwners{}, Tags: runtimeTagTags{}, Surveys: runtimeTagSurveys{}, Timeline: runtimeTagTimeline{}, Chat: runtimeTagChat{},
		TagCommands: commands, TagHistory: customerstore.TagCommandPostgreSQL{}, ProfileSigningKey: []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	// This is the production composition ordering: the explicit command route
	// and mountSurveyAPIs compatibility subtree share the exact TagCommand host.
	mux.Handle("/api/v1/customer-tag-commands", handler.TagCommandRoutes())
	mux.Handle("/api/v1/customer-tag-commands/", handler.TagCommandRoutes())
	mountSurveyAPIs(mux, http.NotFoundHandler(), handler.TagCommandRoutes())
	server := httptest.NewServer(mux)
	defer server.Close()

	body := []byte(`{"customer_ids":[1],"add_tag_ids":[7],"idempotency_key":"runtime-tag-command-key"}`)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/v1/customer-tag-commands/preview", bytesReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", "runtime-csrf")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("preview status=%d", response.StatusCode)
	}
	var preview customerport.TagCommandResult
	if err = json.NewDecoder(response.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(preview.Lines) != 1 || preview.Lines[0].State != "eligible" || preview.ID != 0 {
		t.Fatalf("preview=%+v", preview)
	}

	request, err = http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/v1/customer-tag-commands", bytesReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", "runtime-csrf")
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("accept status=%d", response.StatusCode)
	}
	var accepted customerport.TagCommandResult
	if err = json.NewDecoder(response.Body).Decode(&accepted); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if accepted.ID < 1 || accepted.State != "queued" || len(accepted.Lines) != 1 || accepted.Lines[0].EffectRef == "" {
		t.Fatalf("accepted=%+v", accepted)
	}

	request, err = http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/customers/1/tag-commands", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("history status=%d", response.StatusCode)
	}
	var history struct {
		Items []customerport.TagCommandResult `json:"items"`
	}
	if err = json.NewDecoder(response.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(history.Items) != 1 || history.Items[0].ID != accepted.ID || len(history.Items[0].Lines) != 1 || history.Items[0].Lines[0].EffectRef != accepted.Lines[0].EffectRef {
		t.Fatalf("history=%+v accepted=%+v", history, accepted)
	}
	// The frozen WebShell template and Host script make the real preview and
	// confirmation requests against this live mux; only the unrelated directory
	// list fixture is local to the browser harness.
	browser := exec.Command("node", "customer_tag_command_runtime_e2e.mjs", server.URL)
	browser.Dir = "."
	browser.Env = os.Environ()
	if output, browserErr := browser.CombinedOutput(); browserErr != nil {
		t.Fatalf("frozen Host journey: %v output=%s", browserErr, output)
	}
	for table, want := range map[string]int{"customer_tag_commands": 2, "customer_tag_command_lines": 2, "external_effects": 2, "external_effect_operation_receipts": 4, "river_job": 2, "audit_events": 2, "outbox_events": 2} {
		var got int
		if err = native.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s=%d want=%d err=%v", table, got, want, err)
		}
	}
}

func bytesReader(v []byte) *bytes.Reader { return bytes.NewReader(v) }

type runtimeTagSecurity struct{ principal accessdomain.Principal }

func (s runtimeTagSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, nil
}
func (s runtimeTagSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, nil
}

type runtimeTagDirectory struct{}

func (runtimeTagDirectory) List(context.Context, customerapp.Query) (customerapp.PageData, error) {
	return customerapp.PageData{}, nil
}
func (runtimeTagDirectory) Detail(context.Context, customerdomain.CustomerID) (customerapp.Detail, error) {
	return customerapp.Detail{}, nil
}

type runtimeTagIdentities struct{}

func (runtimeTagIdentities) VerifiedWeComCustomer(context.Context, string, string) (customerdomain.CustomerID, bool, error) {
	return 0, false, nil
}
func (runtimeTagIdentities) CustomerForPhone(context.Context, string) (customerdomain.CustomerID, bool, error) {
	return 0, false, nil
}
func (runtimeTagIdentities) DirectoryIdentities(context.Context, customerdomain.CustomerID) ([]identityport.DirectoryIdentitySummary, []identityport.MaskedPhone, error) {
	return nil, nil, nil
}
func (runtimeTagIdentities) RevealPhone(context.Context, customerdomain.CustomerID) (string, bool, error) {
	return "", false, nil
}

type runtimeTagAudit struct{}

func (runtimeTagAudit) Append(_ context.Context, v platformaudit.Event) (platformaudit.Event, error) {
	return v, nil
}

type runtimeTagCanonical struct{}

func (runtimeTagCanonical) ResolveCanonicalCustomer(_ context.Context, id customerdomain.CustomerID) (customerport.CanonicalCustomer, error) {
	return customerport.CanonicalCustomer{RequestedCustomerID: id, CustomerID: id}, nil
}

type runtimeTagOwners struct{}

func (runtimeTagOwners) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (runtimeTagOwners) CustomerOwners(context.Context, customerdomain.CustomerID) (customerport.OwnerPage, error) {
	return customerport.OwnerPage{}, nil
}

type runtimeTagTags struct{}

func (runtimeTagTags) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (runtimeTagTags) CustomerTags(context.Context, customerdomain.CustomerID) (customerport.TagPage, error) {
	return customerport.TagPage{}, nil
}

type runtimeTagSurveys struct{}

func (runtimeTagSurveys) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (runtimeTagSurveys) CustomerSurveys(context.Context, customerdomain.CustomerID, customerport.PageQuery) (customerport.SurveyPage, error) {
	return customerport.SurveyPage{}, nil
}

type runtimeTagTimeline struct{}

func (runtimeTagTimeline) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (runtimeTagTimeline) CustomerTimeline(context.Context, customerdomain.CustomerID, customerport.PageQuery) (customerport.TimelinePage, error) {
	return customerport.TimelinePage{}, nil
}

type runtimeTagChat struct{}

func (runtimeTagChat) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionNotReady}
}
func (runtimeTagChat) CustomerChatActivity(context.Context, customerdomain.CustomerID, customerport.PageQuery) (customerport.ChatActivityPage, error) {
	return customerport.ChatActivityPage{}, customerport.ErrCapabilityNotReady
}

type runtimeTagGate struct{}

func (runtimeTagGate) FreezeTagCommandTarget(_ context.Context, target customerport.TagCommandTarget) (customerport.FrozenTagCommandTarget, error) {
	target.StaffID = 1
	return customerport.FrozenTagCommandTarget{TagCommandTarget: target, BindingDigest: string(effectport.Hash("runtime-tag-binding")), TargetDigest: string(effectport.Hash("customer.tag.command.target.v1", "runtime-staff", "runtime-external"))}, nil
}

func customerTagRuntimePool(t *testing.T, ctx context.Context, url string) (*platformpostgres.Pool, func()) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	schema := "aicrm_tag_runtime_" + hex.EncodeToString(raw)
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		t.Fatal(err)
	}
	testCfg := cfg.Copy()
	testCfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, testCfg)
	if err != nil {
		t.Fatal(err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(native), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join("..", ".."))
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0004_wecom.sql", "0005_external_effects.sql", "0008_tag_catalog.sql", "0009_customer_activation.sql", "0093_customer_tag_commands.sql"} {
		body, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(body)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		native.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+ident+" CASCADE")
		admin.Close()
	}
}
