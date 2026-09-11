package store_test

import (
	"context"
	"errors"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLAbandonmentAuditOutboxAtomicReplay(t *testing.T) {
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
	var orderID, paymentID int64
	err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,source_row_digest,created_at,updated_at) VALUES('wechat_pay','test','abandon','abandon',11,11,990,'CNY','paid','history',false,$1,now(),now()) RETURNING id`, make([]byte, 32)).Scan(&orderID)
	if err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,created_at,updated_at) VALUES($1,'wechat_pay','v3pay_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',4,11,11,990,'CNY','awaiting_prepay',1,now(),now()) RETURNING id`, orderID).Scan(&paymentID)
	if err != nil {
		t.Fatal(err)
	}
	repo := paymentstore.NewPostgreSQL()
	cmd := paymentport.AbandonCheckoutCommand{PaymentID: paymentID, ActorScope: "admin:1", EvidenceDigest: strings.Repeat("a", 64), ConfirmedNoDebit: true}
	sentinel := errors.New("rollback")
	err = uow.Within(ctx, func(tx context.Context) error {
		if e := repo.RecordCheckoutAbandonment(tx, cmd, time.Now().UTC()); e != nil {
			return e
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	assertCounts := func(want int) {
		t.Helper()
		for _, table := range []string{"payment_checkout_abandonments", "payment_audit_events", "payment_outbox"} {
			var count int
			if e := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); e != nil || count != want {
				t.Fatalf("%s count=%d err=%v", table, count, e)
			}
		}
	}
	assertCounts(0)
	for i := 0; i < 2; i++ {
		if err = uow.Within(ctx, func(tx context.Context) error { return repo.RecordCheckoutAbandonment(tx, cmd, time.Now().UTC()) }); err != nil {
			t.Fatal(err)
		}
	}
	assertCounts(1)
	cmd.EvidenceDigest = strings.Repeat("b", 64)
	if err = uow.Within(ctx, func(tx context.Context) error { return repo.RecordCheckoutAbandonment(tx, cmd, time.Now().UTC()) }); !errors.Is(err, paymentport.ErrConflict) {
		t.Fatalf("different evidence replay: %v", err)
	}
	var state string
	var payer int64
	if err = pool.QueryRow(ctx, `SELECT status,payer_customer_id FROM payments WHERE id=$1`, paymentID).Scan(&state, &payer); err != nil || state != "awaiting_prepay" || payer != 11 {
		t.Fatal("payment facts changed")
	}
}
