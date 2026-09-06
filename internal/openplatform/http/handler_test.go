package http

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"reflect"
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
func (handlerManagementStub) Update(context.Context, accessdomain.Principal, string, accessport.UpdateMachineClientInput) (accessport.MachineClientSummary, error) {
	return accessport.MachineClientSummary{}, nil
}
func (handlerManagementStub) Activate(context.Context, accessdomain.Principal, string, string, bool) (accessport.MachineClientSummary, error) {
	return accessport.MachineClientSummary{}, nil
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

func TestMCPToolsMatchFrozenDD8D60DCatalogFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/mcp_tools_dd8d60d.json")
	if err != nil {
		t.Fatal(err)
	}
	var want []map[string]any
	if err = json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	actualRaw, err := json.Marshal(mcpTools(accessdomain.MachinePrincipal{Capabilities: []string{"mcp_execute"}}))
	if err != nil {
		t.Fatal(err)
	}
	var actual []map[string]any
	if err = json.Unmarshal(actualRaw, &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("MCP tool catalog drift\nactual=%s\nwant=%s", actualRaw, raw)
	}
}

func TestMCPRejectsUnknownMethodAsJSONRPC(t *testing.T) {
	executor := &handlerExecutorStub{}
	handler, err := NewHandler(Config{MachineAuthentication: handlerMachineStub{principal: accessdomain.MachinePrincipal{ClientID: "mcp", Scopes: []string{"write"}, Capabilities: []string{"mcp_read", "mcp_execute"}}}, AdminAuthentication: handlerAdminStub{}, Management: handlerManagementStub{}, Executor: executor, SessionCookieName: "session", CSRFCookieName: "csrf"})
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

func TestMCPPostRequiresWriteScopeEvenWhenClientCapabilitiesExist(t *testing.T) {
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
		{http.MethodGet, "/api/admin/config/api-clients", "machine"},
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

func TestMountWithLegacyProtocolsPreservesDedicatedAuthenticationOwners(t *testing.T) {
	machine := markerHandler("machine")
	legacy := markerHandler("legacy")
	mounted := MountWithLegacyProtocols(legacy, machine, "legacy.token.with.dots")
	for _, item := range []struct {
		name, method, path, bearer, signature, expected string
	}{
		{"operation service bearer with dots", http.MethodPost, "/api/operation-cycles/reports", "legacy.token.with.dots", "", "legacy"},
		{"machine jwt", http.MethodPost, "/api/operation-cycles/reports", "header.payload.signature", "", "machine"},
		{"invalid jwt shaped bearer remains machine-owned", http.MethodPost, "/api/operation-cycles/reports", "broken.payload.signature", "", "machine"},
		// AI Assistant has its own actual signed endpoints outside the frozen
		// machine inventory; do not use an invented campaign route as a protocol proxy.
	} {
		t.Run(item.name, func(t *testing.T) {
			request := httptest.NewRequest(item.method, "https://crm.example.com"+item.path, nil)
			request.Header.Set("Authorization", "Bearer "+item.bearer)
			if item.signature != "" {
				request.Header.Set("X-AICRM-Signature", item.signature)
			}
			response := httptest.NewRecorder()
			mounted.ServeHTTP(response, request)
			if body := strings.TrimSpace(response.Body.String()); body != item.expected {
				t.Fatalf("response=%d body=%q want=%q", response.Code, body, item.expected)
			}
		})
	}
	request := httptest.NewRequest(http.MethodPost, "https://crm.example.com/api/operation-cycles/reports", nil)
	request.Header.Set("Authorization", "Bearer header.payload.signature")
	request.Header.Set("X-AICRM-Signature", "legacy-proof")
	response := httptest.NewRecorder()
	mounted.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "ambiguous_authentication") {
		t.Fatalf("ambiguous auth response=%d body=%s", response.Code, response.Body.String())
	}
}

func markerHandler(value string) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { _, _ = response.Write([]byte(value)) })
}

type legacyManagementStub struct {
	created accessport.CreateMachineClientInput
	list    []accessport.MachineClientSummary
}

