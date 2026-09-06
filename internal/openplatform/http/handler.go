// Package http owns only machine protocol and DTO adaptation. Business
// operations are dispatched through the composition-owned openplatform Port.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
)

const maxBodyBytes int64 = 64 << 10

type AdminAuthentication interface {
	Authenticate(context.Context, string) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, string, string, string) (accessdomain.Principal, error)
}

type Config struct {
	MachineAuthentication accessport.MachineTokenIssuer
	AdminAuthentication   AdminAuthentication
	Management            accessport.MachineManagement
	Executor              openplatformport.Executor
	SessionCookieName     string
	CSRFCookieName        string
	TrustedProxyCIDRs     []string
	PublicOrigin          string
}

type Handler struct {
	machine        accessport.MachineTokenIssuer
	admin          AdminAuthentication
	management     accessport.MachineManagement
	executor       openplatformport.Executor
	sessionCookie  string
	csrfCookie     string
	trustedProxies []netip.Prefix
	publicOrigin   string
}

func NewHandler(config Config) (*Handler, error) {
	if config.MachineAuthentication == nil || config.AdminAuthentication == nil || config.Management == nil || config.Executor == nil || config.SessionCookieName == "" || config.CSRFCookieName == "" {
		return nil, errors.New("open platform HTTP dependencies are required")
	}
	proxies := make([]netip.Prefix, 0, len(config.TrustedProxyCIDRs))
	for _, raw := range config.TrustedProxyCIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return nil, errors.New("invalid trusted proxy CIDR")
		}
		proxies = append(proxies, prefix.Masked())
	}
	return &Handler{machine: config.MachineAuthentication, admin: config.AdminAuthentication, management: config.Management,
		executor: config.Executor, sessionCookie: config.SessionCookieName, csrfCookie: config.CSRFCookieName, trustedProxies: proxies, publicOrigin: strings.TrimRight(strings.TrimSpace(config.PublicOrigin), "/")}, nil
}

func (handler *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth/token", handler.token)
	mux.HandleFunc("GET /mcp", handler.mcpMetadata)
	mux.HandleFunc("POST /mcp", handler.mcp)
	mux.HandleFunc("GET /api/admin/open-platform/clients", handler.listClients)
	mux.HandleFunc("POST /api/admin/open-platform/clients", handler.createClient)
	mux.HandleFunc("POST /api/admin/open-platform/clients/{client_id}/rotate", handler.rotateClient)
	mux.HandleFunc("POST /api/admin/open-platform/clients/{client_id}/enable", handler.enableClient)
	mux.HandleFunc("POST /api/admin/open-platform/clients/{client_id}/disable", handler.disableClient)
	mux.HandleFunc("GET /api/admin/open-platform/routes", handler.routes)
	mux.HandleFunc("GET /api/admin/config/api-clients", handler.legacyListClients)
	mux.HandleFunc("GET /api/admin/config/api-clients/{client_id}", handler.legacyGetClient)
	mux.HandleFunc("POST /api/admin/config/api-clients", handler.legacyCreateClient)
	mux.HandleFunc("PUT /api/admin/config/api-clients/{client_id}", handler.legacyUpdateClient)
	mux.HandleFunc("POST /api/admin/config/api-clients/{client_id}/activate", handler.legacyActivateClient)
	mux.HandleFunc("POST /api/admin/config/api-clients/{client_id}/rotate-secret", handler.legacyRotateClient)
	mux.HandleFunc("PUT /api/admin/config/api-clients/{client_id}/enabled", handler.legacyDisableClient)
	mux.HandleFunc("GET /api/admin/config/api-key", handler.legacyDirectKeyStatus)
	mux.HandleFunc("POST /api/admin/config/api-key/generate", handler.legacyGenerateDirectKey)
	mux.HandleFunc("POST /api/admin/config/api-key/rotate", handler.legacyRotateDirectKey)
	mux.HandleFunc("PUT /api/admin/config/api-key/enabled", handler.legacyDisableDirectKey)
	for _, route := range Inventory {
		if route.Path == "/mcp" {
			continue
		}
		mux.HandleFunc(route.Method+" "+route.Path, handler.external(route))
	}
	return noStore(mux)
}

