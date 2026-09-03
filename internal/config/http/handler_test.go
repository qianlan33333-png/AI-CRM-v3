package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
)

type testSecurity struct {
	principal accessdomain.Principal
	csrf      error
}

func (s testSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, nil
}
func (s testSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, s.csrf
}

type testSettings struct{ save configapp.SaveSettingsInput }

func (s *testSettings) List(context.Context, configapp.SettingsListInput) (configapp.SettingsProjection, error) {
	return configapp.SettingsProjection{Rows: []any{}}, nil
}
func (s *testSettings) Save(_ context.Context, input configapp.SaveSettingsInput) error {
	s.save = input
	return nil
}

type testConfig struct {
	settings map[configport.Key]configport.Setting
	commands []configport.SetCommand
}

func (s *testConfig) Get(_ context.Context, key configport.Key) (configport.Setting, error) {
	if value, ok := s.settings[key]; ok {
		return value, nil
	}
	return configport.Setting{}, configport.ErrSettingNotFound
}
func (s *testConfig) Set(_ context.Context, command configport.SetCommand) (configport.Setting, error) {
	s.commands = append(s.commands, command)
	if s.settings == nil {
		s.settings = map[configport.Key]configport.Setting{}
	}
	setting := configport.Setting{Key: command.Key, Value: append([]byte(nil), command.Value...), UpdatedBy: command.Actor}
	s.settings[command.Key] = setting
	return setting, nil
}

func newTestConfig() *testConfig {
	return &testConfig{settings: map[configport.Key]configport.Setting{}}
}

func adminSessionRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.AddCookie(&http.Cookie{Name: adminSessionCookie, Value: "test-admin-session"})
	return request
}

type testWizard struct {
	save configapp.SetupWizardSaveInput
}
type testProjections struct {
	releases    []configport.ReleaseProjection
	diagnostics []configport.DiagnosticProjection
}

func (projections testProjections) ListReleaseProjections(context.Context) ([]configport.ReleaseProjection, error) {
	return projections.releases, nil
}
func (projections testProjections) ListDiagnosticSnapshots(context.Context) ([]configport.DiagnosticProjection, error) {
	return projections.diagnostics, nil
}

func (s *testWizard) Get(context.Context) (configapp.SetupWizardSnapshot, error) {
	return configapp.SetupWizardSnapshot{ExpectedDigest: strings.Repeat("a", 64)}, nil
}
func (s *testWizard) Save(_ context.Context, input configapp.SetupWizardSaveInput) (configapp.SetupWizardSaveResult, error) {
	s.save = input
	return configapp.SetupWizardSaveResult{Snapshot: configapp.SetupWizardSnapshot{ExpectedDigest: strings.Repeat("b", 64)}}, nil
}

func TestSetupWizardRequiresSessionCSRFActionTokenAndIdempotency(t *testing.T) {
	p := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	settings, wizard := &testSettings{}, &testWizard{}
	h, err := NewHandler(settings, wizard, newTestConfig(), testProjections{}, testSecurity{principal: p})
	if err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRecorder()
	h.ServeHTTP(get, adminSessionRequest(http.MethodGet, "/api/admin/setup-wizard", nil))
	if get.Code != 200 {
		t.Fatalf("get=%d", get.Code)
	}
	var read map[string]any
	if err = json.Unmarshal(get.Body.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	token, _ := read["admin_action_token"].(string)
	if len(token) != 43 {
		t.Fatalf("token=%q", token)
	}
	body := `{"wecom.corp_id":"corp","wecom.agent_id":7,"wecom.secret":"","wecom.callback_token":"","wecom.callback_aes_key":"","ai.api_key":"","expected_digest":"` + strings.Repeat("a", 64) + `","admin_action_token":"` + token + `"}`
	request := adminSessionRequest(http.MethodPost, "/api/admin/setup-wizard", strings.NewReader(body))
	request.Header.Set("Idempotency-Key", "wizard-1")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("post=%d %s", response.Code, response.Body.String())
	}
	if wizard.save.Actor != "7" || wizard.save.IdempotencyKey != "wizard-1" || wizard.save.WeComCorpID != "corp" {
		t.Fatalf("save=%#v", wizard.save)
	}
	bad := adminSessionRequest(http.MethodPost, "/api/admin/setup-wizard", strings.NewReader(body))
	bad.Header.Set("Idempotency-Key", " ")
	response = httptest.NewRecorder()
	h.ServeHTTP(response, bad)
	if response.Code != 400 {
		t.Fatalf("bad idempotency=%d", response.Code)
	}
}