func (stub *legacyManagementStub) Create(_ context.Context, _ accessdomain.Principal, input accessport.CreateMachineClientInput) (accessport.IssuedMachineClient, error) {
	stub.created = input
	return accessport.IssuedMachineClient{Client: accessport.MachineClientSummary{ClientID: input.ClientID, DisplayName: input.DisplayName, Purpose: input.Purpose, Audiences: input.Audiences, Scopes: input.Scopes, Capabilities: input.Capabilities, AllowedCIDRs: input.AllowedCIDRs, TokenTTLSeconds: input.TokenTTLSeconds, AuthVersion: 1}, Secret: "mc_visible_once"}, nil
}
func (stub *legacyManagementStub) List(context.Context, accessdomain.Principal) ([]accessport.MachineClientSummary, error) {
	return append([]accessport.MachineClientSummary(nil), stub.list...), nil
}
func (*legacyManagementStub) Rotate(context.Context, accessdomain.Principal, string) (accessport.IssuedMachineClient, error) {
	return accessport.IssuedMachineClient{}, nil
}
func (*legacyManagementStub) Update(context.Context, accessdomain.Principal, string, accessport.UpdateMachineClientInput) (accessport.MachineClientSummary, error) {
	return accessport.MachineClientSummary{}, nil
}
func (*legacyManagementStub) Activate(context.Context, accessdomain.Principal, string, string, bool) (accessport.MachineClientSummary, error) {
	return accessport.MachineClientSummary{}, nil
}
func (*legacyManagementStub) SetEnabled(context.Context, accessdomain.Principal, string, bool) (accessport.MachineClientSummary, error) {
	return accessport.MachineClientSummary{}, nil
}

func TestLegacyAPIClientCreateUsesFrozenPayloadAndResponseFields(t *testing.T) {
	management := &legacyManagementStub{}
	handler, err := NewHandler(Config{MachineAuthentication: handlerMachineStub{}, AdminAuthentication: handlerAdminStub{}, Management: management, Executor: &handlerExecutorStub{}, SessionCookieName: "session", CSRFCookieName: "csrf", PublicOrigin: "https://crm.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"display_name":"Partner Analytics","client_id":"partner.analytics","client_type":"external_api","token_ttl_minutes":30,"allowed_cidrs":["203.0.113.0/24"],"confirm":true}`
	request := httptest.NewRequest(http.MethodPost, "https://crm.example.com/api/admin/config/api-clients", strings.NewReader(body))
	request.TLS = &tls.ConnectionState{}
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusCreated || management.created.Purpose != "external_agent" || management.created.TokenTTLSeconds != 1800 || strings.Join(management.created.Capabilities, ",") != "external_read,external_write" || !strings.Contains(response.Body.String(), `"client_secret":"mc_visible_once"`) || !strings.Contains(response.Body.String(), `"client_type":"external_api"`) {
		t.Fatalf("code=%d input=%+v body=%s", response.Code, management.created, response.Body.String())
	}
}

func TestLegacyAPIClientListPreservesClientTypeAndTTLMinutes(t *testing.T) {
	management := &legacyManagementStub{list: []accessport.MachineClientSummary{{ClientID: "partner.mcp", DisplayName: "Partner MCP", Purpose: "mcp", Scopes: []string{"read", "write"}, Capabilities: []string{"mcp_read", "mcp_execute"}, TokenTTLSeconds: 3600, AuthVersion: 3}}}
	handler, err := NewHandler(Config{MachineAuthentication: handlerMachineStub{}, AdminAuthentication: handlerAdminStub{}, Management: management, Executor: &handlerExecutorStub{}, SessionCookieName: "session", CSRFCookieName: "csrf", PublicOrigin: "https://crm.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://crm.example.com/api/admin/config/api-clients", nil)
	request.TLS = &tls.ConnectionState{}
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"client_type":"mcp"`) || !strings.Contains(response.Body.String(), `"token_ttl_minutes":60`) || !strings.Contains(response.Body.String(), `"configured_count":1`) {
		t.Fatalf("code=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGenericMachineManagementRejectsSystemProfilePurpose(t *testing.T) {
	management := &legacyManagementStub{}
	handler, err := NewHandler(Config{MachineAuthentication: handlerMachineStub{}, AdminAuthentication: handlerAdminStub{}, Management: management, Executor: &handlerExecutorStub{}, SessionCookieName: "session", CSRFCookieName: "csrf"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://crm.example.com/api/admin/open-platform/clients", strings.NewReader(`{"client_id":"system.identity","display_name":"Identity","purpose":"identity","audiences":["external_integration"],"scopes":["read"],"capabilities":["identity_resolve"],"token_ttl_seconds":1800}`))
	request.TLS = &tls.ConnectionState{}
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || management.created.Purpose != "" {
		t.Fatalf("system purpose response=%d input=%+v body=%s", response.Code, management.created, response.Body.String())
	}
}
