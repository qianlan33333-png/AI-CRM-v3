package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
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
	tagapp "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/app"
	tagdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	taghttp "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/http"
	tagstore "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
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
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,session_version) VALUES('runtime-tag-admin','$argon2id$test','Runtime tag admin',true,1); INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active'),(2,'active'); INSERT INTO tag_groups(group_name,sort_order) VALUES('运行时分组',0); INSERT INTO tag_catalog_tags(group_id,tag_name,sort_order) VALUES(1,'运行时标签',0); INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,completed_at) VALUES('runtime-observation','manual','succeeded','wecom-corp:runtime',clock_timestamp()); INSERT INTO wecom_customer_tag_observations(customer_id,corp_scope,employee_id,provider_tag_id,provider_tag_type,observed_name,last_seen_run_id,observed_at) VALUES(1,'wecom-corp:runtime','runtime-staff','observed-runtime-tag',2,'已观察标签',1,clock_timestamp())`); err != nil {
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
	catalogStore, err := tagstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	profileObservations := wecom.NewPostgreSQLCustomerSyncStore()
	handler, err := customerhttp.NewHandler(customerhttp.Config{
		UnitOfWork: uow, Auth: security, CSRF: security,
		Directory: customerapp.Directory{Store: runtimeTagDirectory{}, SigningKey: []byte("0123456789abcdef0123456789abcdef")}, Store: runtimeTagDirectory{},
		Identities: runtimeTagIdentities{}, Audit: runtimeTagAudit{}, Canonical: runtimeTagCanonical{}, Owners: runtimeTagOwners{}, Tags: customerTagAdapter{uow: uow, observations: profileObservations, names: catalogStore}, Surveys: runtimeTagSurveys{}, Timeline: runtimeTagTimeline{}, Chat: runtimeTagChat{},
		TagCommands: commands, TagHistory: customerstore.TagCommandPostgreSQL{}, ProfileSigningKey: []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatal(err)
	}
	catalogHandler, err := taghttp.NewHandler(tagapp.NewService(uow, catalogStore, nil, nil, nil), &tagapp.SyncService{}, runtimeTagCatalogGate{}, security)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	// This is the production composition ordering: the explicit command route
	// and mountSurveyAPIs compatibility subtree share the exact TagCommand host.
	mux.Handle("/api/v1/customer-tag-commands", handler.TagCommandRoutes())
	mux.Handle("/api/v1/customer-tag-commands/", handler.TagCommandRoutes())
	mux.Handle("/api/admin/wecom/tags", catalogHandler)
	mux.Handle("/api/admin/customers/", handler.Routes())
	mountSurveyAPIs(mux, http.NotFoundHandler(), handler.TagCommandRoutes())
	server := httptest.NewServer(mux)
	defer server.Close()

	body := []byte(`{"customer_ids":[2],"add_tag_ids":[1],"idempotency_key":"runtime-tag-command-key"}`)
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

	request, err = http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/customers/2/tag-commands", nil)
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
	// confirmation requests and catalog-name selection against this live mux;
	// only the unrelated customer directory list fixture is local to the browser
	// harness.
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

type runtimeTagCatalogGate struct{}

func (runtimeTagCatalogGate) Get(context.Context) (tagdomain.ExecutionGate, error) {
	return tagdomain.ExecutionGate{LocalCommandAcceptanceAvailable: true, LocalQueueAvailable: true, ObservedAt: time.Now().UTC()}, nil
}

type runtimeTagGate struct{}

func (runtimeTagGate) FreezeTagCommandTarget(_ context.Context, target customerport.TagCommandTarget) (customerport.FrozenTagCommandTarget, error) {
	target.StaffID = 1
	return customerport.FrozenTagCommandTarget{TagCommandTarget: target, BindingDigest: string(effectport.Hash("runtime-tag-binding")), TargetDigest: string(effectport.Hash("customer.tag.command.target.v1", "runtime-staff", "runtime-external"))}, nil
}

type customerTagCompositionGate struct{}

func (customerTagCompositionGate) FreezeTagCommandTarget(_ context.Context, target customerport.TagCommandTarget) (customerport.FrozenTagCommandTarget, error) {
	target.StaffID = 1
	return customerport.FrozenTagCommandTarget{TagCommandTarget: target, BindingDigest: string(effectport.Hash("tag-command-integration-binding")), TargetDigest: string(effectport.Hash("tag-command-integration-target"))}, nil
}

type tagCommandFailingOutbox struct{}

func (tagCommandFailingOutbox) Append(context.Context, platformoutbox.Event) (platformoutbox.Event, error) {
	return platformoutbox.Event{}, errors.New("outbox rejected")
}

func TestCustomerTagCommandCompositionAcceptanceAtomicConcurrentReplay(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, clean := customerTagRuntimePool(t, ctx, url)
	defer clean()
	native := pool.Native()
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,session_version) VALUES('tag-admin-atomic','$argon2id$test','Tag admin',true,1); INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active')`); err != nil {
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
	service, err := customerapp.NewTagCommandService(uow, customerstore.TagCommandPostgreSQL{}, effects, customerTagCompositionGate{}, platformaudit.NewPostgreSQLStore(), platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	command := customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "atomic-replay-key", IdempotencyKey: "atomic-replay-key", OccurredAt: time.Now(), Targets: []customerport.TagCommandTarget{{CustomerID: 1, AddTagIDs: []int64{1}}}}
	results := make(chan customerport.TagCommandResult, 2)
	failures := make(chan error, 2)
	for range 2 {
		go func() {
			result, submitErr := service.SubmitTagCommand(ctx, command)
			if submitErr != nil {
				failures <- submitErr
				return
			}
			results <- result
		}()
	}
	var first customerport.TagCommandResult
	for range 2 {
		select {
		case submitErr := <-failures:
			t.Fatalf("concurrent command=%v", submitErr)
		case result := <-results:
			if first.ID == 0 {
				first = result
			} else if result.ID != first.ID || len(result.Lines) != 1 {
				t.Fatalf("replay result=%+v first=%+v", result, first)
			}
		}
	}
	for table, want := range map[string]int{"customer_tag_commands": 1, "customer_tag_command_lines": 1, "external_effects": 1, "external_effect_operation_receipts": 2, "river_job": 1, "audit_events": 1, "outbox_events": 1} {
		var got int
		if err = native.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s=%d want=%d err=%v", table, got, want, err)
		}
	}
	// A post-acceptance append failure rolls every business and EER fact back;
	// no compensating provider path is used.
	failing, err := customerapp.NewTagCommandService(uow, customerstore.TagCommandPostgreSQL{}, effects, customerTagCompositionGate{}, platformaudit.NewPostgreSQLStore(), tagCommandFailingOutbox{})
	if err != nil {
		t.Fatal(err)
	}
	failed := command
	failed.SourceRef, failed.IdempotencyKey = "atomic-rollback-key", "atomic-rollback-key"
	if _, err = failing.SubmitTagCommand(ctx, failed); err == nil {
		t.Fatal("outbox failure must abort acceptance")
	}
	for table, want := range map[string]int{"customer_tag_commands": 1, "customer_tag_command_lines": 1, "external_effects": 1, "river_job": 1} {
		var got int
		if err = native.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != want {
			t.Fatalf("rollback %s=%d want=%d err=%v", table, got, want, err)
		}
	}
}

