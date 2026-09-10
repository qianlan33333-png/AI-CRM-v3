package outbound

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
)

type materialTestEffects struct{}

func (materialTestEffects) AcceptAndQueueWithin(ctx context.Context, command effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return effectport.Projection{}, effectport.Receipt{}, err
	}
	raw, _ := json.Marshal(command.Envelope)
	var id int64
	if err = tx.QueryRow(ctx, `INSERT INTO material_test_effects(envelope,lane) VALUES($1,$2) RETURNING id`, raw, command.Lane).Scan(&id); err != nil {
		return effectport.Projection{}, effectport.Receipt{}, err
	}
	return effectport.Projection{ID: fmt.Sprintf("eer_%d", id), State: effectport.StateQueued}, effectport.Receipt{}, nil
}

type materialTestSources struct{ content map[string][]byte }

func (s materialTestSources) ListEnabledSourceSnapshots(context.Context, outboundport.MaterialSnapshotPageRequest) (outboundport.MaterialSnapshotPage, error) {
	return outboundport.MaterialSnapshotPage{Done: true}, nil
}

type materialRefreshTestSources struct {
	items []outboundport.MaterialSourceSnapshot
}

func (s materialRefreshTestSources) ListEnabledSourceSnapshots(_ context.Context, request outboundport.MaterialSnapshotPageRequest) (outboundport.MaterialSnapshotPage, error) {
	if request.Cursor != "" {
		return outboundport.MaterialSnapshotPage{Done: true}, nil
	}
	return outboundport.MaterialSnapshotPage{Items: s.items, Failures: []outboundport.MaterialSourceFailure{{SourceRef: "attachment:broken", FailureCode: "source_blob_missing"}}, Done: true}, nil
}
func (s materialRefreshTestSources) GetSourceSnapshot(context.Context, string) (outboundport.MaterialSourceSnapshot, error) {
	return outboundport.MaterialSourceSnapshot{}, outboundport.ErrMaterialSourceChanged
}
func (s materialRefreshTestSources) ReadSourceBytes(context.Context, outboundport.MaterialSourceSnapshot) (outboundport.MaterialSourceContent, error) {
	return outboundport.MaterialSourceContent{}, outboundport.ErrMaterialSourceChanged
}

type materialRefreshTestEnqueuer struct{}

