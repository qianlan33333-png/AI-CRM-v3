package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/http"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	automationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/segment"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentcompiler "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/compiler"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
)

// TestAudienceRefreshToAutomationProviderAndReadOnlyHistoryPostgreSQL proves
// the composed ownership path: a real Segment River refresh creates durable
// entered facts, an approved policy consumes them through its River worker,
// outbound accepts EER effects, and the existing result API reads the
// projected history without a write. The adapter is deliberately local and
// deterministic; no Provider credential or customer identity is fabricated.
func TestAudienceRefreshToAutomationProviderAndReadOnlyHistoryPostgreSQL(t *testing.T) {
	ctx := context.Background()
	native, cleanup := automationAudienceRuntimePool(t)
	defer cleanup()
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	segmentRepo, err := segmentstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	automationRepo, err := automationstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}

	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	refreshWorker := segment.NewAudienceRefreshWorker()
	memberWorker := segment.NewAudienceMemberEventDispatchWorker()
	if err = river.AddWorkerSafely[segment.AudienceRefreshJobArgs](workers, refreshWorker); err != nil {
		t.Fatal(err)
	}
	if err = river.AddWorkerSafely[segment.AudienceMemberEventDispatchJobArgs](workers, memberWorker); err != nil {
		t.Fatal(err)
	}
	client, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, client)
	if err != nil {
		t.Fatal(err)
	}
	refreshJobs, err := segment.NewRiverRefreshEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	memberJobs, err := segment.NewRiverMemberEventEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := segmentapp.NewEvaluator(segmentcompiler.Compiler{}, automationAudienceSource{}, automationAudienceCanonical{})
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := segmentapp.NewSnapshotService(uow, segmentRepo, evaluator, refreshJobs, memberJobs)
	if err != nil {
		t.Fatal(err)
	}
	if err = refreshWorker.BindService(snapshots); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 5, 20, 0, 0, 0, time.UTC)
	packageID := automationAudiencePackage(t, ctx, uow, segmentRepo, now)
	refresh, err := snapshots.AcceptRefresh(ctx, segmentapp.RefreshCommand{PackageID: packageID, Actor: 1, IdempotencyKey: "audience-runtime-refresh-0001", ReferenceTime: now})
	if err != nil || refresh.RiverJobID == nil {
		t.Fatalf("accept refresh=%+v err=%v", refresh, err)
	}
	if err = refreshWorker.Work(ctx, &river.Job[segment.AudienceRefreshJobArgs]{JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 12}, Args: segment.AudienceRefreshJobArgs{RefreshRunID: refresh.ID}}); err != nil {
		t.Fatal(err)
	}
	published, found, err := snapshots.PublishedSnapshot(ctx, segmentport.PackageID(packageID))
	if err != nil || !found || published.MemberCount != 2 {
		t.Fatalf("snapshot=%+v found=%v err=%v", published, found, err)
	}

	digest := [32]byte{1}
	execution, err := segmentapp.NewExecutionService(uow, segmentRepo, automationAudienceAgent{digest: digest}, automationAudienceStaff{}, true)
	if err != nil {
		t.Fatal(err)
	}
	combined := automationAudienceCombinedDigest(digest)
	binding, err := execution.PutBinding(ctx, segmentapp.BindingCommand{PackageID: packageID, ExpectedPackageVersion: 2, AgentID: 77, ExpectedPublishedVersion: 3, ExpectedAgentDigest: combined, Actor: 1, IdempotencyKey: "audience-runtime-binding-0001"})
	if err != nil || binding.ID < 1 {
		t.Fatalf("binding=%+v err=%v", binding, err)
	}
	if _, err = execution.ReplaceSenders(ctx, segmentapp.SendersCommand{PackageID: packageID, ExpectedPackageVersion: 3, ProviderMemberIDs: []string{"sender-a"}, Actor: 1, IdempotencyKey: "audience-runtime-senders-0001"}); err != nil {
		t.Fatal(err)
	}

	messages, err := outbound.NewMessageService(native, uow, effects, automationRepo)
	if err != nil {
		t.Fatal(err)
	}
	router, err := outbound.NewCompletionRouterWithMessage(nil, nil, messages)
	if err != nil {
		t.Fatal(err)
	}
	if err = effects.SetCompletionSink(router); err != nil {
		t.Fatal(err)
	}
	runtimeService, err := automationapp.NewRuntimeService(uow, automationRepo, execution, snapshots, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err = runtimeService.SetMessageAccepter(messages); err != nil {
		t.Fatal(err)
	}
	if err = runtimeService.SetEffectReconciler(effects); err != nil {
		t.Fatal(err)
	}
	if err = memberWorker.Bind(snapshots, automationAudienceEnrollmentSink{runtime: runtimeService}); err != nil {
		t.Fatal(err)
	}

	approval := int64(1)
	policy, err := runtimeService.CreatePolicy(ctx, automationapp.PolicyCommand{Code: "audience-entry", Name: "Audience entry", PackageID: segmentport.PackageID(packageID), TriggerKind: automationport.TriggerAudienceMemberEnteredV1, ActionKind: automationport.ActionOutboundMessage, ActionConfig: json.RawMessage(`{"agent_id":77}`), QuietHours: json.RawMessage(`{"timezone":"UTC","start":"22:00","end":"08:00"}`), SingleRunLimit: 100, ApprovalStaffID: &approval, Actor: 1, IdempotencyKey: "audience-runtime-policy-0001"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtimeService.TransitionPolicy(ctx, automationapp.PolicyLifecycleCommand{PolicyID: policy.ID, ExpectedVersion: policy.Version, Actor: 1, Target: automationdomain.PolicyActive, IdempotencyKey: "audience-runtime-activate-0001"}); err != nil {
		t.Fatal(err)
	}
	if err = memberWorker.Work(ctx, &river.Job[segment.AudienceMemberEventDispatchJobArgs]{JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 12}, Args: segment.AudienceMemberEventDispatchJobArgs{SnapshotID: published.ID}}); err != nil {
		t.Fatal(err)
	}
	// A River replay must reuse enrollment/effect receipts instead of sending again.
	if err = memberWorker.Work(ctx, &river.Job[segment.AudienceMemberEventDispatchJobArgs]{JobRow: &rivertype.JobRow{Attempt: 2, MaxAttempts: 12}, Args: segment.AudienceMemberEventDispatchJobArgs{SnapshotID: published.ID}}); err != nil {
		t.Fatal(err)
	}
	var enrollments, automaticEffects int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_enrollments`).Scan(&enrollments); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM outbound_message_intents WHERE source_kind='automation_enrollment'`).Scan(&automaticEffects); err != nil {
		t.Fatal(err)
	}
	if enrollments != 2 || automaticEffects != 2 {
		t.Fatalf("replayed entered events created enrollments=%d intents=%d", enrollments, automaticEffects)
	}

	preview, err := runtimeService.CreateBroadcastPreview(ctx, packageID, 1)
	if err != nil || preview.TargetCount != 2 {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	manual, err := runtimeService.ConfirmRun(ctx, automationapp.RunConfirmCommand{PackageID: packageID, PackageVersion: preview.PackageVersion, SnapshotID: preview.SnapshotID, AgentID: preview.AgentID, AgentPublishedVersion: preview.AgentPublishedVersion, PreviewDigest: automationapp.PreviewDigestString(preview), Actor: 1, IdempotencyKey: "audience-runtime-manual-0001"})
	if err != nil || manual.TargetCount != 2 {
		t.Fatalf("manual=%+v err=%v", manual, err)
	}

	rows, err := native.Query(ctx, `SELECT effect_id::text,river_job_id FROM external_effect_jobs ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type effectJob struct {
		id      string
		riverID int64
	}
	var jobs []effectJob
	for rows.Next() {
		var j effectJob
		if err = rows.Scan(&j.id, &j.riverID); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, j)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 4 {
		t.Fatalf("external jobs=%+v", jobs)
	}
	var unknownEffect string
	for i, job := range jobs {
		effectID, parseErr := automationAudienceEffectID(job.id)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		completion := effectport.StateExecuted
		if i == len(jobs)-1 {
			completion, unknownEffect = effectport.StateUnknown, job.id
		}
		adapter := automationAudienceProvider{result: effectport.AdapterResult{Completion: completion, ReceiptDigest: effectport.Hash("audience-runtime-provider", job.id), CallAttempted: true, RealExternalCallExecuted: true}}
		if err = effects.RunAttempt(ctx, effectID, 1, job.riverID, &adapter); err != nil {
			t.Fatal(err)
		}
	}
	if unknownEffect == "" {
		t.Fatal("expected one unknown provider effect")
	}
	var unknownRun int64
	if err = native.QueryRow(ctx, `SELECT run_id FROM automation_run_recipients WHERE effect_id=$1`, unknownEffect).Scan(&unknownRun); err != nil {
		t.Fatal(err)
	}
	effectID, err := automationAudienceEffectID(unknownEffect)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `UPDATE external_effects SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, effectID); err != nil {
		t.Fatal(err)
	}
	candidate, err := runtimeService.EffectReconciliationCandidate(ctx, unknownRun, unknownEffect)
	if err != nil {
		t.Fatal(err)
	}
	evidence := string(effectport.Hash("audience-runtime-independent-evidence", unknownEffect))
	if _, err = runtimeService.ReconcileRunEffect(ctx, automationapp.RunEffectReconcileCommand{RunID: unknownRun, Actor: 1, Generation: candidate.Generation, Fence: candidate.Fence, EffectID: unknownEffect, LeaseExpiresAt: candidate.LeaseExpiresAt, EvidenceDigest: evidence[len("sha256:"):], Resolution: "provider_accepted", IdempotencyKey: "audience-runtime-reconcile-0001"}); err != nil {
		t.Fatal(err)
	}

	readHandler, err := automationhttp.NewRuntimeHandler(runtimeService, automationAudienceSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	var before, after int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_runtime_audit_events`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/admin/automation-runs?limit=100", "/api/admin/automation-runs/" + automationAudienceInt(manual.ID) + "/recipients?limit=100"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		readHandler.ServeHTTP(res, req)
		if res.Code != http.StatusOK || !json.Valid(res.Body.Bytes()) {
			t.Fatalf("read history %s status=%d body=%s", path, res.Code, res.Body.String())
		}
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_runtime_audit_events`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("history GET wrote audit rows: before=%d after=%d", before, after)
	}
}

type automationAudienceSource struct{}

func (automationAudienceSource) Evaluate(_ context.Context, _ segmentport.Definition, reference time.Time) (segmentport.Evaluation, error) {
	return segmentport.Evaluation{CustomerIDs: []customerdomain.CustomerID{101, 202}, ReferenceAt: reference.UTC()}, nil
}

type automationAudienceCanonical struct{}

func (automationAudienceCanonical) CanonicalCustomers(_ context.Context, ids []customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	return ids, nil
}

type automationAudienceAgent struct{ digest [32]byte }

func (a automationAudienceAgent) PublishedAgent(context.Context, automationport.AgentID) (automationport.PublishedAgent, bool, error) {
	return automationport.PublishedAgent{AgentID: 77, AutomationType: automationport.AutomationTypeFixedScript, Status: automationport.AgentStatusActive, PublishedVersion: 3, ContentDigest: a.digest, MaterialsDigest: a.digest}, true, nil
}

type automationAudienceStaff struct{}

func (automationAudienceStaff) ResolveAutomationSender(_ context.Context, value string) (accessport.StaffEligibility, bool, error) {
	return accessport.StaffEligibility{StaffID: 9, Active: value == "sender-a", Eligible: value == "sender-a", EligibilityVersion: 1, RefreshedAt: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)}, value == "sender-a", nil
}
func (automationAudienceStaff) AutomationSender(context.Context, accessport.StaffID) (accessport.StaffEligibility, bool, error) {
	return accessport.StaffEligibility{}, false, nil
}

