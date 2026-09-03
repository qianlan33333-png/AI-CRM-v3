package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLHistoricalPaymentRefundReplayAndProviderScopedOrderNumber(t *testing.T) {
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
	repository := paymentstore.NewPostgreSQL()
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	var payOrderID, shopOrderID int64
	for index, provider := range []string{"wechat_pay", "wechat_shop"} {
		var id *int64
		if index == 0 {
			id = &payOrderID
		} else {
			id = &shopOrderID
		}
		if err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,source_row_digest,created_at,updated_at) VALUES($1,'commerce-history',$2,'same-merchant',11,11,100,'CNY','paid','history',false,$3,$4,$4) RETURNING id`, provider, provider+"-1", make([]byte, 32), now).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	payment := domain.Payment{OrderID: payOrderID, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "same-merchant", PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 11, AmountMinor: 100, Currency: "CNY", Status: domain.StatusPaid, Version: 1, CreatedAt: now, UpdatedAt: now}
	var persisted domain.Payment
	err = uow.Within(ctx, func(tx context.Context) error {
		var inner error
		persisted, inner = repository.ImportTerminalPayment(tx, payment, [32]byte{1}, "history-run")
		return inner
	})
	if err != nil || persisted.ID < 1 || persisted.EffectID != "" {
		t.Fatalf("payment=%+v err=%v", persisted, err)
	}
	err = uow.Within(ctx, func(tx context.Context) error {
		replay, inner := repository.ImportTerminalPayment(tx, payment, [32]byte{1}, "history-run")
		if inner == nil && replay.ID != persisted.ID {
			t.Fatalf("replay=%+v", replay)
		}
		return inner
	})
	if err != nil {
		t.Fatal(err)
	}
	refund := domain.Refund{PaymentID: persisted.ID, Provider: domain.ProviderWeChatPay, RefundNo: "history-refund", Reason: "历史退款", AmountMinor: 40, Status: domain.RefundCompleted, Version: 1, CreatedAt: now, UpdatedAt: now}
	err = uow.Within(ctx, func(tx context.Context) error {
		_, inner := repository.ImportTerminalRefund(tx, refund, [32]byte{2}, "history-run")
		return inner
	})
	if err != nil {
		t.Fatal(err)
	}
	var payments, refunds, effects int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM payments),(SELECT count(*) FROM payment_refunds),(SELECT count(*) FROM external_effects WHERE owner='payment')`).Scan(&payments, &refunds, &effects); err != nil || payments != 1 || refunds != 1 || effects != 0 {
		t.Fatalf("payments=%d refunds=%d effects=%d err=%v", payments, refunds, effects, err)
	}
}

func paymentIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
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
	schema := "aicrm_payment_test_" + hex.EncodeToString(random[:])
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
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0005_external_effects.sql", "0020_order.sql", "0021_payment.sql", "0024_transaction_closure.sql"} {
		raw, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(raw)); err != nil {
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
