package provider

import (
	"strings"
	"testing"
	"time"
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

func TestLastUsedScannerAppliesHXCSourceTimezone(t *testing.T) {
	var value sourceNullTime
	if err := value.Scan([]byte("2026-09-04 01:02:03.123456")); err != nil {
		t.Fatalf("scan computed MySQL datetime: %v", err)
	}
	_, offset := value.Time.Zone()
	if !value.Valid || value.Time.Location() != sourceLocation || offset != 8*60*60 {
		t.Fatalf("computed MySQL datetime did not retain Beijing source time: %#v", value)
	}
	want := time.Date(2026, 9, 3, 17, 2, 3, 123456000, time.UTC)
	if !value.Time.Equal(want) {
		t.Fatalf("computed MySQL datetime instant=%s want=%s", value.Time, want)
	}
}

func TestLastUsedScannerAcceptsNativeAndNullValues(t *testing.T) {
	native := time.Date(2026, 9, 4, 2, 3, 4, 0, time.UTC)
	var value sourceNullTime
	if err := value.Scan(native); err != nil || !value.Valid || !value.Time.Equal(native) || value.Time.Location() != sourceLocation {
		t.Fatalf("scan native datetime: value=%#v err=%v", value, err)
	}
	if err := value.Scan(nil); err != nil || value.Valid || !value.Time.IsZero() {
		t.Fatalf("scan NULL datetime: value=%#v err=%v", value, err)
	}
}
