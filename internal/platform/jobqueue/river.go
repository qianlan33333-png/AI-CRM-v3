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
	if client == nil || tx == nil || args == nil {
		return nil, ErrUnavailable
	}
	return client.InsertTx(ctx, tx, args, &river.InsertOpts{Queue: OutboundQueue})
}

type Runtime struct{ client *river.Client[pgx.Tx] }

func NewRuntime(pool *pgxpool.Pool, workers *river.Workers) (*Runtime, error) {
	if pool == nil || workers == nil {
		return nil, ErrUnavailable
	}
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Queues: map[string]river.QueueConfig{OutboundQueue: {MaxWorkers: 4}}, Workers: workers})
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
