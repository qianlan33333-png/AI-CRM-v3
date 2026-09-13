package main

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
	"github.com/riverqueue/river"

	distributionapp "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/app"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// TestPostgreSQLRefundWorkerRepricesOnceAndRevokesOnlyAfterLastQualification
// exercises the real Distribution worker against real Identity, Order, and
// Payment ports. Provider I/O is deliberately absent: payment/refund terminal
// facts already exist before the durable worker starts.
func TestPostgreSQLRefundWorkerRepricesOnceAndRevokesOnlyAfterLastQualification(t *testing.T) {
	pool, cleanup := refundWorkerPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	wrapped, err := platformpostgres.Wrap(pool, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	distributionRepository, err := distributionstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	orderRepository, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	orders := orderapp.NewService(uow, orderRepository)
	payments := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), nil, nil, nil)
	qualification, err := distributionapp.NewQualificationService(identityquery.NewPostgreSQL(), orders, payments)
	if err != nil {
		t.Fatal(err)
	}
	due := &refundWorkerDue{}
	refunds := &refundWorkerRechecks{}
	worker, err := distributionapp.NewRefundService(uow, distributionRepository, due, refunds, qualification, orders)
	if err != nil {
		t.Fatal(err)
	}

	promoter := refundWorkerCustomer(t, ctx, pool)
	buyer := refundWorkerCustomer(t, ctx, pool)
	qualificationA := refundWorkerPaidOrder(t, ctx, pool, promoter, 701, "qualification-a", now.Add(-4*time.Hour))
	qualificationB := refundWorkerPaidOrder(t, ctx, pool, promoter, 701, "qualification-b", now.Add(-3*time.Hour))
	buyerOrder := refundWorkerPaidOrder(t, ctx, pool, buyer, 701, "buyer-order", now.Add(-2*time.Hour))
	refundWorkerCompletedRefund(t, ctx, pool, buyerOrder.paymentID, "buyer-refund", 300, now.Add(-time.Hour))

	commissionID := refundWorkerCommission(t, ctx, pool, promoter, buyerOrder.orderID, 701, now)

	// Two worker deliveries of one immutable refund fact race after a restart.
	// Both read the same cumulative Payment total under locks; exactly one
	// adjustment can be appended.
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			results <- worker.RunRefundRecheck(ctx, distributionapp.RefundRecheckJobArgs{OrderID: buyerOrder.orderID, State: "successful", OccurredAt: now, ReceiptKey: "buyer-refund-terminal-1"})
		}()
	}
	close(start)
	for i := 0; i < 2; i++ {
		if runErr := <-results; runErr != nil {
			t.Fatalf("concurrent refund worker: %v", runErr)
		}
	}
	refundWorkerAssertCommission(t, ctx, pool, commissionID, "pending", 300, 140, "")
	refundWorkerAssertAdjustmentCount(t, ctx, pool, commissionID, "buyer_refund", 1)

	// Refunding the first self-purchase retains the commission because the
	// second exact-product purchase remains payment-confirmed and unrefunded.
	refundWorkerCompletedRefund(t, ctx, pool, qualificationA.paymentID, "qualification-refund-a", 1000, now)
	if err = worker.RunRefundRecheck(ctx, distributionapp.RefundRecheckJobArgs{OrderID: qualificationA.orderID, State: "successful", OccurredAt: now.Add(time.Minute), ReceiptKey: "qualification-refund-a"}); err != nil {
		t.Fatalf("first qualification refund: %v", err)
	}
	refundWorkerAssertCommission(t, ctx, pool, commissionID, "pending", 300, 140, "")

	// The second refund removes the final qualifying proof. The frozen buyer
	// attribution is unchanged, while the current commission is cancelled and
	// the revocation is appended as an auditable adjustment.
	refundWorkerCompletedRefund(t, ctx, pool, qualificationB.paymentID, "qualification-refund-b", 1000, now.Add(2*time.Minute))
	if err = worker.RunRefundRecheck(ctx, distributionapp.RefundRecheckJobArgs{OrderID: qualificationB.orderID, State: "successful", OccurredAt: now.Add(2 * time.Minute), ReceiptKey: "qualification-refund-b"}); err != nil {
		t.Fatalf("last qualification refund: %v", err)
	}
	refundWorkerAssertCommission(t, ctx, pool, commissionID, "cancelled", 300, 0, "qualification_revoked")
	refundWorkerAssertAdjustmentCount(t, ctx, pool, commissionID, "qualification_revoke", 1)
}

