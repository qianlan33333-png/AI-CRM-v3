package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type openPlatformIdentityStub struct {
	result        identityport.ResolveResult
	results       map[string]identityport.ResolveResult
	seen          identitydomain.Reference
	calls         []identitydomain.Reference
	externalValue string
	externalFound bool
	externalErr   error
	externalCalls int
}

func (stub *openPlatformIdentityStub) Resolve(_ context.Context, reference identitydomain.Reference) (identityport.ResolveResult, error) {
	stub.seen = reference
	stub.calls = append(stub.calls, reference)
	if result, exists := stub.results[string(reference.Kind)+"|"+reference.Scope+"|"+reference.Value]; exists {
		return result, nil
	}
	return stub.result, nil
}
func (stub *openPlatformIdentityStub) VerifiedExternalUserID(context.Context, customerdomain.CustomerID, string) (string, bool, error) {
	stub.externalCalls++
	return stub.externalValue, stub.externalFound, stub.externalErr
}

type openPlatformOrderStub struct {
	page            orderport.Page
	last            orderport.ListQuery
	calls           int
	scopedCalls     int
	scopedReference string
	scopedCustomer  int64
	scopedResult    orderdomain.Snapshot
	scopedErr       error
}

func (stub *openPlatformOrderStub) Get(context.Context, int64) (orderdomain.Snapshot, error) {
	stub.calls++
	return orderdomain.Snapshot{}, orderport.ErrNotFound
}
func (stub *openPlatformOrderStub) GetByReference(context.Context, string) (orderdomain.Snapshot, error) {
	stub.calls++
	return orderdomain.Snapshot{}, orderport.ErrNotFound
}
func (stub *openPlatformOrderStub) List(_ context.Context, query orderport.ListQuery) (orderport.Page, error) {
	stub.calls++
	stub.last = query
	return stub.page, nil
}
func (stub *openPlatformOrderStub) GetByReferenceForCustomer(_ context.Context, reference string, customerID int64) (orderdomain.Snapshot, error) {
	stub.scopedCalls++
	stub.scopedReference, stub.scopedCustomer = reference, customerID
	return stub.scopedResult, stub.scopedErr
}

type openPlatformProfileStub struct{ calls int }

func (stub *openPlatformProfileStub) ReadSidebarProfile(_ context.Context, id customerdomain.CustomerID) (customerport.SidebarProfile, error) {
	stub.calls++
	return customerport.SidebarProfile{CustomerID: id, DisplayName: "Customer", Status: "active", UpdatedAt: time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)}, nil
}
func (*openPlatformProfileStub) UpdateSidebarProfile(context.Context, customerport.SidebarProfileUpdate) (customerport.SidebarProfile, error) {
	return customerport.SidebarProfile{}, nil
}
func (*openPlatformProfileStub) BindSidebarPhone(context.Context, customerport.SidebarPhoneBind) (customerport.SidebarPhoneResult, error) {
	return customerport.SidebarPhoneResult{}, nil
}

type openPlatformOwnerStub struct {
	items []wecomport.AudiencePrimaryOwner
	calls int
}

func (stub *openPlatformOwnerStub) AudiencePrimaryOwners(_ context.Context, _ []customerdomain.CustomerID) ([]wecomport.AudiencePrimaryOwner, error) {
	stub.calls++
	return stub.items, nil
}

type openPlatformTimelineStub struct {
	page  customerport.TimelinePage
	err   error
	calls int
}

func (stub *openPlatformTimelineStub) CapabilityStatus() customerport.SectionStatus {
	return customerport.SectionStatus{State: customerport.SectionReady}
}
func (stub *openPlatformTimelineStub) CustomerTimeline(context.Context, customerdomain.CustomerID, customerport.PageQuery) (customerport.TimelinePage, error) {
	stub.calls++
	return stub.page, stub.err
}

type openPlatformArchiveStub struct {
	page          archiveport.CustomerPage
	externalPage  archiveport.ExternalChatRecordPage
	err           error
	calls         int
	externalCalls int
	externalQuery archiveport.ExternalChatRecordQuery
}