// Mount installs only the frozen machine protocol paths ahead of next. It
// deliberately does not use a broad /api/ prefix: ordinary browser/session
// routes retain their existing owner and never become machine endpoints.
func Mount(next, machine http.Handler) http.Handler {
	if next == nil || machine == nil {
		return http.NotFoundHandler()
	}
	mux := http.NewServeMux()
	mux.Handle("POST /oauth/token", machine)
	mux.Handle("GET /mcp", machine)
	mux.Handle("POST /mcp", machine)
	mux.Handle("GET /api/admin/open-platform/clients", machine)
	mux.Handle("POST /api/admin/open-platform/clients", machine)
	mux.Handle("POST /api/admin/open-platform/clients/{client_id}/rotate", machine)
	mux.Handle("POST /api/admin/open-platform/clients/{client_id}/enable", machine)
	mux.Handle("POST /api/admin/open-platform/clients/{client_id}/disable", machine)
	mux.Handle("GET /api/admin/open-platform/routes", machine)
	mux.Handle("GET /api/admin/config/api-clients", machine)
	mux.Handle("GET /api/admin/config/api-clients/{client_id}", machine)
	mux.Handle("POST /api/admin/config/api-clients", machine)
	mux.Handle("PUT /api/admin/config/api-clients/{client_id}", machine)
	mux.Handle("POST /api/admin/config/api-clients/{client_id}/activate", machine)
	mux.Handle("POST /api/admin/config/api-clients/{client_id}/rotate-secret", machine)
	mux.Handle("PUT /api/admin/config/api-clients/{client_id}/enabled", machine)
	mux.Handle("GET /api/admin/config/api-key", machine)
	mux.Handle("POST /api/admin/config/api-key/generate", machine)
	mux.Handle("POST /api/admin/config/api-key/rotate", machine)
	mux.Handle("PUT /api/admin/config/api-key/enabled", machine)
	for _, route := range Inventory {
		if route.Path == "/mcp" {
			continue
		}
		mux.Handle(route.Method+" "+route.Path, machine)
	}
	mux.Handle("/", next)
	return mux
}

func (handler *Handler) token(response http.ResponseWriter, request *http.Request) {
	source, ok := handler.secureSource(response, request)
	if !ok {
		return
	}
	if err := request.ParseForm(); err != nil {
		writeOAuthError(response, http.StatusBadRequest, "invalid_request")
		return
	}
	if request.Form.Get("grant_type") != "client_credentials" {
		writeOAuthError(response, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	clientID, clientSecret, hasBasic := request.BasicAuth()
	formID, formSecret := request.Form.Get("client_id"), request.Form.Get("client_secret")
	if hasBasic && (formID != "" || formSecret != "") && (formID != clientID || formSecret != clientSecret) {
		writeOAuthError(response, http.StatusUnauthorized, "invalid_client")
		return
	}
	if !hasBasic {
		clientID, clientSecret = formID, formSecret
	}
	requestedScopes := strings.Fields(request.Form.Get("scope"))
	issued, err := handler.machine.IssueClientCredentialsToken(request.Context(), accessport.ClientCredentialsInput{
		ClientID: clientID, ClientSecret: clientSecret, Audience: request.Form.Get("audience"), RequestedScopes: requestedScopes, SourceIP: source,
	})
	if err != nil {
		writeOAuthError(response, statusForMachineError(err), oauthErrorFor(err))
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, issued)
}

func (handler *Handler) mcpMetadata(response http.ResponseWriter, request *http.Request) {
	principal, ok := handler.machinePrincipal(response, request, "external_integration", "read", "mcp_read")
	if !ok {
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "transport": "jsonrpc", "methods": []string{"initialize", "tools/list", "tools/call"}, "client_id": principal.ClientID})
}

func (handler *Handler) mcp(response http.ResponseWriter, request *http.Request) {
	body, err := readBody(request)
	if err != nil {
		writeJSONRPCError(response, nil, -32600, "invalid request")
		return
	}
	var rpc struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &rpc); err != nil || rpc.JSONRPC != "2.0" || !validRPCID(rpc.ID) || strings.TrimSpace(rpc.Method) == "" {
		writeJSONRPCError(response, nil, -32600, "invalid request")
		return
	}
	id := json.RawMessage(rpc.ID)
	capability := "mcp_read"
	if rpc.Method == "tools/call" {
		capability = "mcp_execute"
	}
	source, sourceErr := handler.source(request)
	if sourceErr != nil {
		writeJSONRPCError(response, id, -32001, "authentication failed")
		return
	}
	bearer := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
	principal, authErr := handler.machine.AuthenticateBearer(request.Context(), bearer, "external_integration", source)
	if authErr != nil || !principal.HasScope("write") || !principal.HasCapability(capability) {
		writeJSONRPCError(response, id, -32001, "authentication failed")
		return
	}
	switch rpc.Method {
	case "initialize":
		writeJSONRPCResult(response, id, map[string]any{"protocolVersion": "2024-11-05", "serverInfo": map[string]string{"name": "aicrm-v3", "version": "1"}, "capabilities": map[string]any{"tools": map[string]any{}}})
	case "tools/list":
		if !principal.HasScope("write") || !principal.HasCapability("mcp_read") {
			writeJSONRPCError(response, id, -32001, "permission denied")
			return
		}
		writeJSONRPCResult(response, id, map[string]any{"tools": mcpTools(principal)})
	case "tools/call":
		if !principal.HasScope("write") || !principal.HasCapability("mcp_execute") {
			writeJSONRPCError(response, id, -32001, "permission denied")
			return
		}
		result, invokeErr := handler.executor.Execute(request.Context(), openplatformport.Request{Method: http.MethodPost, Path: "/mcp", Query: request.URL.Query(), Body: body, Principal: principal})
		if invokeErr != nil {
			writeJSONRPCError(response, id, -32000, "tool execution failed")
			return
		}
		writeJSONRPCResult(response, id, result.Body)
	default:
		writeJSONRPCError(response, id, -32601, "method not found")
	}
}

