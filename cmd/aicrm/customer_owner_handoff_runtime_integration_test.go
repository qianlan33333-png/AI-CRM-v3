package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	customer "github.com/qianlan33333-png/AI-CRM-v3/internal/customer"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"github.com/riverqueue/river"
)

type ownerHandoffRuntimeResolver struct {
	candidate  customerport.OwnerHandoffCandidate
	candidates []customerport.OwnerHandoffCandidate
}

func (resolver ownerHandoffRuntimeResolver) ResolveOwnerHandoffCandidates(_ context.Context, _ customerport.OwnerHandoffMode, _, _ int64, _ string, ids []customerdomain.CustomerID) ([]customerport.OwnerHandoffCandidate, error) {
	candidates := resolver.candidates
	if candidates == nil {
		candidates = []customerport.OwnerHandoffCandidate{resolver.candidate}
	}
	if len(ids) != len(candidates) {
		return nil, customer.ErrOwnerHandoffConflict
	}
	for index := range ids {
		if ids[index] != candidates[index].CustomerID {
			return nil, customer.ErrOwnerHandoffConflict
		}
	}
	return append([]customerport.OwnerHandoffCandidate(nil), candidates...), nil
}

type stopAfterOwnerHandoffSegment struct {
	service *customerapp.OwnerHandoffService
	stop    func()
}

func (worker stopAfterOwnerHandoffSegment) ProcessOwnerHandoffBatch(ctx context.Context, batchID string, segment int64) error {
	err := worker.service.ProcessOwnerHandoffBatch(ctx, batchID, segment)
	if err == nil && segment == 0 && worker.stop != nil {
		worker.stop()
	}
	return err
}

type ownerHandoffRuntimeWriter struct {
	mu    sync.Mutex
	calls []struct{ source, target, external, welcome string }
}

func (writer *ownerHandoffRuntimeWriter) TransferCustomer(_ context.Context, source, target string, external []string, welcome string) (wecomport.CustomerTransferResult, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(external) != 1 {
		return wecomport.CustomerTransferResult{}, fmt.Errorf("unexpected transfer target count")
	}
	writer.calls = append(writer.calls, struct{ source, target, external, welcome string }{source, target, external[0], welcome})
	return wecomport.CustomerTransferResult{AcceptedExternalUserIDs: []string{external[0]}}, nil
}

func (*ownerHandoffRuntimeWriter) TransferResult(context.Context, string, string, string) (wecomport.CustomerTransferResult, error) {
	return wecomport.CustomerTransferResult{}, nil
}

