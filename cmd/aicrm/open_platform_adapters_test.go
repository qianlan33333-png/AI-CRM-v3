package main

import (
	"context"
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
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type openPlatformIdentityStub struct {
	result identityport.ResolveResult
	seen   identitydomain.Reference
}

func (stub *openPlatformIdentityStub) Resolve(_ context.Context, reference identitydomain.Reference) (identityport.ResolveResult, error) {
	stub.seen = reference
	return stub.result, nil
}

type openPlatformOrderStub struct {
	page  orderport.Page
	last  orderport.ListQuery
	calls int
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

type openPlatformArchiveStub struct {
	page  archiveport.CustomerPage
	err   error
	calls int
}

func (stub *openPlatformArchiveStub) CustomerMessages(context.Context, archiveport.CustomerQuery) (archiveport.CustomerPage, error) {
	stub.calls++
	return stub.page, stub.err
}
func (*openPlatformArchiveStub) CustomerStaff(context.Context, customerdomain.CustomerID) ([]archiveport.StaffOption, error) {
	return nil, nil
}

func TestOpenPlatformIdentityUsesDeclaredScopedReferenceAndDoesNotProvision(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42, IdentityID: 7}}
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, &openPlatformOwnerStub{}, "corp-main")
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

func TestOpenPlatformMCPReturnsArchiveNotReadyAsAnExplicitFact(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42}}
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, &openPlatformProfileStub{}, &openPlatformArchiveStub{err: archiveport.ErrNotReady}, &openPlatformOwnerStub{}, "corp-main")
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
	executor, err := newOpenPlatformExecutor(identity, orders, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, &openPlatformOwnerStub{}, "corp-main")
	if err != nil {
		t.Fatal(err)
	}
	response, err := executor.Execute(context.Background(), openplatformport.Request{Method: "GET", Path: "/api/external/orders", Query: url.Values{"external_userid": {"external-1"}, "limit": {"20"}}})
	if err != nil || response.Status != 200 || orders.last.CustomerID != 42 || orders.last.Limit != 20 {
		t.Fatalf("response=%+v query=%+v err=%v", response, orders.last, err)
	}
}

func TestOpenPlatformScopedCustomerQueryChecksTrustedOwnerBeforeOrderPort(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42}}
	orders := &openPlatformOrderStub{}
	owners := &openPlatformOwnerStub{items: []wecomport.AudiencePrimaryOwner{{CustomerID: 42, CorpScope: "wecom-corp:corp-main", OwnerUserID: "owner-a", Status: "known"}}}
	executor, err := newOpenPlatformExecutor(identity, orders, &openPlatformProfileStub{}, &openPlatformArchiveStub{}, owners, "corp-main")
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
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, profiles, archive, owners, "corp-main")
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_recent_messages","arguments":{"external_userid":"external-1"}}}`)
	_, err = executor.Execute(context.Background(), openplatformport.Request{Method: "POST", Path: "/mcp", Body: body, Principal: accessdomain.MachinePrincipal{CorpID: "corp-main", OwnerScope: accessdomain.OwnerScope{"owner_userid": {"owner-b"}}}})
	if !errors.Is(err, errOpenPlatformResourceOutOfScope) || profiles.calls != 0 || archive.calls != 0 {
		t.Fatalf("err=%v profile_calls=%d archive_calls=%d", err, profiles.calls, archive.calls)
	}
}
