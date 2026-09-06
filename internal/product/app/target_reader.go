package app

import (
	"context"
	"encoding/json"

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

func (reader *TargetReader) ReadCheckoutProductWithin(ctx context.Context, kind productport.ProductOptionType, id productport.ID) (productport.CheckoutProduct, error) {
	if reader == nil || reader.ordinary == nil || reader.period == nil || id < 1 {
		return productport.CheckoutProduct{}, ErrNotFound
	}
	switch kind {
	case productport.ProductOptionStandard:
		item, err := reader.ordinary.store.GetForUpdate(ctx, id)
		if err != nil {
			return productport.CheckoutProduct{}, classify(err)
		}
		if !validOrdinaryProduct(item) || item.LocalLifecycle != productport.LocalProductEnabled {
			return productport.CheckoutProduct{}, ErrNotFound
		}
		var projection struct {
			RequireMobile bool `json:"require_mobile"`
		}
		if json.Unmarshal(item.LegacyAdminProjection, &projection) != nil {
			return productport.CheckoutProduct{}, ErrUnavailable
		}
		return productport.CheckoutProduct{ID: item.ID, ProductType: kind, Code: item.ProductCode, Name: item.Name, PriceMinor: item.PriceMinor, Currency: item.Currency, Version: item.Version, RequireMobile: projection.RequireMobile, Images: append([]string(nil), item.Images...)}, nil
	case productport.ProductOptionServicePeriod:
		item, err := reader.period.store.GetServicePeriodProductForUpdate(ctx, id)
		if err != nil {
			return productport.CheckoutProduct{}, classify(err)
		}
		duration, err := reader.period.store.ReadServicePeriodDuration(ctx, id)
		if err != nil || duration < 1 {
			return productport.CheckoutProduct{}, ErrUnavailable
		}
		projected, err := projectServicePeriodProduct(item, duration)
		if err != nil || !projected.Enabled || projected.Lifecycle != productport.ServicePeriodEnabled {
			return productport.CheckoutProduct{}, ErrNotFound
		}
		return productport.CheckoutProduct{ID: projected.ServiceProductID, ProductType: kind, Code: projected.ProductCode, Name: projected.Name, PriceMinor: projected.PriceMinor, Currency: projected.Currency, Version: projected.Version, Images: append([]string(nil), projected.Images...), ServicePeriodDurationDays: duration}, nil
	default:
		return productport.CheckoutProduct{}, ErrInvalidProduct
	}
}

var _ productport.CheckoutProductReader = (*TargetReader)(nil)