func (handler *Handler) external(route Route) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		principal, ok := handler.machinePrincipal(response, request, audienceFor(route), scopeFor(route), route.Capability)
		if !ok {
			return
		}
		body, err := readBody(request)
		if err != nil {
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
			return
		}
		parts := make(map[string]string)
		for _, placeholder := range routePlaceholders(route.Path) {
			parts[placeholder] = request.PathValue(placeholder)
		}
		result, err := handler.executor.Execute(request.Context(), openplatformport.Request{Method: request.Method, Path: route.Path, PathParts: parts, Query: request.URL.Query(), Body: body, Principal: principal})
		if err != nil {
			writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "operation_unavailable"})
			return
		}
		for key, values := range result.Header {
			response.Header()[key] = append([]string(nil), values...)
		}
		status := result.Status
		if status < 100 || status > 599 {
			status = http.StatusOK
		}
		writeJSON(response, status, result.Body)
	}
}

func (handler *Handler) listClients(response http.ResponseWriter, request *http.Request) {
	actor, ok := handler.adminPrincipal(response, request, false)
	if !ok {
		return
	}
	clients, err := handler.management.List(request.Context(), actor)
	if err != nil {
		writeAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": clients})
}

func (handler *Handler) createClient(response http.ResponseWriter, request *http.Request) {
	actor, ok := handler.adminPrincipal(response, request, true)
	if !ok {
		return
	}
	var input accessport.CreateMachineClientInput
	if err := decodeJSON(request, &input); err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	issued, err := handler.management.Create(request.Context(), actor, input)
	if err != nil {
		writeAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, issued)
}

func (handler *Handler) rotateClient(response http.ResponseWriter, request *http.Request) {
	actor, ok := handler.adminPrincipal(response, request, true)
	if !ok {
		return
	}
	issued, err := handler.management.Rotate(request.Context(), actor, request.PathValue("client_id"))
	if err != nil {
		writeAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, issued)
}

func (handler *Handler) enableClient(response http.ResponseWriter, request *http.Request) {
	handler.setEnabled(response, request, true)
}
func (handler *Handler) disableClient(response http.ResponseWriter, request *http.Request) {
	handler.setEnabled(response, request, false)
}
func (handler *Handler) setEnabled(response http.ResponseWriter, request *http.Request, enabled bool) {
	actor, ok := handler.adminPrincipal(response, request, true)
	if !ok {
		return
	}
	client, err := handler.management.SetEnabled(request.Context(), actor, request.PathValue("client_id"), enabled)
	if err != nil {
		writeAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, client)
}

// The following handlers keep the frozen v2 admin endpoint and payload
// contract. They only adapt it to the stable Access management Port; no old
// runtime component is imported.
func (handler *Handler) legacyListClients(response http.ResponseWriter, request *http.Request) {
	actor, ok := handler.adminPrincipal(response, request, false)
	if !ok {
		return
	}
	clients, err := handler.management.List(request.Context(), actor)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	query := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("q")))
	status := strings.TrimSpace(request.URL.Query().Get("status"))
	if status != "" && status != "enabled" && status != "disabled" {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_status_filter"})
		return
	}
	rows := make([]map[string]any, 0, len(clients))
	configured, enabled := 0, 0
	for _, client := range clients {
		if client.ClientID == accessDirectKeyID || legacyClientType(client) == "" {
			continue
		}
		configured++
		if client.Enabled {
			enabled++
		}
		item := handler.legacyClientItem(client)
		if status != "" && item["status"] != status {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(client.ClientID+" "+client.DisplayName+" "+item["type_label"].(string)+" "+item["permission_label"].(string)), query) {
			continue
		}
		rows = append(rows, item)
	}
	sort.Slice(rows, func(left, right int) bool {
		return rows[left]["client_id"].(string) < rows[right]["client_id"].(string)
	})
	payload := map[string]any{"rows": rows, "summary": map[string]any{
		"configured_count": configured, "enabled_count": enabled, "disabled_count": configured - enabled,
		"system_managed_count": 0, "status_label": legacyConfiguredLabel(configured),
	}, "templates": legacyClientTemplates(handler.baseURL(request))}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "api_clients": payload, "source_status": "auth_platform_read_model", "fallback_used": false})
}

func (handler *Handler) legacyGetClient(response http.ResponseWriter, request *http.Request) {
	actor, ok := handler.adminPrincipal(response, request, false)
	if !ok {
		return
	}
	clients, err := handler.management.List(request.Context(), actor)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	clientID := request.PathValue("client_id")
	for _, client := range clients {
		if client.ClientID == clientID && legacyClientType(client) != "" && client.ClientID != accessDirectKeyID {
			writeJSON(response, http.StatusOK, map[string]any{"ok": true, "client": handler.legacyClientItem(client), "source_status": "auth_platform_read_model", "fallback_used": false})
			return
		}
	}
	writeJSON(response, http.StatusNotFound, map[string]any{"ok": false, "error": "api_client_not_found"})
}

