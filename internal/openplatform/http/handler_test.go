package http

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
)

type handlerMachineStub struct{ principal accessdomain.MachinePrincipal }

func (stub handlerMachineStub) IssueClientCredentialsToken(context.Context, accessport.ClientCredentialsInput) (accessport.IssuedAccessToken, error) {
	return accessport.IssuedAccessToken{AccessToken: "token", TokenType: "Bearer", ExpiresIn: 1800}, nil
}
func (stub handlerMachineStub) AuthenticateBearer(context.Context, string, string, netip.Addr) (accessdomain.MachinePrincipal, error) {
	return stub.principal, nil
}

type handlerAdminStub struct{}

func (handlerAdminStub) Authenticate(context.Context, string) (accessdomain.Principal, error) {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}, nil
}
func (handlerAdminStub) AuthorizeCSRF(context.Context, string, string, string) (accessdomain.Principal, error) {
	return handlerAdminStub{}.Authenticate(context.Background(), "")
}

type handlerManagementStub struct{}

func (handlerManagementStub) Create(context.Context, accessdomain.Principal, accessport.CreateMachineClientInput) (accessport.IssuedMachineClient, error) {
	return accessport.IssuedMachineClient{}, nil
}
func (handlerManagementStub) List(context.Context, accessdomain.Principal) ([]accessport.MachineClientSummary, error) {
	return []accessport.MachineClientSummary{}, nil
}
func (handlerManagementStub) Rotate(context.Context, accessdomain.Principal, string) (accessport.IssuedMachineClient, error) {
	return accessport.IssuedMachineClient{}, nil
}
func (handlerManagementStub) SetEnabled(context.Context, accessdomain.Principal, string, bool) (accessport.MachineClientSummary, error) {
	return accessport.MachineClientSummary{}, nil
}

type handlerExecutorStub struct{ request openplatformport.Request }

func (stub *handlerExecutorStub) Execute(_ context.Context, request openplatformport.Request) (openplatformport.Response, error) {
	stub.request = request
	return openplatformport.Response{Status: http.StatusOK, Body: map[string]string{"status": "ok"}}, nil
}

