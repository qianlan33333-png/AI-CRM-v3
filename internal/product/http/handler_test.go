package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type testSecurity struct {
	principal accessdomain.Principal
	authErr   error
	csrfErr   error
	authCalls int
	csrfCalls int
}

func (security *testSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	security.authCalls++
	return security.principal, security.authErr
}

func (security *testSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	security.csrfCalls++
	return security.principal, security.csrfErr
}

type testCatalog struct {
	page       productport.Page
	product    productport.Product
	listCalls  int
	getCalls   int
	getID      productport.ID
	getCode    string
	createCall *productport.CreateCommand
	updateCall *productport.UpdateCommand
}

func (catalog *testCatalog) List(context.Context, string, int32) (productport.Page, error) {
	catalog.listCalls++
	return catalog.page, nil
}

func (catalog *testCatalog) Get(_ context.Context, id productport.ID) (productport.Product, error) {
	catalog.getCalls++
	catalog.getID = id
	return catalog.product, nil
}

func (catalog *testCatalog) GetByCode(_ context.Context, code string) (productport.Product, error) {
	catalog.getCode = code
	if code != catalog.product.ProductCode {
		return productport.Product{}, productapp.ErrNotFound
	}
	return catalog.product, nil
}

func (catalog *testCatalog) Create(_ context.Context, command productport.CreateCommand) (productport.Product, error) {
	catalog.createCall = &command
	return catalog.product, nil
}

func (catalog *testCatalog) Update(_ context.Context, command productport.UpdateCommand) (productport.Product, error) {
	catalog.updateCall = &command
	return catalog.product, nil
}

type testLifecycle struct {
	local      productport.LocalProduct
	share      productport.LocalProductShare
	shareErr   error
	deleteCall *productport.DeleteLocalProductCommand
}

func (lifecycle *testLifecycle) SetLocalProductEnabled(context.Context, productport.SetLocalProductEnabledCommand) (productport.LocalProduct, error) {
	return lifecycle.local, nil
}

func (lifecycle *testLifecycle) CopyLocalProduct(context.Context, productport.CopyLocalProductCommand) (productport.LocalProduct, error) {
	return lifecycle.local, nil
}

func (lifecycle *testLifecycle) DeleteLocalProduct(_ context.Context, command productport.DeleteLocalProductCommand) (productport.DeleteLocalProductResult, error) {
	lifecycle.deleteCall = &command
	return productport.DeleteLocalProductResult{ProductID: command.ID, Deleted: true}, nil
}

func (lifecycle *testLifecycle) ShareLocalProduct(context.Context, productport.ID) (productport.LocalProductShare, error) {
	return lifecycle.share, lifecycle.shareErr
}

type testServicePeriod struct {
	page    productport.ServicePeriodPage
	product productport.ServicePeriodProduct
}

func (service *testServicePeriod) ListServicePeriodProducts(context.Context, int32, int32) (productport.ServicePeriodPage, error) {
	return service.page, nil
}

func (service *testServicePeriod) GetServicePeriodProduct(context.Context, productport.ID) (productport.ServicePeriodProduct, error) {
	return service.product, nil
}

func (service *testServicePeriod) CreateServicePeriodProduct(context.Context, productport.CreateServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	return service.product, nil
}

func (service *testServicePeriod) UpdateServicePeriodProduct(context.Context, productport.UpdateServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	return service.product, nil
}

func (service *testServicePeriod) SetServicePeriodProductEnabled(context.Context, productport.SetServicePeriodProductEnabledCommand) (productport.ServicePeriodProduct, error) {
	return service.product, nil
}

func (service *testServicePeriod) CopyServicePeriodProduct(context.Context, productport.CopyServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	return service.product, nil
}

func (service *testServicePeriod) ArchiveServicePeriodProduct(context.Context, productport.ArchiveServicePeriodProductCommand) (productport.ServicePeriodProduct, error) {
	return service.product, nil
}

type testExternalPush struct {
	configuration productport.ExternalPushConfiguration
	test          productport.ExternalPushTest
}

