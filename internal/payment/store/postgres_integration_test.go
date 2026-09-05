package store_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
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

func TestPostgreSQLRefundExposureExcludesOnlyFinalFailed(t *testing.T) {
	pool, cleanup := paymentIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, _ := platformpostgres.Wrap(pool, time.Second)
	uow, _ := platformpostgres.NewUnitOfWork(wrapper)
	repository := paymentstore.NewPostgreSQL()
	now := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	orderIDs := make([]int64, 0, 5)
	for index, refundStatus := range []string{"requested", "effect_accepted", "outcome_unknown", "completed", "final_failed"} {
		var orderID, paymentID int64
		key := "refund-exposure-" + string(rune('a'+index))
		if err := pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,source_row_digest,created_at,updated_at) VALUES('wechat_pay','test',$1,$2,11,11,100,'CNY','paid','history',false,$3,$4,$4) RETURNING id`, key, "M-"+key, make([]byte, 32), now).Scan(&orderID); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,created_at,updated_at) VALUES($1,'wechat_pay','mini_program',$2,4,11,11,100,'CNY','paid',1,$3,$3) RETURNING id`, orderID, "M-"+key, now).Scan(&paymentID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO payment_refunds(payment_id,provider,refund_no,amount_minor,reason,status,version,created_at,updated_at) VALUES($1,'wechat_pay',$2,100,'test',$3,1,$4,$4)`, paymentID, "R-"+key, refundStatus, now); err != nil {
			t.Fatal(err)
		}
		orderIDs = append(orderIDs, orderID)
	}
	var exposed map[int64]struct{}
	if err := uow.Within(ctx, func(tx context.Context) error {
		var inner error
		exposed, inner = repository.RefundRelatedOrderIDsWithin(tx, orderIDs)
		return inner
	}); err != nil {
		t.Fatal(err)
	}
	if len(exposed) != 4 {
		t.Fatalf("exposed=%+v", exposed)
	}
	if _, ok := exposed[orderIDs[4]]; ok {
		t.Fatal("final_failed refund reduced sold count")
	}
	var _ paymentport.RefundExposureReader = repository
}

func TestPostgreSQLPaymentSessionBeneficiaryFactsCASAndCheckoutRollback(t *testing.T) {
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
	repository := paymentsession.NewPostgreSQL()
	now := time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC)
	expires := now.Add(10 * time.Minute)
	legacyDigest := sha256.Sum256([]byte("legacy-payment-session"))
	newDigest := sha256.Sum256([]byte("new-payment-session"))
	insert := func(digest [32]byte, beneficiary customerdomain.CustomerID, selection paymentport.BeneficiarySelection, selectedAt *time.Time) {
		t.Helper()
		err := uow.Within(ctx, func(tx context.Context) error {
			_, err := repository.Insert(tx, paymentsession.Record{TokenDigest: digest, PayerIdentityID: 9, PayerCustomerID: 11, BeneficiaryCustomerID: beneficiary, BeneficiarySelection: selection, BeneficiarySelectedAt: selectedAt, AppScopeDigest: sha256.Sum256([]byte("scope")), Channel: domain.ChannelH5Official, ExpiresAt: expires, CreatedAt: now})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	insert(legacyDigest, 44, paymentport.BeneficiarySelectionLegacyPrebound, nil)
	insert(newDigest, 0, paymentport.BeneficiarySelectionUnresolved, nil)

	var legacy paymentsession.Record
	err = uow.Within(ctx, func(tx context.Context) error {
		var inner error
		legacy, inner = repository.Lookup(tx, legacyDigest, now)
		return inner
	})
	if err != nil || legacy.BeneficiaryCustomerID != 44 || legacy.BeneficiarySelection != paymentport.BeneficiarySelectionLegacyPrebound || legacy.BeneficiarySelectedAt != nil {
		t.Fatalf("legacy=%+v err=%v", legacy, err)
	}
	err = uow.Within(ctx, func(tx context.Context) error {
		_, inner := repository.SelectPayerSelf(tx, legacyDigest, now)
		return inner
	})
	if !errors.Is(err, paymentsession.ErrInvalid) {
		t.Fatalf("legacy selection err=%v", err)
	}

	checkoutFailure := errors.New("order creation failed")
	err = uow.Within(ctx, func(tx context.Context) error {
		selected, inner := repository.SelectPayerSelf(tx, newDigest, now)
		if inner != nil || selected.BeneficiaryCustomerID != 11 || selected.BeneficiarySelection != paymentport.BeneficiarySelectionPayerSelf {
			t.Fatalf("selected=%+v err=%v", selected, inner)
		}
		return checkoutFailure // A later order/payment write fails: the selection must roll back too.
	})
	if !errors.Is(err, checkoutFailure) {
		t.Fatalf("rollback err=%v", err)
	}
	var unresolved paymentsession.Record
	err = uow.Within(ctx, func(tx context.Context) error {
		var inner error
		unresolved, inner = repository.Lookup(tx, newDigest, now)
		return inner
	})
	if err != nil || unresolved.BeneficiaryCustomerID != 0 || unresolved.BeneficiarySelection != paymentport.BeneficiarySelectionUnresolved || unresolved.BeneficiarySelectedAt != nil {
		t.Fatalf("rollback left session=%+v err=%v", unresolved, err)
	}

	start := make(chan struct{})
	errorsByAttempt := make(chan error, 2)
	var wait sync.WaitGroup
	for _, at := range []time.Time{now.Add(time.Second), now.Add(2 * time.Second)} {
		wait.Add(1)
		go func(at time.Time) {
			defer wait.Done()
			<-start
			errorsByAttempt <- uow.Within(ctx, func(tx context.Context) error {
				selected, inner := repository.SelectPayerSelf(tx, newDigest, at)
				if inner != nil {
					return inner
				}
				if selected.PayerCustomerID != 11 || selected.BeneficiaryCustomerID != 11 || selected.BeneficiarySelection != paymentport.BeneficiarySelectionPayerSelf {
					return errors.New("payer self selection drift")
				}
				return nil
			})
		}(at)
	}
	close(start)
	wait.Wait()
	close(errorsByAttempt)
	for attemptErr := range errorsByAttempt {
		if attemptErr != nil {
			t.Fatalf("concurrent selection err=%v", attemptErr)
		}
	}
	err = uow.Within(ctx, func(tx context.Context) error {
		var inner error
		unresolved, inner = repository.Lookup(tx, newDigest, now.Add(3*time.Second))
		return inner
	})
	if err != nil || unresolved.BeneficiaryCustomerID != 11 || unresolved.BeneficiarySelection != paymentport.BeneficiarySelectionPayerSelf || unresolved.BeneficiarySelectedAt == nil {
		t.Fatalf("concurrent selected=%+v err=%v", unresolved, err)
	}
}

func TestPostgreSQLPaymentSessionBeneficiaryFactConstraintRejectsMutualExclusion(t *testing.T) {
	pool, cleanup := paymentIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC)
	expires := now.Add(10 * time.Minute)
	selected := now.Add(time.Second)
	scopeDigest := sha256.Sum256([]byte("scope"))

	type invalidFact struct {
		name        string
		beneficiary any
		selection   string
		selectedAt  any
	}
	for _, row := range []invalidFact{
		{name: "legacy recipient cannot claim a fresh selection", beneficiary: int64(44), selection: "legacy_prebound", selectedAt: selected},
		{name: "legacy requires preserved recipient", beneficiary: nil, selection: "legacy_prebound", selectedAt: nil},
		{name: "unresolved cannot have recipient", beneficiary: int64(44), selection: "unresolved", selectedAt: nil},
		{name: "unresolved cannot have selected time", beneficiary: nil, selection: "unresolved", selectedAt: selected},
		{name: "payer self requires recipient", beneficiary: nil, selection: "payer_self", selectedAt: selected},
		{name: "payer self must equal payer", beneficiary: int64(44), selection: "payer_self", selectedAt: selected},
		{name: "payer self requires selected time", beneficiary: int64(11), selection: "payer_self", selectedAt: nil},
		{name: "admin assisted requires recipient", beneficiary: nil, selection: "admin_assisted", selectedAt: selected},
		{name: "admin assisted requires selected time", beneficiary: int64(44), selection: "admin_assisted", selectedAt: nil},
	} {
		t.Run(row.name, func(t *testing.T) {
			digest := sha256.Sum256([]byte("invalid-payment-session-fact:" + row.name))
			_, err := pool.Exec(ctx, `
				INSERT INTO payment_sessions(
					token_digest,payer_identity_id,payer_customer_id,beneficiary_customer_id,
					beneficiary_selection,beneficiary_selected_at,app_scope_digest,payment_channel,expires_at,created_at
				) VALUES($1,9,11,$2,$3,$4,$5,'h5_official',$6,$7)`,
				digest[:], row.beneficiary, row.selection, row.selectedAt, scopeDigest[:], expires, now,
			)
			if err == nil {
				t.Fatal("invalid beneficiary fact was accepted")
			}
		})
	}
}

func TestPostgreSQLH5OAuthReturnPathAcceptsOnlyPublicCommerceSegments(t *testing.T) {
	pool, cleanup := paymentIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC)
	for index, path := range []string{"/pay/course-7", "/s/term-31", "/s/term-31/pay", "/c/cp-a1b2c3"} {
		digest := sha256.Sum256([]byte("valid-return-path:" + path))
		if _, err := pool.Exec(ctx, `INSERT INTO payment_h5_oauth_states(state_digest,return_path,expires_at,created_at) VALUES($1,$2,$3,$4)`, digest[:], path, now.Add(time.Hour), now.Add(time.Duration(index)*time.Second)); err != nil {
			t.Fatalf("valid return path %q: %v", path, err)
		}
	}
	for _, path := range []string{"https://evil.example/pay/course-7", "//evil.example/pay/course-7", "/p/course-7", "/pay/course/7", "/s/term%2F31", "/s/term%5C31", "/s/term%23fragment", "/c/CAPITAL-2026", "/s/term\\31"} {
		digest := sha256.Sum256([]byte("invalid-return-path:" + path))
		if _, err := pool.Exec(ctx, `INSERT INTO payment_h5_oauth_states(state_digest,return_path,expires_at,created_at) VALUES($1,$2,$3,$4)`, digest[:], path, now.Add(time.Hour), now); err == nil {
			t.Fatalf("invalid return path accepted: %q", path)
		}
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
	for _, name := range []string{"0001_platform.sql", "0002_identity.sql", "0005_external_effects.sql", "0020_order.sql", "0021_payment.sql", "0024_order_product_version.sql", "0025_payment_reconciliation.sql", "0061_product_public_purchase.sql", "0068_payment_session_beneficiary_selection.sql"} {
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
