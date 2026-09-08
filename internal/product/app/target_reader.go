package app

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

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
var _ productport.SidebarProductShareReader = (*TargetReader)(nil)

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
		return productport.ProductOption{ID: item.ID, Code: item.ProductCode, ProductType: productport.ProductOptionStandard, Name: item.Name, PriceMinor: item.PriceMinor, Currency: item.Currency, CoverURL: publicProductCardCover(item)}, nil
	case productport.ProductOptionServicePeriod:
		item, err := reader.period.GetServicePeriodProduct(ctx, id)
		if err != nil {
			return productport.ProductOption{}, err
		}
		return productport.ProductOption{ID: item.ServiceProductID, Code: item.ProductCode, ProductType: productport.ProductOptionServicePeriod, Name: item.Name, PriceMinor: item.PriceMinor, Currency: item.Currency, CoverURL: servicePeriodCardCover(item)}, nil
	default:
		return productport.ProductOption{}, ErrInvalidProduct
	}
}

// productCardCover accepts only an HTTPS image supplied as a public asset.
// Admin image-library preview URLs are relative /api/admin paths and require
// an administrator session, so they must never become a WeCom news imgUrl.
func productCardCover(images []string) string {
	for _, raw := range images {
		if image, ok := publicHTTPSImage(raw); ok {
			return image
		}
	}
	return ""
}

// servicePeriodCardCover prefers the Product-owned public detail-media route.
// It is derived only from an enabled service-period item's canonical admin
// projection; the public handler independently verifies both its lifecycle
// and image permission before returning bytes.  A raw images[] entry may be
// an authenticated image-library preview, so it is used only when it is an
// explicit HTTPS public asset.
func servicePeriodCardCover(item productport.ServicePeriodProduct) string {
	presentation, err := publicServicePeriodPresentation(item.AdminProjection)
	if err == nil {
		for _, media := range presentation.Media {
			if media.ImageID > 0 {
				return "/api/h5/service-period-products/" + url.PathEscape(item.ProductCode) + "/images/" + strconv.FormatInt(media.ImageID, 10) + "/variants/original"
			}
		}
	}
	return productCardCover(item.Images)
}

// ReadSidebarShareProduct rechecks the authoritative local lifecycle exactly
// when the sidebar creates its send intent. A generic Product target can be
// suitable for configuration while still being a draft or disabled item that
// must not be shared.
func (reader *TargetReader) ReadSidebarShareProduct(ctx context.Context, kind productport.ProductOptionType, id productport.ID) (productport.SidebarShareProduct, error) {
	if reader == nil || id < 1 {
		return productport.SidebarShareProduct{}, ErrNotFound
	}
	switch kind {
	case productport.ProductOptionStandard:
		item, err := reader.ordinary.Get(ctx, id)
		if err != nil {
			return productport.SidebarShareProduct{}, err
		}
		if item.LocalLifecycle != productport.LocalProductEnabled {
			return productport.SidebarShareProduct{}, ErrNotFound
		}
		return productport.SidebarShareProduct{ID: item.ID, Code: item.ProductCode, ProductType: kind, Name: item.Name, CoverURL: publicProductCardCover(item)}, nil
	case productport.ProductOptionServicePeriod:
		item, err := reader.period.GetServicePeriodProduct(ctx, id)
		if err != nil {
			return productport.SidebarShareProduct{}, err
		}
		if !item.Enabled || item.Archived || item.Lifecycle != productport.ServicePeriodEnabled {
			return productport.SidebarShareProduct{}, ErrNotFound
		}
		return productport.SidebarShareProduct{ID: item.ServiceProductID, Code: item.ProductCode, ProductType: kind, Name: item.Name, CoverURL: servicePeriodCardCover(item)}, nil
	default:
		return productport.SidebarShareProduct{}, ErrInvalidProduct
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
