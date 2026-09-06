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
}

type Handler struct {
	machine        accessport.MachineTokenIssuer
	admin          AdminAuthentication
	management     accessport.MachineManagement
	executor       openplatformport.Executor
	sessionCookie  string
	csrfCookie     string
	trustedProxies []netip.Prefix
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
		executor: config.Executor, sessionCookie: config.SessionCookieName, csrfCookie: config.CSRFCookieName, trustedProxies: proxies}, nil
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
	for _, route := range Inventory {
		if route.Path == "/mcp" {
			continue
		}
		mux.HandleFunc(route.Method+" "+route.Path, handler.external(route))
	}
	return noStore(mux)
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
	principal, ok := handler.machinePrincipal(response, request, "mcp", "mcp", "mcp_read")
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
	principal, authErr := handler.machine.AuthenticateBearer(request.Context(), bearer, "mcp", source)
	if authErr != nil || !principal.HasScope("mcp") || !principal.HasCapability(capability) {
		writeJSONRPCError(response, id, -32001, "authentication failed")
		return
	}
	switch rpc.Method {
	case "initialize":
		writeJSONRPCResult(response, id, map[string]any{"protocolVersion": "2024-11-05", "serverInfo": map[string]string{"name": "aicrm-v3", "version": "1"}, "capabilities": map[string]any{"tools": map[string]any{}}})
	case "tools/list":
		if !principal.HasScope("mcp") || !principal.HasCapability("mcp_read") {
			writeJSONRPCError(response, id, -32001, "permission denied")
			return
		}
		writeJSONRPCResult(response, id, map[string]any{"tools": mcpTools(principal)})
	case "tools/call":
		if !principal.HasScope("mcp") || !principal.HasCapability("mcp_execute") {
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
		{"name": "resolve_customer", "description": "Resolve a customer by customer_ref, mobile, or external_userid.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"customer_ref": map[string]string{"type": "string"}, "external_userid": map[string]string{"type": "string"}, "include_context": map[string]string{"type": "boolean"}}}},
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

func audienceFor(route Route) string {
	if route.Path == "/mcp" {
		return "mcp"
	}
	return "external_integration"
}

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
