package externaleffects

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"time"
)

func (r *Repository) ReadOpsDiagnosticCounts(ctx context.Context, at time.Time) (map[string]int64, error) {
	if r == nil || r.pool == nil || at.IsZero() {
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='3s'; SET LOCAL lock_timeout='500ms'`); err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	var unknown, retryable, failed, expired, due, attemptedHour, executedHour int64
	err = tx.QueryRow(ctx, `SELECT
 count(*) FILTER(WHERE e.state='outcome_unknown'),
 count(*) FILTER(WHERE e.state='retryable_failed'),
 count(*) FILTER(WHERE e.state='final_failed'),
 count(*) FILTER(WHERE e.state='attempted' AND e.lease_expires_at<$1),
 count(*) FILTER(WHERE e.state='queued' AND j.scheduled_at<$1::timestamptz-interval '10 minutes')
 FROM external_effects e LEFT JOIN external_effect_jobs j ON j.effect_id=e.id AND j.generation=e.generation`, at.UTC()).Scan(&unknown, &retryable, &failed, &expired, &due)
	if err != nil {
		return nil, err
	}
	hour := at.UTC().Truncate(time.Hour)
	err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE state='executed') FROM external_effect_attempts WHERE started_at >= $1::timestamptz-interval '1 hour' AND started_at<$1`, hour).Scan(&attemptedHour, &executedHour)
	if err != nil {
		return nil, err
	}
	counts["unknown"] = unknown
	counts["retryable"] = retryable
	counts["final_failed"] = failed
	counts["lease_expired"] = expired
	counts["queued_overdue"] = due
	counts["previous_hour_attempts"] = attemptedHour
	counts["previous_hour_executed"] = executedHour
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return counts, nil
}

var _ port.OpsDiagnosticReader = (*Repository)(nil)