func (stub *openPlatformArchiveStub) CustomerMessages(context.Context, archiveport.CustomerQuery) (archiveport.CustomerPage, error) {
	stub.calls++
	return stub.page, stub.err
}
func (stub *openPlatformArchiveStub) ExternalCustomerMessages(_ context.Context, query archiveport.ExternalChatRecordQuery) (archiveport.ExternalChatRecordPage, error) {
	stub.externalCalls++
	stub.externalQuery = query
	return stub.externalPage, stub.err
}
func (*openPlatformArchiveStub) CustomerStaff(context.Context, customerdomain.CustomerID) ([]archiveport.StaffOption, error) {
	return nil, nil
}

type openPlatformRadarLinksStub struct {
	page  radarport.ExternalLinkMappingPage
	err   error
	calls int
	query radarport.ExternalLinkMappingQuery
}

func (stub *openPlatformRadarLinksStub) ExternalLinkMappings(_ context.Context, query radarport.ExternalLinkMappingQuery) (radarport.ExternalLinkMappingPage, error) {
	stub.calls++
	stub.query = query
	return stub.page, stub.err
}

func TestOpenPlatformStartsWithoutWeComScopeAndDefersIdentityRejection(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42}}
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("", nil, nil))
	if err != nil {
		t.Fatalf("unconfigured platform failed composition: %v", err)
	}
	response, err := executor.Execute(context.Background(), openplatformport.Request{Method: "GET", Path: "/api/external/users/resolve", Query: url.Values{"external_userid": {"external-1"}}})
	if err != nil || response.Status != 409 || len(identity.calls) != 0 {
		t.Fatalf("response=%+v calls=%d err=%v", response, len(identity.calls), err)
	}
}

func TestOpenPlatformIdentityUsesDeclaredScopedReferenceAndDoesNotProvision(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42, IdentityID: 7}}
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	response, err := executor.Execute(context.Background(), openplatformport.Request{Method: "GET", Path: "/api/identity/resolve", Query: url.Values{"external_userid": {"external-1"}}})
	if err != nil || response.Status != 200 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	if identity.seen.Kind != identitydomain.KindWeComExternalUserID || identity.seen.Scope != "wecom-corp:corp-main" || identity.seen.Assurance != identitydomain.AssuranceDeclared || identity.seen.Source != "open_platform.api" {
		t.Fatalf("identity reference=%+v", identity.seen)
	}
}

func TestOpenPlatformExternalUserRetainsFrozenUserEnvelope(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42, IdentityID: 7}}
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	response, err := executor.Execute(context.Background(), openplatformport.Request{Method: "GET", Path: "/api/external/users/resolve", Query: url.Values{"external_userid": {"external-1"}}})
	if err != nil || response.Status != 200 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	body, ok := response.Body.(map[string]any)
	if !ok || body["source_status"] != "external_user_basic" || body["route_owner"] != "ai_crm_next" || body["fallback_used"] != false {
		t.Fatalf("envelope=%#v", response.Body)
	}
	user, ok := body["user"].(map[string]any)
	if !ok || user["person_id"] != "42" || user["external_userid"] != "external-1" || user["customer_name"] != "Customer" || user["matched_by"] != "external_userid" || user["detail_url"] != "/api/customers/external-1" {
		t.Fatalf("user=%#v", body["user"])
	}
}

func TestOpenPlatformMCPReturnsArchiveNotReadyAsAnExplicitFact(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42}}
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, &openPlatformProfileStub{}, &openPlatformArchiveStub{err: archiveport.ErrNotReady}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_recent_messages","arguments":{"external_userid":"external-1"}}}`)
	response, err := executor.Execute(context.Background(), openplatformport.Request{Method: "POST", Path: "/mcp", Body: body})
	if err != nil || response.Status != 200 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	encoded, marshalErr := json.Marshal(response.Body)
	if marshalErr != nil || !strings.Contains(string(encoded), `"archive_status":"not_ready"`) {
		t.Fatalf("MCP result=%s err=%v", encoded, marshalErr)
	}
}

