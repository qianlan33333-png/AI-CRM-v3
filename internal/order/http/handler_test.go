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
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

type securityStub struct {
	principal        accessdomain.Principal
	authErr, csrfErr error
}

func (s securityStub) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, s.authErr
}
func (s securityStub) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, s.csrfErr
}

type appStub struct {
	page    orderport.Page
	detail  domain.Snapshot
	getErr  error
	query   orderport.ListQuery
	exports int
}

func (a *appStub) Get(context.Context, int64) (domain.Snapshot, error) { return a.detail, a.getErr }
func (a *appStub) GetByReference(context.Context, string) (domain.Snapshot, error) {
	return a.detail, a.getErr
}
func (a *appStub) List(_ context.Context, q orderport.ListQuery) (orderport.Page, error) {
	a.query = q
	return a.page, nil
}
func (a *appStub) PreviewExport(context.Context, orderport.ListQuery) (orderport.ExportPreview, error) {
	return orderport.ExportPreview{Rows: 1}, nil
}
func (a *appStub) ExportCSV(context.Context, orderport.ListQuery, int64, string) (orderport.ExportResult, error) {
	a.exports++
	return orderport.ExportResult{ReceiptID: 7, Rows: 1, Content: []byte("a,b\r\n"), ContentDigest: [32]byte{1}}, nil
}

func adminSecurity() securityStub {
	return securityStub{principal: accessdomain.Principal{InternalID: 9, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
}
func sampleOrder() domain.Snapshot {
	payer, beneficiary := int64(11), int64(22)
	return domain.Snapshot{ID: 1, Provider: domain.ProviderWeChatPay, SourceSystem: "v3", SourceKey: "source-1", MerchantOrderNo: "M-1", PayerCustomerID: &payer, BeneficiaryCustomerID: &beneficiary, Amount: domain.Money{AmountMinor: 1099, Currency: "CNY"}, Status: domain.StatusPaid, Items: []domain.ItemSnapshot{{LineNo: 1, ProductCode: "P-1", ProductName: "课程", UnitAmountMinor: 1099, Quantity: 1, LineAmountMinor: 1099}}, RecordOrigin: domain.RecordOriginNative, EffectEligible: true, Version: 2, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
}

func TestListUsesSafeServerFiltersAndNoStore(t *testing.T) {
	application := &appStub{page: orderport.Page{Items: []domain.Snapshot{sampleOrder()}}}
	handler, _ := NewHandler(application, adminSecurity())
	request := httptest.NewRequest(http.MethodGet, "/api/admin/orders?customer_id=11&status=paid&product=%E8%AF%BE%E7%A8%8B&limit=20", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || application.query.CustomerID != 11 || application.query.Status != domain.StatusPaid || application.query.Product != "课程" {
		t.Fatalf("code=%d headers=%v query=%+v body=%s", response.Code, response.Header(), application.query, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "external_userid") || strings.Contains(response.Body.String(), "mobile") || !strings.Contains(response.Body.String(), "customer:11") {
		t.Fatalf("unsafe or missing response: %s", response.Body.String())
	}
}

func TestAmbiguousReferenceReturnsConflict(t *testing.T) {
	app := &appStub{getErr: orderport.ErrConflict}
	handler, _ := NewHandler(app, adminSecurity())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/orders/shared-ref", nil))
	if response.Code != http.StatusConflict {
		t.Fatalf("code=%d body=%s", response.Code, response.Body.String())
	}
}

func TestExportRequiresAdminCSRFAndRejectsRawIdentityFilter(t *testing.T) {
	body := `{"resource":"orders","format":"csv","filters":{"provider":"wechat","identity":"raw-openid"}}`
	for _, test := range []struct {
		name     string
		security securityStub
		want     int
	}{{"viewer", securityStub{principal: accessdomain.Principal{InternalID: 2, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}, http.StatusForbidden}, {"csrf", securityStub{principal: adminSecurity().principal, csrfErr: errors.New("csrf")}, http.StatusForbidden}, {"raw identity", adminSecurity(), http.StatusBadRequest}} {
		t.Run(test.name, func(t *testing.T) {
			app := &appStub{}
			handler, _ := NewHandler(app, test.security)
			request := httptest.NewRequest(http.MethodPost, "/api/admin/wechat-pay/order-exports", strings.NewReader(body))
			request.Header.Set("Idempotency-Key", "order-export-key-0001")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want || app.exports != 0 {
				t.Fatalf("code=%d exports=%d body=%s", response.Code, app.exports, response.Body.String())
			}
		})
	}
}

func TestExportReturnsReceiptBackedCSV(t *testing.T) {
	app := &appStub{}
	handler, _ := NewHandler(app, adminSecurity())
	request := httptest.NewRequest(http.MethodPost, "/api/admin/wechat-pay/order-exports", strings.NewReader(`{"resource":"orders","format":"csv","filters":{"provider":"wechat"}}`))
	request.Header.Set("Idempotency-Key", "order-export-key-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("X-AICRM-Export-Receipt") != "7" || response.Header().Get("Content-Type") != "text/csv; charset=utf-8" || app.exports != 1 {
		t.Fatalf("code=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}
