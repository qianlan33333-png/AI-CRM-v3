package http

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
)

type handlerMachineStub struct{ principal accessdomain.MachinePrincipal }

func (stub handlerMachineStub) IssueClientCredentialsToken(context.Context, accessapp.ClientCredentialsInput) (accessapp.IssuedAccessToken, error) {
	return accessapp.IssuedAccessToken{AccessToken: "token", TokenType: "Bearer", ExpiresIn: 1800}, nil
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

func (handlerManagementStub) Create(context.Context, accessdomain.Principal, accessapp.CreateMachineClientInput) (accessapp.IssuedMachineClient, error) {
	return accessapp.IssuedMachineClient{}, nil
}
func (handlerManagementStub) List(context.Context, accessdomain.Principal) ([]accessapp.MachineClientSummary, error) {
	return []accessapp.MachineClientSummary{}, nil
}
func (handlerManagementStub) Rotate(context.Context, accessdomain.Principal, string) (accessapp.IssuedMachineClient, error) {
	return accessapp.IssuedMachineClient{}, nil
}
func (handlerManagementStub) SetEnabled(context.Context, accessdomain.Principal, string, bool) (accessapp.MachineClientSummary, error) {
	return accessapp.MachineClientSummary{}, nil
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
	handler, err := NewHandler(Config{MachineAuthentication: handlerMachineStub{principal: accessdomain.MachinePrincipal{ClientID: "test", Capabilities: []string{"external_read"}}}, AdminAuthentication: handlerAdminStub{}, Management: handlerManagementStub{}, Executor: executor, SessionCookieName: "session", CSRFCookieName: "csrf"})
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
	handler, err := NewHandler(Config{MachineAuthentication: handlerMachineStub{principal: accessdomain.MachinePrincipal{ClientID: "mcp", Capabilities: []string{"mcp_read", "mcp_execute"}}}, AdminAuthentication: handlerAdminStub{}, Management: handlerManagementStub{}, Executor: executor, SessionCookieName: "session", CSRFCookieName: "csrf"})
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
