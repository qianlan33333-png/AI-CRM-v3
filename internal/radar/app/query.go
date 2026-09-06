package app

import (
	"context"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

type QueryService struct {
	uow   platformport.UnitOfWork
	query radarport.QueryService
}

func NewQueryService(uow platformport.UnitOfWork, query radarport.QueryService) (*QueryService, error) {
	if uow == nil || query == nil {
		return nil, radarport.ErrUnavailable
	}
	return &QueryService{uow: uow, query: query}, nil
}
func (s *QueryService) Stats(ctx context.Context, id radar.RadarID) (radarport.Stats, error) {
	if s == nil || !id.Valid() {
		return radarport.Stats{}, radar.ErrInvalidArgument
	}
	var out radarport.Stats
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; out, e = s.query.Stats(tx, id); return e })
	return out, classify(err)
}
func (s *QueryService) CustomerActivities(ctx context.Context, query radarport.CustomerActivityQuery) (radarport.CustomerActivityPage, error) {
	if s == nil || s.uow == nil || s.query == nil || query.CustomerID < 1 || query.Limit < 1 || query.Limit > 101 || query.Watermark.IsZero() {
		return radarport.CustomerActivityPage{}, radar.ErrInvalidArgument
	}
	store, ok := s.query.(radarport.CustomerActivityStore)
	if !ok || store == nil {
		return radarport.CustomerActivityPage{}, radarport.ErrUnavailable
	}
	var page radarport.CustomerActivityPage
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		page, readErr = store.CustomerActivities(tx, query)
		return readErr
	})
	if err != nil {
		return radarport.CustomerActivityPage{}, classify(err)
	}
	return page, nil
}

var _ radarport.CustomerActivityReader = (*QueryService)(nil)

func (s *QueryService) Events(ctx context.Context, q radarport.EventQuery) (radarport.EventPage, error) {
	if s == nil || !q.RadarID.Valid() {
		return radarport.EventPage{}, radar.ErrInvalidArgument
	}
	if q.Limit == 0 {
		q.Limit = 100
	}
	if q.Limit < 1 || q.Limit > 500 || q.Offset < 0 {
		return radarport.EventPage{}, radar.ErrInvalidArgument
	}
	var out radarport.EventPage
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; out, e = s.query.Events(tx, q); return e })
	return out, classify(err)
}