type testMemberEntitlements struct {
	page      orderport.ServicePeriodMemberPage
	queries   []orderport.ServicePeriodMemberQuery
	remarkCmd *orderport.RemarkCommand
}

func (stub *testMemberEntitlements) ListCustomerEntitlements(context.Context, int64, int32) (orderport.EntitlementPage, error) {
	return orderport.EntitlementPage{}, nil
}
func (stub *testMemberEntitlements) ListServicePeriodMembers(_ context.Context, query orderport.ServicePeriodMemberQuery) (orderport.ServicePeriodMemberPage, error) {
	stub.queries = append(stub.queries, query)
	return stub.page, nil
}
func (stub *testMemberEntitlements) GetCustomerServicePeriodEntitlement(context.Context, int64, int64) (orderport.Entitlement, bool, error) {
	return orderport.Entitlement{}, false, nil
}
func (stub *testMemberEntitlements) UpdateEntitlementRemark(_ context.Context, command orderport.RemarkCommand) (orderport.Entitlement, error) {
	stub.remarkCmd = &command
	return orderport.Entitlement{ID: command.EntitlementID, ServiceProductID: command.ServiceProductID, Remark: command.Remark, Version: command.ExpectedVersion + 1, UpdatedAt: time.Now().UTC()}, nil
}

type testMemberNames struct{}

func (testMemberNames) DisplayNames(context.Context, []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	return map[customerdomain.CustomerID]string{}, nil
}

// This directory is an Access projection, as production composition supplies.
// The old page submits the stable WeCom user id, never an internal numeric id.
type testMemberStaffDirectory struct{ staff productport.MemberGridStaff }

func (directory testMemberStaffDirectory) ActiveMemberGridStaff(_ context.Context, id int64) (bool, error) {
	return directory.staff.Active && id == directory.staff.AdminUserID, nil
}
func (directory testMemberStaffDirectory) MemberGridStaffByWeComUserID(_ context.Context, value string) (productport.MemberGridStaff, bool, error) {
	return directory.staff, directory.staff.WeComUserID == value, nil
}
func (directory testMemberStaffDirectory) MemberGridStaffByID(_ context.Context, id int64) (productport.MemberGridStaff, bool, error) {
	return directory.staff, directory.staff.AdminUserID == id, nil
}
func (directory testMemberStaffDirectory) ListActiveMemberGridStaff(context.Context) ([]productport.MemberGridStaff, error) {
	if !directory.staff.Active {
		return []productport.MemberGridStaff{}, nil
	}
	return []productport.MemberGridStaff{directory.staff}, nil
}

// The HTTP fixture models the Product-owned workspace port.  It keeps the
// handler test on the real HttpApi surface and deliberately has no Order or
// Customer store access.
type testMemberWorkspace struct {
	access        productport.MemberGridAccess
	deniedID      int64
	views         []productport.MemberGridView
	collaborators []productport.MemberGridCollaborator
	share         productport.MemberGridShare
}