func TestInventoryRegistersEveryFrozenMachineRoute(t *testing.T) {
	if len(Inventory) != 56 {
		t.Fatalf("inventory count = %d", len(Inventory))
	}
	seen := map[string]bool{}
	for _, route := range Inventory {
		key := route.Method + " " + route.Path
		if seen[key] {
			t.Fatalf("duplicate route %s", key)
		}
		seen[key] = true
	}
	executor := &handlerExecutorStub{}
	handler, err := NewHandler(Config{MachineAuthentication: handlerMachineStub{principal: accessdomain.MachinePrincipal{ClientID: "test", Scopes: []string{"read"}, Capabilities: []string{"external_read"}}}, AdminAuthentication: handlerAdminStub{}, Management: handlerManagementStub{}, Executor: executor, SessionCookieName: "session", CSRFCookieName: "csrf"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://crm.example.com/api/external/orders?limit=20", nil)
	request.TLS = &tls.ConnectionState{}
	request.Header.Set("Authorization", "Bearer test")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || executor.request.Path != "/api/external/orders" || executor.request.Principal.ClientID != "test" {
		t.Fatalf("external route response=%d request=%+v", response.Code, executor.request)
	}
}

func TestMCPRejectsUnknownMethodAsJSONRPC(t *testing.T) {
	executor := &handlerExecutorStub{}
	handler, err := NewHandler(Config{MachineAuthentication: handlerMachineStub{principal: accessdomain.MachinePrincipal{ClientID: "mcp", Scopes: []string{"mcp"}, Capabilities: []string{"mcp_read", "mcp_execute"}}}, AdminAuthentication: handlerAdminStub{}, Management: handlerManagementStub{}, Executor: executor, SessionCookieName: "session", CSRFCookieName: "csrf"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://crm.example.com/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"unknown"}`))
	request.TLS = &tls.ConnectionState{}
	request.Header.Set("Authorization", "Bearer test")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"code":-32601`) {
		t.Fatalf("MCP response %d: %s", response.Code, response.Body.String())
	}
}

func TestExternalWriteRequiresWriteScopeEvenWhenClientCapabilityIncludesWrite(t *testing.T) {
	executor := &handlerExecutorStub{}
	handler, err := NewHandler(Config{MachineAuthentication: handlerMachineStub{principal: accessdomain.MachinePrincipal{
		ClientID: "mixed", Scopes: []string{"read"}, Capabilities: []string{"external_read", "external_write"},
	}}, AdminAuthentication: handlerAdminStub{}, Management: handlerManagementStub{}, Executor: executor, SessionCookieName: "session", CSRFCookieName: "csrf"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://crm.example.com/api/ai/audience/packages", strings.NewReader(`{}`))
	request.TLS = &tls.ConnectionState{}
	request.Header.Set("Authorization", "Bearer test")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || executor.request.Path != "" {
		t.Fatalf("write with read token response=%d execution=%+v", response.Code, executor.request)
	}
}

func TestMCPRequiresMCPScopeEvenWhenClientCapabilitiesExist(t *testing.T) {
	executor := &handlerExecutorStub{}
	handler, err := NewHandler(Config{MachineAuthentication: handlerMachineStub{principal: accessdomain.MachinePrincipal{
		ClientID: "mcp", Scopes: []string{"read"}, Capabilities: []string{"mcp_read", "mcp_execute"},
	}}, AdminAuthentication: handlerAdminStub{}, Management: handlerManagementStub{}, Executor: executor, SessionCookieName: "session", CSRFCookieName: "csrf"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://crm.example.com/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`))
	request.TLS = &tls.ConnectionState{}
	request.Header.Set("Authorization", "Bearer test")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"code":-32001`) || executor.request.Path != "" {
		t.Fatalf("MCP with non-MCP token response=%d execution=%+v body=%s", response.Code, executor.request, response.Body.String())
	}
}

func TestTrustedProxyTakesRightmostUntrustedForwardedSource(t *testing.T) {
	handler, err := NewHandler(Config{MachineAuthentication: handlerMachineStub{}, AdminAuthentication: handlerAdminStub{}, Management: handlerManagementStub{}, Executor: &handlerExecutorStub{}, SessionCookieName: "session", CSRFCookieName: "csrf", TrustedProxyCIDRs: []string{"192.0.2.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://crm.example.com/api/external/orders", nil)
	request.RemoteAddr = "192.0.2.10:443"
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-For", "198.51.100.7, 203.0.113.9")
	source, err := handler.source(request)
	if err != nil || source != netip.MustParseAddr("203.0.113.9") {
		t.Fatalf("forwarded source = %v, %v", source, err)
	}
	request.Header.Set("X-Forwarded-For", "198.51.100.7, 192.0.2.20")
	source, err = handler.source(request)
	if err != nil || source != netip.MustParseAddr("198.51.100.7") {
		t.Fatalf("trusted intermediary source = %v, %v", source, err)
	}
}

func TestCreateMachineClientUsesFrozenSnakeCaseJSONFields(t *testing.T) {
	var input accessport.CreateMachineClientInput
	if err := json.Unmarshal([]byte(`{"client_id":"partner.analytics","display_name":"Partner analytics","purpose":"api","audiences":["external_integration"],"scopes":["read"],"capabilities":["external_read"],"allowed_cidrs":["203.0.113.0/24"],"token_ttl_seconds":1800,"expires_at":"2026-10-06T00:00:00Z"}`), &input); err != nil {
		t.Fatal(err)
	}
	if input.ClientID != "partner.analytics" || input.DisplayName != "Partner analytics" || input.TokenTTLSeconds != 1800 || len(input.AllowedCIDRs) != 1 || input.ExpiresAt == nil {
		t.Fatalf("frozen create DTO decoded as %+v", input)
	}
}

func TestMountClaimsOnlyFrozenMachineRoutes(t *testing.T) {
	machine := markerHandler("machine")
	legacy := markerHandler("legacy")
	mounted := Mount(legacy, machine)
	for _, item := range []struct {
		method, path, expected string
	}{
		{http.MethodPost, "/oauth/token", "machine"},
		{http.MethodGet, "/api/external/orders", "machine"},
		{http.MethodPost, "/api/operation-cycles/reports", "machine"},
		{http.MethodGet, "/api/admin/open-platform/clients", "machine"},
		{http.MethodGet, "/api/admin/orders", "legacy"},
		{http.MethodGet, "/api/external/not-in-inventory", "legacy"},
	} {
		request := httptest.NewRequest(item.method, "https://crm.example.com"+item.path, nil)
		response := httptest.NewRecorder()
		mounted.ServeHTTP(response, request)
		if body := strings.TrimSpace(response.Body.String()); body != item.expected {
			t.Fatalf("%s %s mounted to %q, want %q", item.method, item.path, body, item.expected)
		}
	}
}

func markerHandler(value string) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { _, _ = response.Write([]byte(value)) })
}