func (handler *Handler) legacyCreateClient(response http.ResponseWriter, request *http.Request) {
	actor, payload, ok := handler.legacyWritePayload(response, request, map[string]struct{}{"display_name": {}, "client_id": {}, "client_type": {}, "token_ttl_minutes": {}, "allowed_cidrs": {}, "confirm": {}, "admin_action_token": {}})
	if !ok {
		return
	}
	if !legacyConfirmed(response, payload) {
		return
	}
	clientType, valid := legacyText(payload, "client_type")
	input, err := legacyCreateInput(clientType, payload)
	if !valid || err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_api_client_type"})
		return
	}
	issued, err := handler.management.Create(request.Context(), actor, input)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"ok": true, "client": handler.legacyClientItem(issued.Client), "client_secret": issued.Secret, "source_status": "auth_platform_command", "fallback_used": false, "real_external_call_executed": false})
}

func (handler *Handler) legacyUpdateClient(response http.ResponseWriter, request *http.Request) {
	actor, payload, ok := handler.legacyWritePayload(response, request, map[string]struct{}{"display_name": {}, "token_ttl_minutes": {}, "allowed_cidrs": {}, "confirm": {}, "admin_action_token": {}})
	if !ok || !legacyConfirmed(response, payload) {
		return
	}
	input, err := legacyUpdateInput(payload)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_token_ttl"})
		return
	}
	client, err := handler.management.Update(request.Context(), actor, request.PathValue("client_id"), input)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "client": handler.legacyClientItem(client), "source_status": "auth_platform_command", "fallback_used": false})
}

func (handler *Handler) legacyActivateClient(response http.ResponseWriter, request *http.Request) {
	actor, payload, ok := handler.legacyWritePayload(response, request, map[string]struct{}{"client_secret": {}, "copied_confirmed": {}, "confirm": {}, "admin_action_token": {}})
	if !ok || !legacyConfirmed(response, payload) {
		return
	}
	secret, secretOK := legacyText(payload, "client_secret")
	copied, copiedOK := legacyBool(payload, "copied_confirmed")
	if !secretOK || !copiedOK {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "secret_copy_confirmation_required"})
		return
	}
	client, err := handler.management.Activate(request.Context(), actor, request.PathValue("client_id"), secret, copied)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "client": handler.legacyClientItem(client), "source_status": "auth_platform_command", "fallback_used": false})
}

func (handler *Handler) legacyRotateClient(response http.ResponseWriter, request *http.Request) {
	actor, payload, ok := handler.legacyWritePayload(response, request, map[string]struct{}{"confirm": {}, "admin_action_token": {}})
	if !ok || !legacyConfirmed(response, payload) {
		return
	}
	issued, err := handler.management.Rotate(request.Context(), actor, request.PathValue("client_id"))
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "client": handler.legacyClientItem(issued.Client), "client_secret": issued.Secret, "source_status": "auth_platform_command", "fallback_used": false})
}

func (handler *Handler) legacyDisableClient(response http.ResponseWriter, request *http.Request) {
	actor, payload, ok := handler.legacyWritePayload(response, request, map[string]struct{}{"enabled": {}, "confirm": {}, "admin_action_token": {}})
	if !ok || !legacyConfirmed(response, payload) {
		return
	}
	enabled, valid := legacyBool(payload, "enabled")
	if !valid || enabled {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "activation_requires_secret_self_check"})
		return
	}
	client, err := handler.management.SetEnabled(request.Context(), actor, request.PathValue("client_id"), false)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "client": handler.legacyClientItem(client), "source_status": "auth_platform_command", "fallback_used": false})
}

func (handler *Handler) legacyDirectKeyStatus(response http.ResponseWriter, request *http.Request) {
	actor, ok := handler.adminPrincipal(response, request, false)
	if !ok {
		return
	}
	clients, err := handler.management.List(request.Context(), actor)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	status := handler.legacyDirectStatus(request, directClient(clients))
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "api_key_status": status, "source_status": "auth_platform_read_model", "fallback_used": false})
}

func (handler *Handler) legacyGenerateDirectKey(response http.ResponseWriter, request *http.Request) {
	actor, payload, ok := handler.legacyWritePayload(response, request, map[string]struct{}{"confirm": {}, "admin_action_token": {}})
	if !ok || !legacyConfirmed(response, payload) {
		return
	}
	clients, err := handler.management.List(request.Context(), actor)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	if directClient(clients) != nil {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "direct_api_key_already_configured"})
		return
	}
	issued, err := handler.management.Create(request.Context(), actor, accessport.CreateMachineClientInput{ClientID: accessDirectKeyID, DisplayName: "CRM 开放 API Key", Purpose: "direct_api_key", Audiences: []string{"external_integration"}, Scopes: []string{"read"}, Capabilities: []string{"external_read"}, TokenTTLSeconds: 1800})
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"ok": true, "api_key": issued.Secret, "api_key_status": handler.legacyDirectStatus(request, &issued.Client), "source_status": "auth_platform_command", "fallback_used": false})
}

