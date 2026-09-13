package store

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

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLTerminalCommissionResolvesInformationalDeadlineWarning(t *testing.T) {
	pool, cleanup := settlementWarningPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	var distributorID, policyID, credentialID, attributionID, commissionID int64
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_distributors(customer_id,public_no,agreement_version,enabled,registered_at,version,created_at,updated_at) VALUES(101,'DSTWARN101','v1',true,$1,1,$1,$1) RETURNING id`, now).Scan(&distributorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES(701,'standard_product',true,1000,7,1,$1,$1) RETURNING id`, now).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_promotion_credentials(distributor_id,product_id,product_type,token_digest,status,created_at,expires_at) VALUES($1,701,'standard_product',$2,'active',$3,$4) RETURNING id`, distributorID, make([]byte, 32), now, now.Add(time.Hour)).Scan(&credentialID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at) VALUES(8101,1,'deadline-product','Deadline product',$1,$2,'order:8001:item:1','eligible',$3,1,1000,7,$4) RETURNING id`, distributorID, credentialID, policyID, now).Scan(&attributionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at) VALUES($1,8101,1,$2,1000,0,100,100,0,1000,$3,$4,'pending','','','',1,$3,$3) RETURNING id`, attributionID, distributorID, now, now.Add(7*24*time.Hour)).Scan(&commissionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO distribution_exceptions(commission_id,kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at) VALUES($1,'settlement_deadline_imminent','open',0,0,0,'split_deadline_within_24h','payref_99','worker:distribution-due',1,$2,$2)`, commissionID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE distribution_commissions SET status='paid',paid_minor=100,version=2,updated_at=$2 WHERE id=$1`, commissionID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var status string
	var unpaid, amount int64
	if err := pool.QueryRow(ctx, `SELECT status,unpaid_due_minor,amount_minor FROM distribution_exceptions WHERE commission_id=$1 AND kind='settlement_deadline_imminent'`, commissionID).Scan(&status, &unpaid, &amount); err != nil || status != "resolved" || unpaid != 0 || amount != 0 {
		t.Fatalf("terminal commission left actionable deadline warning status=%q unpaid=%d amount=%d err=%v", status, unpaid, amount, err)
	}
}

func TestPostgreSQLRefundRecheckOverlappingScopeLocksInGlobalOrder(t *testing.T) {
	pool, cleanup := settlementWarningPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
	distributor, policy, credential := seedRefundRecheckParentFacts(t, ctx, pool, now)
	commissionOne := seedRefundRecheckCommission(t, ctx, pool, distributor, policy, credential, 8201, now)
	commissionTwo := seedRefundRecheckCommission(t, ctx, pool, distributor, policy, credential, 8202, now)
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	scopes := []QualificationProductScope{{ProductID: 702, ProductType: distributiondomain.ProductTypeStandard}}
	locked := make(chan struct{})
	release := make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- uow.Within(ctx, func(tx context.Context) error {
			rows, lockErr := repository.ListRefundRecheckContextsWithin(tx, 8201, scopes)
			if lockErr != nil || len(rows) != 2 || rows[0].Commission.ID != commissionOne || rows[1].Commission.ID != commissionTwo {
				if lockErr != nil {
					return lockErr
				}
				return ErrInvalid
			}
			close(locked)
			<-release
			return nil
		})
	}()
	select {
	case <-locked:
	case err = <-first:
		t.Fatalf("first scope lock failed before overlap: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("first scope lock did not start")
	}
	second := make(chan error, 1)
	go func() {
		second <- uow.Within(ctx, func(tx context.Context) error {
			rows, lockErr := repository.ListRefundRecheckContextsWithin(tx, 8202, scopes)
			if lockErr != nil || len(rows) != 2 || rows[0].Commission.ID != commissionOne || rows[1].Commission.ID != commissionTwo {
				if lockErr != nil {
					return lockErr
				}
				return ErrInvalid
			}
			return nil
		})
	}()
	select {
	case outcome := <-second:
		t.Fatalf("overlapping scope escaped first lock early: %v", outcome)
	case <-time.After(75 * time.Millisecond):
	}
	close(release)
	for name, result := range map[string]<-chan error{"first": first, "second": second} {
		select {
		case err = <-result:
			if err != nil {
				t.Fatalf("%s overlapping scope transaction=%v", name, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s overlapping scope deadlocked", name)
		}
	}
}

func seedRefundRecheckParentFacts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, now time.Time) (int64, int64, int64) {
	t.Helper()
	var distributor, policy, credential int64
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_distributors(customer_id,public_no,agreement_version,enabled,registered_at,version,created_at,updated_at) VALUES(202,'DSTREFUND202','v1',true,$1,1,$1,$1) RETURNING id`, now).Scan(&distributor); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES(702,'standard_product',true,1000,7,1,$1,$1) RETURNING id`, now).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_promotion_credentials(distributor_id,product_id,product_type,token_digest,status,created_at,expires_at) VALUES($1,702,'standard_product',$2,'active',$3,$4) RETURNING id`, distributor, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32}, now, now.Add(time.Hour)).Scan(&credential); err != nil {
		t.Fatal(err)
	}
	return distributor, policy, credential
}

func seedRefundRecheckCommission(t *testing.T, ctx context.Context, pool *pgxpool.Pool, distributor, policy, credential, orderID int64, now time.Time) int64 {
	t.Helper()
	var attribution, commission int64
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at) VALUES($1,1,'refund-lock-product','Refund lock product',$2,$3,'order:8002:item:1','eligible',$4,1,1000,7,$5) RETURNING id`, orderID, distributor, credential, policy, now).Scan(&attribution); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at) VALUES($1,$2,1,$3,1000,0,100,100,0,1000,$4,$5,'pending','','','',1,$4,$4) RETURNING id`, attribution, orderID, distributor, now, now.Add(7*24*time.Hour)).Scan(&commission); err != nil {
		t.Fatal(err)
	}
	return commission
}

func settlementWarningPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping deadline warning PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "aicrm_distribution_warning_" + hex.EncodeToString(random[:])
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config := adminConfig.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		pool.Close()
		admin.Close()
		t.Fatal("locate distribution migrations")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	for _, name := range []string{"0003_access.sql", "0010_product.sql", "0157_distribution_core.sql"} {
		body, readErr := os.ReadFile(filepath.Join(root, "migrations", name))
		if readErr != nil {
			pool.Close()
			admin.Close()
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(ctx, string(body)); execErr != nil {
			pool.Close()
			admin.Close()
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	return pool, func() {
		pool.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	}
}
