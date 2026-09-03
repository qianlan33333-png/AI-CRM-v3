// Package jobqueue owns the PostgreSQL-backed River runtime boundary.
package jobqueue

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
)

const OutboundQueue = "outbound"

var ErrUnavailable = errors.New("River client unavailable")

// CheckReady verifies the durable River schema without importing River's
// migrator into application runtime code. The effects module consumes this
// platform-level readiness boundary alongside its own schema requirement.
func CheckReady(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return ErrUnavailable
	}
	var complete bool
	err := pool.QueryRow(ctx, `SELECT NOT EXISTS (
        SELECT 1 FROM unnest(ARRAY['river_job','river_leader','river_migration']) AS required(name)
        WHERE to_regclass(current_schema() || '.' || required.name) IS NULL
    )`).Scan(&complete)
	if err != nil {
		return err
	}
	if !complete {
		return errors.New("River schema is not ready")
	}
	return nil
}

// NewInsertClient is deliberately insert-only and permits only registered
// worker kinds at runtime. Queue insertion may be called with an active pgx
// transaction so the caller's business fact and River row commit atomically.
func NewInsertClient(pool *pgxpool.Pool, workers *river.Workers) (*river.Client[pgx.Tx], error) {
	if pool == nil {
		return nil, ErrUnavailable
	}
	if workers == nil {
		return nil, ErrUnavailable
	}
	return river.NewClient(riverpgxv5.New(pool), &river.Config{Workers: workers})
}

func InsertTx(ctx context.Context, client *river.Client[pgx.Tx], tx pgx.Tx, args river.JobArgs) (*rivertype.JobInsertResult, error) {
	return InsertTxAt(ctx, client, tx, args, time.Time{})
}

// InsertTxAt is the shared durable-job insertion seam. A non-zero scheduled
// time is guaranteed not to run before that instant; zero preserves the
// immediate queue behavior used by existing effects.
func InsertTxAt(ctx context.Context, client *river.Client[pgx.Tx], tx pgx.Tx, args river.JobArgs, scheduledAt time.Time) (*rivertype.JobInsertResult, error) {
	if client == nil || tx == nil || args == nil {
		return nil, ErrUnavailable
	}
	return client.InsertTx(ctx, tx, args, &river.InsertOpts{Queue: OutboundQueue, ScheduledAt: scheduledAt.UTC()})
}

type Runtime struct{ client *river.Client[pgx.Tx] }

func NewRuntime(pool *pgxpool.Pool, workers *river.Workers, queueNames ...string) (*Runtime, error) {
	if pool == nil || workers == nil {
		return nil, ErrUnavailable
	}
	if len(queueNames) == 0 {
		queueNames = []string{OutboundQueue}
	}
	queues := make(map[string]river.QueueConfig, len(queueNames))
	for _, name := range queueNames {
		if name == "" {
			return nil, ErrUnavailable
		}
		queues[name] = river.QueueConfig{MaxWorkers: 4}
	}
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Queues: queues, Workers: workers})
	if err != nil {
		return nil, err
	}
	return &Runtime{client: client}, nil
}
func (r *Runtime) Run(ctx context.Context) error {
	if r == nil || r.client == nil {
		return ErrUnavailable
	}
	if err := r.client.Start(context.WithoutCancel(ctx)); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return r.client.Stop(shutdown)
	case <-r.client.Stopped():
		return errors.New("River stopped unexpectedly")
	}
}