func TestOpenPlatformOrdersMapScopedIdentityBeforeCallingOrderPort(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42}}
	orders := &openPlatformOrderStub{}
	executor, err := newOpenPlatformExecutor(identity, orders, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	response, err := executor.Execute(context.Background(), openplatformport.Request{Method: "GET", Path: "/api/external/orders", Query: url.Values{"external_userid": {"external-1"}, "limit": {"20"}}})
	if err != nil || response.Status != 200 || orders.last.CustomerID != 42 || orders.last.Limit != 20 {
		t.Fatalf("response=%+v query=%+v err=%v", response, orders.last, err)
	}
}

func TestOpenPlatformExternalChatRecordsUseScopedArchiveProjection(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42}, externalValue: "external-1", externalFound: true}
	archive := &openPlatformArchiveStub{externalPage: archiveport.ExternalChatRecordPage{Items: []archiveport.ExternalChatRecord{{MessageID: "msg-1", ChatScene: "private", ChatType: "private", ExternalUserID: "external-1", WithUserID: "staff-a", Sender: "staff-a", Receiver: "external-1", MessageType: "text", Content: "hello", OccurredAt: time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC), SourceID: "9"}}, Total: 2}}
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, &openPlatformProfileStub{}, archive, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	response, err := executor.Execute(context.Background(), openplatformport.Request{Method: "GET", Path: "/api/external/chat-records", Query: url.Values{"external_userid": {"external-1"}, "chat_scene": {"private"}, "start_time": {"1725580800"}}})
	if err != nil || response.Status != 200 || archive.externalCalls != 1 {
		t.Fatalf("response=%+v archive_calls=%d err=%v", response, archive.externalCalls, err)
	}
	if identity.externalCalls != 1 || archive.externalQuery.CustomerID != 42 || archive.externalQuery.ExternalUserID != "external-1" || archive.externalQuery.ChatScene != "private" || archive.externalQuery.WithUserID != "HuangYouCan" || archive.externalQuery.Limit != 20 || archive.externalQuery.Offset != 0 {
		t.Fatalf("archive query=%+v", archive.externalQuery)
	}
	body, ok := response.Body.(map[string]any)
	if !ok || body["source_status"] != "external_chat_records" || body["external_userid"] != "external-1" || body["matched_by"] != "external_userid" || body["has_more"] != true || body["next_cursor"] == "" {
		t.Fatalf("body=%#v", response.Body)
	}
	items, ok := body["items"].([]map[string]any)
	if !ok || len(items) != 1 || items[0]["msgid"] != "msg-1" || items[0]["send_time"] != "2026-09-06 08:00:00" || items[0]["content"] != "hello" {
		t.Fatalf("items=%#v", body["items"])
	}
}

func TestOpenPlatformExternalChatRecordsUseVerifiedExternalIdentityForScopeAndQuery(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42}, externalValue: "external-trusted", externalFound: true}
	archive := &openPlatformArchiveStub{externalPage: archiveport.ExternalChatRecordPage{Total: 0}}
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, &openPlatformProfileStub{}, archive, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	request := openplatformport.Request{Method: "GET", Path: "/api/external/chat-records", Query: url.Values{"external_userid": {"external-untrusted"}, "chat_scene": {"private"}, "start_time": {"10"}}, Principal: accessdomain.MachinePrincipal{CorpID: "corp-main", OwnerScope: accessdomain.OwnerScope{"corp_id": {"corp-main"}, "external_userid": {"external-trusted"}}}}
	response, err := executor.Execute(context.Background(), request)
	if err != nil || response.Status != 200 || archive.externalCalls != 1 || archive.externalQuery.ExternalUserID != "external-trusted" {
		t.Fatalf("response=%+v err=%v identity=%+v archive=%+v", response, err, identity, archive.externalQuery)
	}
	body := response.Body.(map[string]any)
	if body["external_userid"] != "external-trusted" || body["matched_by"] != "external_userid" {
		t.Fatalf("body=%#v", body)
	}

	archive.externalCalls = 0
	request.Principal.OwnerScope = accessdomain.OwnerScope{"corp_id": {"corp-main"}, "external_userid": {"external-untrusted"}}
	response, err = executor.Execute(context.Background(), request)
	if err != nil || response.Status != 404 || archive.externalCalls != 0 {
		t.Fatalf("untrusted request response=%+v calls=%d err=%v", response, archive.externalCalls, err)
	}
}