func (handler *Handler) legacyRotateDirectKey(response http.ResponseWriter, request *http.Request) {
	actor, payload, ok := handler.legacyWritePayload(response, request, map[string]struct{}{"confirm": {}, "admin_action_token": {}})
	if !ok || !legacyConfirmed(response, payload) {
		return
	}
	issued, err := handler.management.Rotate(request.Context(), actor, accessDirectKeyID)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "api_key": issued.Secret, "api_key_status": handler.legacyDirectStatus(request, &issued.Client), "source_status": "auth_platform_command", "fallback_used": false})
}

func (handler *Handler) legacyDisableDirectKey(response http.ResponseWriter, request *http.Request) {
	actor, payload, ok := handler.legacyWritePayload(response, request, map[string]struct{}{"enabled": {}, "confirm": {}, "admin_action_token": {}})
	if !ok || !legacyConfirmed(response, payload) {
		return
	}
	enabled, valid := legacyBool(payload, "enabled")
	if !valid || enabled {
		writeJSON(response, http.StatusConflict, map[string]any{"ok": false, "error": "direct_api_key_reactivation_requires_rotation"})
		return
	}
	client, err := handler.management.SetEnabled(request.Context(), actor, accessDirectKeyID, false)
	if err != nil {
		writeLegacyAdminError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"ok": true, "api_key_status": handler.legacyDirectStatus(request, &client), "source_status": "auth_platform_command", "fallback_used": false})
}

func (handler *Handler) routes(response http.ResponseWriter, request *http.Request) {
	if _, ok := handler.adminPrincipal(response, request, false); !ok {
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": Inventory, "count": len(Inventory)})
}

func (handler *Handler) machinePrincipal(response http.ResponseWriter, request *http.Request, audience, scope, capability string) (accessdomain.MachinePrincipal, bool) {
	source, ok := handler.secureSource(response, request)
	if !ok {
		return accessdomain.MachinePrincipal{}, false
	}
	bearer := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
	if bearer == "" || !strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") {
		writeJSON(response, http.StatusUnauthorized, map[string]string{"error": "authentication_required"})
		return accessdomain.MachinePrincipal{}, false
	}
	principal, err := handler.machine.AuthenticateBearer(request.Context(), bearer, audience, source)
	if err != nil {
		writeJSON(response, statusForMachineError(err), map[string]string{"error": "invalid_token"})
		return accessdomain.MachinePrincipal{}, false
	}
	if !principal.HasScope(scope) || !principal.HasCapability(capability) {
		writeJSON(response, http.StatusForbidden, map[string]string{"error": "permission_denied"})
		return accessdomain.MachinePrincipal{}, false
	}
	return principal, true
}

func (handler *Handler) adminPrincipal(response http.ResponseWriter, request *http.Request, write bool) (accessdomain.Principal, bool) {
	session, csrf := "", ""
	if cookie, err := request.Cookie(handler.sessionCookie); err == nil {
		session = cookie.Value
	}
	if cookie, err := request.Cookie(handler.csrfCookie); err == nil {
		csrf = cookie.Value
	}
	var principal accessdomain.Principal
	var err error
	if write {
		principal, err = handler.admin.AuthorizeCSRF(request.Context(), session, csrf, strings.TrimSpace(request.Header.Get("X-CSRF-Token")))
	} else {
		principal, err = handler.admin.Authenticate(request.Context(), session)
	}
	if err != nil {
		writeJSON(response, http.StatusUnauthorized, map[string]string{"error": "authentication_required"})
		return accessdomain.Principal{}, false
	}
	return principal, true
}

func (handler *Handler) secureSource(response http.ResponseWriter, request *http.Request) (netip.Addr, bool) {
	source, err := handler.source(request)
	if err != nil {
		code := "invalid_source_ip"
		if errors.Is(err, errHTTPSRequired) {
			code = "https_required"
		}
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": code})
		return netip.Addr{}, false
	}
	return source, true
}

var errHTTPSRequired = errors.New("https required")

func (handler *Handler) source(request *http.Request) (netip.Addr, error) {
	remote, err := remoteAddr(request.RemoteAddr)
	if err != nil {
		return netip.Addr{}, err
	}
	if request.TLS != nil {
		return remote, nil
	}
	if !handler.isTrustedProxy(remote) || request.Header.Get("X-Forwarded-Proto") != "https" {
		return netip.Addr{}, errHTTPSRequired
	}
	return handler.forwardedSource(request.Header.Get("X-Forwarded-For"))
}

