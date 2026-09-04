package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type failCompleteStore struct{ *Repository }

func (failCompleteStore) Complete(context.Context, int64, json.RawMessage, time.Time) (orderapp.Receipt, error) {
	return orderapp.Receipt{}, errors.New("forced receipt completion failure")
}

func nativeCommand(key string) orderport.CreateCommand {
	payer, beneficiary := int64(11), int64(22)
	return orderport.CreateCommand{Actor: 7, IdempotencyKey: "postgres-order-key-" + key, Input: domain.NewOrderInput{
		Provider: domain.ProviderWeChatPay, SourceSystem: "aicrm-v3", SourceKey: key, MerchantOrderNo: "M-" + key,
		PayerCustomerID: &payer, BeneficiaryCustomerID: &beneficiary,
		Amount:       domain.Money{AmountMinor: 2500, Currency: "CNY"},
		Items:        []domain.ItemSnapshot{{LineNo: 1, ProductCode: "course", ProductName: "课程", UnitAmountMinor: 2500, Quantity: 1, LineAmountMinor: 2500}},
		RecordOrigin: domain.RecordOriginNative,
	}}
}

func TestPostgreSQLOrderAtomicReplayCursorAndConstraints(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := orderapp.NewService(uow, repository)

	first, err := service.Create(ctx, nativeCommand("one"))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.Create(ctx, nativeCommand("one"))
	if err != nil || replay.ID != first.ID {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	drift := nativeCommand("one")
	drift.Input.Amount.AmountMinor, drift.Input.Items[0].UnitAmountMinor, drift.Input.Items[0].LineAmountMinor = 3000, 3000, 3000
	if _, err = service.Create(ctx, drift); !errors.Is(err, orderport.ErrConflict) {
		t.Fatalf("drift err=%v", err)
	}
	if _, err = service.Create(ctx, nativeCommand("two")); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Create(ctx, nativeCommand("three")); err != nil {
		t.Fatal(err)
	}
	byReference, err := service.GetByReference(ctx, first.MerchantOrderNo)
	if err != nil || byReference.ID != first.ID || len(byReference.Items) != 1 || byReference.Items[0].ProductName != "课程" {
		t.Fatalf("detail by reference=%#v err=%v", byReference, err)
	}
	settlement := orderport.SettlementCommand{OrderID: first.ID, ExpectedVersion: first.Version, Status: domain.StatusPaid, OccurredAt: first.CreatedAt.Add(time.Second), ActorScope: "payment:settlement", IdempotencyKey: "postgres-settlement-key-one"}
	paid, err := service.ApplySettlement(ctx, settlement)
	if err != nil || paid.Status != domain.StatusPaid || paid.Version != first.Version+1 {
		t.Fatalf("paid=%#v err=%v", paid, err)
	}
	paidReplay, err := service.ApplySettlement(ctx, settlement)
	if err != nil || paidReplay.ID != paid.ID || paidReplay.Version != paid.Version {
		t.Fatalf("settlement replay=%#v err=%v", paidReplay, err)
	}
	page, err := service.List(ctx, orderport.ListQuery{Limit: 2})
	if err != nil || len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	next, err := service.List(ctx, orderport.ListQuery{Limit: 2, Cursor: page.NextCursor})
	if err != nil || len(next.Items) != 1 || next.NextCursor != "" {
		t.Fatalf("next=%#v err=%v", next, err)
	}

	var orders, items, history, receipts, audits, outbox int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM orders),(SELECT count(*) FROM order_items),(SELECT count(*) FROM order_status_history),(SELECT count(*) FROM order_operation_receipts),(SELECT count(*) FROM order_audit_events),(SELECT count(*) FROM order_outbox)`).Scan(&orders, &items, &history, &receipts, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if orders != 3 || items != 3 || history != 4 || receipts != 4 || audits != 4 || outbox != 4 {
		t.Fatalf("rows orders=%d items=%d history=%d receipts=%d audits=%d outbox=%d", orders, items, history, receipts, audits, outbox)
	}

	broken := orderapp.NewService(uow, failCompleteStore{repository})
	if _, err = broken.Create(ctx, nativeCommand("rollback")); !errors.Is(err, orderport.ErrUnavailable) {
		t.Fatalf("rollback error=%v", err)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM orders WHERE source_key='rollback'`).Scan(&orders); err != nil || orders != 0 {
		t.Fatalf("rollback rows=%d err=%v", orders, err)
	}
	if _, err = native.Exec(ctx, `UPDATE orders SET amount_minor=0 WHERE id=$1`, first.ID); err == nil {
		t.Fatal("database accepted zero order amount")
	}
	if _, err = native.Exec(ctx, `UPDATE order_items SET product_name=product_name WHERE order_id=$1`, first.ID); err == nil {
		t.Fatal("immutable item snapshot accepted mutation")
	}
}