func TestOpenPlatformExternalChatRecordsRejectConflictingIdentityInputs(t *testing.T) {
	identity := &openPlatformIdentityStub{results: map[string]identityport.ResolveResult{
		"wecom_external_userid|wecom-corp:corp-main|external-1": {Status: identityport.ResolveFound, CustomerID: 42},
		"unionid|wechat-open-platform:shared|union-2":           {Status: identityport.ResolveFound, CustomerID: 43},
	}, externalValue: "external-1", externalFound: true}
	archive := &openPlatformArchiveStub{}
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, &openPlatformProfileStub{}, archive, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", []string{"wechat-open-platform:shared"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	response, err := executor.Execute(context.Background(), openplatformport.Request{Method: "GET", Path: "/api/external/chat-records", Query: url.Values{"external_userid": {"external-1"}, "unionid": {"union-2"}, "chat_scene": {"private"}, "start_time": {"10"}}})
	if err != nil || response.Status != 409 || identity.externalCalls != 0 || archive.externalCalls != 0 {
		t.Fatalf("response=%+v resolve_calls=%v external_calls=%d archive_calls=%d err=%v", response, identity.calls, identity.externalCalls, archive.externalCalls, err)
	}
}

func TestExternalChatQueryRetainsDonorAliasesAndCursor(t *testing.T) {
	executor, err := newOpenPlatformExecutor(&openPlatformIdentityStub{}, &openPlatformOrderStub{}, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	cursor := base64.URLEncoding.EncodeToString([]byte(`{"offset":2}`))
	query, _, matchedBy, startText, err := executor.externalChatQuery(url.Values{"mobile": {"13800000000"}, "chat_scene": {"群聊"}, "start_time": {"00010"}, "with_userid": {"ignored"}, "cursor": {cursor}, "donor_ignored": {"1"}})
	if err != nil || query.ChatScene != "group" || query.WithUserID != "" || query.Offset != 2 || query.Limit != 20 || matchedBy != "mobile" || startText != "1970-01-01 00:00:10" {
		t.Fatalf("query=%+v matched=%q start=%q err=%v", query, matchedBy, startText, err)
	}
}

func TestOpenPlatformExternalOrdersRetainFrozenEnvelopeAndStatus(t *testing.T) {
	orders := &openPlatformOrderStub{page: orderport.Page{Items: []orderdomain.Snapshot{{ID: 9, MerchantOrderNo: "order-9", Provider: orderdomain.ProviderWeChatPay, Amount: orderdomain.Money{AmountMinor: 1234, Currency: "CNY"}, Status: orderdomain.StatusPartiallyRefunded, RefundedMinor: 200, CreatedAt: time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC), Items: []orderdomain.ItemSnapshot{{ProductCode: "course"}}}}}}
	executor, err := newOpenPlatformExecutor(&openPlatformIdentityStub{}, orders, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	response, err := executor.Execute(context.Background(), openplatformport.Request{Method: "GET", Path: "/api/external/orders", Query: url.Values{"provider": {"all"}, "payment_status": {"partial_refunded"}}})
	if err != nil || response.Status != 200 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	body, ok := response.Body.(map[string]any)
	if !ok || body["source_status"] != "external_orders" || body["route_owner"] != "ai_crm_next" || body["fallback_used"] != false {
		t.Fatalf("envelope=%#v", response.Body)
	}
	items, ok := body["items"].([]map[string]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items=%#v", body["items"])
	}
	item := items[0]
	if item["provider"] != "wechat" || item["payment_status"] != "partial_refunded" || item["status_label"] != "partial_refunded" || item["amount_yuan"] != "12.34" || item["refund_status"] != "partial_refunded" || item["detail_url"] != "/api/external/orders/order-9?provider=wechat" {
		t.Fatalf("item=%#v", item)
	}
	filters, ok := body["filters"].(map[string]string)
	if !ok || filters["payment_status"] != "partial_refunded" {
		t.Fatalf("filters=%#v", body["filters"])
	}
	providers, ok := body["providers"].([]string)
	if !ok || len(providers) != 3 || providers[0] != "wechat" || providers[2] != "wechat_shop" {
		t.Fatalf("providers=%#v", body["providers"])
	}
}

func TestOpenPlatformOrderDetailUsesCustomerBoundPortForCustomerScope(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42}}
	orders := &openPlatformOrderStub{scopedResult: orderdomain.Snapshot{ID: 9, MerchantOrderNo: "order-42", Provider: orderdomain.ProviderWeChatPay, Amount: orderdomain.Money{AmountMinor: 100, Currency: "CNY"}, Status: orderdomain.StatusPaid, CreatedAt: time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)}}
	executor, err := newOpenPlatformExecutor(identity, orders, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	response, err := executor.Execute(context.Background(), openplatformport.Request{Method: "GET", Path: "/api/external/orders/{order_no}", PathParts: map[string]string{"order_no": "order-42"}, Principal: accessdomain.MachinePrincipal{CorpID: "corp-main", OwnerScope: accessdomain.OwnerScope{"customer_id": {"42"}, "corp_id": {"corp-main"}}}})
	if err != nil || response.Status != 200 || orders.scopedCalls != 1 || orders.scopedReference != "order-42" || orders.scopedCustomer != 42 || orders.calls != 0 {
		t.Fatalf("response=%+v err=%v scoped=%d reference=%q customer=%d broad=%d", response, err, orders.scopedCalls, orders.scopedReference, orders.scopedCustomer, orders.calls)
	}
}