func (s *testMemberWorkspace) Access(_ context.Context, _ productport.ID, actor productport.MemberGridActor) (productport.MemberGridAccess, error) {
	if actor.AdminUserID == s.deniedID {
		return productport.MemberGridAccess{}, productapp.ErrNotFound
	}
	return s.access, nil
}
func (s *testMemberWorkspace) ListViews(context.Context, productport.ID) ([]productport.MemberGridView, error) {
	return s.views, nil
}
func (s *testMemberWorkspace) CreateView(_ context.Context, c productport.CreateMemberGridViewCommand) (productport.MemberGridView, error) {
	v := productport.MemberGridView{ID: 13, ProductID: c.ProductID, Name: c.Name, Config: c.Config, Version: 1, CreatedBy: c.Actor.AdminUserID, UpdatedBy: c.Actor.AdminUserID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	s.views = append(s.views, v)
	return v, nil
}
func (s *testMemberWorkspace) UpdateView(_ context.Context, c productport.UpdateMemberGridViewCommand) (productport.MemberGridView, error) {
	return productport.MemberGridView{ID: c.ViewID, ProductID: c.ProductID, Name: c.Name, Config: c.Config, Version: c.ExpectedVersion + 1, UpdatedBy: c.Actor.AdminUserID, UpdatedAt: time.Now()}, nil
}
func (s *testMemberWorkspace) DeleteView(_ context.Context, c productport.DeleteMemberGridViewCommand) (productport.MemberGridView, error) {
	return productport.MemberGridView{ID: c.ViewID, ProductID: c.ProductID, Version: c.ExpectedVersion}, nil
}
func (s *testMemberWorkspace) ListCollaborators(context.Context, productport.ID) ([]productport.MemberGridCollaborator, error) {
	return s.collaborators, nil
}
func (s *testMemberWorkspace) CreateCollaborator(_ context.Context, c productport.CreateMemberGridCollaboratorCommand) (productport.MemberGridCollaborator, error) {
	v := productport.MemberGridCollaborator{ID: 14, ProductID: c.ProductID, AdminUserID: c.AdminUserID, Permission: c.Permission, Version: 1, CreatedBy: c.Actor.AdminUserID, UpdatedBy: c.Actor.AdminUserID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	s.collaborators = append(s.collaborators, v)
	return v, nil
}
func (s *testMemberWorkspace) UpdateCollaborator(_ context.Context, c productport.UpdateMemberGridCollaboratorCommand) (productport.MemberGridCollaborator, error) {
	return productport.MemberGridCollaborator{ID: c.CollaboratorID, ProductID: c.ProductID, Permission: c.Permission, Version: c.ExpectedVersion + 1, UpdatedBy: c.Actor.AdminUserID, UpdatedAt: time.Now()}, nil
}
func (s *testMemberWorkspace) DeleteCollaborator(_ context.Context, c productport.DeleteMemberGridCollaboratorCommand) (productport.MemberGridCollaborator, error) {
	return productport.MemberGridCollaborator{ID: c.CollaboratorID, ProductID: c.ProductID, Version: c.ExpectedVersion}, nil
}
func (s *testMemberWorkspace) Share(context.Context, productport.ID) (productport.MemberGridShare, error) {
	return s.share, nil
}
func (s *testMemberWorkspace) SetShare(_ context.Context, c productport.SetMemberGridShareCommand) (productport.MemberGridShare, bool, error) {
	s.share = productport.MemberGridShare{ProductID: c.ProductID, Enabled: c.Enabled, Version: c.ExpectedVersion + 1}
	if c.Enabled {
		s.share.PublicID = "mgshare1.abcdefghijklmnopqrstuv.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
	return s.share, c.Enabled, nil
}
func (s *testMemberWorkspace) ResolveShare(_ context.Context, token string) (productport.MemberGridShare, error) {
	if !s.share.Enabled || token != s.share.PublicID {
		return productport.MemberGridShare{}, productapp.ErrNotFound
	}
	return s.share, nil
}

func (external *testExternalPush) GetExternalPushConfiguration(context.Context, productport.ID, productport.ExternalPushProductKind) (productport.ExternalPushConfiguration, error) {
	return external.configuration, nil
}

func (external *testExternalPush) SaveExternalPushConfiguration(context.Context, productport.SaveExternalPushConfigurationCommand) (productport.ExternalPushConfiguration, error) {
	return external.configuration, nil
}

func (external *testExternalPush) QueueExternalPushTest(context.Context, productport.QueueExternalPushTestCommand) (productport.ExternalPushTest, error) {
	return external.test, nil
}

func newHandlerForTest(t *testing.T) (*Handler, *testSecurity, *testCatalog, *testLifecycle) {
	t.Helper()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	projection := json.RawMessage(`{"schema_version":1,"status":"draft","enabled":false,"buy_button_text":"","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`)
	product := productport.Product{ID: 7, ProductCode: "p-7", Name: "商品七", Description: "描述", PriceMinor: 1200, Currency: "CNY", StockQuantity: 4, Images: []string{}, CreatedBy: 9, CreatedAt: now, UpdatedAt: now, Version: 2, LegacyAdminProjection: projection}
	security := &testSecurity{principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 9, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	catalog := &testCatalog{product: product, page: productport.Page{Items: []productport.Product{product}}}
	lifecycle := &testLifecycle{local: productport.LocalProduct{ID: 7, ProductCode: "p-7", Name: "商品七", Description: "描述", PriceMinor: 1200, Currency: "CNY", StockQuantity: 4, Images: []string{}, CreatedBy: 9, CreatedAt: now, UpdatedAt: now, Lifecycle: productport.LocalProductDraft, Enabled: false, Version: 2}, share: productport.LocalProductShare{ProductID: 7, ProductCode: "p-7", Lifecycle: productport.LocalProductDraft, Available: false, Reason: productappUnavailableReason}}
	service := &testServicePeriod{page: productport.ServicePeriodPage{OK: true, Items: []productport.ServicePeriodProduct{}, Limit: 50, Offset: 0}, product: productport.ServicePeriodProduct{ServiceProductID: 7, ProductCode: "sp-7", Name: "周期七", Description: "描述", PriceMinor: 1200, Currency: "CNY", StockQuantity: 4, Images: []string{}, AdminProjection: json.RawMessage(`{"schema_version":1,"status":"service_period_enabled","enabled":true,"buy_button_text":"","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`), Lifecycle: productport.ServicePeriodEnabled, Enabled: true, Version: 2, CreatedAt: now, UpdatedAt: now}}
	external := &testExternalPush{configuration: productport.ExternalPushConfiguration{ProductID: 7, ProductKind: productport.ExternalPushWeChatPay, Enabled: false, UpdatedAt: now}, test: productport.ExternalPushTest{ProductID: 7, ProductKind: productport.ExternalPushWeChatPay, EffectID: "eer_1", State: "accepted", CreatedAt: now}}
	handler, err := NewHandler(catalog, lifecycle, service, external, security)
	if err != nil {
		t.Fatal(err)
	}
	members := &testMemberEntitlements{page: orderport.ServicePeriodMemberPage{Items: []orderport.Entitlement{{ID: 41, CustomerID: 77, ServiceProductID: 7, ProductName: "周期七", Status: "active", StartAt: now.Add(-24 * time.Hour), EndAt: now.Add(7 * 24 * time.Hour), Version: 5, UpdatedAt: now}}}}
	if err = handler.SetServicePeriodMemberReaders(members, testMemberNames{}); err != nil {
		t.Fatal(err)
	}
	if err = handler.SetServicePeriodMemberWorkspace(&testMemberWorkspace{access: productport.MemberGridAccess{CanView: true, CanEdit: true, CanManageViews: true, CanShare: true}, deniedID: 22}); err != nil {
		t.Fatal(err)
	}
	if err = handler.SetServicePeriodMemberStaffDirectory(testMemberStaffDirectory{staff: productport.MemberGridStaff{AdminUserID: 5, WeComUserID: "zhangsan", DisplayName: "张三", Active: true}}); err != nil {
		t.Fatal(err)
	}
	return handler, security, catalog, lifecycle
}

// Keep the test fixture independent from the application package's internal
// constant while asserting the public blocked sharing contract.
const productappUnavailableReason = "no_authoritative_public_purchase_route"

func TestHandlerUsesOneToOneLimitAndProductListDTO(t *testing.T) {
	handler, security, catalog, _ := newHandlerForTest(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/products?limit=100", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || catalog.listCalls != 1 || security.authCalls != 1 {
		t.Fatalf("status=%d listCalls=%d authCalls=%d body=%s", recorder.Code, catalog.listCalls, security.authCalls, recorder.Body.String())
	}
	var response struct {
		Items      []productport.Product `json:"items"`
		NextCursor string                `json:"next_cursor"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || len(response.Items) != 1 || response.NextCursor != "" {
		t.Fatalf("response=%s err=%v", recorder.Body.String(), err)
	}
	tooLarge := httptest.NewRecorder()
	handler.ServeHTTP(tooLarge, httptest.NewRequest(http.MethodGet, "/api/v1/products?limit=101", nil))
	if tooLarge.Code != http.StatusBadRequest || catalog.listCalls != 1 {
		t.Fatalf("limit gate status=%d calls=%d body=%s", tooLarge.Code, catalog.listCalls, tooLarge.Body.String())
	}
}

func TestHandlerDeleteCompatibilityPathReachesLifecycle(t *testing.T) {
	handler, security, _, lifecycle := newHandlerForTest(t)
	request := httptest.NewRequest(http.MethodDelete, "/api/admin/wechat-pay/products/7", strings.NewReader(`{"expected_version":2}`))
	request.Header.Set("Idempotency-Key", "product-delete-00000001")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || lifecycle.deleteCall == nil || lifecycle.deleteCall.ID != 7 || lifecycle.deleteCall.ExpectedVersion != 2 || security.csrfCalls != 1 {
		t.Fatalf("status=%d delete=%+v csrfCalls=%d body=%s", recorder.Code, lifecycle.deleteCall, security.csrfCalls, recorder.Body.String())
	}
}

func TestHandlerShareReturnsPublicRouteOrProductNotEnabled(t *testing.T) {
	handler, _, _, lifecycle := newHandlerForTest(t)
	lifecycle.share = productport.LocalProductShare{ProductID: 7, ProductCode: "p-7", Lifecycle: productport.LocalProductEnabled, Available: true, PurchaseURL: "/p/7"}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/wechat-pay/products/7/share", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"available":true`) || !strings.Contains(recorder.Body.String(), `"purchase_url":"/p/7"`) {
		t.Fatalf("share status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	lifecycle.shareErr = productapp.ErrLocalProductNotEnabled
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/wechat-pay/products/7/share", nil))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":"product_not_enabled"`) {
		t.Fatalf("disabled share status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandlerWriteRequiresAdminCSRFAndMintsCompatibilityKey(t *testing.T) {
	handler, security, catalog, _ := newHandlerForTest(t)
	security.csrfErr = errors.New("missing csrf")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/products", strings.NewReader(`{"product_code":"p-8","name":"商品八","description":"","price_minor":0,"currency":"CNY","stock_quantity":0,"images":[]}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || catalog.createCall != nil || security.csrfCalls != 1 {
		t.Fatalf("status=%d create=%+v csrfCalls=%d", recorder.Code, catalog.createCall, security.csrfCalls)
	}
	security.csrfErr = nil
	request = httptest.NewRequest(http.MethodPost, "/api/v1/products", strings.NewReader(`{"product_code":"p-8","name":"商品八","description":"","price_minor":0,"currency":"CNY","stock_quantity":0,"images":[]}`))
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || catalog.createCall == nil || len(catalog.createCall.IdempotencyKey) < 16 {
		t.Fatalf("status=%d create=%+v body=%s", recorder.Code, catalog.createCall, recorder.Body.String())
	}
}

func TestHandlerReturnsTruthfulCompatibilityReads(t *testing.T) {
	handler, _, _, _ := newHandlerForTest(t)
	paths := []string{
		"/api/v1/products/7/local-entitlements",
		"/api/admin/service-period-products/7/members?state=all&source=paid_order&limit=100&cursor=opaque",
		"/api/admin/service-period-products/7/member-grid/access",
		"/api/admin/service-period-products/7/member-grid/schema",
		"/api/admin/service-period-products/7/member-views",
		"/api/admin/service-period-products/7/member-grid/share-settings",
	}
	for _, path := range paths {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("path=%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/service-period-products/7/member-grid/query", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("grid query method gate status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestFrozenMemberGridHTTPAPISavedViewCollaboratorShareAndRemarkJourney(t *testing.T) {
	handler, security, _, _ := newHandlerForTest(t)
	requestNumber := 0
	adminRequest := func(method, path, body string) *httptest.ResponseRecorder {
		requestNumber++
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if method != http.MethodGet {
			r.Header.Set("Idempotency-Key", fmt.Sprintf("grid-http-api-key-%04d", requestNumber))
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	viewConfig := `{"schema_version":1,"filter":{"logic":"and","conditions":[{"field":"remaining_days","operator":"gte","value":7}]},"sorts":[{"field":"remaining_days","direction":"asc"}],"groups":[{"field":"remaining_days","direction":"asc"}]}`

	access := adminRequest(http.MethodGet, "/api/admin/service-period-products/7/member-grid/access", "")
	if access.Code != http.StatusOK || !strings.Contains(access.Body.String(), `"can_manage_views":true`) || !strings.Contains(access.Body.String(), `"can_manage_share":true`) {
		t.Fatalf("old access contract=%d %s", access.Code, access.Body.String())
	}
	schema := adminRequest(http.MethodGet, "/api/admin/service-period-products/7/member-grid/schema", "")
	if schema.Code != http.StatusOK || !strings.Contains(schema.Body.String(), `"schema_version":1`) || !strings.Contains(schema.Body.String(), `"remaining_days"`) {
		t.Fatalf("old schema contract=%d %s", schema.Code, schema.Body.String())
	}

	createView := adminRequest(http.MethodPost, "/api/admin/service-period-products/7/member-views", `{"name":"本周","config":`+viewConfig+`}`)
	if createView.Code != http.StatusCreated || !strings.Contains(createView.Body.String(), `"id":"13"`) || !strings.Contains(createView.Body.String(), `"version":1`) || !strings.Contains(createView.Body.String(), `"config"`) {
		t.Fatalf("view create=%d %s", createView.Code, createView.Body.String())
	}
	updateView := adminRequest(http.MethodPut, "/api/admin/service-period-products/7/member-views/13", `{"name":"本周更新","version":1,"config":`+viewConfig+`}`)
	if updateView.Code != http.StatusOK || !strings.Contains(updateView.Body.String(), `"version":2`) || !strings.Contains(updateView.Body.String(), "本周更新") {
		t.Fatalf("view update=%d %s", updateView.Code, updateView.Body.String())
	}
	query := adminRequest(http.MethodPost, "/api/admin/service-period-products/7/member-grid/query", `{"config":`+viewConfig+`,"limit":50}`)
	if query.Code != http.StatusOK || !strings.Contains(query.Body.String(), `"record_id":"spm_`) || !strings.Contains(query.Body.String(), `"group_path"`) || !strings.Contains(query.Body.String(), `"renewal_count_unavailable":true`) {
		t.Fatalf("saved view query=%d %s", query.Code, query.Body.String())
	}
	members := handler.members.(*testMemberEntitlements)
	if len(members.queries) == 0 || members.queries[len(members.queries)-1].Sort != "remaining_days_asc" || members.queries[len(members.queries)-1].RemainingDays == nil {
		t.Fatalf("saved view config was not applied: %+v", members.queries)
	}
	deleteView := adminRequest(http.MethodDelete, "/api/admin/service-period-products/7/member-views/13", `{"version":2}`)
	if deleteView.Code != http.StatusOK || !strings.Contains(deleteView.Body.String(), `"deleted":true`) || !strings.Contains(deleteView.Body.String(), `"id":"13"`) {
		t.Fatalf("view delete=%d %s", deleteView.Code, deleteView.Body.String())
	}

	numericCollaborator := adminRequest(http.MethodPost, "/api/admin/service-period-products/7/member-grid/collaborators", `{"staff_id":5,"permission":"edit"}`)
	if numericCollaborator.Code != http.StatusBadRequest {
		t.Fatalf("numeric collaborator id bypass=%d %s", numericCollaborator.Code, numericCollaborator.Body.String())
	}
	staff := adminRequest(http.MethodGet, "/api/admin/service-period-products/7/member-grid/staff", "")
	if staff.Code != http.StatusOK || !strings.Contains(staff.Body.String(), `"user_id":"zhangsan"`) {
		t.Fatalf("staff picker=%d %s", staff.Code, staff.Body.String())
	}
	createCollaborator := adminRequest(http.MethodPost, "/api/admin/service-period-products/7/member-grid/collaborators", `{"wecom_userid":"zhangsan","permission":"edit"}`)
	if createCollaborator.Code != http.StatusCreated || !strings.Contains(createCollaborator.Body.String(), `"id":"14"`) || !strings.Contains(createCollaborator.Body.String(), `"wecom_userid":"zhangsan"`) || !strings.Contains(createCollaborator.Body.String(), `"version":1`) {
		t.Fatalf("collaborator create=%d %s", createCollaborator.Code, createCollaborator.Body.String())
	}
	updateCollaborator := adminRequest(http.MethodPut, "/api/admin/service-period-products/7/member-grid/collaborators/14", `{"permission":"read","version":1}`)
	if updateCollaborator.Code != http.StatusOK || !strings.Contains(updateCollaborator.Body.String(), `"version":2`) {
		t.Fatalf("collaborator update=%d %s", updateCollaborator.Code, updateCollaborator.Body.String())
	}
	deleteCollaborator := adminRequest(http.MethodDelete, "/api/admin/service-period-products/7/member-grid/collaborators/14", `{"version":2}`)
	if deleteCollaborator.Code != http.StatusOK || !strings.Contains(deleteCollaborator.Body.String(), `"deleted":true`) || !strings.Contains(deleteCollaborator.Body.String(), `"version":2`) {
		t.Fatalf("collaborator delete=%d %s", deleteCollaborator.Code, deleteCollaborator.Body.String())
	}

	enableShare := adminRequest(http.MethodPut, "/api/admin/service-period-products/7/member-grid/external-share", `{"enabled":true,"version":0}`)
	if enableShare.Code != http.StatusOK || strings.Contains(enableShare.Body.String(), `"token"`) || !strings.Contains(enableShare.Body.String(), "/shared/service-period-member-grid#mgshare1.") {
		t.Fatalf("share enable=%d %s", enableShare.Code, enableShare.Body.String())
	}
	workspace := handler.workspace.(*testMemberWorkspace)
	bootstrap := httptest.NewRequest(http.MethodGet, "/api/public/service-period-member-grid/bootstrap", nil)
	bootstrap.Header.Set("X-AICRM-Grid-Share-Token", workspace.share.PublicID)
	bootResponse := httptest.NewRecorder()
	handler.ServeHTTP(bootResponse, bootstrap)
	if bootResponse.Code != http.StatusOK || strings.Contains(bootResponse.Body.String(), workspace.share.PublicID) {
		t.Fatalf("public bootstrap=%d %s", bootResponse.Code, bootResponse.Body.String())
	}
	publicQuery := httptest.NewRequest(http.MethodPost, "/api/public/service-period-member-grid/query", strings.NewReader(`{"view_id":"default","limit":50}`))
	publicQuery.Header.Set("X-AICRM-Grid-Share-Token", workspace.share.PublicID)
	publicResponse := httptest.NewRecorder()
	handler.ServeHTTP(publicResponse, publicQuery)
	if publicResponse.Code != http.StatusOK || strings.Contains(publicResponse.Body.String(), workspace.share.PublicID) || !strings.Contains(publicResponse.Body.String(), `"rows"`) {
		t.Fatalf("public query=%d %s", publicResponse.Code, publicResponse.Body.String())
	}
	revokeShare := adminRequest(http.MethodPut, "/api/admin/service-period-products/7/member-grid/external-share", `{"enabled":false,"version":1}`)
	if revokeShare.Code != http.StatusOK || strings.Contains(revokeShare.Body.String(), "mgshare1.") || !strings.Contains(revokeShare.Body.String(), `"enabled":false`) {
		t.Fatalf("share revoke=%d %s", revokeShare.Code, revokeShare.Body.String())
	}
	revoked := httptest.NewRecorder()
	handler.ServeHTTP(revoked, bootstrap)
	if revoked.Code != http.StatusGone || strings.Contains(revoked.Body.String(), `"rows"`) {
		t.Fatalf("revoked public token=%d %s", revoked.Code, revoked.Body.String())
	}

	remark := adminRequest(http.MethodPut, "/api/admin/service-period-products/7/members/"+memberGridMemberRef(41)+"/remark", `{"remark":"已联系","version":5}`)
	if remark.Code != http.StatusOK || !strings.Contains(remark.Body.String(), `"version":6`) {
		t.Fatalf("remark=%d %s", remark.Code, remark.Body.String())
	}
	if members.remarkCmd == nil || members.remarkCmd.CustomerID != 0 || members.remarkCmd.ServiceProductID != 7 || members.remarkCmd.EntitlementID != 41 {
		t.Fatalf("opaque remark did not carry only product scope: %+v", members.remarkCmd)
	}

	security.principal = accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 22, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/api/admin/service-period-products/7/members", nil))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("revoked/no workspace read=%d %s", denied.Code, denied.Body.String())
	}
}

func TestMemberGridPublicHttpAPIOnlyReadsEnabledShareAndSavedViews(t *testing.T) {
	handler, _, _, _ := newHandlerForTest(t)
	workspace := handler.workspace.(*testMemberWorkspace)
	workspace.share = productport.MemberGridShare{ProductID: 7, Enabled: true, PublicID: "mgshare1.abcdefghijklmnopqrstuv.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", Version: 1}
	bootstrap := httptest.NewRequest(http.MethodGet, "/api/public/service-period-member-grid/bootstrap", nil)
	bootstrap.Header.Set("X-AICRM-Grid-Share-Token", workspace.share.PublicID)
	bootResponse := httptest.NewRecorder()
	handler.ServeHTTP(bootResponse, bootstrap)
	if bootResponse.Code != http.StatusOK || !strings.Contains(bootResponse.Body.String(), `"views"`) || strings.Contains(bootResponse.Body.String(), workspace.share.PublicID) {
		t.Fatalf("bootstrap=%d %s", bootResponse.Code, bootResponse.Body.String())
	}
	query := httptest.NewRequest(http.MethodPost, "/api/public/service-period-member-grid/query", strings.NewReader(`{"view_id":"default","limit":50}`))
	query.Header.Set("X-AICRM-Grid-Share-Token", workspace.share.PublicID)
	queryResponse := httptest.NewRecorder()
	handler.ServeHTTP(queryResponse, query)
	if queryResponse.Code != http.StatusOK || !strings.Contains(queryResponse.Body.String(), `"rows"`) {
		t.Fatalf("query=%d %s", queryResponse.Code, queryResponse.Body.String())
	}
	workspace.share.Enabled = false
	revoked := httptest.NewRecorder()
	handler.ServeHTTP(revoked, bootstrap)
	if revoked.Code != http.StatusGone || strings.Contains(revoked.Body.String(), `"rows"`) {
		t.Fatalf("revoked=%d %s", revoked.Code, revoked.Body.String())
	}
}

func TestHandlerRejectsMalformedIDWithoutApplicationCall(t *testing.T) {
	handler, _, catalog, _ := newHandlerForTest(t)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/products/07", nil))
	if recorder.Code != http.StatusNotFound || catalog.getCalls != 0 {
		t.Fatalf("status=%d getCalls=%d", recorder.Code, catalog.getCalls)
	}
}

func TestCompatibilityIdempotencyKeyFailsClosedWhenRandomReadFails(t *testing.T) {
	wantErr := errors.New("entropy unavailable")
	key, err := compatibilityIdempotencyKey(func([]byte) (int, error) {
		return 0, wantErr
	})
	if !errors.Is(err, wantErr) || key != "" {
		t.Fatalf("key=%q err=%v, want empty key and entropy error", key, err)
	}
}