// forwardedSource walks right to left. Each configured proxy hop is removed
// before selecting the first untrusted address, because a client can prepend
// arbitrary X-Forwarded-For values before the trusted proxy appends its peer.
func (handler *Handler) forwardedSource(value string) (netip.Addr, error) {
	hops := strings.Split(value, ",")
	if len(hops) == 0 || strings.TrimSpace(value) == "" {
		return netip.Addr{}, errors.New("missing forwarded source")
	}
	for index := len(hops) - 1; index >= 0; index-- {
		hop := strings.TrimSpace(hops[index])
		candidate, err := netip.ParseAddr(hop)
		if err != nil {
			return netip.Addr{}, err
		}
		if handler.isTrustedProxy(candidate) {
			continue
		}
		return candidate, nil
	}
	return netip.Addr{}, errors.New("forwarded source is only trusted proxies")
}

func (handler *Handler) isTrustedProxy(address netip.Addr) bool {
	for _, prefix := range handler.trustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(response, request)
	})
}

const accessDirectKeyID = "direct_external_api_key"

func (handler *Handler) legacyWritePayload(response http.ResponseWriter, request *http.Request, allowed map[string]struct{}) (accessdomain.Principal, map[string]json.RawMessage, bool) {
	actor, ok := handler.adminPrincipal(response, request, true)
	if !ok {
		return accessdomain.Principal{}, nil, false
	}
	body, err := readBody(request)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_request"})
		return accessdomain.Principal{}, nil, false
	}
	payload := map[string]json.RawMessage{}
	if json.Unmarshal(body, &payload) != nil {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "payload_must_be_object"})
		return accessdomain.Principal{}, nil, false
	}
	for key := range payload {
		if _, known := allowed[key]; !known {
			writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "unknown_fields:" + key})
			return accessdomain.Principal{}, nil, false
		}
	}
	if raw, exists := payload["admin_action_token"]; exists {
		var token string
		if json.Unmarshal(raw, &token) != nil || strings.TrimSpace(token) == "" {
			writeJSON(response, http.StatusUnauthorized, map[string]any{"ok": false, "error": "invalid_admin_action_token"})
			return accessdomain.Principal{}, nil, false
		}
	}
	return actor, payload, true
}

func legacyConfirmed(response http.ResponseWriter, payload map[string]json.RawMessage) bool {
	confirmed, valid := legacyBool(payload, "confirm")
	if !valid || !confirmed {
		writeJSON(response, http.StatusBadRequest, map[string]any{"ok": false, "error": "operation_confirmation_required"})
		return false
	}
	return true
}