func TestOpenPlatformScopedCustomerQueryChecksTrustedOwnerBeforeOrderPort(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42}}
	orders := &openPlatformOrderStub{}
	owners := &openPlatformOwnerStub{items: []wecomport.AudiencePrimaryOwner{{CustomerID: 42, CorpScope: "wecom-corp:corp-main", OwnerUserID: "owner-a", Status: "known"}}}
	executor, err := newOpenPlatformExecutor(identity, orders, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, owners, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	response, err := executor.Execute(context.Background(), openplatformport.Request{Method: "GET", Path: "/api/external/orders", Query: url.Values{"external_userid": {"external-1"}}, Principal: accessdomain.MachinePrincipal{CorpID: "corp-main", OwnerScope: accessdomain.OwnerScope{"owner_userid": {"owner-b"}}}})
	if err != nil || response.Status != 404 || owners.calls != 1 || orders.calls != 0 || orders.last.CustomerID != 0 {
		t.Fatalf("response=%+v owner_calls=%d query=%+v err=%v", response, owners.calls, orders.last, err)
	}
}

func TestOpenPlatformScopedMCPDoesNotReadProfileOrArchiveOutsideOwnerScope(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42}}
	profiles := &openPlatformProfileStub{}
	archive := &openPlatformArchiveStub{}
	owners := &openPlatformOwnerStub{items: []wecomport.AudiencePrimaryOwner{{CustomerID: 42, CorpScope: "wecom-corp:corp-main", OwnerUserID: "owner-a", Status: "known"}}}
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, profiles, archive, &openPlatformTimelineStub{}, owners, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_recent_messages","arguments":{"external_userid":"external-1"}}}`)
	_, err = executor.Execute(context.Background(), openplatformport.Request{Method: "POST", Path: "/mcp", Body: body, Principal: accessdomain.MachinePrincipal{CorpID: "corp-main", OwnerScope: accessdomain.OwnerScope{"owner_userid": {"owner-b"}}}})
	if !errors.Is(err, errOpenPlatformResourceOutOfScope) || profiles.calls != 0 || archive.calls != 0 {
		t.Fatalf("err=%v profile_calls=%d archive_calls=%d", err, profiles.calls, archive.calls)
	}
}