func (materialRefreshTestEnqueuer) EnqueueMaterialRefreshWithin(ctx context.Context, roundID int64) (int64, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO material_test_refresh_jobs(round_id) VALUES($1) RETURNING id`, roundID).Scan(&id)
	return id, err
}
func (s materialTestSources) GetSourceSnapshot(context.Context, string) (outboundport.MaterialSourceSnapshot, error) {
	return outboundport.MaterialSourceSnapshot{}, outboundport.ErrMaterialSourceChanged
}
func (s materialTestSources) ReadSourceBytes(_ context.Context, source outboundport.MaterialSourceSnapshot) (outboundport.MaterialSourceContent, error) {
	content, ok := s.content[source.SourceRef]
	if !ok || sha256.Sum256(content) != source.ContentDigest || int64(len(content)) != source.SizeBytes {
		return outboundport.MaterialSourceContent{}, outboundport.ErrMaterialSourceChanged
	}
	return outboundport.MaterialSourceContent{Bytes: content, FileName: source.FileName, MediaType: source.MediaType}, nil
}

type materialTestUploader struct {
	mu    sync.Mutex
	calls int
	now   time.Time
}

type materialUnknownUploader struct{}

func (materialUnknownUploader) UploadMaterial(context.Context, outboundport.MaterialSourceSnapshot, outboundport.MaterialSourceContent, string) (outboundport.MaterialUploadReceipt, bool, error) {
	return outboundport.MaterialUploadReceipt{}, true, errors.New("response lost")
}

func (u *materialTestUploader) UploadMaterial(ctx context.Context, _ outboundport.MaterialSourceSnapshot, _ outboundport.MaterialSourceContent, _ string) (outboundport.MaterialUploadReceipt, bool, error) {
	if _, err := platformpostgres.RequireTransaction(ctx); err == nil {
		return outboundport.MaterialUploadReceipt{}, false, errors.New("provider called inside transaction")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls++
	return outboundport.MaterialUploadReceipt{MediaID: "shared-provider-media", ProviderCreatedAt: u.now}, true, nil
}

func TestMaterialSharedImageRefreshFailureAndUnknownPostgreSQL(t *testing.T) {
	ctx := context.Background()
	native, cleanup := materialTestDatabase(t, ctx)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("same immutable image")
	digest := sha256.Sum256(content)
	first := outboundport.MaterialSourceSnapshot{SourceRef: "image:1", SourceType: "image", ContentDigest: digest, FileName: "first.png", MediaType: "image/png", SizeBytes: int64(len(content)), SnapshotVersion: 1}
	second := first
	second.SourceRef = "image:2"
	second.FileName = "renamed.png"
	sources := materialTestSources{content: map[string][]byte{first.SourceRef: content, second.SourceRef: content}}
	service, err := NewMaterialPreparationService(uow, materialTestEffects{}, native, sources)
	if err != nil {
		t.Fatal(err)
	}
	providerTime := time.Date(2026, 9, 10, 2, 0, 1, 0, time.UTC)
	service.now = func() time.Time { return providerTime }
	scope := string(effectport.Hash("material-test-scope"))

	results := make(chan outboundport.MaterialResult, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, source := range []outboundport.MaterialSourceSnapshot{first, second} {
		wg.Add(1)
		go func(source outboundport.MaterialSourceSnapshot) {
			defer wg.Done()
			result, prepareErr := service.Prepare(ctx, outboundport.MaterialRequest{MaterialSourceSnapshot: source, CorpScopeDigest: scope})
			results <- result
			errs <- prepareErr
		}(source)
	}
	wg.Wait()
	close(results)
	close(errs)
	for prepareErr := range errs {
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
	}
	var effectID string
	for result := range results {
		if effectID == "" {
			effectID = result.EffectID
		} else if result.EffectID != effectID {
			t.Fatalf("same image produced effects %s and %s", effectID, result.EffectID)
		}
	}
	var accepted int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM material_test_effects`).Scan(&accepted); err != nil || accepted != 1 {
		t.Fatalf("accepted=%d err=%v", accepted, err)
	}
	var preparationID int64
	var acceptedSource outboundport.MaterialSourceSnapshot
	var acceptedDigest []byte
	if err = native.QueryRow(ctx, `SELECT id,source_ref,source_type,content_digest,file_name,media_type,size_bytes,source_version FROM outbound_material_preparations WHERE effect_id=$1`, effectID).Scan(&preparationID, &acceptedSource.SourceRef, &acceptedSource.SourceType, &acceptedDigest, &acceptedSource.FileName, &acceptedSource.MediaType, &acceptedSource.SizeBytes, &acceptedSource.SnapshotVersion); err != nil {
		t.Fatal(err)
	}
	copy(acceptedSource.ContentDigest[:], acceptedDigest)
	uploader := &materialTestUploader{now: providerTime}
	provider, err := NewMaterialPreparationProvider(service, uploader)
	if err != nil {
		t.Fatal(err)
	}
	request := outboundport.MaterialRequest{MaterialSourceSnapshot: acceptedSource, CorpScopeDigest: scope}
	envelope := materialEnvelope(preparationID, request)
	attempt := effectport.Attempt{EffectID: effectID, Number: 1, Generation: 1, Fence: 1}
	result, err := provider.Execute(ctx, envelope, attempt)
	if err != nil || result.Completion != effectport.StateExecuted || uploader.calls != 1 {
		t.Fatalf("completion=%s uploads=%d err=%v", result.Completion, uploader.calls, err)
	}
	if err = uow.Within(ctx, func(txctx context.Context) error {
		return service.CompleteEffect(txctx, effectID, envelope, attempt, result)
	}); err != nil {
		t.Fatal(err)
	}
	nearExpiry := providerTime.Add(72*time.Hour - 10*time.Second)
	service.now = func() time.Time { return nearExpiry }
	marginPreparation, err := service.ReadyForSend(ctx, outboundport.MaterialRequest{MaterialSourceSnapshot: first, CorpScopeDigest: scope, ValidThrough: nearExpiry.Add(30 * time.Second)})
	if err != nil || marginPreparation.State != "queued" || marginPreparation.EffectID == effectID {
		t.Fatalf("near-expiry credential was sent without refresh margin: %+v err=%v", marginPreparation, err)
	}
	if err = completeMaterialTestEffect(ctx, uow, service, native, first, scope, marginPreparation.EffectID, effectport.StateCancelled, ""); err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return providerTime }

	refresh, err := service.Prepare(ctx, outboundport.MaterialRequest{MaterialSourceSnapshot: second, CorpScopeDigest: scope, ForceRefresh: true, RoundDate: "2026-09-11"})
	if err != nil || refresh.EffectID == effectID {
		t.Fatalf("force refresh=%+v err=%v", refresh, err)
	}
	if err = completeMaterialTestEffect(ctx, uow, service, native, second, scope, refresh.EffectID, effectport.StateFinalFailed, "provider_rejected"); err != nil {
		t.Fatal(err)
	}
	status, err := service.GetMaterialStatus(ctx, first, scope)
	if err != nil || status.State != "final_failed" || status.MediaID != "shared-provider-media" || status.CredentialState != "ready" || !status.CredentialUsable {
		t.Fatalf("failed refresh did not retain usable credential: %+v err=%v", status, err)
	}
	service.now = func() time.Time { return providerTime.Add(73 * time.Hour) }
	status, err = service.GetMaterialStatus(ctx, second, scope)
	if err != nil || status.CredentialState != "expired" || status.CredentialUsable || status.MediaID == "" {
		t.Fatalf("expired credential status=%+v err=%v", status, err)
	}

	unknown, err := service.Prepare(ctx, outboundport.MaterialRequest{MaterialSourceSnapshot: first, CorpScopeDigest: scope, ForceRefresh: true, RoundDate: "2026-09-14"})
	if err != nil {
		t.Fatal(err)
	}
	if err = completeMaterialTestEffect(ctx, uow, service, native, first, scope, unknown.EffectID, effectport.StateUnknown, "upload_outcome_unknown"); err != nil {
		t.Fatal(err)
	}
	replay, err := service.Prepare(ctx, outboundport.MaterialRequest{MaterialSourceSnapshot: second, CorpScopeDigest: scope, ForceRefresh: true, RoundDate: "2026-09-15"})
	if err != nil || replay.EffectID != unknown.EffectID || replay.State != "outcome_unknown" {
		t.Fatalf("unknown was bypassed across date: %+v err=%v", replay, err)
	}
	_, err = service.ReadyForSend(ctx, outboundport.MaterialRequest{MaterialSourceSnapshot: first, CorpScopeDigest: scope})
	var terminal outboundport.MediaPreparationTerminalError
	if !errors.As(err, &terminal) || terminal.State != "outcome_unknown" {
		t.Fatalf("ready-for-send unknown err=%v", err)
	}
	if err = completeMaterialTestEffect(ctx, uow, service, native, first, scope, unknown.EffectID, effectport.StateReconciled, ""); err == nil {
		t.Fatal("generic reconciliation released a media upload without explicit no-effect evidence")
	}
	afterReconcile, err := service.Prepare(ctx, outboundport.MaterialRequest{MaterialSourceSnapshot: first, CorpScopeDigest: scope, ForceRefresh: true, RoundDate: "2026-09-16"})
	if err != nil || afterReconcile.EffectID != unknown.EffectID || afterReconcile.State != "outcome_unknown" {
		t.Fatalf("generic reconciliation bypassed unknown: %+v err=%v", afterReconcile, err)
	}
	var unresolvedState string
	if err = native.QueryRow(ctx, `SELECT state FROM outbound_material_preparations WHERE effect_id=$1`, unknown.EffectID).Scan(&unresolvedState); err != nil || unresolvedState != "outcome_unknown" {
		t.Fatalf("unresolved state=%s err=%v", unresolvedState, err)
	}
	if err = completeMaterialTestEffect(ctx, uow, service, native, first, scope, unknown.EffectID, effectport.StateReconciled, "reconciled_no_effect"); err != nil {
		t.Fatal(err)
	}
	confirmed, err := service.Prepare(ctx, outboundport.MaterialRequest{MaterialSourceSnapshot: first, CorpScopeDigest: scope, ForceRefresh: true, RoundDate: "2026-09-17"})
	if err != nil || confirmed.EffectID == unknown.EffectID {
		t.Fatalf("no-effect reconciliation did not release cache: %+v err=%v", confirmed, err)
	}
	if err = completeMaterialTestEffect(ctx, uow, service, native, first, scope, confirmed.EffectID, effectport.StateUnknown, "upload_outcome_unknown"); err != nil {
		t.Fatal(err)
	}
	if err = completeMaterialConfirmedEffect(ctx, uow, service, native, scope, confirmed.EffectID, "invalid-future", service.now().UTC().Add(24*time.Hour)); err == nil {
		t.Fatal("future ProviderCreatedAt accepted")
	}
	validCreatedAt := service.now().UTC().Add(-80 * time.Hour)
	if err = completeMaterialConfirmedEffect(ctx, uow, service, native, scope, confirmed.EffectID, "confirmed-historical", validCreatedAt); err != nil {
		t.Fatal(err)
	}
	status, err = service.GetMaterialStatus(ctx, first, scope)
	if err != nil || status.MediaID != "confirmed-historical" || status.CredentialState != "expired" || status.CredentialUsable {
		t.Fatalf("historical confirmed credential status=%+v err=%v", status, err)
	}
	retryCase, err := service.Prepare(ctx, outboundport.MaterialRequest{MaterialSourceSnapshot: first, CorpScopeDigest: scope, ForceRefresh: true, RoundDate: "2026-09-18"})
	if err != nil {
		t.Fatal(err)
	}
	if err = completeMaterialTestEffect(ctx, uow, service, native, first, scope, retryCase.EffectID, effectport.StateRetryable, "45009"); err != nil {
		t.Fatal(err)
	}
	if err = completeMaterialTestEffect(ctx, uow, service, native, first, scope, retryCase.EffectID, effectport.StateQueued, "45009"); err != nil {
		t.Fatal(err)
	}
	if err = completeMaterialTestEffect(ctx, uow, service, native, first, scope, retryCase.EffectID, effectport.StateCancelled, ""); err != nil {
		t.Fatal(err)
	}
	var cancelledState, cancelledCode string
	if err = native.QueryRow(ctx, `SELECT state,failure_code FROM outbound_material_preparations WHERE effect_id=$1`, retryCase.EffectID).Scan(&cancelledState, &cancelledCode); err != nil || cancelledState != "cancelled" || cancelledCode != "cancelled" {
		t.Fatalf("cancelled preparation state=%s code=%s err=%v", cancelledState, cancelledCode, err)
	}
	afterCancel, err := service.Prepare(ctx, outboundport.MaterialRequest{MaterialSourceSnapshot: first, CorpScopeDigest: scope, ForceRefresh: true, RoundDate: "2026-09-19"})
	if err != nil || afterCancel.EffectID == retryCase.EffectID {
		t.Fatalf("cancel did not release preparation: %+v err=%v", afterCancel, err)
	}
}

