package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// TestPostgreSQLAdminDistributorOrderDetailPagination proves the drawer's
// related-order list is scoped and paged by the database. The browser must
// never fetch a global page and filter it locally, or lose matching rows that
// land on later pages.
func TestPostgreSQLAdminDistributorOrderDetailPagination(t *testing.T) {
	pool, cleanup := settlementWarningPool(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 14, 10, 30, 0, 0, time.UTC)
	owner := seedAdminDetailDistributor(t, ctx, pool, 301, "DSTDETAIL301", now)
	other := seedAdminDetailDistributor(t, ctx, pool, 302, "DSTDETAIL302", now)
	policy := seedAdminDetailPolicy(t, ctx, pool, now)
	credential := seedAdminDetailCredential(t, ctx, pool, owner, now)
	otherCredential := seedAdminDetailCredential(t, ctx, pool, other, now)

	firstID := seedAdminDetailAttribution(t, ctx, pool, owner, credential, policy, 9101, "owner-first", now)
	secondID := seedAdminDetailAttribution(t, ctx, pool, owner, credential, policy, 9102, "owner-second", now.Add(time.Minute))
	thirdID := seedAdminDetailAttribution(t, ctx, pool, owner, credential, policy, 9103, "owner-third", now.Add(2*time.Minute))
	_ = seedAdminDetailAttribution(t, ctx, pool, other, otherCredential, policy, 9199, "other-distributor", now.Add(3*time.Minute))

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

	var first, secondIDs []int64
	var cursor string
	err = uow.Within(ctx, func(tx context.Context) error {
		page, readErr := repository.ListAdminOrdersByDistributor(tx, owner, "", 2)
		if readErr != nil {
			return readErr
		}
		for _, item := range page.Items {
			first = append(first, item.AttributionID)
			if item.DistributorPublicNo != "DSTDETAIL301" {
				return fmt.Errorf("leaked distributor %q", item.DistributorPublicNo)
			}
		}
		cursor = page.NextCursor
		page, readErr = repository.ListAdminOrdersByDistributor(tx, owner, cursor, 2)
		if readErr != nil {
			return readErr
		}
		for _, item := range page.Items {
			secondIDs = append(secondIDs, item.AttributionID)
			if item.DistributorPublicNo != "DSTDETAIL301" {
				return fmt.Errorf("leaked distributor %q", item.DistributorPublicNo)
			}
		}
		if page.NextCursor != "" {
			return fmt.Errorf("unexpected terminal cursor %q", page.NextCursor)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0] != firstID || first[1] != secondID || cursor != fmt.Sprint(secondID) {
		t.Fatalf("first detail page ids=%v cursor=%q", first, cursor)
	}
	if len(secondIDs) != 1 || secondIDs[0] != thirdID {
		t.Fatalf("second detail page ids=%v", secondIDs)
	}
}

func seedAdminDetailDistributor(t *testing.T, ctx context.Context, pool *pgxpool.Pool, customerID int64, publicNo string, now time.Time) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_distributors(customer_id,public_no,agreement_version,enabled,registered_at,version,created_at,updated_at) VALUES($1,$2,'v1',true,$3,1,$3,$3) RETURNING id`, customerID, publicNo, now).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func seedAdminDetailPolicy(t *testing.T, ctx context.Context, pool *pgxpool.Pool, now time.Time) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES(9301,'standard_product',true,1000,7,1,$1,$1) RETURNING id`, now).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func seedAdminDetailCredential(t *testing.T, ctx context.Context, pool *pgxpool.Pool, distributorID int64, now time.Time) int64 {
	t.Helper()
	var id int64
	digest := make([]byte, 32)
	digest[0] = byte(distributorID)
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_promotion_credentials(distributor_id,product_id,product_type,token_digest,status,created_at,expires_at) VALUES($1,9301,'standard_product',$2,'active',$3,$4) RETURNING id`, distributorID, digest, now, now.Add(time.Hour)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func seedAdminDetailAttribution(t *testing.T, ctx context.Context, pool *pgxpool.Pool, distributorID, credentialID, policyID, orderID int64, productName string, now time.Time) int64 {
	t.Helper()
	var attributionID int64
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at) VALUES($1,1,$2,$3,$4,$5,$6,'eligible',$7,1,1000,7,$8) RETURNING id`, orderID, productName, productName, distributorID, credentialID, "order:"+fmt.Sprint(orderID)+":line:1", policyID, now).Scan(&attributionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at) VALUES($1,$2,1,$3,1000,0,100,100,0,1000,$4,$5,'pending','','','',1,$4,$4)`, attributionID, orderID, distributorID, now, now.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	return attributionID
}
