package provider

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

type sharedFactsScanner struct {
	testing *testing.T
	plan    bool
}

func (s sharedFactsScanner) Scan(dest ...any) error {
	s.testing.Helper()
	if len(dest) != 38 {
		s.testing.Fatalf("shared-facts scan destinations=%d", len(dest))
	}
	beijing := time.Date(2026, 9, 5, 9, 0, 0, 0, sourceLocation)
	*dest[0].(*string) = "user-1"
	*dest[1].(*sql.NullString) = sql.NullString{String: "union-1", Valid: true}
	*dest[2].(*sql.NullString) = sql.NullString{String: "13800138000", Valid: true}
	*dest[3].(*string) = "standard"
	*dest[4].(*sql.NullTime) = sql.NullTime{Time: beijing.Add(72 * time.Hour), Valid: true}
	*dest[5].(*int64), *dest[6].(*int64), *dest[7].(*int64), *dest[8].(*int64) = 100, 1, 5, 2
	*dest[9].(*string) = "user_id"
	*dest[10].(*int64), *dest[11].(*int64), *dest[12].(*int64) = 1, 2, 3
	*dest[13].(*int64), *dest[14].(*int64), *dest[15].(*int64) = 4, 5, 6
	*dest[16].(*[]byte) = []byte(`{"source":"fixture"}`)
	*dest[17].(*sourceNullTime) = sourceNullTime{Time: beijing.Add(-time.Hour), Valid: true}
	*dest[18].(*sql.NullString) = sql.NullString{String: "user_message", Valid: true}
	*dest[19].(*sql.NullString) = sql.NullString{String: "stage", Valid: true}
	*dest[20].(*sql.NullString) = sql.NullString{String: "main", Valid: true}
	*dest[21].(*sql.NullString) = sql.NullString{String: "segment", Valid: true}
	*dest[22].(*[]byte) = []byte(`["topic"]`)
	*dest[23].(*sql.NullString) = sql.NullString{String: "pain", Valid: true}
	*dest[24].(*sql.NullTime) = sql.NullTime{Time: beijing.Add(-48 * time.Hour), Valid: true}
	*dest[25].(*int64) = 1
	if s.plan {
		*dest[26].(*int64) = 1
		*dest[27].(*sql.NullString) = sql.NullString{String: "active", Valid: true}
		*dest[28].(*sql.NullInt64) = sql.NullInt64{Int64: 2, Valid: true}
		*dest[29].(*sql.NullInt64) = sql.NullInt64{Int64: 4, Valid: true}
	}
	*dest[30].(*int64) = 7
	*dest[31].(*sourceNullTime) = sourceNullTime{Time: beijing.Add(-2 * time.Hour), Valid: true}
	*dest[32].(*int64), *dest[33].(*int64) = 1, 1
	*dest[34].(*sql.NullString) = sql.NullString{String: "user_id", Valid: true}
	*dest[35].(*sql.NullString) = sql.NullString{String: "active", Valid: true}
	*dest[36].(*sql.NullTime) = sql.NullTime{Time: beijing.Add(24 * time.Hour), Valid: true}
	*dest[37].(*time.Time) = beijing
	return nil
}

func TestSharedFactsScannerPreservesMembershipSourceAndNullableLearningPlan(t *testing.T) {
	for _, plan := range []bool{false, true} {
		row, err := scanSourceRow(sharedFactsScanner{testing: t, plan: plan})
		if err != nil {
			t.Fatal(err)
		}
		if row.MembershipSource != "user_id" || row.MembershipStatus != "active" || row.MembershipExpiresAt == nil || !row.IsMember || !row.MembershipRecordFound {
			t.Fatalf("membership source facts=%+v", row)
		}
		if plan {
			if !row.LearningPlanFound || row.LearningPlanStatus != "active" || row.LearningPlanCurrent == nil || *row.LearningPlanCurrent != 2 || row.LearningPlanTotal == nil || *row.LearningPlanTotal != 4 {
				t.Fatalf("learning plan=%+v", row)
			}
		} else if row.LearningPlanFound || row.LearningPlanCurrent != nil || row.LearningPlanTotal != nil || row.LearningPlanStatus != "" {
			t.Fatalf("missing learning plan became a zero plan: %+v", row)
		}
	}
}

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

func TestCurrentQueryReadsDistinctLegacySharedFacts(t *testing.T) {
	for _, required := range []string{
		"first_login_at", "total_tokens", "new_version_user_path_progress", "new_version_lesson_path_items", "new_version_card_open_log",
		"p.status IN ('active','done','paused')", "CASE WHEN p.status='active' THEN 0 ELSE 1 END", "p.updated_at DESC,p.id DESC",
		"LEAST(GREATEST(COALESCE(p.current_seq,0),0),COALESCE(t.total_lessons,0))", "o.opened_at>=? - INTERVAL 7 DAY",
	} {
		if !strings.Contains(currentBatchSQL, required) {
			t.Fatalf("shared fact query missing %q", required)
		}
	}
	for _, required := range []string{
		"THEN 'subscription' ELSE 'none' END",
		"THEN COALESCE(s.expires_at,u.member_expires_at) ELSE NULL END",
		"mc.status COLLATE utf8mb4_general_ci",
	} {
		if !strings.Contains(currentBatchSQL, required) {
			t.Fatalf("shared membership source rule missing %q", required)
		}
	}
	if strings.Contains(currentBatchSQL, "sessions_7d AS has_token_usage") || strings.Contains(currentBatchSQL, "last_used AS formally_logged_in") {
		t.Fatal("dashboard counters must not stand in for legacy shared facts")
	}
}
