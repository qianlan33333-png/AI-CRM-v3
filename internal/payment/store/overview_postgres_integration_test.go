package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLOverviewPaymentUsesTrustedHistoryTimeAndLatestRefundAppend(t *testing.T) {
	pool, cleanup := paymentIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 14, 16, 0, 0, 0, time.UTC) // Beijing 15th midnight.
	end := start.Add(24 * time.Hour)
	inRangeNative := start
	inRangeHistory := start.Add(8 * time.Hour)
	atEnd := end

	nativePayment := insertOverviewPayment(t, ctx, pool, "native-in-range", 100, false, &inRangeNative, 11)
	historyPayment := insertOverviewPayment(t, ctx, pool, "history-trusted", 200, true, &inRangeHistory, 12)
	_ = insertOverviewPayment(t, ctx, pool, "at-exclusive-end", 400, false, &atEnd, 13)
	_ = insertOverviewPayment(t, ctx, pool, "history-no-time", 500, true, nil, 14)

	// The earlier event is in the selected window, but a late callback wrote a
	// newer audit row with an older Provider occurrence. The current completed
	// refund must use the newest append (id order), therefore it is outside this
	// window; ordering by occurred_at would be wrong.
	lateRefund := insertOverviewRefund(t, ctx, pool, nativePayment, "late-callback", 30)
	insertRefundAudit(t, ctx, pool, "payment.refund_settled", lateRefund, start.Add(time.Hour))
	insertRefundAudit(t, ctx, pool, "payment.refund_settled", lateRefund, start.Add(-time.Hour))

	// A completed imported refund is admissible only through the immutable
	// import audit, whose occurred_at is the source refund time, not the import
	// transaction time.
	historyRefund := insertOverviewRefund(t, ctx, pool, historyPayment, "history-completed", 40)
	insertRefundAudit(t, ctx, pool, "payment.refund_history_imported", historyRefund, start.Add(2*time.Hour))

	// This is a locally completed row with no source completion evidence. It
	// remains visible as data missing rather than becoming an invented zero.
	missingEvidence := insertOverviewPayment(t, ctx, pool, "current-missing-refund-evidence", 90, false, &inRangeNative, 15)
	_ = insertOverviewRefund(t, ctx, pool, missingEvidence, "missing-evidence", 10)

	repository := paymentstore.NewPostgreSQL()
	var paid paymentport.PaidOverview
	var refunds paymentport.RefundOverview
	err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		paid, readErr = repository.ReadPaidOverview(tx, paymentport.OverviewWindow{Start: start, End: end})
		if readErr != nil {
			return readErr
		}
		refunds, readErr = repository.ReadRefundOverview(tx, paymentport.OverviewWindow{Start: start, End: end})
		return readErr
	})
	if err != nil {
		t.Fatal(err)
	}
	// 100 native + 200 trusted history + 90 current missing-refund-evidence;
	// 400 at [end) and 500 unknown-confirmation history must not enter known
	// period money.
	if paid.OrderCount != 3 || paid.DistinctCanonicalPayers != 0 || len(paid.Gross) != 1 || paid.Gross[0].Currency != "CNY" || paid.Gross[0].AmountMinor != 390 {
		t.Fatalf("paid known aggregate=%+v", paid)
	}
	var payerPage paymentport.PaidOverviewPayerPage
	err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		payerPage, readErr = repository.ReadPaidOverviewPayerPage(tx, paymentport.OverviewWindow{Start: start, End: end}, 0, 500)
		return readErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(payerPage.CustomerIDs) != 3 || payerPage.CustomerIDs[0] != 11 || payerPage.CustomerIDs[1] != 12 || payerPage.CustomerIDs[2] != 15 {
		t.Fatalf("paid payer keyset page=%+v", payerPage)
	}
	if paid.MissingConfirmationEvidenceCount != 1 || len(paid.MissingConfirmationEvidenceAmount) != 1 || paid.MissingConfirmationEvidenceAmount[0].AmountMinor != 500 {
		t.Fatalf("paid missing evidence=%+v", paid)
	}
	if len(paid.Trend) != 1 || paid.Trend[0].Date != "2026-09-15" || paid.Trend[0].OrderCount != 3 || len(paid.Trend[0].Gross) != 1 || paid.Trend[0].Gross[0].AmountMinor != 390 {
		t.Fatalf("paid Beijing trend=%+v", paid.Trend)
	}
	// The late native settlement is excluded by its latest audit append; the
	// trusted historical completion remains in the window.
	if refunds.CompletedCount != 1 || len(refunds.Completed) != 1 || refunds.Completed[0].AmountMinor != 40 || refunds.MissingCompletionEvidence != 1 {
		t.Fatalf("refund aggregate=%+v", refunds)
	}
}

func insertOverviewPayment(t *testing.T, ctx context.Context, pool *pgxpool.Pool, key string, amount int64, historical bool, confirmedAt *time.Time, payer int64) int64 {
	t.Helper()
	createdAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if confirmedAt != nil && createdAt.After(*confirmedAt) {
		createdAt = confirmedAt.Add(-time.Hour)
	}
	var orderID int64
	if err := pool.QueryRow(ctx, `INSERT INTO orders(
		provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,
		amount_minor,currency,status,record_origin,effect_eligible,source_row_digest,created_at,updated_at
	) VALUES('wechat_pay','overview-fixture',$1,$2,$3,$3,$4,'CNY','paid','history',false,$5,$6,$6) RETURNING id`,
		key, "M-"+key, payer, amount, overviewDigest(key), createdAt,
	).Scan(&orderID); err != nil {
		t.Fatalf("insert overview order %s: %v", key, err)
	}
	var paymentID int64
	if err := pool.QueryRow(ctx, `INSERT INTO payments(
		order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,
		amount_minor,currency,status,version,paid_confirmed_at,created_at,updated_at,historical
	) VALUES($1,'wechat_pay','mini_program',$2,$3,$3,$3,$4,'CNY','paid',1,$5,$6,$6,$7) RETURNING id`,
		orderID, "M-"+key, payer, amount, confirmedAt, createdAt, historical,
	).Scan(&paymentID); err != nil {
		t.Fatalf("insert overview payment %s: %v", key, err)
	}
	return paymentID
}

func insertOverviewRefund(t *testing.T, ctx context.Context, pool *pgxpool.Pool, paymentID int64, key string, amount int64) int64 {
	t.Helper()
	var refundID int64
	if err := pool.QueryRow(ctx, `INSERT INTO payment_refunds(
		payment_id,provider,refund_no,amount_minor,reason,status,version,created_at,updated_at
	) VALUES($1,'wechat_pay',$2,$3,'overview fixture','completed',1,$4,$4) RETURNING id`,
		paymentID, "R-"+key, amount, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	).Scan(&refundID); err != nil {
		t.Fatalf("insert overview refund %s: %v", key, err)
	}
	return refundID
}

func insertRefundAudit(t *testing.T, ctx context.Context, pool *pgxpool.Pool, event string, refundID int64, occurredAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO payment_audit_events(event_type,aggregate_id,actor_scope,payload,occurred_at)
		VALUES($1,$2,'overview-fixture','{}'::jsonb,$3)`, event, refundID, occurredAt); err != nil {
		t.Fatalf("insert %s refund audit: %v", event, err)
	}
}

func overviewDigest(key string) []byte {
	value := make([]byte, 32)
	copy(value, []byte(key))
	return value
}