type refundWorkerDue struct {
	mu  sync.Mutex
	ids []int64
}

func (q *refundWorkerDue) EnqueueCommissionDueWithin(_ context.Context, id int64, _ time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ids = append(q.ids, id)
	return nil
}

type refundWorkerRechecks struct{}

func (*refundWorkerRechecks) EnqueueRefundRecheckWithin(context.Context, distributionapp.RefundRecheckJobArgs) error {
	return nil
}

type refundWorkerOrder struct{ orderID, paymentID int64 }

func refundWorkerCustomer(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO customers(status,version,lineage_version,created_at,updated_at) VALUES('active',1,1,clock_timestamp(),clock_timestamp()) RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func refundWorkerPaidOrder(t *testing.T, ctx context.Context, pool *pgxpool.Pool, customerID, productID int64, key string, paidAt time.Time) refundWorkerOrder {
	t.Helper()
	var orderID, paymentID int64
	merchant := "M-refund-worker-" + key
	if err := pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at) VALUES('wechat_pay','refund-worker',$1,$2,$3,$3,1000,'CNY','paid','native',true,2,$4,$4) RETURNING id`, key, merchant, customerID, paidAt).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_version,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,$2,1,'refund-worker-product','Refund worker product',1000,1,1000)`, orderID, productID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_checkout_snapshots(order_id,product_type,product_id,product_code,product_name,product_version,service_period_duration_days,gross_amount_minor,discount_amount_minor,payable_amount_minor,currency,coupon_applied,coupon_reservation_ref,reserved_at,created_at) VALUES($1,'standard_product',$2,'refund-worker-product','Refund worker product',1,0,1000,0,1000,'CNY',false,'',$3,$3)`, orderID, productID, paidAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at) VALUES($1,2,$2,$3)`, orderID, refundWorkerDigest(key+":paid"), paidAt); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,profit_sharing_marked,version,paid_confirmed_at,created_at,updated_at) VALUES($1,'wechat_pay','mini_program',$2,1,$3,$3,1000,'CNY','paid',false,1,$4,$4,$4) RETURNING id`, orderID, merchant, customerID, paidAt).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	return refundWorkerOrder{orderID: orderID, paymentID: paymentID}
}

func refundWorkerCompletedRefund(t *testing.T, ctx context.Context, pool *pgxpool.Pool, paymentID int64, refundNo string, amount int64, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_refunds(payment_id,provider,refund_no,amount_minor,reason,status,version,created_at,updated_at) VALUES($1,'wechat_pay',$2,$3,'terminal refund fixture','completed',1,$4,$4)`, paymentID, refundNo, amount, at); err != nil {
		t.Fatal(err)
	}
}