func TestOpenPlatformUnionIDUsesOneConfiguredScopeForLegacyRequest(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42}}
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", []string{"wechat-open-platform:shared"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	response, err := executor.Execute(context.Background(), openplatformport.Request{Method: "GET", Path: "/api/external/users/resolve", Query: url.Values{"unionid": {"union-1"}}})
	if err != nil || response.Status != 200 || identity.seen.Scope != "wechat-open-platform:shared" {
		t.Fatalf("response=%+v scope=%q err=%v", response, identity.seen.Scope, err)
	}
}

func TestOpenPlatformRejectsUntrustedCallerSuppliedIdentityScope(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42}}
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", []string{"wechat-open-platform:shared"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	response, err := executor.Execute(context.Background(), openplatformport.Request{Method: "GET", Path: "/api/external/users/resolve", Query: url.Values{"unionid": {"union-1"}, "scope": {"wechat-open-platform:other"}}})
	if err != nil || response.Status != 409 || len(identity.calls) != 0 {
		t.Fatalf("response=%+v calls=%d err=%v", response, len(identity.calls), err)
	}
}

func TestOpenPlatformRejectsContradictoryLegacyIdentityReferences(t *testing.T) {
	identity := &openPlatformIdentityStub{results: map[string]identityport.ResolveResult{
		"wecom_external_userid|wecom-corp:corp-main|external-1": {Status: identityport.ResolveFound, CustomerID: 42},
		"unionid|wechat-open-platform:shared|union-1":           {Status: identityport.ResolveFound, CustomerID: 43},
	}}
	profiles := &openPlatformProfileStub{}
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, profiles, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", []string{"wechat-open-platform:shared"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	response, err := executor.Execute(context.Background(), openplatformport.Request{Method: "GET", Path: "/api/external/users/resolve", Query: url.Values{"external_userid": {"external-1"}, "unionid": {"union-1"}}})
	if err != nil || response.Status != 409 || profiles.calls != 0 || len(identity.calls) != 2 {
		t.Fatalf("response=%+v profile_calls=%d identity_calls=%d err=%v", response, profiles.calls, len(identity.calls), err)
	}
}

