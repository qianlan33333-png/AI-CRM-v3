package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type fakeSecurity struct{ csrf error }

func (fakeSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (s fakeSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, s.csrf
}

type fakeOptions struct{}

func (fakeOptions) ListProductOptions(_ context.Context, query productport.ProductOptionQuery) (productport.ProductOptionPage, error) {
	kind := productport.ProductOptionStandard
	id := productport.ID(6)
	name := "标准商品"
	if query.ProductType == productport.ProductOptionServicePeriod {
		kind, id, name = productport.ProductOptionServicePeriod, 8, "周期商品"
	}
	return productport.ProductOptionPage{Items: []productport.ProductOption{{ID: id, Name: name, PriceMinor: 1000, Currency: "CNY", ProductType: kind}}, Total: 1, Limit: query.Limit, Offset: query.Offset}, nil
}

type fakeRules struct {
	created couponport.UpsertCommand
	page    couponport.Page
	item    couponport.Coupon
}

type fakeClaims struct {
	page couponport.AdminCouponClaimPage
}

func (f fakeClaims) ListCouponClaims(_ context.Context, couponID couponport.ID, limit, offset int32) (couponport.AdminCouponClaimPage, error) {
	if couponID != 3 {
		return couponport.AdminCouponClaimPage{}, errors.New("unexpected coupon")
	}
	f.page.Limit, f.page.Offset = limit, offset
	return f.page, nil
}

func (f *fakeRules) List(context.Context, int32, int32, string, string) (couponport.Page, error) {
	return f.page, nil
}
func (f *fakeRules) Get(context.Context, couponport.ID) (couponport.Coupon, error) {
	return f.item, nil
}
func (f *fakeRules) Stats(context.Context, couponport.ID) (couponport.RuleStats, error) {
	return couponport.RuleStats{}, nil
}
func (f *fakeRules) Create(_ context.Context, x couponport.UpsertCommand) (couponport.Coupon, error) {
	f.created = x
	return f.item, nil
}
func (f *fakeRules) Update(context.Context, couponport.UpsertCommand) (couponport.Coupon, error) {
	return f.item, nil
}
func (f *fakeRules) UpdateDraft(context.Context, couponport.UpsertCommand) (couponport.Coupon, error) {
	return f.item, nil
}
func (f *fakeRules) Publish(context.Context, couponport.ID, int64, string) (couponport.Coupon, error) {
	return f.item, nil
}
func (f *fakeRules) Stop(context.Context, couponport.ID, int64, string) (couponport.Coupon, error) {
	return f.item, nil
}
func (f *fakeRules) Archive(context.Context, couponport.ID, int64, string) (couponport.Coupon, error) {
	return f.item, nil
}
func (f *fakeRules) Delete(context.Context, couponport.ID, int64, string) (couponport.Coupon, error) {
	return f.item, nil
}
func (f *fakeRules) Copy(context.Context, couponport.ID, int64, string) (couponport.Coupon, error) {
	return f.item, nil
}

func couponFixture() couponport.Coupon {
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	days := int32(7)
	return couponport.Coupon{ID: 3, Name: "券", DiscountAmountTotal: 100, Currency: "CNY", Status: "draft", AvailabilityStatus: "draft", TotalIssueLimit: 5, PerUserIssueLimit: 1, ClaimStartsAt: start, ClaimEndsAt: start.Add(time.Hour), ValidityMode: couponport.ValidityRelativeDays, RelativeValidityDays: &days, TargetRefs: []string{"standard_product:6"}, CreatedBy: 7, UpdatedBy: 7, Version: 1, CreatedAt: start, UpdatedAt: start}
}
func TestCreateCouponUsesAuthenticatedActorCSRFAndHeaderKey(t *testing.T) {
	rules := &fakeRules{item: couponFixture()}
	h, err := NewHandler(rules, fakeOptions{}, fakeSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"name":"券","discount_amount_total":100,"total_issue_limit":5,"per_user_issue_limit":1,"claim_starts_at":"2026-01-02T03:04:05Z","claim_ends_at":"2026-01-02T04:04:05Z","validity_mode":"relative_days","relative_validity_days":7,"target_refs":["standard_product:6"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/coupons", strings.NewReader(body))
	req.Header.Set("Idempotency-Key", "1234567890abcdef")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("code=%d body=%s", res.Code, res.Body.String())
	}
	if rules.created.Actor != 7 || rules.created.IdempotencyKey != "1234567890abcdef" {
		t.Fatalf("command=%+v", rules.created)
	}
	if !strings.Contains(res.Body.String(), `"create_replay_safe":true`) {
		t.Fatalf("body=%s", res.Body.String())
	}
}
func TestCouponWriteCSRFAndUnknownFieldsFailClosed(t *testing.T) {
	rules := &fakeRules{item: couponFixture()}
	h, _ := NewHandler(rules, fakeOptions{}, fakeSecurity{csrf: errors.New("no")})
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodPost, "/api/admin/coupons", strings.NewReader(`{}`)))
	if r.Code != 403 {
		t.Fatalf("csrf=%d", r.Code)
	}
	h, _ = NewHandler(rules, fakeOptions{}, fakeSecurity{})
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodPost, "/api/admin/coupons", strings.NewReader(`{"unknown":1}`)))
	if r.Code != 400 {
		t.Fatalf("unknown=%d", r.Code)
	}
}
func TestCouponProductOptionsAndExcludedClaims(t *testing.T) {
	rules := &fakeRules{item: couponFixture()}
	h, _ := NewHandler(rules, fakeOptions{}, fakeSecurity{})
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/admin/coupons/product-options?product_type=standard_product", nil))
	if r.Code != 200 || !strings.Contains(r.Body.String(), "standard_product:6") {
		t.Fatalf("options %d %s", r.Code, r.Body.String())
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/admin/coupons/3/claims", nil))
	if r.Code != 404 {
		t.Fatalf("claims=%d", r.Code)
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/admin/coupons/product-options?product_type=service_period", nil))
	if r.Code != 200 || !strings.Contains(r.Body.String(), "service_period:8") {
		t.Fatalf("service-period options %d %s", r.Code, r.Body.String())
	}
}

func TestCouponClaimListUsesDedicatedMaskedReadPort(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	h, err := NewHandlerWithClaims(&fakeRules{item: couponFixture()}, fakeOptions{}, fakeClaims{page: couponport.AdminCouponClaimPage{Items: []couponport.AdminCouponClaim{{ClaimID: 9, CustomerID: 11, CouponID: 3, Status: "available", ClaimNoMasked: "***7", ClaimedAt: now}}, Total: 1}}, fakeSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/admin/coupons/3/claims?limit=10&offset=0", nil))
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"claim_no_masked":"***7"`) || strings.Contains(r.Body.String(), "unionid") {
		t.Fatalf("claims status=%d body=%s", r.Code, r.Body.String())
	}
}
