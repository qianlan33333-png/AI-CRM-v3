package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
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
type testProjections struct{}

func (testProjections) ListReleaseProjections(context.Context) ([]map[string]any, error) {
	return []map[string]any{}, nil
}
func (testProjections) ListDiagnosticSnapshots(context.Context) ([]map[string]any, error) {
	return []map[string]any{}, nil
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