// TestCustomerOwnerHandoffRiverExecutesFrozenTransferThenLocalCAS covers the
// actual bounded path: Customer UoW accepts/binds one EER intent, a restarted
// River runtime executes outbound after that commit, and completion atomically
// projects only the accepted frozen target as the local CRM owner.
func TestCustomerOwnerHandoffRiverExecutesFrozenTransferThenLocalCAS(t *testing.T) {
	native, cleanup := channelWelcomeIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate owner-handoff runtime migration")
	}
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "migrations", "0092_customer_owner_handoff.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	if err = river.AddWorkerSafely[customer.OwnerHandoffBatchJobArgs](workers, customer.NewOwnerHandoffBatchWorker()); err != nil {
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
	batchEnqueuer, err := customer.NewRiverOwnerHandoffEnqueuer(insert)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := customer.NewOwnerHandoffCipher("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	store := customer.NewPostgreSQLOwnerHandoffStoreWithCipher(cipher)
	staff := accessstore.NewPostgreSQL()
	var sourceID, targetID int64
	var customerID customerdomain.CustomerID
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, txErr := platformpostgres.RequireTransaction(txctx)
		if txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active) VALUES('handoff-runtime-source','$argon2id$fixture','Former','former-user',false) RETURNING id`).Scan(&sourceID); txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active) VALUES('handoff-runtime-target','$argon2id$fixture','Next','next-user',true) RETURNING id`).Scan(&targetID); txErr != nil {
			return txErr
		}
		return tx.QueryRow(txctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID)
	}); err != nil {
		t.Fatal(err)
	}
	candidate := customerport.OwnerHandoffCandidate{CustomerID: customerID, State: "ready", RelationshipDigest: sha256.Sum256([]byte("runtime-trusted-relation")), SourceUserID: "former-user", TargetUserID: "next-user", ExternalUserID: "external-runtime"}
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	service, err := customerapp.NewOwnerHandoffService(uow, store, staff, ownerHandoffRuntimeResolver{candidate: candidate}, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetExternalEffectAccepter(effects); err != nil {
		t.Fatal(err)
	}
	if err = service.SetBatchEnqueuer(batchEnqueuer); err != nil {
		t.Fatal(err)
	}
	service.SetWeComProviderEnabled(true)
	preview, err := service.PreviewOwnerHandoff(ctx, customerport.OwnerHandoffPreviewCommand{ActorAdminUserID: sourceID, Mode: customerport.OwnerHandoffWeComThenCRM, SourceStaffID: sourceID, TargetStaffID: targetID, CorpScope: "wecom-corp:runtime", CustomerIDs: []customerdomain.CustomerID{customerID}, WelcomeMessage: "欢迎", ConfirmationPhrase: "CONFIRM", IdempotencyKey: "runtime-preview-key"})
	if err != nil {
		t.Fatal(err)
	}
	// A second independently generated preview sees the same still-current
	// relation. Confirm both concurrently: the Customer row lock must allow
	// exactly one EER acceptance, rather than issue transfer_customer twice.
	secondPreview, err := service.PreviewOwnerHandoff(ctx, customerport.OwnerHandoffPreviewCommand{ActorAdminUserID: sourceID, Mode: customerport.OwnerHandoffWeComThenCRM, SourceStaffID: sourceID, TargetStaffID: targetID, CorpScope: "wecom-corp:runtime", CustomerIDs: []customerdomain.CustomerID{customerID}, WelcomeMessage: "欢迎", ConfirmationPhrase: "CONFIRM", IdempotencyKey: "runtime-preview-key-second"})
	if err != nil {
		t.Fatal(err)
	}
	type confirmation struct {
		batch customerport.OwnerHandoffBatch
		err   error
	}
	start := make(chan struct{})
	results := make(chan confirmation, 2)
	for _, input := range []struct {
		preview customerport.OwnerHandoffPreview
		key     string
	}{{preview: preview, key: "runtime-confirm-key-first"}, {preview: secondPreview, key: "runtime-confirm-key-second"}} {
		input := input
		go func() {
			<-start
			batch, confirmErr := service.ConfirmOwnerHandoff(ctx, customerport.OwnerHandoffConfirmCommand{ActorAdminUserID: sourceID, PreviewID: input.preview.ID, PreviewHash: input.preview.Hash, ConfirmationPhrase: "CONFIRM", IdempotencyKey: input.key})
			results <- confirmation{batch: batch, err: confirmErr}
		}()
	}
	close(start)
	var batch customerport.OwnerHandoffBatch
	accepted, conflicts := 0, 0
	for range 2 {
		result := <-results
		if result.err == nil {
			accepted++
			batch = result.batch
			continue
		}
		if errors.Is(result.err, customer.ErrOwnerHandoffConflict) {
			conflicts++
			continue
		}
		t.Fatalf("concurrent confirmation: %v", result.err)
	}
	if accepted != 1 || conflicts != 1 || len(batch.Lines) != 1 || batch.Lines[0].State != "queued" || batch.Lines[0].EffectID != "" {
		t.Fatalf("accepted=%d conflicts=%d batch=%+v", accepted, conflicts, batch)
	}
	var acceptedEffects int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects WHERE kind='customer_owner_handoff'`).Scan(&acceptedEffects); err != nil || acceptedEffects != 0 {
		t.Fatalf("handoff effects before batch worker=%d err=%v", acceptedEffects, err)
	}
	writer := &ownerHandoffRuntimeWriter{}
	provider, err := outbound.NewCustomerOwnerHandoffProvider(customerOwnerHandoffExecutionAdapter{uow: uow, executions: store, staff: staff}, writer)
	if err != nil {
		t.Fatal(err)
	}
	workers = river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(effects, provider)); err != nil {
		t.Fatal(err)
	}
	runtimeBatchWorker := customer.NewOwnerHandoffBatchWorker()
	if err = runtimeBatchWorker.Bind(service); err != nil {
		t.Fatal(err)
	}
	if err = river.AddWorkerSafely[customer.OwnerHandoffBatchJobArgs](workers, runtimeBatchWorker); err != nil {
		t.Fatal(err)
	}
	completion, err := outbound.NewCustomerOwnerHandoffCompletionSink(store)
	if err != nil {
		t.Fatal(err)
	}
	if err = effects.SetCompletionSink(completion); err != nil {
		t.Fatal(err)
	}
	runtimeService, err := platformjobqueue.NewRuntime(native, workers, platformjobqueue.OutboundQueue, customer.OwnerHandoffQueue)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stopRun := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- runtimeService.Run(runCtx) }()
	defer func() {
		stopRun()
		select {
		case runErr := <-done:
			if runErr != nil && runErr != context.Canceled {
				t.Errorf("runtime stop: %v", runErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("owner-handoff runtime did not stop")
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var lineState, effectState string
		var ownerStaff int64
		err = native.QueryRow(ctx, `SELECT l.state,e.state,lo.staff_id
			FROM customer_owner_handoff_lines l
			JOIN external_effects e ON e.id=substring(l.effect_id FROM 5)::bigint
			JOIN customer_local_owners lo ON lo.customer_id=l.customer_id
			WHERE l.batch_id=$1`, batch.ID).Scan(&lineState, &effectState, &ownerStaff)
		if err == nil && lineState == "provider_accepted" && effectState == string(effectport.StateExecuted) && ownerStaff == targetID {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	var lineState, effectState string
	var ownerStaff int64
	if err = native.QueryRow(ctx, `SELECT l.state,e.state,lo.staff_id
		FROM customer_owner_handoff_lines l
		JOIN external_effects e ON e.id=substring(l.effect_id FROM 5)::bigint
		JOIN customer_local_owners lo ON lo.customer_id=l.customer_id
		WHERE l.batch_id=$1`, batch.ID).Scan(&lineState, &effectState, &ownerStaff); err != nil || lineState != "provider_accepted" || effectState != string(effectport.StateExecuted) || ownerStaff != targetID {
		t.Fatalf("completion line=%s effect=%s owner=%d err=%v", lineState, effectState, ownerStaff, err)
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.calls) != 1 || writer.calls[0].source != "former-user" || writer.calls[0].target != "next-user" || writer.calls[0].external != "external-runtime" || writer.calls[0].welcome != "欢迎" {
		t.Fatalf("provider calls=%+v", writer.calls)
	}
}

var _ wecomport.CustomerTransferWriter = (*ownerHandoffRuntimeWriter)(nil)
var _ pgx.Tx

// TestCustomerOwnerHandoffRiverSegmentsLocalOnly101 verifies the documented
// 100-row bound with the actual River runtime. local_only still has no
// provider/EER write, but each segment commits its local CAS/audit/outbox facts
// before it atomically creates the following River job.
func TestCustomerOwnerHandoffRiverSegmentsLocalOnly101(t *testing.T) {
	native, cleanup := channelWelcomeIntegrationPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate owner-handoff runtime migration")
	}
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "migrations", "0092_customer_owner_handoff.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[customer.OwnerHandoffBatchJobArgs](workers, customer.NewOwnerHandoffBatchWorker()); err != nil {
		t.Fatal(err)
	}
	insert, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer, err := customer.NewRiverOwnerHandoffEnqueuer(insert)
	if err != nil {
		t.Fatal(err)
	}
	store := customer.NewPostgreSQLOwnerHandoffStore()
	staff := accessstore.NewPostgreSQL()
	var sourceID, targetID int64
	ids := make([]customerdomain.CustomerID, 0, 101)
	candidates := make([]customerport.OwnerHandoffCandidate, 0, 101)
	if err = uow.Within(ctx, func(txctx context.Context) error {
		tx, txErr := platformpostgres.RequireTransaction(txctx)
		if txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active) VALUES('handoff-segment-source','$argon2id$fixture','Former','segment-former',false) RETURNING id`).Scan(&sourceID); txErr != nil {
			return txErr
		}
		if txErr = tx.QueryRow(txctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active) VALUES('handoff-segment-target','$argon2id$fixture','Next','segment-next',true) RETURNING id`).Scan(&targetID); txErr != nil {
			return txErr
		}
		for index := 0; index < 101; index++ {
			var customerID customerdomain.CustomerID
			if txErr = tx.QueryRow(txctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); txErr != nil {
				return txErr
			}
			ids = append(ids, customerID)
			candidates = append(candidates, customerport.OwnerHandoffCandidate{CustomerID: customerID, State: "ready", RelationshipDigest: sha256.Sum256([]byte(fmt.Sprintf("segment-%d", index)))})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	audit, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		t.Fatal(err)
	}
	service, err := customerapp.NewOwnerHandoffService(uow, store, staff, ownerHandoffRuntimeResolver{candidates: candidates}, audit, platformoutbox.NewPostgreSQL())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetBatchEnqueuer(enqueuer); err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewOwnerHandoff(ctx, customerport.OwnerHandoffPreviewCommand{ActorAdminUserID: sourceID, Mode: customerport.OwnerHandoffLocalOnly, SourceStaffID: sourceID, TargetStaffID: targetID, CorpScope: "wecom-corp:runtime", CustomerIDs: ids, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "segment-preview-101"})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := service.ConfirmOwnerHandoff(ctx, customerport.OwnerHandoffConfirmCommand{ActorAdminUserID: sourceID, PreviewID: preview.ID, PreviewHash: preview.Hash, ConfirmationPhrase: "CONFIRM", IdempotencyKey: "segment-confirm-101"})
	if err != nil || len(batch.Lines) != 101 || batch.State != "accepted" {
		t.Fatalf("accept batch=%+v err=%v", batch, err)
	}
	runCtx, stopRun := context.WithCancel(ctx)
	// Stop immediately after the first committed segment. The second durable
	// River job already exists at that point, so a fresh runtime must resume it.
	firstWorker := customer.NewOwnerHandoffBatchWorker()
	if err = firstWorker.Bind(stopAfterOwnerHandoffSegment{service: service, stop: stopRun}); err != nil {
		t.Fatal(err)
	}
	workers = river.NewWorkers()
	if err = river.AddWorkerSafely[customer.OwnerHandoffBatchJobArgs](workers, firstWorker); err != nil {
		t.Fatal(err)
	}
	firstRuntime, err := platformjobqueue.NewRuntime(native, workers, customer.OwnerHandoffQueue)
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- firstRuntime.Run(runCtx) }()
	select {
	case runErr := <-firstDone:
		if runErr != nil && runErr != context.Canceled {
			t.Fatalf("first runtime stop: %v", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first owner-handoff segment did not stop")
	}
	var firstUpdated, firstQueued int
	if err = native.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state='local_updated'),count(*) FILTER (WHERE state='queued') FROM customer_owner_handoff_lines WHERE batch_id=$1`, batch.ID).Scan(&firstUpdated, &firstQueued); err != nil || firstUpdated != 100 || firstQueued != 1 {
		t.Fatalf("first segment updated=%d queued=%d err=%v", firstUpdated, firstQueued, err)
	}
	restartWorker := customer.NewOwnerHandoffBatchWorker()
	if err = restartWorker.Bind(service); err != nil {
		t.Fatal(err)
	}
	workers = river.NewWorkers()
	if err = river.AddWorkerSafely[customer.OwnerHandoffBatchJobArgs](workers, restartWorker); err != nil {
		t.Fatal(err)
	}
	restartRuntime, err := platformjobqueue.NewRuntime(native, workers, customer.OwnerHandoffQueue)
	if err != nil {
		t.Fatal(err)
	}
	restartCtx, restartStop := context.WithCancel(ctx)
	restartDone := make(chan error, 1)
	go func() { restartDone <- restartRuntime.Run(restartCtx) }()
	defer func() {
		restartStop()
		select {
		case runErr := <-restartDone:
			if runErr != nil && runErr != context.Canceled {
				t.Errorf("restart runtime stop: %v", runErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("owner-handoff restart runtime did not stop")
		}
	}()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var updated, owners, jobs, effects int
		err = native.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM customer_owner_handoff_lines WHERE batch_id=$1 AND state='local_updated'),
			(SELECT count(*) FROM customer_local_owners WHERE source='owner_handoff_local_only'),
			(SELECT count(*) FROM river_job WHERE kind='customer.owner-handoff.v1'),
			(SELECT count(*) FROM external_effects WHERE kind='customer_owner_handoff')`, batch.ID).Scan(&updated, &owners, &jobs, &effects)
		if err == nil && updated == 101 && owners == 101 && jobs == 2 && effects == 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	var updated, owners, jobs, effects int
	if err = native.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM customer_owner_handoff_lines WHERE batch_id=$1 AND state='local_updated'),
		(SELECT count(*) FROM customer_local_owners WHERE source='owner_handoff_local_only'),
		(SELECT count(*) FROM river_job WHERE kind='customer.owner-handoff.v1'),
		(SELECT count(*) FROM external_effects WHERE kind='customer_owner_handoff')`, batch.ID).Scan(&updated, &owners, &jobs, &effects); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("segment completion updated=%d owners=%d jobs=%d effects=%d", updated, owners, jobs, effects)
}
