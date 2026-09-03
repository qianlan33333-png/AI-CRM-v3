// Package http exposes only the frozen PR09 local-configuration compatibility
// endpoints.  It has no Provider client and never applies runtime secrets.
package http

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
)

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}

type Handler struct {
	settings    settingsService
	wizard      wizardService
	projections projectionReader
	security    RequestSecurity
	tokenKey    []byte
}

type settingsService interface {
	List(context.Context, configapp.SettingsListInput) (configapp.SettingsProjection, error)
	Save(context.Context, configapp.SaveSettingsInput) error
}
type wizardService interface {
	Get(context.Context) (configapp.SetupWizardSnapshot, error)
	Save(context.Context, configapp.SetupWizardSaveInput) (configapp.SetupWizardSaveResult, error)
}
type projectionReader interface {
	ListReleaseProjections(context.Context) ([]map[string]any, error)
	ListDiagnosticSnapshots(context.Context) ([]map[string]any, error)
}

func NewHandler(settings settingsService, wizard wizardService, projections projectionReader, security RequestSecurity) (*Handler, error) {
	if settings == nil || wizard == nil || projections == nil || security == nil {
		return nil, errors.New("config HTTP dependencies are required")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return &Handler{settings: settings, wizard: wizard, projections: projections, security: security, tokenKey: key}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/admin/setup-wizard" {
		h.setupWizard(w, r)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/config/"), "/")
	switch path {
	case "app-settings":
		h.appSettings(w, r)
	case "setup-wizard":
		h.setupWizard(w, r)
	case "categories":
		h.categories(w, r)
	case "push-capabilities":
		h.pushCapabilities(w, r)
	case "releases":
		h.releases(w, r)
	case "diagnostics":
		h.diagnostics(w, r)
	default:
		if strings.HasPrefix(path, "categories/") {
			h.categoryDetail(w, r, strings.TrimPrefix(path, "categories/"))
			return
		}
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func readRole(p accessdomain.Principal) bool {
	return p.InternalID > 0 && (p.Kind == accessdomain.KindAdmin || p.Kind == accessdomain.KindStaff) && has(p, accessdomain.RoleViewer, accessdomain.RoleAdmin, accessdomain.RoleSuperAdmin)
}
func writeRole(p accessdomain.Principal) bool {
	return readRole(p) && has(p, accessdomain.RoleAdmin, accessdomain.RoleSuperAdmin)
}
func has(p accessdomain.Principal, want ...accessdomain.Role) bool {
	for _, r := range p.Roles {
		for _, x := range want {
			if r == x {
				return true
			}
		}
	}
	return false
}
func (h *Handler) read(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	p, e := h.security.Authenticate(r.Context(), r)
	if e != nil {
		writeError(w, 401, "unauthorized")
		return p, false
	}
	if !readRole(p) {
		writeError(w, 403, "forbidden")
		return p, false
	}
	return p, true
}
func (h *Handler) mutate(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	p, ok := h.read(w, r)
	if !ok {
		return p, false
	}
	if !writeRole(p) {
		writeError(w, 403, "forbidden")
		return p, false
	}
	if _, e := h.security.AuthorizeCSRF(r.Context(), r); e != nil {
		writeError(w, 403, "csrf_required")
		return p, false
	}
	return p, true
}

func (h *Handler) actionToken(p accessdomain.Principal, action string) string {
	mac := hmac.New(sha256.New, h.tokenKey)
	_, _ = mac.Write([]byte(strconv.FormatInt(p.InternalID, 10) + ":" + action))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (h *Handler) validActionToken(p accessdomain.Principal, action, value string) bool {
	return len(value) == 43 && hmac.Equal([]byte(value), []byte(h.actionToken(p, action)))
}
func actionFrom(r *http.Request, body string) string {
	if body != "" {
		return body
	}
	return r.Header.Get("X-Admin-Action-Token")
}

func (h *Handler) appSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		p, ok := h.read(w, r)
		if !ok {
			return
		}
		projection, e := h.settings.List(r.Context(), configapp.SettingsListInput{Search: r.URL.Query().Get("search"), Scope: r.URL.Query().Get("scope")})
		if e != nil {
			writeError(w, 503, "settings_unavailable")
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "config": projection, "source_status": "next_read_model", "fallback_used": false, "admin_action_token": h.actionToken(p, "app-settings")})
	case http.MethodPut:
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		var body struct {
			Settings map[string]json.RawMessage `json:"settings"`
			Confirm  bool                       `json:"confirm"`
			Action   string                     `json:"admin_action_token"`
			Operator json.RawMessage            `json:"operator"`
		}
		if e := decode(r, &body); e != nil {
			writeError(w, 400, "payload_must_be_object")
			return
		}
		if !body.Confirm {
			writeError(w, 400, "confirmation_required")
			return
		}
		if !h.validActionToken(p, "app-settings", actionFrom(r, body.Action)) {
			writeError(w, 400, "invalid_action_token")
			return
		}
		values := make(map[string][]string, len(body.Settings))
		for key, raw := range body.Settings {
			var text string
			if json.Unmarshal(raw, &text) == nil {
				values[key] = []string{text}
				continue
			}
			var number int64
			if json.Unmarshal(raw, &number) == nil {
				values[key] = []string{strconv.FormatInt(number, 10)}
				continue
			}
			writeError(w, 400, "invalid_setting_value")
			return
		}
		key := idempotency(r)
		if key == "" {
			writeError(w, 400, "invalid_idempotency_key")
			return
		}
		if e := h.settings.Save(r.Context(), configapp.SaveSettingsInput{Values: values, Actor: strconv.FormatInt(p.InternalID, 10), RequestID: key}); e != nil {
			writeSettingsError(w, e)
			return
		}
		projection, e := h.settings.List(r.Context(), configapp.SettingsListInput{})
		if e != nil {
			writeError(w, 503, "settings_unavailable")
			return
		}
		changed := make([]map[string]any, 0, len(values))
		for key := range values {
			changed = append(changed, map[string]any{"key": key, "value": values[key][0]})
		}
		writeJSON(w, 200, map[string]any{"ok": true, "changed": changed, "changed_count": len(changed), "config": projection, "source_status": "next_command", "fallback_used": false, "real_external_call_executed": false})
	default:
		method(w, "GET, PUT")
	}
}

func (h *Handler) setupWizard(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		p, ok := h.read(w, r)
		if !ok {
			return
		}
		snapshot, e := h.wizard.Get(r.Context())
		if e != nil {
			writeError(w, 503, "setup_wizard_unavailable")
			return
		}
		writeJSON(w, 200, wizardRead(snapshot, h.actionToken(p, "setup-wizard")))
	case http.MethodPost:
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		var body struct {
			Corp          string `json:"wecom.corp_id"`
			Agent         int64  `json:"wecom.agent_id"`
			Secret        string `json:"wecom.secret"`
			CallbackToken string `json:"wecom.callback_token"`
			CallbackAES   string `json:"wecom.callback_aes_key"`
			AI            string `json:"ai.api_key"`
			Expected      string `json:"expected_digest"`
			Action        string `json:"admin_action_token"`
		}
		if e := decode(r, &body); e != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		if !h.validActionToken(p, "setup-wizard", actionFrom(r, body.Action)) {
			writeError(w, 400, "invalid_action_token")
			return
		}
		key := idempotency(r)
		if key == "" {
			writeError(w, 400, "invalid_idempotency_key")
			return
		}
		result, e := h.wizard.Save(r.Context(), configapp.SetupWizardSaveInput{WeComCorpID: body.Corp, WeComAgentID: body.Agent, WeComSecret: body.Secret, WeComCallbackToken: body.CallbackToken, WeComCallbackAESKey: body.CallbackAES, AIAPIKey: body.AI, ExpectedDigest: body.Expected, Actor: strconv.FormatInt(p.InternalID, 10), IdempotencyKey: key})
		if e != nil {
			writeWizardError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "config": result.Snapshot, "receipt": result.Receipt, "external": false, "local_only": true, "runtime_applied": false})
	default:
		method(w, "GET, POST")
	}
}