func refundWorkerCommission(t *testing.T, ctx context.Context, pool *pgxpool.Pool, promoterID, buyerOrderID, productID int64, now time.Time) int64 {
	t.Helper()
	var distributorID, policyID, credentialID, attributionID, commissionID int64
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_distributors(customer_id,public_no,agreement_version,enabled,registered_at,version,created_at,updated_at) VALUES($1,'DSTREFUNDWORKER','v1',true,$2,1,$2,$2) RETURNING id`, promoterID, now).Scan(&distributorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES($1,'standard_product',true,2000,7,1,$2,$2) RETURNING id`, productID, now).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_promotion_credentials(distributor_id,product_id,product_type,token_digest,status,created_at,expires_at) VALUES($1,$2,'standard_product',$3,'active',$4,$5) RETURNING id`, distributorID, productID, refundWorkerDigest("credential"), now, now.Add(time.Hour)).Scan(&credentialID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at) VALUES($1,1,'refund-worker-product','Refund worker product',$2,$3,'order:1:item:1','eligible',$4,1,2000,7,$5) RETURNING id`, buyerOrderID, distributorID, credentialID, policyID, now).Scan(&attributionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at) VALUES($1,$2,1,$3,1000,0,200,200,0,2000,$4,$5,'pending','','','',1,$4,$4) RETURNING id`, attributionID, buyerOrderID, distributorID, now, now.Add(7*24*time.Hour)).Scan(&commissionID); err != nil {
		t.Fatal(err)
	}
	return commissionID
}

func refundWorkerAssertCommission(t *testing.T, ctx context.Context, pool *pgxpool.Pool, commissionID int64, wantStatus string, wantRefund, wantPayable int64, wantCancel string) {
	t.Helper()
	var status, cancel string
	var refunded, payable int64
	if err := pool.QueryRow(ctx, `SELECT status,successful_refund_minor,current_payable_minor,cancel_reason FROM distribution_commissions WHERE id=$1`, commissionID).Scan(&status, &refunded, &payable, &cancel); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || refunded != wantRefund || payable != wantPayable || cancel != wantCancel {
		t.Fatalf("commission status=%q refunded=%d payable=%d cancel=%q; want %q %d %d %q", status, refunded, payable, cancel, wantStatus, wantRefund, wantPayable, wantCancel)
	}
}

func refundWorkerAssertAdjustmentCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, commissionID int64, kind string, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM distribution_commission_adjustments WHERE commission_id=$1 AND kind=$2`, commissionID, kind).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("commission=%d adjustment kind=%s got=%d want=%d", commissionID, kind, got, want)
	}
}

func refundWorkerDigest(value string) []byte {
	result := make([]byte, 32)
	copy(result, []byte(fmt.Sprintf("%032s", value)))
	return result
}

func refundWorkerPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping Distribution refund worker PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	var entropy [8]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	schema := "aicrm_distribution_refund_worker_" + hex.EncodeToString(entropy[:])
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close(ctx)
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		pool.Close()
		admin.Close(ctx)
		t.Fatal("locate migrations")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	for _, name := range []string{
		"0001_platform.sql", "0002_identity.sql", "0005_external_effects.sql", "0010_product.sql",
		"0020_order.sql", "0021_payment.sql", "0024_order_product_version.sql", "0025_payment_reconciliation.sql",
		"0049_order_history_attribution.sql", "0055_order_service_entitlements.sql", "0061_product_public_purchase.sql",
		"0068_payment_session_beneficiary_selection.sql", "0070_service_period_entitlement_fulfillment.sql",
		"0076_order_checkout_snapshots.sql", "0088_order_service_entitlement_alliance.sql", "0095_product_external_push.sql",
		"0127_payment_historical_refund_states.sql", "0131_payment_historical_unassigned.sql", "0134_payment_history_source_delta.sql",
		"0140_payment_h5_unionid_verified.sql", "0143_payment_checkout_abandonments.sql", "0144_payment_checkout_restart_permissions.sql",
		"0156_distribution_profit_sharing_payment.sql", "0157_distribution_core.sql", "0158_order_distribution_qualification_evidence.sql", "0161_payment_paid_confirmation_time.sql",
	} {
		body, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			pool.Close()
			admin.Close(ctx)
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(body)); execErr != nil {
			pool.Close()
			admin.Close(ctx)
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	return pool, func() {
		pool.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close(cleanup)
	}
}

var _ = distributiondomain.CommissionPending

