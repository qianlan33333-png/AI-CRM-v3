package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

type distributionHTTPRegistrationStub struct{}

func (distributionHTTPRegistrationStub) CurrentAgreement(context.Context) (distributionport.Agreement, error) {
	return distributionport.Agreement{}, nil
}
func (distributionHTTPRegistrationStub) Profile(context.Context, distributionport.TrustedSessionActor) (distributionport.DistributorProfile, error) {
	return distributionport.DistributorProfile{}, nil
}
func (distributionHTTPRegistrationStub) Register(context.Context, distributionport.RegisterCommand) (distributionport.DistributorProfile, error) {
	return distributionport.DistributorProfile{}, nil
}
func (distributionHTTPRegistrationStub) PrepareReceiver(context.Context, distributionport.TrustedSessionActor) (distributionport.ReceiverPreparationResult, error) {
	return distributionport.ReceiverPreparationResult{}, nil
}

type distributionHTTPPromotionStub struct{}

func (distributionHTTPPromotionStub) ListPromotionProducts(context.Context, distributionport.TrustedSessionActor, string, int32) (distributionport.PromotionPage, error) {
	return distributionport.PromotionPage{}, nil
}
func (distributionHTTPPromotionStub) IssuePromotionLink(context.Context, distributionport.IssuePromotionCommand) (distributionport.PromotionLink, error) {
	return distributionport.PromotionLink{}, nil
}
func (distributionHTTPPromotionStub) ResolvePromotionTarget(context.Context, string) (string, error) {
	return "", distributionport.ErrNotFound
}

type distributionHTTPSessionStub struct{}

func (distributionHTTPSessionStub) Resolve(context.Context, string) (distributionport.TrustedSessionActor, error) {
	return distributionport.TrustedSessionActor{CustomerID: 11, IdentityID: 12, AppID: "wx-test", AppScope: "wechat-app:test", Channel: "mini_program", OccurredAt: time.Now().UTC()}, nil
}

type distributionHTTPBridgeStub struct{}

func (distributionHTTPBridgeStub) BridgePaymentSession(context.Context, string) (string, time.Time, error) {
	return "", time.Time{}, distributionport.ErrUnavailable
}

type distributionHTTPEarningsStub struct{}

func (distributionHTTPEarningsStub) Earnings(context.Context, distributionport.TrustedSessionActor) (distributionport.Earnings, error) {
	return distributionport.Earnings{}, nil
}
func (distributionHTTPEarningsStub) ListCommissions(_ context.Context, actor distributionport.TrustedSessionActor, status distributiondomain.CommissionStatus, cursor string, limit int32) (distributionport.CommissionPage, error) {
	if actor.CustomerID != 11 || status != distributiondomain.CommissionPending || cursor != "3" || limit != 20 {
		return distributionport.CommissionPage{}, distributionport.ErrConflict
	}
	now := time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC)
	return distributionport.CommissionPage{Items: []distributionport.CommissionListItem{{CommissionID: "4", OrderReference: "order-9001", ProductName: "冻结商品", InitialMinor: 990, CurrentPayableMinor: 880, PaidMinor: 0, Status: "pending", PaidConfirmedAt: now, DueAt: now.Add(24 * time.Hour), CreatedAt: now, Currency: "CNY"}}, NextCursor: "4"}, nil
}

func TestCommissionsResponseMapsFrozenReadModelToPublicContract(t *testing.T) {
	h, err := NewHandler(Config{Registration: distributionHTTPRegistrationStub{}, Promotion: distributionHTTPPromotionStub{}, Earnings: distributionHTTPEarningsStub{}, Sessions: distributionHTTPSessionStub{}, Bridge: distributionHTTPBridgeStub{}, AllowedOrigins: []string{"https://crm.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/distribution/commissions?status=pending&cursor=3&limit=20", nil)
	r.AddCookie(&http.Cookie{Name: DistributionSessionCookieName, Value: "trusted"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body := w.Body.String()
	for _, wanted := range []string{`"items":[{`, `"commission_id":"4"`, `"order_reference":"order-9001"`, `"product_name":"冻结商品"`, `"next_cursor":"4"`} {
		if !strings.Contains(body, wanted) {
			t.Fatalf("response missing %s: %s", wanted, body)
		}
	}
	if w.Code != http.StatusOK || strings.Contains(body, `"Items"`) || strings.Contains(body, `"NextCursor"`) {
		t.Fatalf("response=%d body=%s", w.Code, body)
	}
}
