package wecom

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const CustomerSyncQueue = "customer-directory"

type CustomerSyncJobArgs struct {
	RunID int64 `json:"run_id"`
}

func (CustomerSyncJobArgs) Kind() string { return "wecom.customer-directory-sync.v1" }

// CustomerSyncJobEnqueuer is the narrow durable-delivery port used while the
// sync run, audit and River row share the caller's PostgreSQL transaction.
type CustomerSyncJobEnqueuer interface {
	EnqueueCustomerSync(context.Context, int64) error
}

type RiverCustomerSyncEnqueuer struct {
	client *river.Client[pgx.Tx]
}

func NewRiverCustomerSyncEnqueuer(client *river.Client[pgx.Tx]) (*RiverCustomerSyncEnqueuer, error) {
	if client == nil {
		return nil, ErrSyncNotReady
	}
	return &RiverCustomerSyncEnqueuer{client: client}, nil
}

func (enqueuer *RiverCustomerSyncEnqueuer) EnqueueCustomerSync(ctx context.Context, runID int64) error {
	if enqueuer == nil || enqueuer.client == nil || runID < 1 {
		return ErrSyncNotReady
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = enqueuer.client.InsertTx(ctx, tx, CustomerSyncJobArgs{RunID: runID}, &river.InsertOpts{
		Queue: CustomerSyncQueue, MaxAttempts: 12,
	})
	return err
}

type CustomerSyncWorker struct {
	river.WorkerDefaults[CustomerSyncJobArgs]
	service *CustomerSyncService
}

func NewCustomerSyncWorker() *CustomerSyncWorker { return &CustomerSyncWorker{} }

func (worker *CustomerSyncWorker) BindService(service CustomerSyncService) error {
	if worker == nil || worker.service != nil || !service.Ready() {
		return ErrSyncNotReady
	}
	worker.service = &service
	return nil
}

func (worker *CustomerSyncWorker) Work(ctx context.Context, job *river.Job[CustomerSyncJobArgs]) error {
	if worker == nil || worker.service == nil || job == nil || job.JobRow == nil || job.Args.RunID < 1 {
		return ErrSyncNotReady
	}
	for {
		run, err := worker.service.Get(ctx, job.Args.RunID)
		if err != nil {
			return err
		}
		if run.Status == SyncSucceeded || run.Status == SyncFailedTerminal {
			return nil
		}
		if err = worker.service.processRunOnce(ctx, run); err == nil {
			continue
		}
		latest, loadErr := worker.service.Get(ctx, job.Args.RunID)
		if loadErr != nil {
			return errors.Join(err, loadErr)
		}
		if latest.Status == SyncFailedTerminal {
			return nil
		}
		if job.MaxAttempts > 0 && job.Attempt >= job.MaxAttempts {
			return worker.service.UOW.Within(ctx, func(txContext context.Context) error {
				return worker.service.Store.Terminate(txContext, job.Args.RunID, "retry_exhausted:"+syncRetryCode(err))
			})
		}
		return err
	}
}
