package app

import (
	"context"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// TargetReader is the Product-owned implementation of the narrow product
// applicability port. It composes the two Product application services; the
// Coupon domain imports only product/port.
type TargetReader struct {
	ordinary *Service
	period   *ServicePeriodService
}

var _ productport.ProductTargetReader = (*TargetReader)(nil)

func NewTargetReader(ordinary *Service, period *ServicePeriodService) (*TargetReader, error) {
	if ordinary == nil || period == nil {
		return nil, ErrUnavailable
	}
	return &TargetReader{ordinary: ordinary, period: period}, nil
}

func (reader *TargetReader) ReadProductTarget(ctx context.Context, kind productport.ProductOptionType, id productport.ID) (productport.ProductOption, error) {
	if reader == nil || id < 1 {
		return productport.ProductOption{}, ErrNotFound
	}
	switch kind {
	case productport.ProductOptionStandard:
		item, err := reader.ordinary.Get(ctx, id)
		if err != nil {
			return productport.ProductOption{}, err
		}
		return productport.ProductOption{ID: item.ID, ProductType: productport.ProductOptionStandard, Name: item.Name, PriceMinor: item.PriceMinor, Currency: item.Currency}, nil
	case productport.ProductOptionServicePeriod:
		item, err := reader.period.GetServicePeriodProduct(ctx, id)
		if err != nil {
			return productport.ProductOption{}, err
		}
		return productport.ProductOption{ID: item.ServiceProductID, ProductType: productport.ProductOptionServicePeriod, Name: item.Name, PriceMinor: item.PriceMinor, Currency: item.Currency}, nil
	default:
		return productport.ProductOption{}, ErrInvalidProduct
	}
}
