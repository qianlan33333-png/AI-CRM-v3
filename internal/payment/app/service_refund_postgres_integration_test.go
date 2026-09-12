package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// postgresRefundEffects is a transaction-local stand-in for the Effects Port.
// It persists only a queued fixture effect so Payment's real UoW/FK boundary is
// exercised without provider I/O or a worker.
type postgresRefundEffects struct{}

func (postgresRefundEffects) AcceptAndQueueWithin(ctx context.Context, command effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	if !command.Valid() {
		return effectport.Projection{}, effectport.Receipt{}, errors.New("invalid effect command")
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return effectport.Projection{}, effectport.Receipt{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state) VALUES($1,$2,$3,$4,$5,$6,$7,'queued') RETURNING id`, command.Envelope.Owner, command.Envelope.Kind, command.Envelope.SourceRefDigest, command.Envelope.TargetRefDigest, command.Envelope.PayloadDigest, command.Envelope.PolicyVersionHash, command.Envelope.Fingerprint()).Scan(&id)
	if err != nil {
		return effectport.Projection{}, effectport.Receipt{}, err
	}
	return effectport.Projection{ID: fmt.Sprintf("eer_%d", id), Owner: command.Envelope.Owner, Kind: command.Envelope.Kind, State: effectport.StateQueued, Generation: 1, UpdatedAt: time.Now().UTC()}, effectport.Receipt{}, nil
}

func TestPostgreSQLWeChatPayRefundSerializesNewKeysPerPayment(t *testing.T) {
	pool, cleanup := paymentAppIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository := paymentstore.NewPostgreSQL()
	now := time.Date(2026, 9, 12, 7, 0, 0, 0, time.UTC)
	var orderID, paymentID int64
	if err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,created_at,updated_at) VALUES('wechat_pay','refund-concurrency','native-refund-concurrency','M-refund-concurrency',11,11,1000,'CNY','paid','native',true,$1,$1) RETURNING id`, now).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,created_at,updated_at) VALUES($1,'wechat_pay','mini_program','M-refund-concurrency',4,11,11,1000,'CNY','paid',1,$2,$2) RETURNING id`, orderID, now).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	service := NewService(uow, repository, orderStub{}, sessionStub{}, postgresRefundEffects{})
	commands := []paymentport.RefundCommand{
		{PaymentID: paymentID, AmountMinor: 100, RefundNo: "RF-concurrency-1", Reason: "fixture refund", ActorScope: "admin:17", IdempotencyKey: "refund-concurrency-key-0001"},
		{PaymentID: paymentID, AmountMinor: 200, RefundNo: "RF-concurrency-2", Reason: "fixture refund", ActorScope: "admin:18", IdempotencyKey: "refund-concurrency-key-0002"},
	}
	start := make(chan struct{})
	type result struct {
		command paymentport.RefundCommand
		refund  domain.Refund
		err     error
	}
	results := make(chan result, len(commands))
	var wait sync.WaitGroup
	for _, command := range commands {
		command := command
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			refund, requestErr := service.RequestRefund(ctx, command)
			results <- result{command: command, refund: refund, err: requestErr}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	var accepted result
	acceptedCount, conflictCount := 0, 0
	for result := range results {
		if result.err == nil {
			acceptedCount++
			accepted = result
			continue
		}
		if errors.Is(result.err, paymentport.ErrConflict) {
			conflictCount++
			continue
		}
		t.Fatalf("unexpected concurrent refund error: %v", result.err)
	}
	if acceptedCount != 1 || conflictCount != 1 || accepted.refund.ID < 1 {
		t.Fatalf("accepted=%d conflicts=%d receipt=%+v", acceptedCount, conflictCount, accepted.refund)
	}
	var refundCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM payment_refunds WHERE payment_id=$1`, paymentID).Scan(&refundCount); err != nil || refundCount != 1 {
		t.Fatalf("refunds=%d err=%v", refundCount, err)
	}

	// An original-key replay stays valid even while its external outcome is not
	// terminal; it must not be mistaken for a second refund command.
	replayed, err := service.RequestRefund(ctx, accepted.command)
	if err != nil || replayed.ID != accepted.refund.ID {
		t.Fatalf("replay=%+v accepted=%+v err=%v", replayed, accepted.refund, err)
	}

	// A genuine terminal result permits a later explicit partial refund with a
	// different key. The amount gate still applies through ReservedRefundMinor.
	if err = uow.Within(ctx, func(tx context.Context) error {
		current, inner := repository.GetRefund(tx, accepted.refund.ID, true)
		if inner != nil {
			return inner
		}
		terminal, inner := current.Complete(current.Version, domain.RefundFinalFailed, current.UpdatedAt.Add(time.Minute))
		if inner != nil {
			return inner
		}
		_, inner = repository.UpdateRefundSettlement(tx, terminal, string(effectport.Hash("fixture-terminal-refund", accepted.refund.RefundNo)), "fixture-terminal-refund")
		return inner
	}); err != nil {
		t.Fatal(err)
	}
	later, err := service.RequestRefund(ctx, paymentport.RefundCommand{PaymentID: paymentID, AmountMinor: 300, RefundNo: "RF-concurrency-terminal-next", Reason: "fixture later partial", ActorScope: "admin:19", IdempotencyKey: "refund-concurrency-key-0003"})
	if err != nil || later.ID < 1 || later.ID == accepted.refund.ID {
		t.Fatalf("later=%+v original=%+v err=%v", later, accepted.refund, err)
	}
}

func paymentAppIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping Payment PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_payment_app_test_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0005_external_effects.sql", "0020_order.sql", "0021_payment.sql", "0024_order_product_version.sql", "0025_payment_reconciliation.sql", "0061_product_public_purchase.sql", "0068_payment_session_beneficiary_selection.sql", "0127_payment_historical_refund_states.sql", "0131_payment_historical_unassigned.sql", "0134_payment_history_source_delta.sql", "0140_payment_h5_unionid_verified.sql", "0143_payment_checkout_abandonments.sql", "0144_payment_checkout_restart_permissions.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			pool.Close()
			admin.Close(ctx)
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(raw)); err != nil {
			pool.Close()
			admin.Close(ctx)
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	return pool, func() {
		pool.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close(cleanup)
	}
}