func TestAppSettingsRejectsBodyTokenMismatchBeforeSave(t *testing.T) {
	p := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	settings := &testSettings{}
	h, err := NewHandler(settings, &testWizard{}, newTestConfig(), testProjections{}, testSecurity{principal: p})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"settings":{"wecom.corp_id":"corp"},"confirm":true,"admin_action_token":"bad"}`
	r := httptest.NewRequest(http.MethodPut, "/api/admin/config/app-settings", strings.NewReader(body))
	r.Header.Set("Idempotency-Key", "settings-1")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, r)
	if response.Code != 400 || settings.save.Actor != "" {
		t.Fatalf("status/save=%d/%#v", response.Code, settings.save)
	}
}

func TestHistoricalProjectionsExposeOnlyTypedSafeFields(t *testing.T) {
	principal := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	projections := testProjections{
		releases:    []configport.ReleaseProjection{{ID: 9, ReleaseSHA: "release-sha", Status: "observed"}},
		diagnostics: []configport.DiagnosticProjection{{ID: 3, Key: "runtime", Status: "ok"}},
	}
	h, err := NewHandler(&testSettings{}, &testWizard{}, newTestConfig(), projections, testSecurity{principal: principal})
	if err != nil {
		t.Fatal(err)
	}
	releaseResponse := httptest.NewRecorder()
	h.ServeHTTP(releaseResponse, httptest.NewRequest(http.MethodGet, "/api/admin/config/releases", nil))
	if releaseResponse.Code != http.StatusOK || strings.Contains(releaseResponse.Body.String(), "details") || strings.Contains(releaseResponse.Body.String(), `"items"`) {
		t.Fatalf("releases status=%d body=%s", releaseResponse.Code, releaseResponse.Body.String())
	}
	var releaseBody struct {
		Releases []struct {
			ID       int64  `json:"id"`
			State    string `json:"state"`
			Checksum string `json:"checksum"`
		} `json:"releases"`
	}
	if err = json.Unmarshal(releaseResponse.Body.Bytes(), &releaseBody); err != nil {
		t.Fatal(err)
	}
	if len(releaseBody.Releases) != 1 || releaseBody.Releases[0].ID != 9 || releaseBody.Releases[0].State != "observed" || releaseBody.Releases[0].Checksum != "release-sha" {
		t.Fatalf("release projection=%#v", releaseBody.Releases)
	}
	diagnosticResponse := httptest.NewRecorder()
	h.ServeHTTP(diagnosticResponse, httptest.NewRequest(http.MethodGet, "/api/admin/config/diagnostics", nil))
	if diagnosticResponse.Code != http.StatusOK || strings.Contains(diagnosticResponse.Body.String(), "details") || strings.Contains(diagnosticResponse.Body.String(), `"items"`) {
		t.Fatalf("diagnostics status=%d body=%s", diagnosticResponse.Code, diagnosticResponse.Body.String())
	}
	var diagnosticBody struct {
		Diagnostics []struct {
			ID     int64  `json:"id"`
			Key    string `json:"key"`
			Status string `json:"status"`
		} `json:"diagnostics"`
	}
	if err = json.Unmarshal(diagnosticResponse.Body.Bytes(), &diagnosticBody); err != nil {
		t.Fatal(err)
	}
	if len(diagnosticBody.Diagnostics) != 1 || diagnosticBody.Diagnostics[0].ID != 3 || diagnosticBody.Diagnostics[0].Key != "runtime" || diagnosticBody.Diagnostics[0].Status != "ok" {
		t.Fatalf("diagnostic projection=%#v", diagnosticBody.Diagnostics)
	}
}

func TestPushCapabilitiesExposeDonorReadOnlyProjectionShape(t *testing.T) {
	principal := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	h, err := NewHandler(&testSettings{}, &testWizard{}, newTestConfig(), testProjections{diagnostics: []configport.DiagnosticProjection{{ID: 3, Key: "runtime", Status: "ok"}}}, testSecurity{principal: principal})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/config/push-capabilities", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"items":[]`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Capabilities map[string]map[string]any `json:"capabilities"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Capabilities) != 3 || body.Capabilities["local_projection"]["enabled"] != true || body.Capabilities["runtime_diagnostics"]["enabled"] != true || body.Capabilities["provider_write"]["enabled"] != false {
		t.Fatalf("capabilities=%#v", body.Capabilities)
	}
}

func TestHistoricalProjectionStoreFailureIsNotAnEmptySuccess(t *testing.T) {
	principal := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	h, err := NewHandler(&testSettings{}, &testWizard{}, newTestConfig(), failingProjections{}, testSecurity{principal: principal})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/admin/config/push-capabilities", "/api/admin/config/releases", "/api/admin/config/diagnostics"} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), `"releases":[]`) || strings.Contains(response.Body.String(), `"diagnostics":[]`) {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestEmptyHistoricalProjectionIsFailClosed(t *testing.T) {
	principal := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	h, err := NewHandler(&testSettings{}, &testWizard{}, newTestConfig(), testProjections{}, testSecurity{principal: principal})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/admin/config/push-capabilities", "/api/admin/config/releases", "/api/admin/config/diagnostics"} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestCategoryToggleAndCheckUseAuthenticatedCSRFActionAndLocalConfigReceipt(t *testing.T) {
	p := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	config := newTestConfig()
	projections := testProjections{diagnostics: []configport.DiagnosticProjection{{ID: 3, Key: "runtime", Status: "ok"}}}
	h, err := NewHandler(&testSettings{}, &testWizard{}, config, projections, testSecurity{principal: p})
	if err != nil {
		t.Fatal(err)
	}
	detail := httptest.NewRecorder()
	h.ServeHTTP(detail, adminSessionRequest(http.MethodGet, "/api/admin/config/categories/runtime-diagnostics", nil))
	if detail.Code != http.StatusOK {
		t.Fatalf("detail=%d %s", detail.Code, detail.Body.String())
	}
	var read struct {
		Tokens map[string]string `json:"admin_action_tokens"`
	}
	if err = json.Unmarshal(detail.Body.Bytes(), &read); err != nil || len(read.Tokens["enabled"]) != 43 || len(read.Tokens["check"]) != 43 {
		t.Fatalf("detail tokens=%q err=%v", read.Tokens, err)
	}
	write := adminSessionRequest(http.MethodPut, "/api/admin/config/categories/runtime-diagnostics/enabled", strings.NewReader(`{"enabled":false,"admin_action_token":"`+read.Tokens["enabled"]+`"}`))
	write.Header.Set("Idempotency-Key", "toggle-1")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, write)
	if response.Code != http.StatusOK || len(config.commands) != 1 || config.commands[0].Key != configport.AdminDiagnosticsEnabled || string(config.commands[0].Value) != "false" || config.commands[0].RequestID == "toggle-1" {
		t.Fatalf("toggle status/command=%d/%#v", response.Code, config.commands)
	}
	check := adminSessionRequest(http.MethodPost, "/api/admin/config/categories/runtime-diagnostics/check", strings.NewReader(`{"admin_action_token":"`+read.Tokens["check"]+`"}`))
	response = httptest.NewRecorder()
	h.ServeHTTP(response, check)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "已停用") {
		t.Fatalf("check=%d %s", response.Code, response.Body.String())
	}
	denied, err := NewHandler(&testSettings{}, &testWizard{}, newTestConfig(), projections, testSecurity{principal: p, csrf: errors.New("csrf")})
	if err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	denied.ServeHTTP(response, write)
	if response.Code != http.StatusForbidden {
		t.Fatalf("csrf status=%d", response.Code)
	}
}

func TestCategorySavePersistsReadonlyFrozenDetailInsteadOfChecking(t *testing.T) {
	p := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	config := newTestConfig()
	projections := testProjections{diagnostics: []configport.DiagnosticProjection{{ID: 3, Key: "runtime", Status: "ok"}}}
	h, err := NewHandler(&testSettings{}, &testWizard{}, config, projections, testSecurity{principal: p})
	if err != nil {
		t.Fatal(err)
	}
	detail := httptest.NewRecorder()
	h.ServeHTTP(detail, adminSessionRequest(http.MethodGet, "/api/admin/config/categories/runtime-diagnostics", nil))
	var read struct {
		Tokens map[string]string `json:"admin_action_tokens"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &read); err != nil || len(read.Tokens["settings"]) != 43 {
		t.Fatalf("detail=%s token=%q err=%v", detail.Body.String(), read.Tokens["settings"], err)
	}
	save := adminSessionRequest(http.MethodPut, "/api/admin/config/categories/runtime-diagnostics/settings", strings.NewReader(`{"settings":{},"admin_action_token":"`+read.Tokens["settings"]+`"}`))
	save.Header.Set("Idempotency-Key", "category-save-1")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, save)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"saved":true`) || len(config.commands) != 1 || config.commands[0].Key != configport.AdminDiagnosticsEnabled || string(config.commands[0].Value) != "true" {
		t.Fatalf("save=%d %s commands=%#v", response.Code, response.Body.String(), config.commands)
	}
}

func TestActionTokenIsSessionPathExpiryAndOneTimeBound(t *testing.T) {
	p := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	h, err := NewHandler(&testSettings{}, &testWizard{}, newTestConfig(), testProjections{}, testSecurity{principal: p})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return now }
	read := adminSessionRequest(http.MethodGet, "/api/admin/config/app-settings", nil)
	token := h.actionToken(read, p, "app-settings", read.URL.Path)
	if len(token) != 43 {
		t.Fatalf("token=%q", token)
	}
	write := adminSessionRequest(http.MethodPut, "/api/admin/config/app-settings", nil)
	if !h.validActionToken(write, p, "app-settings", write.URL.Path, token) {
		t.Fatal("same session/path token rejected")
	}
	if h.validActionToken(write, p, "app-settings", write.URL.Path, token) {
		t.Fatal("one-time token was accepted twice")
	}
	token = h.actionToken(read, p, "app-settings", read.URL.Path)
	otherSession := httptest.NewRequest(http.MethodPut, "/api/admin/config/app-settings", nil)
	otherSession.AddCookie(&http.Cookie{Name: adminSessionCookie, Value: "different-session"})
	if h.validActionToken(otherSession, p, "app-settings", otherSession.URL.Path, token) {
		t.Fatal("token crossed sessions")
	}
	token = h.actionToken(read, p, "app-settings", read.URL.Path)
	otherPath := adminSessionRequest(http.MethodPut, "/api/admin/setup-wizard", nil)
	if h.validActionToken(otherPath, p, "app-settings", otherPath.URL.Path, token) {
		t.Fatal("token crossed paths")
	}
	token = h.actionToken(read, p, "app-settings", read.URL.Path)
	now = now.Add(6 * time.Minute)
	if h.validActionToken(write, p, "app-settings", write.URL.Path, token) {
		t.Fatal("expired token accepted")
	}
}

func TestDisabledApplicationSettingsRejectsTheUnchangedDonorSaveBeforeMutation(t *testing.T) {
	p := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	config := newTestConfig()
	config.settings[configport.AdminAppSettingsEnabled] = configport.Setting{Key: configport.AdminAppSettingsEnabled, Value: []byte("false")}
	settings := &testSettings{}
	h, err := NewHandler(settings, &testWizard{}, config, testProjections{}, testSecurity{principal: p})
	if err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRecorder()
	h.ServeHTTP(get, adminSessionRequest(http.MethodGet, "/api/admin/config/app-settings", nil))
	var projection map[string]any
	if err = json.Unmarshal(get.Body.Bytes(), &projection); err != nil {
		t.Fatal(err)
	}
	token, _ := projection["admin_action_token"].(string)
	request := adminSessionRequest(http.MethodPut, "/api/admin/config/app-settings", strings.NewReader(`{"settings":{"wecom.corp_id":"corp"},"confirm":true,"admin_action_token":"`+token+`"}`))
	request.Header.Set("Idempotency-Key", "disabled-save")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || settings.save.Actor != "" || !strings.Contains(response.Body.String(), "config_category_disabled") {
		t.Fatalf("disabled save=%d %s %#v", response.Code, response.Body.String(), settings.save)
	}
}

func TestOpenAPIDownloadIsAuthenticatedAndMatchesTheReleaseSource(t *testing.T) {
	p := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	h, err := NewHandler(&testSettings{}, &testWizard{}, newTestConfig(), testProjections{}, testSecurity{principal: p})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/config/openapi.yaml", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "application/yaml") || !bytes.HasPrefix(response.Body.Bytes(), []byte("openapi:")) {
		t.Fatalf("download=%d %q %q", response.Code, response.Header().Get("Content-Type"), response.Body.Bytes()[:min(20, response.Body.Len())])
	}
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response.Body.Bytes(), source) {
		t.Fatal("embedded OpenAPI differs from the release source")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type failingProjections struct{}

func (failingProjections) ListReleaseProjections(context.Context) ([]configport.ReleaseProjection, error) {
	return nil, errors.New("projection store unavailable")
}

func (failingProjections) ListDiagnosticSnapshots(context.Context) ([]configport.DiagnosticProjection, error) {
	return nil, errors.New("projection store unavailable")
}
