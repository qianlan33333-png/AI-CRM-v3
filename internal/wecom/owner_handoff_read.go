package wecom

import (
	"context"
	"crypto/sha256"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// OwnerHandoffRelationship returns only one exact, current relationship. It
// does not infer an employee from any other contact relation and never writes
// the existing WeCom projection.
func (PostgreSQLFollowRelationshipStore) OwnerHandoffRelationship(ctx context.Context, customerID customerdomain.CustomerID, corpScope, employeeUserID string) (wecomport.OwnerHandoffRelationship, error) {
	corpID, ok := ownerHandoffCorpID(corpScope)
	if !ok || customerID < 1 || !validFollowText(employeeUserID, 1024) {
		return wecomport.OwnerHandoffRelationship{}, ErrInvalidFollowRelationship
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return wecomport.OwnerHandoffRelationship{}, err
	}
	var active bool
	var updated time.Time
	err = tx.QueryRow(ctx, `SELECT active,updated_at FROM wecom_follow_relationships WHERE corp_id=$1 AND employee_id=$2 AND customer_id=$3`, corpID, employeeUserID, customerID).Scan(&active, &updated)
	if err == pgx.ErrNoRows {
		return wecomport.OwnerHandoffRelationship{CustomerID: customerID, CorpScope: corpScope, EmployeeUserID: employeeUserID}, nil
	}
	if err != nil {
		return wecomport.OwnerHandoffRelationship{}, err
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{"owner-handoff-relation-v1", corpID, employeeUserID, strconv.FormatInt(int64(customerID), 10), strconv.FormatBool(active), updated.UTC().Format(time.RFC3339Nano)}, "\x00")))
	return wecomport.OwnerHandoffRelationship{CustomerID: customerID, CorpScope: corpScope, EmployeeUserID: employeeUserID, Active: active, VersionDigest: digest}, nil
}

func ownerHandoffCorpID(scope string) (string, bool) {
	if !strings.HasPrefix(scope, "wecom-corp:") {
		return "", false
	}
	corpID := strings.TrimPrefix(scope, "wecom-corp:")
	return corpID, validFollowText(corpID, 256)
}

var _ wecomport.OwnerHandoffRelationshipReader = PostgreSQLFollowRelationshipStore{}
