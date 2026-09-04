package port

import (
	"context"
	"errors"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
)

var (
	ErrNotFound            = errors.New("radar: not found")
	ErrConflict            = errors.New("radar: conflict")
	ErrIdempotencyConflict = errors.New("radar: idempotency conflict")
	ErrUnavailable         = errors.New("radar: unavailable")
)

type CreateCommand struct {
	Name           string
	Title          string
	Description    string
	Content        radar.Content
	AuthPolicy     radar.AuthPolicy
	ActorID        int64
	IdempotencyKey string
}

type UpdateCommand struct {
	RadarID        radar.RadarID
	Expected       radar.LinkVersion
	Revision       radar.Revision
	ActorID        int64
	IdempotencyKey string
}

type SetStatusCommand struct {
	RadarID        radar.RadarID
	Expected       radar.LinkVersion
	Target         radar.Status
	ActorID        int64
	IdempotencyKey string
}

type Manager interface {
	List(context.Context, ListQuery) (LinkPage, error)
	Get(context.Context, radar.RadarID) (LinkDetail, error)
	Create(context.Context, CreateCommand) (LinkDetail, error)
	Update(context.Context, UpdateCommand) (LinkDetail, error)
	SetStatus(context.Context, SetStatusCommand) (LinkDetail, error)
}

type PublicCodeGenerator interface {
	GeneratePublicCode() (radar.PublicCode, error)
}

type Clock interface {
	Now() time.Time
}