func TestOpenPlatformMCPRejectsContradictoryCustomerReferences(t *testing.T) {
	identity := &openPlatformIdentityStub{results: map[string]identityport.ResolveResult{
		"wecom_external_userid|wecom-corp:corp-main|external-a": {Status: identityport.ResolveFound, CustomerID: 42},
		"wecom_external_userid|wecom-corp:corp-main|external-b": {Status: identityport.ResolveFound, CustomerID: 43},
	}}
	profiles := &openPlatformProfileStub{}
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, profiles, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"resolve_customer","arguments":{"customer_ref":"external-a","external_userid":"external-b"}}}`)
	_, err = executor.Execute(context.Background(), openplatformport.Request{Method: "POST", Path: "/mcp", Body: body})
	if !errors.Is(err, errOpenPlatformIdentityConflict) || profiles.calls != 0 || len(identity.calls) != 2 {
		t.Fatalf("err=%v profile_calls=%d identity_calls=%d", err, profiles.calls, len(identity.calls))
	}
}

func TestOpenPlatformExternalRadarLinksRetainsDonorKeysetEnvelope(t *testing.T) {
	links := &openPlatformRadarLinksStub{page: radarport.ExternalLinkMappingPage{Items: []radarport.ExternalLinkMapping{{RadarID: 12, RadarCode: "rd_1234567890abcdef", Title: "Disabled historical mapping"}}, Total: 3, HasMore: true}}
	executor, err := newOpenPlatformExecutor(&openPlatformIdentityStub{}, &openPlatformOrderStub{}, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err = executor.BindExternalRadarLinkMappings(links); err != nil {
		t.Fatal(err)
	}
	cursor := base64.URLEncoding.EncodeToString([]byte(`{"radar_id":99}`))
	response, err := executor.Execute(context.Background(), openplatformport.Request{Method: "GET", Path: "/api/external/radar-links", Query: url.Values{"radar_id": {"12"}, "radar_code": {" rd_1234567890abcdef "}, "limit": {"2"}, "cursor": {cursor}, "donor_ignored": {"1"}}})
	if err != nil || response.Status != 200 || links.calls != 1 {
		t.Fatalf("response=%+v calls=%d err=%v", response, links.calls, err)
	}
	if links.query.RadarID != 12 || links.query.RadarCode != "rd_1234567890abcdef" || links.query.BeforeRadarID != 99 || links.query.Limit != 2 {
		t.Fatalf("query=%+v", links.query)
	}
	body, ok := response.Body.(map[string]any)
	if !ok || body["source_status"] != "external_radar_links" || body["route_owner"] != "ai_crm_next" || body["fallback_used"] != false || body["total"] != int64(3) || body["limit"] != int32(2) || body["has_more"] != true {
		t.Fatalf("body=%#v", response.Body)
	}
	items, ok := body["items"].([]map[string]any)
	if !ok || len(items) != 1 || items[0]["radar_id"] != radarport.RadarID(12) || items[0]["title"] != "Disabled historical mapping" {
		t.Fatalf("items=%#v", body["items"])
	}
	next, ok := body["next_cursor"].(string)
	if !ok || next == "" {
		t.Fatalf("next cursor=%#v", body["next_cursor"])
	}
	decoded, decodeErr := decodeExternalKeysetCursor(next, "radar_id")
	if decodeErr != nil || decoded != 12 {
		t.Fatalf("next cursor=%q decoded=%d err=%v", next, decoded, decodeErr)
	}
	filters, ok := body["filters"].(map[string]any)
	if !ok || filters["radar_id"] != int64(12) || filters["radar_code"] != "rd_1234567890abcdef" {
		t.Fatalf("filters=%#v", body["filters"])
	}
}

func TestOpenPlatformExternalRadarLinksRejectsBadCursorAndBoundOwnerScope(t *testing.T) {
	links := &openPlatformRadarLinksStub{}
	executor, err := newOpenPlatformExecutor(&openPlatformIdentityStub{}, &openPlatformOrderStub{}, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err = executor.BindExternalRadarLinkMappings(links); err != nil {
		t.Fatal(err)
	}
	response, err := executor.Execute(context.Background(), openplatformport.Request{Method: "GET", Path: "/api/external/radar-links", Query: url.Values{"cursor": {base64.RawURLEncoding.EncodeToString([]byte(`{"wrong":1}`))}}})
	if err != nil || response.Status != 400 || links.calls != 0 {
		t.Fatalf("bad cursor response=%+v calls=%d err=%v", response, links.calls, err)
	}
	response, err = executor.Execute(context.Background(), openplatformport.Request{Method: "GET", Path: "/api/external/radar-links", Principal: accessdomain.MachinePrincipal{CorpID: "corp-main", OwnerScope: accessdomain.OwnerScope{"customer_id": {"42"}, "corp_id": {"corp-main"}}}})
	if err != nil || response.Status != 404 || links.calls != 0 {
		t.Fatalf("scope response=%+v calls=%d err=%v", response, links.calls, err)
	}
}

func TestOpenPlatformExternalRadarLinksReportsUnboundReadModel(t *testing.T) {
	executor, err := newOpenPlatformExecutor(&openPlatformIdentityStub{}, &openPlatformOrderStub{}, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	response, err := executor.Execute(context.Background(), openplatformport.Request{Method: "GET", Path: "/api/external/radar-links"})
	if err != nil || response.Status != 503 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	body := response.Body.(map[string]any)
	if body["error_code"] != "production_unavailable" || body["source_status"] != "production_unavailable" {
		t.Fatalf("body=%#v", body)
	}
}
