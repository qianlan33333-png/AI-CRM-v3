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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/http"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	automationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
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
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
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
	staffID := automationAudienceInsertProviderStaff(t, ctx, native)
	customerIDs := automationAudienceInsertProviderCustomers(t, ctx, native)
	source := &automationAudienceSource{}
	source.Set(customerIDs[:1])
	evaluator, err := segmentapp.NewEvaluator(segmentcompiler.Compiler{}, source, automationAudienceCanonical{})
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
	automationService := automationapp.NewAgentService(uow, automationRepo, automationRepo)
	agent := automationAudiencePublishedAgent(t, ctx, automationService, staffID)
	publishedAgent, found, err := automationService.PublishedAgent(ctx, agent.ID)
	if err != nil || !found {
		t.Fatalf("published agent=%+v found=%v err=%v", agent, found, err)
	}
	segmentStaff := automationOpsStaffAdapter{uow: uow, users: accessstore.NewPostgreSQL()}
	execution, err := segmentapp.NewExecutionService(uow, segmentRepo, automationService, segmentStaff, true)
	if err != nil {
		t.Fatal(err)
	}
	combined := automationAudienceCombinedDigest(publishedAgent.ContentDigest, publishedAgent.MaterialsDigest)
	binding, err := execution.PutBinding(ctx, segmentapp.BindingCommand{PackageID: packageID, ExpectedPackageVersion: 2, AgentID: agent.ID, ExpectedPublishedVersion: publishedAgent.PublishedVersion, ExpectedAgentDigest: combined, Actor: staffID, IdempotencyKey: "audience-runtime-binding-0001"})
	if err != nil || binding.ID < 1 {
		t.Fatalf("binding=%+v err=%v", binding, err)
	}
	if _, err = execution.ReplaceSenders(ctx, segmentapp.SendersCommand{PackageID: packageID, ExpectedPackageVersion: 3, ProviderMemberIDs: []string{"sender-a"}, Actor: staffID, IdempotencyKey: "audience-runtime-senders-0001"}); err != nil {
		t.Fatal(err)
	}

	messages, err := outbound.NewMessageService(native, uow, effects, automationRepo)
	if err != nil {
		t.Fatal(err)
	}
	wecomServer := newAutomationAudienceWeComServer(t)
	defer wecomServer.Close()
	writer, err := wecomadapter.NewDirectory(wecomadapter.Config{
		Enabled: true, CorpID: "runtime-corp", ContactSecret: "runtime-contact-secret",
		APIBase: wecomServer.URL, HTTPClient: wecomServer.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	messageProvider, err := outbound.NewMessageProvider(outbound.MessageProviderConfig{
		Enabled: true, CorpScope: "wecom-corp:runtime-corp", Executions: messages,
		Identities: outboundIdentityAdapter{uow: uow, reader: identityquery.NewPostgreSQL()},
		Staff:      segmentStaff, Content: automationService, Writer: writer,
	})
	if err != nil {
		t.Fatal(err)
	}
	effectWorker := externaleffects.NewWorker(nil, messageProvider)
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, effectWorker); err != nil {
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
	if err = effectWorker.BindRepository(effects); err != nil {
		t.Fatal(err)
	}

	approval := staffID
	actionConfig, err := json.Marshal(map[string]int64{"agent_id": int64(agent.ID)})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := runtimeService.CreatePolicy(ctx, automationapp.PolicyCommand{Code: "audience-entry", Name: "Audience entry", PackageID: segmentport.PackageID(packageID), TriggerKind: automationport.TriggerAudienceMemberEnteredV1, ActionKind: automationport.ActionOutboundMessage, ActionConfig: actionConfig, QuietHours: json.RawMessage(`{"timezone":"UTC","start":"22:00","end":"08:00"}`), SingleRunLimit: 100, ApprovalStaffID: &approval, Actor: staffID, IdempotencyKey: "audience-runtime-policy-0001"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtimeService.TransitionPolicy(ctx, automationapp.PolicyLifecycleCommand{PolicyID: policy.ID, ExpectedVersion: policy.Version, Actor: staffID, Target: automationdomain.PolicyActive, IdempotencyKey: "audience-runtime-activate-0001"}); err != nil {
		t.Fatal(err)
	}
	runtime, err := platformjobqueue.NewRuntime(native, workers, segment.AudienceRefreshQueue, platformjobqueue.OutboundQueue)
	if err != nil {
		t.Fatal(err)
	}
	stop := automationAudienceStartRuntime(t, runtime)
	refresh, err := snapshots.AcceptRefresh(ctx, segmentapp.RefreshCommand{PackageID: packageID, Actor: staffID, IdempotencyKey: "audience-runtime-refresh-0001", ReferenceTime: now})
	if err != nil || refresh.RiverJobID == nil {
		t.Fatalf("accept refresh=%+v err=%v", refresh, err)
	}
	var published segmentport.Snapshot
	automationAudienceEventually(t, "initial audience and automatic delivery", func() bool {
		var found bool
		published, found, err = snapshots.PublishedSnapshot(ctx, segmentport.PackageID(packageID))
		if err != nil || !found || published.MemberCount != 1 {
			return false
		}
		var complete int
		if native.QueryRow(ctx, `SELECT count(*) FROM outbound_message_intents WHERE source_kind='automation_enrollment' AND state='provider_accepted'`).Scan(&complete) != nil {
			return false
		}
		return complete == 1
	})
	// Incremental evaluation contains only the new result. Segment merges it
	// with the prior snapshot, so the original member remains present.
	source.Set(customerIDs[1:])
	incremental, err := snapshots.AcceptRefresh(ctx, segmentapp.RefreshCommand{PackageID: packageID, Actor: staffID, IdempotencyKey: "audience-runtime-incremental-0001", RefreshKind: segmentdomain.RefreshIncremental, ReferenceTime: now.Add(time.Minute)})
	if err != nil || incremental.RiverJobID == nil {
		t.Fatalf("accept incremental=%+v err=%v", incremental, err)
	}
	automationAudienceEventually(t, "incremental membership merge and automatic delivery", func() bool {
		var found bool
		published, found, err = snapshots.PublishedSnapshot(ctx, segmentport.PackageID(packageID))
		if err != nil || !found || published.MemberCount != 2 {
			return false
		}
		var prior, added, complete int
		if native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_snapshot_members WHERE snapshot_id=$1 AND customer_id=$2`, published.ID, customerIDs[0]).Scan(&prior) != nil {
			return false
		}
		if native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_snapshot_members WHERE snapshot_id=$1 AND customer_id=$2`, published.ID, customerIDs[1]).Scan(&added) != nil {
			return false
		}
		if native.QueryRow(ctx, `SELECT count(*) FROM outbound_message_intents WHERE source_kind='automation_enrollment' AND state='provider_accepted'`).Scan(&complete) != nil {
			return false
		}
		return prior == 1 && added == 1 && complete == 2
	})
	stop()
	var enrollments, automaticEffects int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_enrollments`).Scan(&enrollments); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM outbound_message_intents WHERE source_kind='automation_enrollment'`).Scan(&automaticEffects); err != nil {
		t.Fatal(err)
	}
	if enrollments != 2 || automaticEffects != 2 {
		t.Fatalf("entered events created enrollments=%d intents=%d", enrollments, automaticEffects)
	}

	preview, err := runtimeService.CreateBroadcastPreview(ctx, packageID, 1)
	if err != nil || preview.TargetCount != 2 {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	manual, err := runtimeService.ConfirmRun(ctx, automationapp.RunConfirmCommand{PackageID: packageID, PackageVersion: preview.PackageVersion, SnapshotID: preview.SnapshotID, AgentID: preview.AgentID, AgentPublishedVersion: preview.AgentPublishedVersion, PreviewDigest: automationapp.PreviewDigestString(preview), Actor: staffID, IdempotencyKey: "audience-runtime-manual-0001"})
	if err != nil || manual.TargetCount != 2 {
		t.Fatalf("manual=%+v err=%v", manual, err)
	}
	// The manual intents were accepted while the first runtime was stopped.
	// Restarting the shared runtime is what executes their already-committed
	// EER jobs; it never recreates the entered-policy sends from the first run.
	stop = automationAudienceStartRuntime(t, runtime)
	automationAudienceEventually(t, "manual effects after runtime restart", func() bool {
		var accepted, unknown int
		if native.QueryRow(ctx, `SELECT count(*) FROM automation_run_recipients WHERE run_id=$1 AND state='provider_accepted'`, manual.ID).Scan(&accepted) != nil {
			return false
		}
		if native.QueryRow(ctx, `SELECT count(*) FROM automation_run_recipients WHERE run_id=$1 AND state='outcome_unknown'`, manual.ID).Scan(&unknown) != nil {
			return false
		}
		return accepted == 1 && unknown == 1 && wecomServer.Calls() == 4
	})
	stop()
	var unknownEffect string
	if err = native.QueryRow(ctx, `SELECT effect_id FROM automation_run_recipients WHERE run_id=$1 AND state='outcome_unknown'`, manual.ID).Scan(&unknownEffect); err != nil {
		t.Fatal(err)
	}
	var unknownRun int64
	if err = native.QueryRow(ctx, `SELECT run_id FROM automation_run_recipients WHERE effect_id=$1`, unknownEffect).Scan(&unknownRun); err != nil {
		t.Fatal(err)
	}
	var effectID int64
	if _, err = fmt.Sscanf(unknownEffect, "eer_%d", &effectID); err != nil || effectID < 1 {
		t.Fatalf("effect ref=%q err=%v", unknownEffect, err)
	}
	if _, err = native.Exec(ctx, `UPDATE external_effects SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, effectID); err != nil {
		t.Fatal(err)
	}
	candidate, err := runtimeService.EffectReconciliationCandidate(ctx, unknownRun, unknownEffect)
	if err != nil {
		t.Fatal(err)
	}
	evidence := string(effectport.Hash("audience-runtime-independent-evidence", unknownEffect))
	if _, err = runtimeService.ReconcileRunEffect(ctx, automationapp.RunEffectReconcileCommand{RunID: unknownRun, Actor: staffID, Generation: candidate.Generation, Fence: candidate.Fence, EffectID: unknownEffect, LeaseExpiresAt: candidate.LeaseExpiresAt, EvidenceDigest: evidence[len("sha256:"):], Resolution: "provider_accepted", IdempotencyKey: "audience-runtime-reconcile-0001"}); err != nil {
		t.Fatal(err)
	}
	replayed, err := runtimeService.ConfirmRun(ctx, automationapp.RunConfirmCommand{PackageID: packageID, PackageVersion: preview.PackageVersion, SnapshotID: preview.SnapshotID, AgentID: preview.AgentID, AgentPublishedVersion: preview.AgentPublishedVersion, PreviewDigest: automationapp.PreviewDigestString(preview), Actor: staffID, IdempotencyKey: "audience-runtime-manual-0001"})
	if err != nil || replayed.ID != manual.ID || wecomServer.Calls() != 4 {
		t.Fatalf("manual replay=%+v provider calls=%d err=%v", replayed, wecomServer.Calls(), err)
	}

	readHandler, err := automationhttp.NewRuntimeHandler(runtimeService, automationAudienceSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	var before, after int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_runtime_audit_events`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct{ path, contains string }{
		{"/api/admin/automation-runs?limit=100", automationAudienceInt(manual.ID)},
		{"/api/admin/automation-runs/" + automationAudienceInt(manual.ID) + "/recipients?limit=100", unknownEffect},
	} {
		req := httptest.NewRequest(http.MethodGet, check.path, nil)
		res := httptest.NewRecorder()
		readHandler.ServeHTTP(res, req)
		if res.Code != http.StatusOK || !json.Valid(res.Body.Bytes()) || !strings.Contains(res.Body.String(), check.contains) {
			t.Fatalf("read history %s status=%d body=%s", check.path, res.Code, res.Body.String())
		}
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM automation_runtime_audit_events`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("history GET wrote audit rows: before=%d after=%d", before, after)
	}
	// Daily exits are Segment facts only. A third runtime start processes the
	// persisted daily job, but no member-entered event or outbound send appears.
	source.Set(customerIDs[:1])
	stop = automationAudienceStartRuntime(t, runtime)
	daily, err := snapshots.AcceptRefresh(ctx, segmentapp.RefreshCommand{PackageID: packageID, Actor: staffID, IdempotencyKey: "audience-runtime-daily-exit-0001", RefreshKind: segmentdomain.RefreshDaily, ReferenceTime: now.Add(time.Hour)})
	if err != nil || daily.RiverJobID == nil {
		t.Fatalf("accept daily=%+v err=%v", daily, err)
	}
	var exitSnapshot segmentport.Snapshot
	automationAudienceEventually(t, "daily exit without entered send", func() bool {
		var found bool
		exitSnapshot, found, err = snapshots.PublishedSnapshot(ctx, segmentport.PackageID(packageID))
		if err != nil || !found || exitSnapshot.ID == published.ID {
			return false
		}
		var exits, entered, intents int
		if native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_member_exit_events WHERE snapshot_id=$1 AND customer_id=$2`, exitSnapshot.ID, customerIDs[1]).Scan(&exits) != nil {
			return false
		}
		if native.QueryRow(ctx, `SELECT count(*) FROM segment_audience_member_events WHERE snapshot_id=$1`, exitSnapshot.ID).Scan(&entered) != nil {
			return false
		}
		if native.QueryRow(ctx, `SELECT count(*) FROM outbound_message_intents`).Scan(&intents) != nil {
			return false
		}
		return exits == 1 && entered == 0 && intents == 4 && wecomServer.Calls() == 4
	})
	stop()
}

type automationAudienceSource struct {
	mu  sync.RWMutex
	ids []customerdomain.CustomerID
}

func (s *automationAudienceSource) Set(ids []customerdomain.CustomerID) {
	s.mu.Lock()
	s.ids = append([]customerdomain.CustomerID(nil), ids...)
	s.mu.Unlock()
}

func (s *automationAudienceSource) Evaluate(_ context.Context, _ segmentport.Definition, reference time.Time) (segmentport.Evaluation, error) {
	s.mu.RLock()
	ids := append([]customerdomain.CustomerID(nil), s.ids...)
	s.mu.RUnlock()
	return segmentport.Evaluation{CustomerIDs: ids, ReferenceAt: reference.UTC()}, nil
}

type automationAudienceCanonical struct{}

func (automationAudienceCanonical) CanonicalCustomers(_ context.Context, ids []customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	return ids, nil
}

type automationAudienceEnrollmentSink struct{ runtime *automationapp.RuntimeService }

func (s automationAudienceEnrollmentSink) HandleAudienceMemberEntered(ctx context.Context, e segmentport.MemberEnteredV1) error {
	_, err := s.runtime.EnrollAudienceMember(ctx, e)
	return err
}

type automationAudienceWeComServer struct {
	*httptest.Server
	t     *testing.T
	mu    sync.Mutex
	calls int
	errs  []string
}

func newAutomationAudienceWeComServer(t *testing.T) *automationAudienceWeComServer {
	t.Helper()
	s := &automationAudienceWeComServer{t: t}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

func (s *automationAudienceWeComServer) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/cgi-bin/gettoken":
		if r.URL.Query().Get("corpid") != "runtime-corp" || r.URL.Query().Get("corpsecret") != "runtime-contact-secret" {
			s.record("unexpected token request")
			http.Error(w, "bad token request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"errcode":0,"access_token":"runtime-token","expires_in":7200}`))
	case "/cgi-bin/externalcontact/add_msg_template":
		if r.Method != http.MethodPost || r.URL.Query().Get("access_token") != "runtime-token" {
			s.record("unexpected message endpoint or token")
			http.Error(w, "bad message request", http.StatusBadRequest)
			return
		}
		var body struct {
			ChatType string   `json:"chat_type"`
			External []string `json:"external_userid"`
			Sender   string   `json:"sender"`
			Text     struct {
				Content string `json:"content"`
			} `json:"text"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || body.ChatType != "single" || len(body.External) != 1 || (body.External[0] != "runtime-external-1" && body.External[0] != "runtime-external-2") || body.Sender != "sender-a" || body.Text.Content != "runtime hello" {
			s.record("invalid signed message body")
			http.Error(w, "bad message body", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.calls++
		call := s.calls
		s.mu.Unlock()
		if call == 4 {
			h, ok := w.(http.Hijacker)
			if !ok {
				s.record("httptest response writer cannot hijack")
				return
			}
			conn, _, err := h.Hijack()
			if err != nil {
				s.record("cannot terminate fourth provider response")
				return
			}
			_ = conn.Close()
			return
		}
		_, _ = w.Write([]byte(fmt.Sprintf(`{"errcode":0,"msgid":"runtime-msg-%d"}`, call)))
	default:
		s.record("unexpected provider path " + r.URL.Path)
		http.NotFound(w, r)
	}
}

func (s *automationAudienceWeComServer) record(value string) {
	s.mu.Lock()
	s.errs = append(s.errs, value)
	s.mu.Unlock()
}
func (s *automationAudienceWeComServer) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.errs) != 0 {
		s.t.Errorf("provider assertions: %s", strings.Join(s.errs, "; "))
	}
	return s.calls
}

func automationAudienceInsertProviderStaff(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid) VALUES('runtime-staff','$argon2id$runtime','Runtime sender','sender-a') RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func automationAudienceInsertProviderCustomers(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []customerdomain.CustomerID {
	t.Helper()
	ids := make([]customerdomain.CustomerID, 0, 2)
	for _, external := range []string{"runtime-external-1", "runtime-external-2"} {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'wecom_external_userid','wecom-corp:runtime-corp',$2,'verified','runtime-fixture',1,clock_timestamp())`, id, external); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, customerdomain.CustomerID(id))
	}
	return ids
}

func automationAudiencePublishedAgent(t *testing.T, ctx context.Context, service *automationapp.Service, actor int64) automationport.Agent {
	t.Helper()
	agent, err := service.Create(ctx, automationport.CreateCommand{Agent: automationport.Agent{AgentName: "Runtime fixed script", AgentCode: "runtime-fixed-script", AutomationType: automationport.AutomationTypeFixedScript, Status: automationport.AgentStatusPaused, DraftRolePrompt: "runtime role", DraftTaskPrompt: "runtime task", FixedContentPackage: automationport.FixedContentPackage{ContentText: "runtime hello"}}, Actor: actor, IdempotencyKey: "audience-runtime-agent-create-0001"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err = service.SetStatus(ctx, automationport.MutationCommand{ID: agent.ID, Actor: actor, IdempotencyKey: "audience-runtime-agent-active-0001"}, automationport.AgentStatusActive)
	if err != nil {
		t.Fatal(err)
	}
	return agent
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
func automationAudienceCombinedDigest(content, materials [32]byte) string {
	raw := append(append([]byte{}, content[:]...), materials[:]...)
	out := sha256.Sum256(raw)
	return hex.EncodeToString(out[:])
}
func automationAudienceInt(v int64) string { return strconv.FormatInt(v, 10) }

func automationAudienceStartRuntime(t *testing.T, runtime *platformjobqueue.Runtime) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	return func() {
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("River runtime stop=%v", err)
		}
	}
}

func automationAudienceEventually(t *testing.T, label string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", label)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

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
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0003_access.sql", "0005_external_effects.sql", "0013_automation_agents.sql", "0039_segment_audience_configuration.sql", "0040_segment_audience_snapshots.sql", "0041_segment_audience_webhooks.sql", "0042_segment_audience_execution_bindings.sql", "0043_automation_runtime.sql", "0044_outbound_automation_messages.sql", "0045_segment_audience_member_events.sql", "0046_automation_run_reconciliations.sql", "0048_segment_audience_schedule_state.sql", "0053_segment_audience_member_event_fact_kinds.sql", "0083_segment_audience_refresh_modes.sql", "0085_segment_audience_refresh_kind.sql"} {
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