func TestCustomerTagCommandCompositionBatchOver100QueuesIndependentRiverEffects(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, clean := customerTagRuntimePool(t, ctx, url)
	defer clean()
	native := pool.Native()
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,session_version) VALUES('tag-admin-batch','$argon2id$test','Tag batch admin',true,1); INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE SELECT value,'active' FROM generate_series(1,101) value`); err != nil {
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
	service, err := customerapp.NewTagCommandService(uow, customerstore.TagCommandPostgreSQL{}, effects, customerTagCompositionGate{}, platformaudit.NewPostgreSQLStore(), platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	targets := make([]customerport.TagCommandTarget, 0, 101)
	for customerID := 1; customerID <= 101; customerID++ {
		targets = append(targets, customerport.TagCommandTarget{CustomerID: customerdomain.CustomerID(customerID), AddTagIDs: []int64{1}})
	}
	result, err := service.SubmitTagCommand(ctx, customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "batch-over-100", IdempotencyKey: "batch-over-100", OccurredAt: time.Now(), Targets: targets})
	if err != nil || result.ID < 1 || len(result.Lines) != 101 || result.State != "queued" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for table, want := range map[string]int{"customer_tag_commands": 1, "customer_tag_command_lines": 101, "external_effects": 101, "river_job": 101} {
		var got int
		if err = native.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s=%d want=%d err=%v", table, got, want, err)
		}
	}
	// River holds one stable customer effect per job. Restarting between jobs can
	// never merge two customer mutations or cause a batch-wide resend.
	var duplicateArgs int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM (SELECT args->>'effect_id' effect_id,count(*) FROM river_job GROUP BY args->>'effect_id' HAVING count(*) > 1) duplicates`).Scan(&duplicateArgs); err != nil || duplicateArgs != 0 {
		t.Fatalf("duplicate river effects=%d err=%v", duplicateArgs, err)
	}
}