func TestPostgreSQLHistoricalImportIsEffectIneligibleAndReplayableAcrossRuns(t *testing.T) {
	native, cleanup := orderIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapper, _ := platformpostgres.Wrap(native, time.Second)
	uow, _ := platformpostgres.NewUnitOfWork(wrapper)
	repository, _ := NewPostgreSQL(native, uow)
	service := orderapp.NewService(uow, repository)

	manifest, schema := [32]byte{1}, [32]byte{2}
	for _, run := range []string{"history-run-1", "history-run-2"} {
		if _, err := native.Exec(ctx, `INSERT INTO order_import_runs(run_key,source_manifest_digest,source_schema_digest,status) VALUES($1,$2,$3,'applying')`, run, manifest[:], schema[:]); err != nil {
			t.Fatal(err)
		}
	}
	input := nativeCommand("history").Input
	input.RecordOrigin, input.SourceSystem = domain.RecordOriginHistory, "aicrm-production"
	input.PayerCustomerID, input.BeneficiaryCustomerID = nil, nil
	input.CreatedAt = time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	history, err := domain.NewOrder(input)
	if err != nil {
		t.Fatal(err)
	}
	digest := [32]byte{9}
	command := orderport.HistoricalImportCommand{RunID: "history-run-1", SourceDigest: digest, Order: history.Snapshot()}
	first, err := service.ImportHistorical(ctx, command)
	if err != nil || first.EffectEligible || first.RecordOrigin != domain.RecordOriginHistory {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	replay, err := service.ImportHistorical(ctx, command)
	if err != nil || replay.ID != first.ID {
		t.Fatalf("same-run replay=%#v err=%v", replay, err)
	}
	command.RunID = "history-run-2"
	crossRun, err := service.ImportHistorical(ctx, command)
	if err != nil || crossRun.ID != first.ID {
		t.Fatalf("cross-run replay=%#v err=%v", crossRun, err)
	}
	command.SourceDigest = [32]byte{8}
	if _, err = service.ImportHistorical(ctx, command); !errors.Is(err, orderport.ErrConflict) {
		t.Fatalf("source drift err=%v", err)
	}
	if _, err = native.Exec(ctx, `UPDATE orders SET effect_eligible=TRUE WHERE id=$1`, first.ID); err == nil {
		t.Fatal("historical order became effect eligible")
	}
	var orderCount, receiptCount int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM orders),(SELECT count(*) FROM order_import_receipts)`).Scan(&orderCount, &receiptCount); err != nil || orderCount != 1 || receiptCount != 2 {
		t.Fatalf("orders=%d receipts=%d err=%v", orderCount, receiptCount, err)
	}
}

func orderIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping Order PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schemaName := "aicrm_order_test_" + hex.EncodeToString(random[:])
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schemaName}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration test")
	}
	for _, name := range []string{"0020_order.sql", "0024_order_product_version.sql"} {
		migration, readErr := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	for _, table := range []string{"orders", "order_items", "order_status_history", "order_operation_receipts", "order_export_receipts", "order_audit_events", "order_outbox", "order_import_runs", "order_import_receipts", "order_import_quarantine"} {
		var owned bool
		if err = pool.QueryRow(ctx, `SELECT tableowner=current_user FROM pg_tables WHERE schemaname=current_schema() AND tablename=$1`, table).Scan(&owned); err != nil || !owned {
			t.Fatalf("table %s owner=%t err=%v", table, owned, err)
		}
	}
	return pool, func() {
		pool.Close()
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schemaName}.Sanitize()+" CASCADE")
		admin.Close(cleanup)
	}
}