func legacyText(payload map[string]json.RawMessage, key string) (string, bool) {
	raw, found := payload[key]
	if !found {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func legacyBool(payload map[string]json.RawMessage, key string) (bool, bool) {
	raw, found := payload[key]
	if !found {
		return false, false
	}
	var value bool
	return value, json.Unmarshal(raw, &value) == nil
}

func legacyTTL(payload map[string]json.RawMessage) (int, error) {
	raw, found := payload["token_ttl_minutes"]
	if !found {
		return 0, errors.New("missing ttl")
	}
	var minutes int
	if json.Unmarshal(raw, &minutes) != nil || (minutes != 15 && minutes != 30 && minutes != 60) {
		return 0, errors.New("invalid ttl")
	}
	return minutes * 60, nil
}

func legacyCIDRs(payload map[string]json.RawMessage) ([]string, error) {
	raw, found := payload["allowed_cidrs"]
	if !found || string(raw) == "null" {
		return []string{}, nil
	}
	var values []string
	if json.Unmarshal(raw, &values) == nil {
		return values, nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return nil, errors.New("invalid cidrs")
	}
	return strings.FieldsFunc(value, func(character rune) bool { return character == ',' || character == '\n' }), nil
}

func legacyCreateInput(clientType string, payload map[string]json.RawMessage) (accessport.CreateMachineClientInput, error) {
	clientID, clientIDOK := legacyText(payload, "client_id")
	displayName, displayNameOK := legacyText(payload, "display_name")
	ttl, err := legacyTTL(payload)
	if !clientIDOK || !displayNameOK || err != nil {
		return accessport.CreateMachineClientInput{}, errors.New("invalid api client")
	}
	cidrs, err := legacyCIDRs(payload)
	if err != nil {
		return accessport.CreateMachineClientInput{}, err
	}
	input := accessport.CreateMachineClientInput{ClientID: clientID, DisplayName: displayName, Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"}, AllowedCIDRs: cidrs, TokenTTLSeconds: ttl}
	switch clientType {
	case "external_api":
		input.Purpose, input.Capabilities = "external_agent", []string{"external_read", "external_write"}
	case "mcp":
		input.Purpose, input.Capabilities = "mcp", []string{"mcp_read", "mcp_execute"}
	default:
		return accessport.CreateMachineClientInput{}, errors.New("invalid type")
	}
	return input, nil
}

func legacyUpdateInput(payload map[string]json.RawMessage) (accessport.UpdateMachineClientInput, error) {
	displayName, valid := legacyText(payload, "display_name")
	ttl, err := legacyTTL(payload)
	if !valid || err != nil {
		return accessport.UpdateMachineClientInput{}, errors.New("invalid update")
	}
	cidrs, err := legacyCIDRs(payload)
	if err != nil {
		return accessport.UpdateMachineClientInput{}, err
	}
	return accessport.UpdateMachineClientInput{DisplayName: displayName, TokenTTLSeconds: ttl, AllowedCIDRs: cidrs}, nil
}

func legacyClientType(client accessport.MachineClientSummary) string {
	switch client.Purpose {
	case "external_agent":
		return "external_api"
	case "mcp":
		return "mcp"
	default:
		return ""
	}
}

func (handler *Handler) legacyClientItem(client accessport.MachineClientSummary) map[string]any {
	clientType := legacyClientType(client)
	label, resource := "External API", "/api/external"
	if clientType == "mcp" {
		label, resource = "MCP", "/mcp"
	}
	return map[string]any{"client_id": client.ClientID, "display_name": client.DisplayName, "client_type": clientType, "type_label": label,
		"purpose": client.Purpose, "audience": "external_integration", "scopes": client.Scopes, "capabilities": client.Capabilities,
		"permission_label": label, "allowed_cidrs": client.AllowedCIDRs, "token_ttl_minutes": client.TokenTTLSeconds / 60,
		"enabled": client.Enabled, "status": map[bool]string{true: "enabled", false: "disabled"}[client.Enabled], "status_label": map[bool]string{true: "已启用", false: "已停用"}[client.Enabled],
		"auth_version": client.AuthVersion, "credential_hint": client.CredentialHint, "credential_hint_available": client.CredentialHint != "", "last_rotated_at": "", "created_at": client.CreatedAt, "updated_at": "",
		"system_managed": false, "mutable": true, "base_url": handler.publicOrigin, "token_url": handler.publicOrigin + "/oauth/token", "resource_url": handler.publicOrigin + resource, "grant_type": "client_credentials"}
}

func legacyClientTemplates(baseURL string) []map[string]any {
	return []map[string]any{
		{"key": "external_api", "label": "External API", "purpose": "external_agent", "audience": "external_integration", "scopes": []string{"read", "write"}, "capabilities": []string{"external_read", "external_write"}, "base_url": baseURL, "token_url": baseURL + "/oauth/token", "resource_url": baseURL + "/api/external", "grant_type": "client_credentials"},
		{"key": "mcp", "label": "MCP", "purpose": "mcp", "audience": "external_integration", "scopes": []string{"read", "write"}, "capabilities": []string{"mcp_read", "mcp_execute"}, "base_url": baseURL, "token_url": baseURL + "/oauth/token", "resource_url": baseURL + "/mcp", "grant_type": "client_credentials"},
	}
}

func legacyConfiguredLabel(count int) string {
	if count == 0 {
		return "未配置"
	}
	return "已配置 " + strconv.Itoa(count) + " 个"
}

func directClient(clients []accessport.MachineClientSummary) *accessport.MachineClientSummary {
	for index := range clients {
		if clients[index].ClientID == accessDirectKeyID {
			return &clients[index]
		}
	}
	return nil
}

func (handler *Handler) legacyDirectStatus(request *http.Request, client *accessport.MachineClientSummary) map[string]any {
	configured := client != nil
	enabled := configured && client.Enabled
	status, label := "unconfigured", "未配置"
	if configured && enabled {
		status, label = "enabled", "已启用"
	} else if configured {
		status, label = "disabled", "已停用"
	}
	hint, authVersion := "aics_••••••••••••••••••", int64(0)
	if client != nil {
		hint, authVersion = client.CredentialHint, client.AuthVersion
	}
	baseURL := handler.baseURL(request)
	return map[string]any{"configured": configured, "enabled": enabled, "status": status, "status_label": label, "auth_version": authVersion, "credential_hint": hint, "credential_hint_available": client != nil && client.CredentialHint != "", "last_rotated_at": "", "created_at": "", "base_url": baseURL, "resource_url": baseURL + "/api/external", "authorization_header": "Authorization: Bearer <CRM_API_KEY>", "permission_label": "CRM 开放 API 只读"}
}

func (handler *Handler) baseURL(request *http.Request) string {
	if handler.publicOrigin != "" {
		return handler.publicOrigin
	}
	scheme := "https"
	if request.TLS == nil {
		scheme = "http"
	}
	return scheme + "://" + request.Host
}

func writeLegacyAdminError(response http.ResponseWriter, err error) {
	status, code := http.StatusBadRequest, "api_client_operation_failed"
	switch {
	case errors.Is(err, accessdomain.ErrNotFound):
		status, code = http.StatusNotFound, "api_client_not_found"
	case errors.Is(err, accessdomain.ErrMachineClientActive):
		status, code = http.StatusConflict, "active_client_update_requires_disable"
	case errors.Is(err, accessdomain.ErrMachineActivation):
		status, code = http.StatusBadRequest, "client_secret_self_check_failed"
	case errors.Is(err, accessdomain.ErrPermissionDenied):
		status, code = http.StatusForbidden, "manage_api_clients_required"
	}
	writeJSON(response, status, map[string]any{"ok": false, "error": code})
}

func remoteAddr(value string) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		host = value
	}
	return netip.ParseAddr(host)
}

func readBody(request *http.Request) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(nil, request.Body, maxBodyBytes))
}

