package app

import (
	"context"

	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type overviewStore interface {
	ReadPaidOverview(context.Context, paymentport.OverviewWindow) (paymentport.PaidOverview, error)
	ReadRefundOverview(context.Context, paymentport.OverviewWindow) (paymentport.RefundOverview, error)
}

type OverviewReader struct {
	uow   platformport.UnitOfWork
	store overviewStore
}

func NewOverviewReader(uow platformport.UnitOfWork, store overviewStore) (*OverviewReader, error) {
	if uow == nil || store == nil {
		return nil, paymentport.ErrUnavailable
	}
	return &OverviewReader{uow: uow, store: store}, nil
}

func (reader *OverviewReader) ReadPaidOverview(ctx context.Context, window paymentport.OverviewWindow) (paymentport.PaidOverview, error) {
	if reader == nil || reader.uow == nil || reader.store == nil || !window.Valid() {
		return paymentport.PaidOverview{}, paymentport.ErrUnavailable
	}
	var result paymentport.PaidOverview
	err := reader.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = reader.store.ReadPaidOverview(tx, window)
		return readErr
	})
	return result, err
}

func (reader *OverviewReader) ReadRefundOverview(ctx context.Context, window paymentport.OverviewWindow) (paymentport.RefundOverview, error) {
	if reader == nil || reader.uow == nil || reader.store == nil || !window.Valid() {
		return paymentport.RefundOverview{}, paymentport.ErrUnavailable
	}
	var result paymentport.RefundOverview
	err := reader.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = reader.store.ReadRefundOverview(tx, window)
		return readErr
	})
	return result, err
}

var _ paymentport.OverviewReader = (*OverviewReader)(nil)
