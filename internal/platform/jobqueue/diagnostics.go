package jobqueue

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"time"
)

// DiagnosticCounts uses scheduled_at, not creation age: future appointments
// are not backlog. Historical terminal failures are reported separately.
func DiagnosticCounts(ctx context.Context, pool *pgxpool.Pool, at time.Time) (map[string]int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, e := platformpostgres.DiagnosticTransaction(ctx, pool)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	var overdue, age, running, discarded, future, completed int64
	err := tx.QueryRow(ctx, `SELECT
 count(*) FILTER(WHERE state IN ('available','scheduled','retryable') AND scheduled_at<$1::timestamptz-interval '10 minutes'),
 coalesce(extract(epoch FROM $1::timestamptz-min(scheduled_at) FILTER(WHERE state IN ('available','scheduled','retryable') AND scheduled_at<$1)),0)::bigint,
 count(*) FILTER(WHERE state='running' AND attempted_at<$1::timestamptz-interval '10 minutes'),
 count(*) FILTER(WHERE state='discarded'),
 count(*) FILTER(WHERE state IN ('scheduled','retryable') AND scheduled_at>$1),
 count(*) FILTER(WHERE state='completed' AND finalized_at >= $1::timestamptz-interval '1 hour')
 FROM river_job`, at.UTC()).Scan(&overdue, &age, &running, &discarded, &future, &completed)
	return map[string]int64{"due_over_10m": overdue, "oldest_due_seconds": age, "running_over_10m": running, "discarded_retained": discarded, "future_scheduled": future, "completed_last_hour": completed}, err
}

// Queue observations are advisory live worker evidence maintained by River.
// They do not prove an external host can reach this machine.
func WorkerDiagnosticCounts(ctx context.Context, pool *pgxpool.Pool, at time.Time, queues ...string) (map[string]int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, e := platformpostgres.DiagnosticTransaction(ctx, pool)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	var fresh, stale, paused, missing int64
	expected := queues
	if len(expected) == 0 {
		expected = []string{OutboundQueue, OutboundWelcomeQueue, OutboundExcelQueue, OutboundMediaQueue, OpsInspectionQueue, OpsNotificationQueue, OpsRetentionQueue}
	}
	err := tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE q.name IS NOT NULL AND q.updated_at >= $1::timestamptz-interval '2 minutes'),count(*) FILTER(WHERE q.name IS NOT NULL AND q.updated_at < $1::timestamptz-interval '2 minutes'),count(*) FILTER(WHERE q.paused_at IS NOT NULL),count(*) FILTER(WHERE q.name IS NULL) FROM unnest($2::text[]) expected(name) LEFT JOIN river_queue q USING(name)`, at.UTC(), expected).Scan(&fresh, &stale, &paused, &missing)
	return map[string]int64{"expected_queues": int64(len(expected)), "fresh_queue_observations": fresh, "stale_queue_observations": stale, "missing_queue_observations": missing, "paused_queues": paused}, err
}
