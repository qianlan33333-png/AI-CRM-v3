package app

import (
	"context"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type readModelStore interface {
	Within(context.Context, func(context.Context) error) error
	EarningsByCustomer(context.Context, int64) (distributionport.Earnings, error)
	ListCommissionsByCustomer(context.Context, int64, distributiondomain.CommissionStatus, string, int32) (distributionport.CommissionPage, error)
	ListAdminDistributors(context.Context, string, int32) (distributionport.AdminPage[distributionport.AdminDistributor], error)
	ListAdminOrders(context.Context, string, int32) (distributionport.AdminPage[distributionport.AdminOrder], error)
	ListAdminExceptions(context.Context, string, int32) (distributionport.AdminPage[distributionport.AdminException], error)
}
type ReadModelService struct {
	uow   platformport.UnitOfWork
	store readModelStore
}

func NewReadModelService(uow platformport.UnitOfWork, store readModelStore) (*ReadModelService, error) {
	if uow == nil || store == nil {
		return nil, distributionport.ErrUnavailable
	}
	return &ReadModelService{uow, store}, nil
}
func (s *ReadModelService) Earnings(c context.Context, a distributionport.TrustedSessionActor) (v distributionport.Earnings, e error) {
	if s == nil || !a.Valid() {
		return v, distributionport.ErrUnauthorized
	}
	e = s.uow.Within(c, func(t context.Context) error { v, e = s.store.EarningsByCustomer(t, a.CustomerID); return e })
	return
}
func (s *ReadModelService) ListCommissions(c context.Context, a distributionport.TrustedSessionActor, st distributiondomain.CommissionStatus, cur string, l int32) (v distributionport.CommissionPage, e error) {
	if s == nil || !a.Valid() {
		return v, distributionport.ErrUnauthorized
	}
	e = s.uow.Within(c, func(t context.Context) error {
		v, e = s.store.ListCommissionsByCustomer(t, a.CustomerID, st, cur, l)
		return e
	})
	return
}
func (s *ReadModelService) ListAdminDistributors(c context.Context, x string, l int32) (v distributionport.AdminPage[distributionport.AdminDistributor], e error) {
	e = s.uow.Within(c, func(t context.Context) error { v, e = s.store.ListAdminDistributors(t, x, l); return e })
	return
}
func (s *ReadModelService) ListAdminOrders(c context.Context, x string, l int32) (v distributionport.AdminPage[distributionport.AdminOrder], e error) {
	e = s.uow.Within(c, func(t context.Context) error { v, e = s.store.ListAdminOrders(t, x, l); return e })
	return
}
func (s *ReadModelService) ListAdminExceptions(c context.Context, x string, l int32) (v distributionport.AdminPage[distributionport.AdminException], e error) {
	e = s.uow.Within(c, func(t context.Context) error { v, e = s.store.ListAdminExceptions(t, x, l); return e })
	return
}