func TestMaterialDailyCatchUpRoundMembershipAndRestartPostgreSQL(t *testing.T) {
	ctx := context.Background()
	native, cleanup := materialTestDatabase(t, ctx)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("daily shared image")
	digest := sha256.Sum256(content)
	first := outboundport.MaterialSourceSnapshot{SourceRef: "image:10", SourceType: "image", ContentDigest: digest, FileName: "one.png", MediaType: "image/png", SizeBytes: int64(len(content)), SnapshotVersion: 1}
	second := first
	second.SourceRef = "image:11"
	second.FileName = "two.png"
	sources := materialRefreshTestSources{items: []outboundport.MaterialSourceSnapshot{first, second}}
	service, err := NewMaterialPreparationService(uow, materialTestEffects{}, native, sources)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.BindRefreshEnqueuer(materialRefreshTestEnqueuer{}); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 9, 10, 1, 59, 59, 0, loc)
	service.now = func() time.Time { return now }
	if err = service.EnsureDailyRefresh(ctx); err != nil {
		t.Fatal(err)
	}
	var rounds int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM outbound_material_refresh_rounds`).Scan(&rounds); err != nil || rounds != 0 {
		t.Fatalf("before 02:00 rounds=%d err=%v", rounds, err)
	}
	now = time.Date(2026, 9, 10, 3, 0, 0, 0, loc)
	if err = service.EnsureDailyRefresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err = service.EnsureDailyRefresh(ctx); err != nil {
		t.Fatal(err)
	}
	var jobs int
	if err = native.QueryRow(ctx, `SELECT count(*),count(DISTINCT round_id) FROM material_test_refresh_jobs`).Scan(&jobs, &rounds); err != nil || jobs != 1 || rounds != 1 {
		t.Fatalf("same-day restart jobs=%d rounds=%d err=%v", jobs, rounds, err)
	}
	round, found, err := service.GetTodayRefreshRound(ctx)
	if err != nil || !found {
		t.Fatalf("today round found=%v err=%v", found, err)
	}
	worker := NewMaterialRefreshWorker(service, string(effectport.Hash("material-round-scope")))
	if err = worker.Work(ctx, &river.Job[MaterialRefreshJobArgs]{Args: MaterialRefreshJobArgs{RoundID: round.ID}}); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT total,queued,failed,state FROM outbound_material_refresh_rounds WHERE id=$1`, round.ID).Scan(&round.Total, &round.Queued, &round.Failed, &round.State); err != nil {
		t.Fatal(err)
	}
	if round.Total != 2 || round.Queued != 1 || round.Failed != 1 || round.State != "waiting" {
		t.Fatalf("round membership=%+v", round)
	}
	var effectID string
	if err = native.QueryRow(ctx, `SELECT preparation_effect_id FROM outbound_material_refresh_items WHERE round_id=$1 AND preparation_effect_id IS NOT NULL`, round.ID).Scan(&effectID); err != nil {
		t.Fatal(err)
	}
	if err = completeMaterialTestEffect(ctx, uow, service, native, first, string(effectport.Hash("material-round-scope")), effectID, effectport.StateExecuted, "uploaded"); err != nil {
		t.Fatal(err)
	}
	if err = worker.Work(ctx, &river.Job[MaterialRefreshJobArgs]{Args: MaterialRefreshJobArgs{RoundID: round.ID}}); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT succeeded,failed,state FROM outbound_material_refresh_rounds WHERE id=$1`, round.ID).Scan(&round.Succeeded, &round.Failed, &round.State); err != nil || round.Succeeded != 1 || round.Failed != 1 || round.State != "completed_with_failures" {
		t.Fatalf("completed round=%+v err=%v", round, err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM material_test_effects`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("restart duplicated preparation effects=%d err=%v", jobs, err)
	}
	now = time.Date(2026, 9, 11, 2, 0, 0, 0, loc)
	if err = service.EnsureDailyRefresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM outbound_material_refresh_rounds`).Scan(&rounds); err != nil || rounds != 2 {
		t.Fatalf("next-day catch-up rounds=%d err=%v", rounds, err)
	}
}

func TestMaterialAdminOperationKeyConcurrentDriftPostgreSQL(t *testing.T) {
	ctx := context.Background()
	native, cleanup := materialTestDatabase(t, ctx)
	defer cleanup()
	wrapped, err := platformpostgres.Wrap(native, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewMaterialPreparationService(uow, materialTestEffects{}, native, materialTestSources{})
	if err != nil {
		t.Fatal(err)
	}
	scope := string(effectport.Hash("operation-key-scope"))
	makeSource := func(ref, body string) outboundport.MaterialSourceSnapshot {
		digest := sha256.Sum256([]byte(body))
		return outboundport.MaterialSourceSnapshot{SourceRef: ref, SourceType: "file", ContentDigest: digest, FileName: ref + ".pdf", MediaType: "application/pdf", SizeBytes: int64(len(body)), SnapshotVersion: 1}
	}
	requests := []outboundport.MaterialRequest{
		{MaterialSourceSnapshot: makeSource("attachment:1", "first"), CorpScopeDigest: scope, ForceRefresh: true, ActorAdminID: 9, OperationKey: "same-admin-operation"},
		{MaterialSourceSnapshot: makeSource("attachment:2", "second"), CorpScopeDigest: scope, ForceRefresh: true, ActorAdminID: 9, OperationKey: "same-admin-operation"},
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, request := range requests {
		wg.Add(1)
		go func(request outboundport.MaterialRequest) {
			defer wg.Done()
			_, prepareErr := service.Prepare(ctx, request)
			results <- prepareErr
		}(request)
	}
	wg.Wait()
	close(results)
	var succeeded, conflicted int
	for resultErr := range results {
		switch {
		case resultErr == nil:
			succeeded++
		case errors.Is(resultErr, outboundport.ErrMaterialOperationConflict):
			conflicted++
		default:
			t.Fatalf("unexpected operation result: %v", resultErr)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("succeeded=%d conflicted=%d", succeeded, conflicted)
	}
	if _, err = service.Prepare(ctx, outboundport.MaterialRequest{MaterialSourceSnapshot: requests[0].MaterialSourceSnapshot, CorpScopeDigest: scope, ActorAdminID: 9, OperationKey: "force-required"}); !errors.Is(err, outboundport.ErrInvalidMaterialRequest) {
		t.Fatalf("admin force=false err=%v", err)
	}
}

func completeMaterialTestEffect(ctx context.Context, uow *platformpostgres.UnitOfWork, service *MaterialPreparationService, pool *pgxpool.Pool, _ outboundport.MaterialSourceSnapshot, scope, effectID string, completion effectport.State, code string) error {
	var id int64
	var source outboundport.MaterialSourceSnapshot
	var digest []byte
	if err := pool.QueryRow(ctx, `SELECT id,source_ref,source_type,content_digest,file_name,media_type,size_bytes,source_version FROM outbound_material_preparations WHERE effect_id=$1`, effectID).Scan(&id, &source.SourceRef, &source.SourceType, &digest, &source.FileName, &source.MediaType, &source.SizeBytes, &source.SnapshotVersion); err != nil {
		return err
	}
	copy(source.ContentDigest[:], digest)
	envelope := materialEnvelope(id, outboundport.MaterialRequest{MaterialSourceSnapshot: source, CorpScopeDigest: scope})
	attempt := effectport.Attempt{EffectID: effectID, Number: 1, Generation: 1, Fence: 1}
	result := effectport.AdapterResult{Completion: completion, ReceiptDigest: effectport.Hash("test-completion", effectID, string(completion)), FailureCode: code}
	if completion == effectport.StateExecuted {
		receipt := outboundport.MaterialUploadReceipt{MediaID: "round-provider-media", ProviderCreatedAt: service.now().UTC()}
		raw, _ := json.Marshal(receipt)
		result.Artifact = effectport.ResultArtifact{Kind: "outbound.material.upload.v1", Payload: raw}
		result.Artifact.Digest = effectport.Hash("external-effect.artifact.v1", result.Artifact.Kind, string(raw))
	}
	return uow.Within(ctx, func(txctx context.Context) error {
		return service.CompleteEffect(txctx, effectID, envelope, attempt, result)
	})
}

func completeMaterialConfirmedEffect(ctx context.Context, uow *platformpostgres.UnitOfWork, service *MaterialPreparationService, pool *pgxpool.Pool, scope, effectID, mediaID string, createdAt time.Time) error {
	var id int64
	var source outboundport.MaterialSourceSnapshot
	var digest []byte
	if err := pool.QueryRow(ctx, `SELECT id,source_ref,source_type,content_digest,file_name,media_type,size_bytes,source_version FROM outbound_material_preparations WHERE effect_id=$1`, effectID).Scan(&id, &source.SourceRef, &source.SourceType, &digest, &source.FileName, &source.MediaType, &source.SizeBytes, &source.SnapshotVersion); err != nil {
		return err
	}
	copy(source.ContentDigest[:], digest)
	envelope := materialEnvelope(id, outboundport.MaterialRequest{MaterialSourceSnapshot: source, CorpScopeDigest: scope})
	receipt := outboundport.MaterialUploadReceipt{MediaID: mediaID, ProviderCreatedAt: createdAt}
	raw, _ := json.Marshal(receipt)
	artifact := effectport.ResultArtifact{Kind: "outbound.material.upload.v1", Payload: raw}
	artifact.Digest = effectport.Hash("external-effect.artifact.v1", artifact.Kind, string(raw))
	result := effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("confirmed-reconciliation", effectID, mediaID), Artifact: artifact}
	return uow.Within(ctx, func(txctx context.Context) error {
		return service.CompleteEffect(txctx, effectID, envelope, effectport.Attempt{EffectID: effectID, Number: 1, Generation: 1, Fence: 1}, result)
	})
}

func materialTestDatabase(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()
	dsn, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL not configured")
	}
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("material_%d", time.Now().UnixNano())
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "0125_outbound_material_preparation.sql"))
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(raw), "CREATE TABLE outbound_material_preparations")
	if start < 0 {
		t.Fatal("material migration body missing")
	}
	if _, err = pool.Exec(ctx, string(raw)[start:]+`; CREATE TABLE material_test_effects(id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,envelope JSONB NOT NULL,lane TEXT NOT NULL); CREATE TABLE material_test_refresh_jobs(id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,round_id BIGINT NOT NULL);`); err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+ident+" CASCADE")
		admin.Close()
	}
}
