package provider

import (
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestCurrentQueryIsKeysetBatchedAndArgumentComplete(t *testing.T) {
	if strings.Contains(currentBatchSQL, "10001") || !strings.Contains(currentBatchSQL, "id>?") || !strings.Contains(currentBatchSQL, "LIMIT ?") {
		t.Fatal("query must use an uncapped keyset batch")
	}
	if got, want := strings.Count(currentBatchSQL, "?"), len(batchArgs("", time.Now())); got != want {
		t.Fatalf("placeholders=%d args=%d", got, want)
	}
}

func TestCurrentQueryNormalizesKnownMixedProductionCollations(t *testing.T) {
	for _, join := range []string{
		"a.user_id COLLATE utf8mb4_general_ci=u.id",
		"r.user_id COLLATE utf8mb4_general_ci=u.id",
	} {
		if !strings.Contains(currentBatchSQL, join) {
			t.Fatalf("production mixed-collation join is not normalized: %s", join)
		}
	}
}

func TestCurrentQueryUsesTimestampSentinelForLastUsedExpression(t *testing.T) {
	if strings.Contains(currentBatchSQL, "NULLIF(GREATEST(COALESCE(c.peer_last,'1000-01-01')") {
		t.Fatal("last-used sentinel must not coerce the result to bytes")
	}
	if !strings.Contains(currentBatchSQL, "COALESCE(c.peer_last,TIMESTAMP('1000-01-01 00:00:00'))") {
		t.Fatal("last-used sentinel must be an explicit timestamp")
	}
}

func TestLastUsedScannerAcceptsMySQLExpressionBytes(t *testing.T) {
	var value mysql.NullTime
	if err := value.Scan([]byte("2026-09-04 01:02:03.123456")); err != nil {
		t.Fatalf("scan computed MySQL datetime: %v", err)
	}
	if !value.Valid || value.Time.Location() != time.UTC {
		t.Fatalf("computed MySQL datetime was not normalized to UTC: %#v", value)
	}
}
