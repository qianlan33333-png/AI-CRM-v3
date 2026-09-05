package http

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// ServicePeriodPublicHandler owns the old /s/{code} public surface. It is a
// separate route from ordinary /p and /pay: codes are exact, never numeric
// aliases, and a standard Product with the same text cannot be selected here.
type ServicePeriodPublicHandler struct {
	products     productport.ServicePeriodPublicReader
	uow          platformport.UnitOfWork
	sessions     paymentport.SessionReader
	entitlements orderport.EntitlementService
	now          func() time.Time
}

func NewServicePeriodPublicHandler(products productport.ServicePeriodPublicReader) (*ServicePeriodPublicHandler, error) {
	if products == nil {
		return nil, errors.New("service period public reader is required")
	}
	return &ServicePeriodPublicHandler{products: products, now: time.Now}, nil
}

// SetTrustedPublicState adds existing session and entitlement readers to the
// public Host. It only reads canonical Customer facts from Payment's opaque
// session; the route never resolves identity or mutates access.
func (h *ServicePeriodPublicHandler) SetTrustedPublicState(uow platformport.UnitOfWork, sessions paymentport.SessionReader, entitlements orderport.EntitlementService) error {
	if h == nil || uow == nil || sessions == nil || entitlements == nil {
		return errors.New("service period public state dependencies are required")
	}
	h.uow, h.sessions, h.entitlements = uow, sessions, entitlements
	return nil
}

func (h *ServicePeriodPublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.products == nil || r.Method != http.MethodGet || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	code, payment, ok := servicePeriodPublicCode(r.URL.EscapedPath())
	if !ok {
		http.NotFound(w, r)
		return
	}
	product, err := h.products.ReadPublicServicePeriodByCode(r.Context(), code)
	if err != nil || product.ID < 1 || product.ProductType != productport.ProductOptionServicePeriod || product.Code != code || product.Name == "" || product.PriceMinor < 1 || product.Currency != "CNY" || product.Version < 1 || product.ServicePeriodDurationDays < 1 {
		http.NotFound(w, r)
		return
	}
	public := publicProduct{ID: product.ID, Name: product.Name, PriceMinor: product.PriceMinor, Currency: product.Currency, PaymentPath: "/s/" + url.PathEscape(product.Code) + "/pay", BuyButtonText: "立即报名", ProductKind: "service_period", CouponTargetRef: "service_period:" + strconv.FormatInt(int64(product.ID), 10), ServicePeriodDurationDays: product.ServicePeriodDurationDays}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' https: data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	if payment {
		if err = publicProductPage.Execute(w, struct {
			Product publicProduct
			Payment bool
		}{Product: public, Payment: true}); err != nil {
			return
		}
		return
	}
	state := servicePeriodPublicState{DonorStyle: servicePeriodDonorStyles(), Product: public, Status: "none", CTA: "立即报名"}
	if entitlement, found := h.trustedEntitlement(r.Context(), r, product.ID); found {
		state.Status, state.EndAt = entitlement.Status, entitlement.EndAt.UTC()
		if state.Status == "active" && !state.EndAt.After(h.now().UTC()) {
			state.Status = "expired"
		}
		switch state.Status {
		case "active":
			state.CTA = "立即续费"
			state.RemainingDays = remainingServicePeriodDays(h.now().UTC(), state.EndAt)
		case "expired", "refunded":
			state.Status, state.CTA = "expired", "重新开通"
		default:
			state.Status, state.CTA = "none", "立即报名"
		}
	}
	_ = servicePeriodPublicPage.Execute(w, state)
}

func servicePeriodPublicCode(path string) (string, bool, bool) {
	if !strings.HasPrefix(path, "/s/") {
		return "", false, false
	}
	tail := strings.TrimPrefix(path, "/s/")
	payment := false
	if strings.HasSuffix(tail, "/pay") {
		payment, tail = true, strings.TrimSuffix(tail, "/pay")
	}
	if tail == "" || strings.Contains(tail, "/") {
		return "", false, false
	}
	code, err := url.PathUnescape(tail)
	if err != nil || code == "" || code != strings.TrimSpace(code) || len(code) > 200 || strings.ContainsRune(code, '\x00') {
		return "", false, false
	}
	return code, payment, true
}

func (h *ServicePeriodPublicHandler) trustedEntitlement(ctx context.Context, r *http.Request, productID productport.ID) (orderport.Entitlement, bool) {
	if h == nil || h.uow == nil || h.sessions == nil || h.entitlements == nil {
		return orderport.Entitlement{}, false
	}
	cookie, err := r.Cookie(paymentport.TrustedSessionCookieName)
	if err != nil || cookie.Value == "" {
		return orderport.Entitlement{}, false
	}
	var actor paymentport.SessionActor
	var page orderport.EntitlementPage
	err = h.uow.Within(ctx, func(txctx context.Context) error {
		var lookupErr error
		actor, lookupErr = h.sessions.LookupWithin(txctx, cookie.Value, h.now().UTC())
		if lookupErr != nil || actor.PayerCustomerID < 1 {
			return errors.New("trusted session unavailable")
		}
		page, lookupErr = h.entitlements.ListCustomerEntitlements(txctx, actor.PayerCustomerID, 100)
		return lookupErr
	})
	if err != nil {
		return orderport.Entitlement{}, false
	}
	for _, item := range page.Items {
		if item.ServiceProductID == int64(productID) {
			return item, true
		}
	}
	return orderport.Entitlement{}, false
}

func remainingServicePeriodDays(now, end time.Time) int32 {
	if !end.After(now) {
		return 0
	}
	days := int32(end.Sub(now).Hours() / 24)
	if end.After(now.Add(time.Duration(days) * 24 * time.Hour)) {
		days++
	}
	if days < 1 {
		return 1
	}
	return days
}
