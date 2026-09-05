package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	groupopsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/store"
	groupopsmaterial "github.com/qianlan33333-png/AI-CRM-v3/internal/media/groupopsmaterial"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// TestGroupOpsPostgreSQLJourney exercises the real owner stores and EER
// transaction seam. It intentionally uses opaque local group references and
// a deterministic adapter: no Provider credentials, customer, OneID, or
// audience data enters this Journey.
func TestGroupOpsPostgreSQLJourney(t *testing.T) {
	native, cleanup := groupOpsIntegrationPool(t)
	defer cleanup()

	ctx := context.Background()
	platformPool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer platformPool.Close()
	uow, err := platformpostgres.NewUnitOfWork(platformPool)
	if err != nil {
		t.Fatal(err)
	}

	var actorID int64
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active) VALUES('groupops-journey','$argon2id$journey','Group Ops Journey','journey-sender',true) RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}

	groupStore, err := groupopsstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	mediaStore, err := mediastore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	invite, err := mediaStore.CreateGroupInvite(ctx, actorID, "groupops-journey-invite-0001", map[string]any{"name": "Journey invite", "title": "Join Journey", "description": "Journey material", "join_url": "https://work.weixin.qq.com/gm/0123456789abcdef0123456789abcdef", "enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	inviteID, ok := invite["id"].(int64)
	if !ok || inviteID < 1 {
		t.Fatalf("invite=%+v", invite)
	}
	freezer, err := groupopsmaterial.NewFreezer(mediaPreparedPlanReader{reader: mediaStore})
	if err != nil {
		t.Fatal(err)
	}
	materialResolver, err := newGroupOpsMaterialAdapter(mediaStore, freezer)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	effectClient, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effectStore, err := externaleffects.NewRepository(native, effectClient)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := newJourneyCompletionSink(groupStore)
	if err != nil {
		t.Fatal(err)
	}
	if err = effectStore.SetCompletionSink(completion); err != nil {
		t.Fatal(err)
	}

	planID := createJourneyPlan(t, ctx, uow, groupStore, actorID, inviteID)
	evidence := &journeyEvidence{status: 1}
	runtimeService := groupopsapp.NewRuntimeService(
		uow,
		groupStore,
		groupStore,
		effectStore,
		journeyStaff{},
		nil,
		journeySender{},
		evidence,
		journeyReconciler{repository: effectStore},
		materialResolver,
	)
	runtimeService.SetDispatchEnabled(true)

	first, err := runtimeService.AcceptBroadcast(ctx, planID, actorID, "journey-broadcast-key-0001")
	if err != nil {
		t.Fatal(err)
	}
	if first.Run.ID < 1 || len(first.Executions) != 2 || first.Accepted != 2 || first.ProviderExecutionEligible || first.RealExternalCallExecuted {
		t.Fatalf("accepted summary=%+v", first)
	}
	// A replay returns the same local run and execution facts without creating
	// a second EER effect.
	replayed, err := runtimeService.AcceptBroadcast(ctx, planID, actorID, "journey-broadcast-key-0001")
	if err != nil || replayed.Run.ID != first.Run.ID || len(replayed.Executions) != len(first.Executions) {
		t.Fatalf("replay=%+v err=%v first=%+v", replayed, err, first)
	}

	for index, execution := range first.Executions {
		effectID, err := strconv.ParseInt(execution.ExternalEffectID[len("eer_"):], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		var riverJobID int64
		if err = native.QueryRow(ctx, `SELECT river_job_id FROM external_effect_jobs WHERE effect_id=$1 AND generation=1`, effectID).Scan(&riverJobID); err != nil {
			t.Fatal(err)
		}
		adapter := journeyProviderAdapter{result: effectport.AdapterResult{
			Completion:               effectport.StateExecuted,
			ReceiptDigest:            effectport.Hash("journey-provider-receipt", execution.ExternalEffectID),
			CallAttempted:            true,
			RealExternalCallExecuted: true,
		}}
		if index == 1 {
			adapter.result = effectport.AdapterResult{
				Completion:               effectport.StateUnknown,
				ReceiptDigest:            effectport.Hash("journey-provider-unknown", execution.ExternalEffectID),
				CallAttempted:            true,
				RealExternalCallExecuted: true,
			}
		}
		if err = effectStore.RunAttempt(ctx, effectID, 1, riverJobID, &adapter); err != nil {
			t.Fatal(err)
		}
	}

	var projected []groupopsport.Execution
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		projected, _, readErr = groupStore.ListExecutions(tx, planID, 10, 0)
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(projected) != 2 {
		t.Fatalf("projected=%+v", projected)
	}
	var unknown groupopsport.Execution
	for _, execution := range projected {
		switch execution.State {
		case groupopsport.ExecutionProviderAccepted:
			if !execution.ProviderAccepted || execution.DeliveryProven || !execution.ProviderReceiptPresent {
				t.Fatalf("provider projection=%+v", execution)
			}
		case groupopsport.ExecutionOutcomeUnknown:
			unknown = execution
		default:
			t.Fatalf("unexpected projected state=%+v", execution)
		}
	}
	if unknown.ID < 1 {
		t.Fatal("unknown execution was not projected")
	}
	var accepted groupopsport.Execution
	for _, execution := range projected {
		if execution.State == groupopsport.ExecutionProviderAccepted {
			accepted = execution
		}
	}
	if accepted.ID < 1 {
		t.Fatal("provider accepted execution was not projected")
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		return groupStore.RecordGroupMessageTask(tx, groupopsport.GroupMessageReceipt{ExecutionID: accepted.ID, ExternalEffectID: accepted.ExternalEffectID, MessageID: "journey-msgid", SenderUserID: "journey-sender", ChatID: "chat-journey-1", TaskEvidenceDigest: string(effectport.Hash("journey-task", accepted.ExternalEffectID))})
	}); err != nil {
		t.Fatal(err)
	}
	read, err := runtimeService.ReadProviderDelivery(ctx, groupopsport.ProviderDeliveryReadCommand{ExecutionID: accepted.ID, ActorID: actorID, IdempotencyKey: "journey-delivery-read-0001"})
	if err != nil || !read.DeliveryProven || read.DeliveryStatus == nil || *read.DeliveryStatus != 1 || read.State != groupopsport.ExecutionProviderAccepted {
		t.Fatalf("delivery read=%+v err=%v", read, err)
	}
	acceptedEffectID, err := strconv.ParseInt(accepted.ExternalEffectID[len("eer_"):], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	var acceptedEffectState string
	if err = native.QueryRow(ctx, `SELECT state FROM external_effects WHERE id=$1`, acceptedEffectID).Scan(&acceptedEffectState); err != nil || acceptedEffectState != string(effectport.StateExecuted) {
		t.Fatalf("accepted EER state=%q err=%v", acceptedEffectState, err)
	}
	var materialJSON, sourceJSON []byte
	var sourceDigest string
	if err = native.QueryRow(ctx, `SELECT material_snapshot,material_source_snapshot,material_source_digest FROM group_ops_executions WHERE id=$1`, accepted.ID).Scan(&materialJSON, &sourceJSON, &sourceDigest); err != nil {
		t.Fatal(err)
	}
	var sourceValue any
	if json.Unmarshal(sourceJSON, &sourceValue) != nil {
		t.Fatalf("source snapshot=%s", sourceJSON)
	}
	canonicalSource, err := json.Marshal(sourceValue)
	if err != nil || sourceDigest != string(effectport.Hash("group-ops.material.intent.v1", string(canonicalSource))) {
		t.Fatalf("source digest=%q canonical=%s err=%v", sourceDigest, canonicalSource, err)
	}
	// This executes the actual Media SourceCapturer and Preparation Reader
	// through the Composition adapter against JSONB-read facts. It verifies
	// semantic equality rather than byte ordering and performs no upload.
	readiness := groupOpsMaterialReadinessAdapter{uow: uow, capturer: mediaStore, freezer: freezer}
	if err = readiness.VerifyMaterialReady(ctx, materialJSON, sourceJSON, sourceDigest, time.Now().UTC()); err != nil {
		t.Fatalf("real Media readiness after JSONB round trip: %v", err)
	}
	if evidence.calls != 1 {
		t.Fatalf("provider delivery calls=%d", evidence.calls)
	}
	// A same-key retry performs another safe Provider read, then returns the
	// persisted completed receipt without a second local mutation.
	sameKey, err := runtimeService.ReadProviderDelivery(ctx, groupopsport.ProviderDeliveryReadCommand{ExecutionID: accepted.ID, ActorID: actorID, IdempotencyKey: "journey-delivery-read-0001"})
	if err != nil || !sameKey.DeliveryProven || sameKey.DeliveryStatus == nil || *sameKey.DeliveryStatus != 1 || evidence.calls != 2 {
		t.Fatalf("same key=%+v err=%v calls=%d", sameKey, err, evidence.calls)
	}
	evidence.status = 0 // a stale page must never downgrade an established delivery fact.
	replayedDelivery, err := runtimeService.ReadProviderDelivery(ctx, groupopsport.ProviderDeliveryReadCommand{ExecutionID: accepted.ID, ActorID: actorID, IdempotencyKey: "journey-delivery-read-0002"})
	if err != nil || !replayedDelivery.DeliveryProven || replayedDelivery.DeliveryStatus == nil || *replayedDelivery.DeliveryStatus != 1 || evidence.calls != 3 {
		t.Fatalf("stale delivery=%+v err=%v calls=%d", replayedDelivery, err, evidence.calls)
	}
	// Exercise the SQL monotonic update under two real PostgreSQL transactions.
	// Either order is allowed; status=1 must survive a concurrent status=0.
	if _, err = native.Exec(ctx, `UPDATE group_ops_group_message_tasks SET delivery_status=NULL,delivery_evidence_digest=NULL,delivery_checked_at=NULL WHERE execution_id=$1`, accepted.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `UPDATE group_ops_executions SET delivery_proven=FALSE WHERE id=$1`, accepted.ID); err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	for _, status := range []int{0, 1} {
		status := status
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- uow.Within(ctx, func(tx context.Context) error {
				return groupStore.RecordGroupMessageDelivery(tx, groupopsport.GroupMessageReceipt{ExecutionID: accepted.ID, ExternalEffectID: accepted.ExternalEffectID, MessageID: "journey-msgid", SenderUserID: "journey-sender", ChatID: "chat-journey-1", DeliveryStatus: &status, DeliveryEvidenceDigest: string(effectport.Hash("journey-concurrent-delivery", accepted.ExternalEffectID, strconv.Itoa(status)))}, string(effectport.Hash("journey-concurrent-delivery", accepted.ExternalEffectID, strconv.Itoa(status))))
			})
		}()
	}
	wg.Wait()
	close(errCh)
	for concurrentErr := range errCh {
		if concurrentErr != nil {
			t.Fatal(concurrentErr)
		}
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		accepted, readErr = groupStore.GetExecution(tx, accepted.ID)
		return readErr
	}); err != nil || !accepted.DeliveryProven || accepted.DeliveryStatus == nil || *accepted.DeliveryStatus != 1 {
		t.Fatalf("concurrent delivery=%+v err=%v", accepted, err)
	}

	var generation, fence int64
	var lease time.Time
	effectID, err := strconv.ParseInt(unknown.ExternalEffectID[len("eer_"):], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `UPDATE external_effects SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, effectID); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT generation,lease_fence,lease_expires_at FROM external_effects WHERE id=$1`, effectID).Scan(&generation, &fence, &lease); err != nil {
		t.Fatal(err)
	}
	evidenceDigest := string(effectport.Hash("journey-independent-evidence", unknown.ExternalEffectID))
	reconciled, err := runtimeService.ManualReconcile(ctx, groupopsport.ManualReconcileCommand{
		ExecutionID:    unknown.ID,
		ActorID:        actorID,
		IdempotencyKey: "journey-reconcile-key-0001",
		Generation:     generation,
		Fence:          fence,
		LeaseExpiresAt: lease,
		EvidenceDigest: evidenceDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.State != groupopsport.ExecutionReconciled || reconciled.DeliveryProven || !reconciled.ReconciliationEvidencePresent {
		t.Fatalf("reconciled=%+v", reconciled)
	}
	var finalEffectState string
	if err = native.QueryRow(ctx, `SELECT state FROM external_effects WHERE id=$1`, effectID).Scan(&finalEffectState); err != nil {
		t.Fatal(err)
	}
	if finalEffectState != string(effectport.StateReconciled) {
		t.Fatalf("EER state=%q", finalEffectState)
	}
}

type journeyStaff struct{}

func (journeyStaff) ListEligibleStaff(context.Context) ([]groupopsport.OperationMember, error) {
	return []groupopsport.OperationMember{{StaffID: 1, SenderUserID: "journey-sender", DisplayName: "Journey"}}, nil
}

type journeySender struct{}

func (journeySender) ResolveExecutionSender(_ context.Context, target string) (string, bool, error) {
	if target == "" {
		return "", false, nil
	}
	return "journey-sender", true, nil
}

type journeyEvidence struct{ status, calls int }

func (*journeyEvidence) VerifyReconciliationEvidence(_ context.Context, input groupopsport.ReconciliationEvidence) (groupopsport.ReconciliationEvidenceResult, error) {
	if input.ExecutionID < 1 || input.ExternalEffectID == "" || !effectport.ValidDigest(effectport.Digest(input.EvidenceDigest)) {
		return groupopsport.ReconciliationEvidenceResult{}, errors.New("invalid journey evidence")
	}
	return groupopsport.ReconciliationEvidenceResult{EvidenceDigest: input.EvidenceDigest}, nil
}

func (evidence *journeyEvidence) ReadProviderDelivery(_ context.Context, input groupopsport.ReconciliationEvidence) (groupopsport.GroupMessageReceipt, bool, error) {
	if input.ExecutionID < 1 || input.ExternalEffectID == "" {
		return groupopsport.GroupMessageReceipt{}, false, errors.New("invalid journey delivery")
	}
	evidence.calls++
	status := evidence.status
	return groupopsport.GroupMessageReceipt{ExecutionID: input.ExecutionID, ExternalEffectID: input.ExternalEffectID, MessageID: "journey-msgid", SenderUserID: "journey-sender", ChatID: "chat-journey-1", DeliveryStatus: &status, DeliveryEvidenceDigest: string(effectport.Hash("journey-delivery", input.ExternalEffectID))}, true, nil
}

type journeyReconciler struct {
	repository *externaleffects.Repository
}

func (reconciler journeyReconciler) ReconcileExternalEffect(ctx context.Context, command groupopsport.ExternalReconcileCommand) error {
	return reconciler.repository.ReconcileWithin(ctx, externaleffects.ControlCommand{
		EffectID:         command.EffectID,
		ReceiptKey:       externaleffects.Hash("journey-reconcile", command.EffectID, command.ReceiptKey),
		EvidenceDigest:   externaleffects.Digest(command.EvidenceDigest),
		ActorAdminUserID: command.ActorID,
		Generation:       command.Generation,
		Fence:            command.Fence,
		LeaseExpiresAt:   command.LeaseExpiresAt,
	})
}

type journeyProviderAdapter struct {
	result effectport.AdapterResult
}

func (adapter *journeyProviderAdapter) Execute(context.Context, effectport.Envelope, effectport.Attempt) (effectport.AdapterResult, error) {
	return adapter.result, nil
}

func newJourneyCompletionSink(projector groupopsport.RuntimeStore) (*journeyCompletionSink, error) {
	if projector == nil {
		return nil, errors.New("journey projector unavailable")
	}
	return &journeyCompletionSink{projector: projector}, nil
}

// journeyCompletionSink is intentionally test-local and mirrors the stable
// outbound owner projection: EER state is translated into local GroupOps
// execution state in the same transaction.
type journeyCompletionSink struct {
	projector groupopsport.RuntimeStore
}

func (sink *journeyCompletionSink) CompleteEffect(ctx context.Context, effectRef string, _ effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	state := groupopsport.ExecutionOutcomeUnknown
	switch result.Completion {
	case effectport.StateExecuted:
		state = groupopsport.ExecutionProviderAccepted
	case effectport.StateUnknown:
		state = groupopsport.ExecutionOutcomeUnknown
	default:
		return errors.New("unsupported journey completion")
	}
	return sink.projector.CompleteEffect(ctx, effectRef, state, result.Completion == effectport.StateExecuted, false, string(result.ReceiptDigest), attempt.Number, time.Now().UTC())
}

func createJourneyPlan(t *testing.T, ctx context.Context, uow *platformpostgres.UnitOfWork, repository *groupopsstore.Repository, actorID, inviteID int64) int64 {
	t.Helper()
	now := time.Now().UTC()
	var planID int64
	err := uow.Within(ctx, func(tx context.Context) error {
		var err error
		planID, err = repository.Create(tx, groupopsport.Plan{Name: "PG Journey", Status: groupopsport.PlanActive, Revision: 1, CreatedBy: actorID, UpdatedBy: actorID, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			return err
		}
		return repository.Save(tx, groupopsport.Detail{
			Plan:    groupopsport.Plan{ID: planID, Name: "PG Journey", Status: groupopsport.PlanActive, Revision: 1, CreatedBy: actorID, UpdatedBy: actorID, CreatedAt: now, UpdatedAt: now},
			Members: []groupopsport.Member{{StaffID: actorID}},
			GroupAssets: []groupopsport.GroupAsset{
				{AssetRef: "chat-journey-1"},
				{AssetRef: "chat-journey-2"},
			},
			Nodes: []groupopsport.Node{{Position: 1, Kind: groupopsport.NodeMessage, MessageText: "PG journey", MaterialPlan: groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{{Kind: "group_invite", ID: inviteID}}}}},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return planID
}

func groupOpsIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping Group Ops PostgreSQL Journey")
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
	rawSchema := make([]byte, 8)
	if _, err = rand.Read(rawSchema); err != nil {
		t.Fatal(err)
	}
	schema := "group_ops_journey_" + hex.EncodeToString(rawSchema)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	config = config.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(native), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		native.Close()
		admin.Close()
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate Group Ops Journey test")
	}
	for _, migration := range []string{"0003_access.sql", "0005_external_effects.sql", "0012_group_ops.sql", "0078_group_ops_provider_tasks.sql"} {
		sql, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "migrations", migration))
		if readErr != nil {
			native.Close()
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := native.Exec(ctx, string(sql)); execErr != nil {
			native.Close()
			admin.Close()
			t.Fatalf("%s: %v", migration, execErr)
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