// TestPostgreSQLSettlementWorkerAcceptsOneInstructionAndReplaysAfterRestart
// joins the real Distribution and Payment applications in one PostgreSQL UoW.
// The reconciler is intentionally a deterministic Provider read: it proves
// durable EER acceptance, immutable instruction reuse, reserve release, and
// unfreeze replay without calling WeChat during a repository test.
func TestPostgreSQLSettlementWorkerAcceptsOneInstructionAndReplaysAfterRestart(t *testing.T) {
	pool, cleanup := refundWorkerPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	wrapped, err := platformpostgres.Wrap(pool, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	distributionRepository, err := distributionstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	orderRepository, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	orders := orderapp.NewService(uow, orderRepository)
	provider := &settlementWorkerProvider{now: now}
	payments := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), nil, nil, settlementWorkerEffects{})
	if err = payments.SetPaymentChannelAppIDs("wx-settlement-worker", ""); err != nil {
		t.Fatal(err)
	}
	if err = payments.SetProfitSharingReconciler(provider); err != nil {
		t.Fatal(err)
	}
	qualification, err := distributionapp.NewQualificationService(identityquery.NewPostgreSQL(), orders, payments)
	if err != nil {
		t.Fatal(err)
	}

	promoter := refundWorkerCustomer(t, ctx, pool)
	buyer := refundWorkerCustomer(t, ctx, pool)
	_ = refundWorkerPaidOrder(t, ctx, pool, promoter, 702, "settlement-qualification", now.Add(-2*time.Hour))
	buyerOrder := refundWorkerPaidOrder(t, ctx, pool, buyer, 702, "settlement-buyer", now.Add(-time.Hour))
	if _, err = pool.Exec(ctx, `UPDATE payments SET profit_sharing_marked=true,provider_transaction_reference='4200000000000001',provider_transaction_digest=$2 WHERE id=$1`, buyerOrder.paymentID, effectport.Hash("settlement-worker-transaction")); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO payment_profit_sharing_receivers(customer_id,identity_id,app_id,app_scope,channel,account_digest,state,version,created_at,updated_at) VALUES($1,1,'wx-settlement-worker','wechat-app:wx-settlement-worker','mini_program',$2,'ready',1,$3,$3)`, promoter, effectport.Hash("settlement-worker-receiver"), now); err != nil {
		t.Fatal(err)
	}
	commissionID := refundWorkerCommission(t, ctx, pool, promoter, buyerOrder.orderID, 702, now)
	if _, err = pool.Exec(ctx, `UPDATE distribution_distributors SET receiver_reference='psrecv_1',receiver_app_id='wx-settlement-worker',receiver_ready=true,receiver_reason='',receiver_checked_at=$2 WHERE customer_id=$1`, promoter, now); err != nil {
		t.Fatal(err)
	}
	// A commission cannot become due before the immutable payment-confirmed
	// fact. Seed a completed waiting period rather than manufacturing that
	// invalid state just to make the worker runnable.
	paidAt := now.Add(-8 * 24 * time.Hour)
	dueAt := paidAt.Add(7 * 24 * time.Hour)
	if _, err = pool.Exec(ctx, `UPDATE distribution_commissions SET paid_confirmed_at=$2,due_at=$3,created_at=$2,updated_at=$2 WHERE id=$1`, commissionID, paidAt, dueAt); err != nil {
		t.Fatal(err)
	}

	first, err := distributionapp.NewSettlementService(uow, distributionRepository, qualification, payments)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.RunCommissionDueCheck(ctx, commissionID); err == nil {
		t.Fatal("accepted split must retain a durable reconciliation job")
	} else {
		var snooze *river.JobSnoozeError
		if !errors.As(err, &snooze) {
			t.Fatalf("first due execution=%v, want durable snooze", err)
		}
	}

	// Reconstructing the application models a worker process restart. It must
	// query and reuse the exact accepted instruction rather than minting a
	// second EER effect or payment reserve.
	restarted, err := distributionapp.NewSettlementService(uow, distributionRepository, qualification, payments)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.RunCommissionDueCheck(ctx, commissionID); err != nil {
		t.Fatalf("receiver success plus unfreeze reconciliation=%v", err)
	}
	if err = restarted.RunCommissionDueCheck(ctx, commissionID); err != nil {
		t.Fatalf("post-restart replay=%v", err)
	}

	var status string
	var paidMinor int64
	if err = pool.QueryRow(ctx, `SELECT status,paid_minor FROM distribution_commissions WHERE id=$1`, commissionID).Scan(&status, &paidMinor); err != nil || status != string(distributiondomain.CommissionPaid) || paidMinor != 200 {
		t.Fatalf("commission status=%q paid=%d err=%v", status, paidMinor, err)
	}
	var instructionCount, reserveCount, releasedReserveCount, unfreezeCount, effectCount int
	if err = pool.QueryRow(ctx, `SELECT
	(SELECT count(*) FROM payment_profit_sharing_instructions),
	(SELECT count(*) FROM payment_profit_sharing_reserves),
	(SELECT count(*) FROM payment_profit_sharing_reserves WHERE state='released'),
	(SELECT count(*) FROM payment_profit_sharing_unfreezes),
	(SELECT count(*) FROM external_effects WHERE owner='payment' AND kind IN ('wechat_pay_profit_sharing_order_v1','wechat_pay_profit_sharing_unfreeze_v1'))`).Scan(&instructionCount, &reserveCount, &releasedReserveCount, &unfreezeCount, &effectCount); err != nil {
		t.Fatal(err)
	}
	if instructionCount != 1 || reserveCount != 1 || releasedReserveCount != 1 || unfreezeCount != 1 || effectCount != 2 {
		t.Fatalf("instruction=%d reserve=%d released=%d unfreeze=%d effects=%d", instructionCount, reserveCount, releasedReserveCount, unfreezeCount, effectCount)
	}
	if splits, unfreezes := provider.calls(); splits != 1 || unfreezes != 1 {
		t.Fatalf("provider query replayed terminal effects: splits=%d unfreezes=%d", splits, unfreezes)
	}
}

type settlementWorkerEffects struct{}

func (settlementWorkerEffects) AcceptAndQueueWithin(ctx context.Context, command effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	if !command.Valid() {
		return effectport.Projection{}, effectport.Receipt{}, errors.New("invalid fixture effect")
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return effectport.Projection{}, effectport.Receipt{}, err
	}
	var id int64
	if err = tx.QueryRow(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state) VALUES($1,$2,$3,$4,$5,$6,$7,'queued') RETURNING id`, command.Envelope.Owner, command.Envelope.Kind, command.Envelope.SourceRefDigest, command.Envelope.TargetRefDigest, command.Envelope.PayloadDigest, command.Envelope.PolicyVersionHash, command.Envelope.Fingerprint()).Scan(&id); err != nil {
		return effectport.Projection{}, effectport.Receipt{}, err
	}
	return effectport.Projection{ID: fmt.Sprintf("eer_%d", id), Owner: command.Envelope.Owner, Kind: command.Envelope.Kind, State: effectport.StateQueued, Generation: 1, UpdatedAt: time.Now().UTC()}, effectport.Receipt{}, nil
}

type settlementWorkerProvider struct {
	mu                        sync.Mutex
	now                       time.Time
	splitCalls, unfreezeCalls int
}

func (p *settlementWorkerProvider) QueryProfitSharing(context.Context, string) (paymentport.ProfitSharingProviderResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.splitCalls++
	return paymentport.ProfitSharingProviderResult{State: "FINISHED", ReceiverConfirmedSuccess: true, OutcomeKnown: true, EvidenceDigest: effectport.Hash("settlement-worker-split", fmt.Sprint(p.splitCalls)), OccurredAt: p.now.Add(time.Minute)}, nil
}

func (p *settlementWorkerProvider) QueryProfitSharingUnfreeze(context.Context, string) (paymentport.ProfitSharingProviderResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unfreezeCalls++
	return paymentport.ProfitSharingProviderResult{State: "FINISHED", OutcomeKnown: true, EvidenceDigest: effectport.Hash("settlement-worker-unfreeze", fmt.Sprint(p.unfreezeCalls)), OccurredAt: p.now.Add(2 * time.Minute)}, nil
}

func (p *settlementWorkerProvider) calls() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.splitCalls, p.unfreezeCalls
}
