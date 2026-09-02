package externaleffects

import (
	"context"
	"github.com/riverqueue/river"
)

type EffectJobArgs struct {
	EffectID   int64 `json:"effect_id"`
	Generation int64 `json:"generation"`
}

func (EffectJobArgs) Kind() string { return "external_effect.execute.v1" }

type Worker struct {
	river.WorkerDefaults[EffectJobArgs]
	repository *Repository
	adapter    ProviderAdapter
}

// ProviderAdapter is owned by outbound. A nil adapter means Provider disabled;
// it cannot claim an external call occurred.
type ProviderAdapter interface {
	Execute(context.Context, Envelope, Attempt) (AdapterResult, error)
}
type Attempt struct {
	Number            int32
	Generation, Fence int64
}
type Completion State
type AdapterResult struct {
	Completion               State
	ReceiptDigest            Digest
	CallAttempted            bool
	RealExternalCallExecuted bool
}

func NewWorker(repository *Repository, adapter ProviderAdapter) *Worker {
	return &Worker{repository: repository, adapter: adapter}
}
func (w *Worker) BindRepository(repository *Repository) error {
	if w == nil || repository == nil || w.repository != nil {
		return ErrInvalid
	}
	w.repository = repository
	return nil
}
func (w *Worker) Work(ctx context.Context, job *river.Job[EffectJobArgs]) error {
	if w == nil || w.repository == nil || job == nil {
		return ErrInvalid
	}
	return w.repository.RunAttempt(ctx, job.Args.EffectID, job.Args.Generation, job.ID, w.adapter)
}
