package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	identityadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/adapter"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	ordermigration "github.com/qianlan33333-png/AI-CRM-v3/internal/order/migration"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type options struct {
	mode, snapshot, digest string
	confirm                bool
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "commerce migration failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("migrate-commerce-history", flag.ContinueOnError)
	var cfg options
	flags.StringVar(&cfg.mode, "mode", "inspect", "inspect|dry-run|apply|reconcile")
	flags.StringVar(&cfg.snapshot, "snapshot", "", "path to normalized snapshot")
	flags.StringVar(&cfg.digest, "manifest-sha256", "", "required snapshot sha256")
	flags.BoolVar(&cfg.confirm, "confirm-apply", false, "confirm the exact apply manifest")
	if err := flags.Parse(args); err != nil || cfg.snapshot == "" {
		return errors.New("snapshot is required")
	}
	manifest, err := ordermigration.Load(cfg.snapshot)
	if err != nil {
		return err
	}
	summary := manifest.Summary()
	if cfg.mode == "inspect" {
		return printJSON(map[string]any{"mode": cfg.mode, "manifest_sha256": hex.EncodeToString(manifest.Digest[:]), "summary": summary})
	}
	if err = manifest.Validate(true); err != nil {
		return err
	}
	if cfg.mode == "dry-run" {
		return printJSON(map[string]any{"mode": cfg.mode, "eligible": true, "manifest_sha256": hex.EncodeToString(manifest.Digest[:]), "summary": summary})
	}
	provided, err := hex.DecodeString(cfg.digest)
	if err != nil || len(provided) != 32 || string(provided) != string(manifest.Digest[:]) {
		return errors.New("manifest digest confirmation mismatch")
	}
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL, MaxConnections: 10, MinConnections: 1})
	if err != nil {
		return err
	}
	defer pool.Close()
	if cfg.mode == "reconcile" {
		return reconcile(ctx, pool, manifest)
	}
	if cfg.mode != "apply" || !cfg.confirm {
		return errors.New("apply requires --confirm-apply")
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	orderRepository, err := orderstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return err
	}
	identity := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	paymentRepository := paymentstore.NewPostgreSQL()
	runner := ordermigration.Runner{UOW: uow, Identities: identity, Facts: identityadapter.ProviderHistory{}, Orders: orderapp.NewService(uow, orderRepository), Payments: paymentapp.NewService(uow, paymentRepository, nil, nil, nil), Runs: ordermigration.PostgreSQLRuns{Pool: pool.Native()}}
	result, err := runner.Apply(ctx, manifest)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"mode": "apply", "result": result, "run_key": manifest.RunKey})
}

func reconcile(ctx context.Context, pool *platformpostgres.Pool, manifest ordermigration.Manifest) error {
	var orders, payments, refunds, runInput, runImported int64
	var amount, refundAmount int64
	err := pool.Native().QueryRow(ctx, `
		SELECT count(*),COALESCE(sum(o.amount_minor),0)
		FROM order_import_runs run
		JOIN order_import_receipts receipt ON receipt.run_id=run.id
		JOIN orders o ON o.id=receipt.order_id
		WHERE run.run_key=$1 AND receipt.outcome IN ('imported','replayed')`, manifest.RunKey).Scan(&orders, &amount)
	if err != nil {
		return err
	}
	err = pool.Native().QueryRow(ctx, `
		SELECT count(*) FROM order_import_runs run
		JOIN order_import_receipts receipt ON receipt.run_id=run.id
		JOIN payments p ON p.order_id=receipt.order_id
		WHERE run.run_key=$1 AND receipt.outcome IN ('imported','replayed')`, manifest.RunKey).Scan(&payments)
	if err != nil {
		return err
	}
	err = pool.Native().QueryRow(ctx, `
		SELECT count(*),COALESCE(sum(refund.amount_minor),0)
		FROM order_import_runs run
		JOIN order_import_receipts receipt ON receipt.run_id=run.id
		JOIN payments p ON p.order_id=receipt.order_id
		JOIN payment_refunds refund ON refund.payment_id=p.id
		WHERE run.run_key=$1 AND receipt.outcome IN ('imported','replayed')`, manifest.RunKey).Scan(&refunds, &refundAmount)
	if err != nil {
		return err
	}
	if err = pool.Native().QueryRow(ctx, `SELECT input_count,imported_count FROM order_import_runs WHERE run_key=$1 AND status IN ('applied','reconciled')`, manifest.RunKey).Scan(&runInput, &runImported); err != nil {
		return err
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	resolvedIdentities := 0
	for _, row := range manifest.Identities {
		var resolved bool
		err = uow.Within(ctx, func(tx context.Context) error {
			result, resolveErr := oneID.Resolve(tx, identitydomain.Reference{Kind: identitydomain.Kind(row.Kind), Scope: row.Scope, Value: row.Value, Assurance: identitydomain.AssuranceVerified, Source: row.Source})
			resolved = resolveErr == nil && result.Status == identityport.ResolveFound && result.CustomerID > 0 && result.IdentityID > 0
			return resolveErr
		})
		if err != nil || !resolved {
			return errors.New("commerce identity reconciliation mismatch")
		}
		resolvedIdentities++
	}
	summary := manifest.Summary()
	expectedInput := int64(summary.IdentityRows + summary.OrderRows + summary.RefundRows)
	match := resolvedIdentities == summary.IdentityRows && runInput == expectedInput && runImported == expectedInput && orders == int64(summary.OrderRows) && amount == summary.AmountMinor && payments == int64(summary.PaymentRows) && refunds == int64(summary.RefundRows) && refundAmount == summary.RefundMinor
	if !match {
		return errors.New("commerce reconciliation mismatch")
	}
	result, err := pool.Native().Exec(ctx, `UPDATE order_import_runs SET status='reconciled',completed_at=clock_timestamp() WHERE run_key=$1 AND status IN ('applied','reconciled')`, manifest.RunKey)
	if err != nil || result.RowsAffected() != 1 {
		if err != nil {
			return err
		}
		return errors.New("commerce reconciliation run state mismatch")
	}
	return printJSON(map[string]any{"mode": "reconcile", "matched": true, "identities": resolvedIdentities, "orders": orders, "payments": payments, "refunds": refunds, "amount_minor": amount, "refund_minor": refundAmount, "checked_at": time.Now().UTC()})
}

func printJSON(value any) error { return json.NewEncoder(os.Stdout).Encode(value) }
