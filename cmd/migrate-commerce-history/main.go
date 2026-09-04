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
	identitymigration "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/migration"
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
	confirm, orderOnly     bool
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
	flags.BoolVar(&cfg.orderOnly, "order-only", false, "accept only the audited floating WeChat Pay order snapshot")
	if err := flags.Parse(args); err != nil || cfg.snapshot == "" {
		return errors.New("snapshot is required")
	}
	manifest, err := ordermigration.Load(cfg.snapshot)
	if err != nil {
		return err
	}
	summary := manifest.Summary()
	if cfg.orderOnly {
		if err = ordermigration.ValidateOrderOnly(manifest); err != nil {
			return err
		}
	}
	if cfg.mode == "inspect" {
		return printJSON(map[string]any{"mode": cfg.mode, "order_only": cfg.orderOnly, "manifest_sha256": hex.EncodeToString(manifest.Digest[:]), "summary": summary})
	}
	if !cfg.orderOnly {
		err = manifest.Validate(true)
	}
	if err != nil {
		return err
	}
	if cfg.mode == "dry-run" {
		return printJSON(map[string]any{"mode": cfg.mode, "order_only": cfg.orderOnly, "eligible": true, "manifest_sha256": hex.EncodeToString(manifest.Digest[:]), "summary": summary})
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
	runs := ordermigration.PostgreSQLRuns{Pool: pool.Native()}
	if cfg.mode == "reconcile" && cfg.orderOnly {
		result, reconcileErr := runs.ReconcileOrders(ctx, manifest)
		if reconcileErr != nil {
			return reconcileErr
		}
		return printJSON(map[string]any{"mode": "reconcile", "order_only": true, "run_key": manifest.RunKey, "result": result})
	}
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
	orderService := orderapp.NewService(uow, orderRepository)
	if cfg.orderOnly {
		result, applyErr := (ordermigration.OrderOnlyRunner{Orders: orderService, Runs: runs}).Apply(ctx, manifest)
		if applyErr != nil {
			return applyErr
		}
		return printJSON(map[string]any{"mode": "apply", "order_only": true, "result": result, "run_key": manifest.RunKey})
	}
	identity := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	paymentRepository := paymentstore.NewPostgreSQL()
	runner := ordermigration.Runner{UOW: uow, Identities: identity, Facts: identityadapter.ProviderHistory{}, IdentityRuns: identitymigration.PostgreSQLReceipts{}, Orders: orderService, Payments: paymentapp.NewService(uow, paymentRepository, nil, nil, nil), Runs: runs}
	result, err := runner.Apply(ctx, manifest)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"mode": "apply", "result": result, "run_key": manifest.RunKey})
}

func reconcile(ctx context.Context, pool *platformpostgres.Pool, manifest ordermigration.Manifest) error {
	var orders, payments, refunds, runInput, runImported, canonicalSubjects, quarantinedIdentities int64
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
	identityByKey := make(map[string]ordermigration.IdentityRow, len(manifest.Identities))
	for _, row := range manifest.Identities {
		identityByKey[row.SourceKey] = row
	}
	resolvedIdentities := 0
	for _, subject := range manifest.Subjects {
		var subjectCustomer int64
		for _, identityKey := range subject.IdentityKeys {
			row := identityByKey[identityKey]
			var resolved identityport.ResolveResult
			err = uow.Within(ctx, func(tx context.Context) error {
				var resolveErr error
				resolved, resolveErr = oneID.Resolve(tx, identitydomain.Reference{Kind: identitydomain.Kind(row.Kind), Scope: row.Scope, Value: row.Value, Assurance: identitydomain.AssuranceVerified, Source: row.Source})
				return resolveErr
			})
			if err != nil || resolved.Status != identityport.ResolveFound || resolved.CustomerID < 1 || resolved.IdentityID < 1 || (subjectCustomer > 0 && int64(resolved.CustomerID) != subjectCustomer) {
				return errors.New("commerce identity reconciliation mismatch")
			}
			subjectCustomer = int64(resolved.CustomerID)
			resolvedIdentities++
		}
	}
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FILTER (WHERE outcome='canonical'),count(*) FILTER (WHERE outcome='quarantined') FROM identity_history_import_receipts WHERE run_key=$1`, manifest.RunKey).Scan(&canonicalSubjects, &quarantinedIdentities); err != nil {
		return err
	}
	summary := manifest.Summary()
	expectedInput := int64(summary.SubjectRows + summary.IdentityQuarantineRows + summary.OrderRows + summary.RefundRows)
	match := resolvedIdentities == summary.IdentityRows && canonicalSubjects == int64(summary.SubjectRows) && quarantinedIdentities == int64(summary.IdentityQuarantineRows) && runInput == expectedInput && runImported == expectedInput && orders == int64(summary.OrderRows) && amount == summary.AmountMinor && payments == int64(summary.PaymentRows) && refunds == int64(summary.RefundRows) && refundAmount == summary.RefundMinor
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
	return printJSON(map[string]any{"mode": "reconcile", "matched": true, "subjects": canonicalSubjects, "identities": resolvedIdentities, "identity_quarantines": quarantinedIdentities, "orders": orders, "payments": payments, "refunds": refunds, "amount_minor": amount, "refund_minor": refundAmount, "checked_at": time.Now().UTC()})
}

func printJSON(value any) error { return json.NewEncoder(os.Stdout).Encode(value) }