func wizardRead(s configapp.SetupWizardSnapshot, token string) map[string]any {
	return map[string]any{"ok": true, "expected_digest": s.ExpectedDigest, "editable": s.Editable, "editable_configured": s.Configured, "masked": s.Masked, "admin_action_token": token, "external": false, "local_only": true, "runtime_applied": false}
}
func (h *Handler) categories(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if _, ok := h.read(w, r); !ok {
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "items": []map[string]any{{"key": "app-settings", "name": "应用设置", "enabled": true, "local_only": true}}, "source_status": "next_read_model"})
}
func (h *Handler) categoryDetail(w http.ResponseWriter, r *http.Request, key string) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if _, ok := h.read(w, r); !ok {
		return
	}
	if key != "app-settings" {
		writeError(w, 404, "not_found")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "key": "app-settings", "name": "应用设置", "enabled": true, "local_only": true, "runtime_applied": false})
}
func (h *Handler) pushCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if _, ok := h.read(w, r); !ok {
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "items": []any{}, "source_status": "next_read_model", "local_only": true})
}
func (h *Handler) releases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if _, ok := h.read(w, r); !ok {
		return
	}
	items, err := h.projections.ListReleaseProjections(r.Context())
	if err != nil {
		writeError(w, 503, "unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "items": items, "source_status": "next_read_model", "local_only": true})
}
func (h *Handler) diagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if _, ok := h.read(w, r); !ok {
		return
	}
	items, err := h.projections.ListDiagnosticSnapshots(r.Context())
	if err != nil {
		writeError(w, 503, "unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "items": items, "source_status": "next_read_model", "local_only": true})
}

func decode(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func idempotency(r *http.Request) string {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return ""
	}
	key := values[0]
	if key == "" || strings.TrimSpace(key) != key || len(key) > 200 {
		return ""
	}
	return key
}
func method(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, 405, "method_not_allowed")
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": code})
}
func writeSettingsError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, configport.ErrIdempotencyConflict):
		writeError(w, 409, "settings_idempotency_conflict")
	case errors.Is(e, configport.ErrSecretSetting):
		writeError(w, 400, "secret_input_forbidden")
	case errors.Is(e, configport.ErrInvalidSetting), errors.Is(e, configapp.ErrInvalidAppSettingsRequest):
		writeError(w, 400, "invalid_setting")
	default:
		writeError(w, 503, "settings_unavailable")
	}
}
func writeWizardError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, configapp.ErrSetupWizardConflict), errors.Is(e, configport.ErrIdempotencyConflict):
		writeError(w, 409, "setup_wizard_conflict")
	case errors.Is(e, configport.ErrSecretSetting):
		writeError(w, 400, "secret_input_forbidden")
	case errors.Is(e, configport.ErrInvalidSetting), errors.Is(e, configapp.ErrInvalidSetupWizardRequest):
		writeError(w, 400, "invalid_setting")
	default:
		writeError(w, 503, "setup_wizard_unavailable")
	}
}
