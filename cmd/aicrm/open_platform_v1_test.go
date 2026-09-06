package main

import (
	"context"
	"encoding/json"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
)

func v1ExecutorForTest(t *testing.T, identity *openPlatformIdentityStub, profiles *openPlatformProfileStub) *openPlatformExecutor {
	t.Helper()
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, profiles, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", []string{"wechat-open-platform:shared"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func TestV1CatalogOnlyPublishesComposedGrantedOperations(t *testing.T) {
	executor := v1ExecutorForTest(t, &openPlatformIdentityStub{}, &openPlatformProfileStub{})
	principal := accessdomain.MachinePrincipal{Scopes: []string{"read"}, Capabilities: []string{
		string(openplatformport.CapabilityPlatformCapabilitiesRead),
		string(openplatformport.CapabilityCustomerResolve),
		string(openplatformport.CapabilityCustomerRead),
		string(openplatformport.CapabilityCustomerActivityRead),
	}}
	items, err := executor.Available(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]openplatformport.OperationID, 0, len(items))
	for _, item := range items {
		got = append(got, item.OperationID)
	}
	want := map[openplatformport.OperationID]bool{
		openplatformport.OperationCapabilitiesList: true,
		openplatformport.OperationCustomerResolve:  true,
		openplatformport.OperationCustomerContext:  true,
	}
	if len(got) != len(want) {
		t.Fatalf("available = %#v", got)
	}
	for _, operation := range got {
		if !want[operation] {
			t.Fatalf("unexpected operation %q", operation)
		}
	}
}

func TestV1ResolveUsesScopedOneIDAndNeverProvisions(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42, IdentityID: 7}}
	executor := v1ExecutorForTest(t, identity, &openPlatformProfileStub{})
	input := json.RawMessage(`{"references":[{"kind":"unionid","scope":"wechat-open-platform:shared","value":"union-42"}]}`)
	result, err := executor.Invoke(context.Background(), openplatformport.Invocation{
		Operation: openplatformport.OperationCustomerResolve,
		Principal: accessdomain.MachinePrincipal{Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityCustomerResolve)}},
		Input:     input,
	})
	if err != nil {
		t.Fatal(err)
	}
	data := result.Data.(map[string]any)
	if customerID, ok := data["customer_id"].(customerdomain.CustomerID); !ok || customerID != 42 || data["identity_id"] != int64(7) || data["status"] != "found" {
		t.Fatalf("result = %#v", data)
	}
	if len(identity.calls) != 1 || identity.seen.Scope != "wechat-open-platform:shared" || identity.seen.Assurance != "declared" || identity.seen.Source != "open_platform.v1" {
		t.Fatalf("identity reference = %+v", identity.seen)
	}
}

func TestV1ResolveRejectsCallerAssuranceAndConflictingRoots(t *testing.T) {
	identity := &openPlatformIdentityStub{results: map[string]identityport.ResolveResult{
		"unionid|wechat-open-platform:shared|union-42": {Status: identityport.ResolveFound, CustomerID: 42},
		"unionid|wechat-open-platform:shared|union-43": {Status: identityport.ResolveFound, CustomerID: 43},
	}}
	executor := v1ExecutorForTest(t, identity, &openPlatformProfileStub{})
	principal := accessdomain.MachinePrincipal{Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityCustomerResolve)}}
	_, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerResolve, Principal: principal, Input: json.RawMessage(`{"references":[{"kind":"unionid","scope":"wechat-open-platform:shared","value":"union-42","assurance":"verified"}]}`)})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorValidation || len(identity.calls) != 0 {
		t.Fatalf("declared assurance err=%v calls=%d", err, len(identity.calls))
	}
	_, err = executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerResolve, Principal: principal, Input: json.RawMessage(`{"references":[{"kind":"unionid","scope":"wechat-open-platform:shared","value":"union-42"},{"kind":"unionid","scope":"wechat-open-platform:shared","value":"union-43"}]}`)})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorIdentityConflict || len(identity.calls) != 2 {
		t.Fatalf("conflict err=%v calls=%d", err, len(identity.calls))
	}
}

func TestV1CustomerContextChecksOwnerScopeBeforeProfileRead(t *testing.T) {
	profiles := &openPlatformProfileStub{}
	executor := v1ExecutorForTest(t, &openPlatformIdentityStub{}, profiles)
	principal := accessdomain.MachinePrincipal{
		Scopes:       []string{"read"},
		Capabilities: []string{string(openplatformport.CapabilityCustomerRead)},
		OwnerScope:   accessdomain.OwnerScope{"customer_id": {"43"}},
	}
	_, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerContext, Principal: principal, Input: json.RawMessage(`{"customer_id":42}`)})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorNotFound || profiles.calls != 0 {
		t.Fatalf("scope err=%v profile_calls=%d", err, profiles.calls)
	}
}

func TestV1CustomerContextRequiresReadTokenAndCurrentGrant(t *testing.T) {
	executor := v1ExecutorForTest(t, &openPlatformIdentityStub{}, &openPlatformProfileStub{})
	_, err := executor.Invoke(context.Background(), openplatformport.Invocation{
		Operation: openplatformport.OperationCustomerContext,
		Principal: accessdomain.MachinePrincipal{Scopes: []string{"write"}, Capabilities: []string{string(openplatformport.CapabilityCustomerRead)}},
		Input:     json.RawMessage(`{"customer_id":42}`),
	})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorPermission {
		t.Fatalf("error = %v", err)
	}
}