func decodeJSON(request *http.Request, target any) error {
	body, err := readBody(request)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func writeJSON(response http.ResponseWriter, status int, body any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(body)
}

func writeOAuthError(response http.ResponseWriter, status int, code string) {
	response.Header().Set("WWW-Authenticate", `Basic realm="aicrm-token"`)
	writeJSON(response, status, map[string]string{"error": code})
}

func writeJSONRPCResult(response http.ResponseWriter, id json.RawMessage, result any) {
	writeJSON(response, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result})
}

func writeJSONRPCError(response http.ResponseWriter, id json.RawMessage, code int, message string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	writeJSON(response, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "error": map[string]any{"code": code, "message": message}})
}

func validRPCID(id json.RawMessage) bool {
	if len(id) == 0 {
		return false
	}
	var stringID string
	if json.Unmarshal(id, &stringID) == nil {
		return true
	}
	var numberID json.Number
	decoder := json.NewDecoder(strings.NewReader(string(id)))
	decoder.UseNumber()
	return decoder.Decode(&numberID) == nil && numberID.String() != ""
}

func mcpTools(principal accessdomain.MachinePrincipal) []map[string]any {
	if !principal.HasCapability("mcp_execute") {
		return []map[string]any{}
	}
	return []map[string]any{
		{"name": "resolve_customer", "description": "Resolve a customer by customer_ref, mobile, or external_userid.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"customer_ref": map[string]string{"type": "string"}, "external_userid": map[string]string{"type": "string"}, "include_context": map[string]string{"type": "boolean"}, "recent_message_limit": map[string]string{"type": "integer"}, "timeline_limit": map[string]string{"type": "integer"}}}},
		{"name": "get_customer_context", "description": "Return customer detail, recent messages, and timeline context.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"customer_ref": map[string]string{"type": "string"}, "external_userid": map[string]string{"type": "string"}, "recent_message_limit": map[string]string{"type": "integer"}, "timeline_limit": map[string]string{"type": "integer"}}}},
		{"name": "get_recent_messages", "description": "Return recent single-customer archived messages.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"customer_ref": map[string]string{"type": "string"}, "external_userid": map[string]string{"type": "string"}, "limit": map[string]string{"type": "integer"}}}},
	}
}

func routePlaceholders(path string) []string {
	result := make([]string, 0, 2)
	for remaining := path; ; {
		start := strings.Index(remaining, "{")
		if start < 0 {
			return result
		}
		end := strings.Index(remaining[start:], "}")
		if end < 2 {
			return result
		}
		result = append(result, remaining[start+1:start+end])
		remaining = remaining[start+end+1:]
	}
}

func audienceFor(Route) string { return "external_integration" }

func statusForMachineError(err error) int {
	switch {
	case errors.Is(err, accessdomain.ErrMachineAudience), errors.Is(err, accessdomain.ErrMachineScope), errors.Is(err, accessdomain.ErrMachineSourceIP):
		return http.StatusForbidden
	case errors.Is(err, accessdomain.ErrMachineClientDisabled), errors.Is(err, accessdomain.ErrMachineClientExpired), errors.Is(err, accessdomain.ErrMachineReissueRequired), errors.Is(err, accessdomain.ErrMachineCredential):
		return http.StatusUnauthorized
	default:
		return http.StatusBadRequest
	}
}

func oauthErrorFor(err error) string {
	if errors.Is(err, accessdomain.ErrMachineAudience) || errors.Is(err, accessdomain.ErrMachineScope) || errors.Is(err, accessdomain.ErrMachineSourceIP) {
		return "invalid_scope"
	}
	if errors.Is(err, accessdomain.ErrMachineCredential) || errors.Is(err, accessdomain.ErrMachineClientDisabled) || errors.Is(err, accessdomain.ErrMachineClientExpired) || errors.Is(err, accessdomain.ErrMachineReissueRequired) {
		return "invalid_client"
	}
	return "invalid_request"
}

func writeAdminError(response http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, accessdomain.ErrAuthentication) {
		status = http.StatusUnauthorized
	}
	if errors.Is(err, accessdomain.ErrPermissionDenied) {
		status = http.StatusForbidden
	}
	if errors.Is(err, accessdomain.ErrNotFound) {
		status = http.StatusNotFound
	}
	writeJSON(response, status, map[string]string{"error": "open_platform_request_failed"})
}