type automationAudienceEnrollmentSink struct{ runtime *automationapp.RuntimeService }

func (s automationAudienceEnrollmentSink) HandleAudienceMemberEntered(ctx context.Context, e segmentport.MemberEnteredV1) error {
	_, err := s.runtime.EnrollAudienceMember(ctx, e)
	return err
}

type automationAudienceProvider struct{ result effectport.AdapterResult }

func (p *automationAudienceProvider) Execute(context.Context, effectport.Envelope, effectport.Attempt) (effectport.AdapterResult, error) {
	return p.result, nil
}

type automationAudienceSecurity struct{}

func (automationAudienceSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (automationAudienceSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return automationAudienceSecurity{}.Authenticate(context.Background(), nil)
}

func automationAudiencePackage(t *testing.T, ctx context.Context, uow *platformpostgres.UnitOfWork, repo *segmentstore.Repository, now time.Time) int64 {
	t.Helper()
	var packageID int64
	err := uow.Within(ctx, func(tx context.Context) error {
		group, err := segmentdomain.NewGroup("automation runtime", 1, 1, now)
		if err != nil {
			return err
		}
		group, err = repo.CreateGroup(tx, group)
		if err != nil {
			return err
		}
		pkg, err := segmentdomain.NewPackage("automation-runtime", "automation runtime", &group.ID, 1, now)
		if err != nil {
			return err
		}
		pkg, err = repo.CreatePackage(tx, pkg)
		if err != nil {
			return err
		}
		config, err := segmentdomain.NewConfigurationVersion(pkg.ID, 1, json.RawMessage(`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"all","owner_staff_ids":[],"contact_statuses":["active"],"registration_status":"any"}}`), "", "manual", 1, now)
		if err != nil {
			return err
		}
		config, err = repo.CreateConfigurationVersion(tx, config)
		if err != nil {
			return err
		}
		pkg, err = repo.SetCurrentConfiguration(tx, pkg.ID, config.ID, pkg.Version, 1, now)
		if err != nil {
			return err
		}
		packageID = pkg.ID
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return packageID
}
func automationAudienceCombinedDigest(d [32]byte) string {
	raw := append(append([]byte{}, d[:]...), d[:]...)
	out := sha256.Sum256(raw)
	return hex.EncodeToString(out[:])
}
func automationAudienceEffectID(v string) (int64, error) {
	var id int64
	_, err := fmt.Sscanf(v, "eer_%d", &id)
	return id, err
}
func automationAudienceInt(v int64) string { return strconv.FormatInt(v, 10) }

func automationAudienceRuntimePool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping Automation audience runtime PostgreSQL journey")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	bytes := make([]byte, 8)
	if _, err = rand.Read(bytes); err != nil {
		t.Fatal(err)
	}
	schema := "automation_audience_runtime_" + hex.EncodeToString(bytes)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	config = config.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(native), nil)
	if err != nil {
		native.Close()
		admin.Close()
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		native.Close()
		admin.Close()
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate automation audience journey")
	}
	for _, name := range []string{"0003_access.sql", "0005_external_effects.sql", "0039_segment_audience_configuration.sql", "0040_segment_audience_snapshots.sql", "0041_segment_audience_webhooks.sql", "0042_segment_audience_execution_bindings.sql", "0043_automation_runtime.sql", "0044_outbound_automation_messages.sql", "0045_segment_audience_member_events.sql", "0046_automation_run_reconciliations.sql", "0048_segment_audience_schedule_state.sql", "0053_segment_audience_member_event_fact_kinds.sql", "0083_segment_audience_refresh_modes.sql", "0085_segment_audience_refresh_kind.sql"} {
		sql, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", name))
		if readErr != nil {
			native.Close()
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(sql)); execErr != nil {
			native.Close()
			admin.Close()
			t.Fatalf("%s: %v", name, execErr)
		}
	}
	return native, func() {
		native.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}
