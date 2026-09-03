package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	h, err := NewHandler(settings, wizard, testProjections{}, testSecurity{principal: p})
	if err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRecorder()
	h.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/admin/setup-wizard", nil))
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
	request := httptest.NewRequest(http.MethodPost, "/api/admin/setup-wizard", strings.NewReader(body))
	request.Header.Set("Idempotency-Key", "wizard-1")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("post=%d %s", response.Code, response.Body.String())
	}
	if wizard.save.Actor != "7" || wizard.save.IdempotencyKey != "wizard-1" || wizard.save.WeComCorpID != "corp" {
		t.Fatalf("save=%#v", wizard.save)
	}
	bad := httptest.NewRequest(http.MethodPost, "/api/admin/setup-wizard", strings.NewReader(body))
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
	h, err := NewHandler(settings, &testWizard{}, testProjections{}, testSecurity{principal: p})
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
	h, err := NewHandler(&testSettings{}, &testWizard{}, projections, testSecurity{principal: principal})
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
	h, err := NewHandler(&testSettings{}, &testWizard{}, testProjections{}, testSecurity{principal: principal})
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
	if len(body.Capabilities) != 2 || body.Capabilities["local_projection"]["enabled"] != true || body.Capabilities["provider_write"]["enabled"] != false {
		t.Fatalf("capabilities=%#v", body.Capabilities)
	}
}

func TestHistoricalProjectionStoreFailureIsNotAnEmptySuccess(t *testing.T) {
	principal := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	h, err := NewHandler(&testSettings{}, &testWizard{}, failingProjections{}, testSecurity{principal: principal})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/admin/config/releases", "/api/admin/config/diagnostics"} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), `"releases":[]`) || strings.Contains(response.Body.String(), `"diagnostics":[]`) {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestEmptyHistoricalProjectionIsFailClosed(t *testing.T) {
	principal := accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	h, err := NewHandler(&testSettings{}, &testWizard{}, testProjections{}, testSecurity{principal: principal})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/admin/config/releases", "/api/admin/config/diagnostics"} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

type failingProjections struct{}

func (failingProjections) ListReleaseProjections(context.Context) ([]configport.ReleaseProjection, error) {
	return nil, errors.New("projection store unavailable")
}

func (failingProjections) ListDiagnosticSnapshots(context.Context) ([]configport.DiagnosticProjection, error) {
	return nil, errors.New("projection store unavailable")
}