func TestCustomerTagCommandCompositionSerializesDifferentKeysUntilTerminal(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, clean := customerTagRuntimePool(t, ctx, url)
	defer clean()
	native := pool.Native()
	if _, err = native.Exec(ctx, `INSERT INTO admin_users(username,password_hash,display_name,is_active,session_version) VALUES('tag-order-admin','$argon2id$test','Tag order admin',true,1); INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active'),(2,'active')`); err != nil {
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
	service, err := customerapp.NewTagCommandService(uow, customerstore.TagCommandPostgreSQL{}, effects, customerTagCompositionGate{}, platformaudit.NewPostgreSQLStore(), platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}

	// Two distinct keys for the same Customer race. Customer-row locking makes
	// exactly one accepted; the other cannot reorder an add/remove call.
	start := make(chan struct{})
	outcomes := make(chan error, 2)
	var wg sync.WaitGroup
	for _, command := range []customerport.TagCommand{
		{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "same-customer-add", IdempotencyKey: "same-customer-add", OccurredAt: time.Now(), Targets: []customerport.TagCommandTarget{{CustomerID: 2, AddTagIDs: []int64{1}}}},
		{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "same-customer-remove", IdempotencyKey: "same-customer-remove", OccurredAt: time.Now(), Targets: []customerport.TagCommandTarget{{CustomerID: 2, RemoveTagIDs: []int64{1}}}},
	} {
		wg.Add(1)
		go func(command customerport.TagCommand) {
			defer wg.Done()
			<-start
			_, submitErr := service.SubmitTagCommand(ctx, command)
			outcomes <- submitErr
		}(command)
	}
	close(start)
	wg.Wait()
	close(outcomes)
	accepted, conflicts := 0, 0
	for submitErr := range outcomes {
		if submitErr == nil {
			accepted++
		} else if errors.Is(submitErr, customerport.ErrTagCommandConflict) {
			conflicts++
		} else {
			t.Fatalf("same-customer submission=%v", submitErr)
		}
	}
	if accepted != 1 || conflicts != 1 {
		t.Fatalf("accepted=%d conflicts=%d", accepted, conflicts)
	}

	first, err := service.SubmitTagCommand(ctx, customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "unknown-add", IdempotencyKey: "unknown-add", OccurredAt: time.Now(), Targets: []customerport.TagCommandTarget{{CustomerID: 1, AddTagIDs: []int64{1}}}})
	if err != nil || len(first.Lines) != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		return customerstore.TagCommandPostgreSQL{}.CompleteTagCommand(tx, customerport.TagCommandCompletion{EffectRef: first.Lines[0].EffectRef, State: "outcome_unknown", ResultDigest: string(effectport.Hash("unknown-before-opposite")), Attempt: 1, Generation: 1, Fence: 1, CompletedAt: time.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	_, err = service.SubmitTagCommand(ctx, customerport.TagCommand{ActorAdminUserID: 1, Source: "admin_customer_ui", SourceRef: "unknown-remove", IdempotencyKey: "unknown-remove", OccurredAt: time.Now(), Targets: []customerport.TagCommandTarget{{CustomerID: 1, RemoveTagIDs: []int64{1}}}})
	if !errors.Is(err, customerport.ErrTagCommandConflict) {
		t.Fatalf("opposite after unknown=%v", err)
	}
	var effectsCount int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effectsCount); err != nil || effectsCount != 2 {
		t.Fatalf("effects=%d want=2 err=%v", effectsCount, err)
	}
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
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0004_wecom.sql", "0005_external_effects.sql", "0008_tag_catalog.sql", "0009_customer_activation.sql", "0022_customer_profile_sections.sql", "0093_customer_tag_commands.sql"} {
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
