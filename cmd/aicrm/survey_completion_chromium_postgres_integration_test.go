package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
)

// TestPostgreSQLSurveyCompletionChromiumJourney exercises the real Access
// login, frozen questionnaire operations page, V3 Host selector, PostgreSQL
// save/reload and synthetic completion effect. The receiver is a local TLS
// server trusted only by the test-injected client; production composition
// retains its default transport and cannot use this certificate.
func TestPostgreSQLSurveyCompletionChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the Chromium journey")
	}
	fixture := newSurveyCompletionChromiumFixture(t)
	command := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(fixture.script), "survey_completion_chromium_journey.mjs"))
	command.Env = append(os.Environ(), "AICRM_SURVEY_BROWSER_URL="+fixture.server.URL, "AICRM_SURVEY_BROWSER_USERNAME=survey-browser-owner", "AICRM_SURVEY_BROWSER_PASSWORD=survey-browser-owner-password", "AICRM_SURVEY_BROWSER_QUESTIONNAIRE_ID="+strconv.FormatInt(fixture.questionnaireID, 10), "AICRM_SURVEY_BROWSER_TARGET=survey.browser.target")
	output, err := command.CombinedOutput()
	if strings.Contains(string(output), "survey_completion_chromium: SKIP_DEVTOOLS") && runtime.GOOS == "darwin" {
		t.Skip("local Chromium DevTools is unavailable; Linux CI runs the required journey")
	}
	if err != nil || !strings.Contains(string(output), "survey_completion_chromium: PASS") {
		t.Fatalf("survey Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}
	deadline := time.Now().Add(12 * time.Second)
	for fixture.receiverCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if fixture.receiverCalls.Load() != 1 {
		t.Fatalf("controlled receiver calls=%d", fixture.receiverCalls.Load())
	}
	var ref string
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT external_push_configuration_ref FROM survey_operation_configurations WHERE questionnaire_id=$1`, fixture.questionnaireID).Scan(&ref); err != nil || ref != "survey.browser.target" {
		t.Fatalf("saved target ref=%q err=%v", ref, err)
	}
}

type surveyCompletionChromiumFixture struct {
	ctx             context.Context
	application     *composedApplication
	server          *httptest.Server
	questionnaireID int64
	receiverCalls   atomic.Int64
	script          string
}

func newSurveyCompletionChromiumFixture(t *testing.T) *surveyCompletionChromiumFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	t.Chdir(root)
	prepareProductExternalPushChromiumArtifacts(t, root)
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	t.Cleanup(cleanup)
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal(err)
	}
	fixture := &surveyCompletionChromiumFixture{ctx: ctx, script: source}
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-AICRM-Signature") == "" || r.Header.Get("X-AICRM-Event-Id") == "" {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		fixture.receiverCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(receiver.Close)
	targets, err := json.Marshal(map[string]any{"survey.browser.target": map[string]any{"endpoint": receiver.URL, "signing_key": base64.RawStdEncoding.EncodeToString(key[:]), "client_id": "survey-browser", "version": "v1", "identity_kind": "unionid", "identity_scope": "wechat-open-platform:browser", "day": 30, "frequency": 1, "expires_at_ts": 2147483647}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	origin := "https://" + server.Listener.Addr().String()
	application, err := composeWithWeComClientFactoryAndSurveyCompletionHTTPClient(ctx, platformconfig.Runtime{Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: origin, ReleaseSHA: "survey-completion-chromium", WorkerOwner: "survey-completion-chromium", WorkerLimit: 1, GroupOps: platformconfig.GroupOps{WebhookSecret: "survey-browser-webhook-secret"}, Survey: platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(key[:]), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(key[:]), CompletionProviderEnabled: true, CompletionTargetsJSON: string(targets)}, Effects: platformconfig.Effects{ProviderEnabled: true}, Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "survey-browser-owner", Password: "survey-browser-owner-password", DisplayName: "Survey Browser Owner"}}, wecomadapter.New, receiver.Client())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "survey-browser-owner", Password: "survey-browser-owner-password", DisplayName: "Survey Browser Owner"}); err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = application.handler
	server.StartTLS()
	t.Cleanup(server.Close)
	workerCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- application.effectsRuntime.Run(workerCtx) }()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("worker: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("worker did not stop")
		}
	})
	session, csrf := adminAccessLogin(t, application.handler, "survey-browser-owner", "survey-browser-owner-password")
	body := `{"name":"Survey browser","title":"Survey browser","description":"","answer_display_mode":"all_in_one","assessment_enabled":false,"assessment_config":{},"slug":"survey-browser","questions":[{"type":"single_choice","title":"Ready?","required":true,"sort_order":0,"validation":{"max_selections":1},"options":[{"option_text":"Yes","score":0,"tag_codes":[],"is_other":false,"sort_order":0},{"option_text":"No","score":0,"tag_codes":[],"is_other":false,"sort_order":1}]}],"score_rules":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/questionnaires", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "survey-browser-create-0001")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	req.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("create questionnaire status=%d body=%s", response.Code, response.Body.String())
	}
	var created struct {
		Questionnaire struct {
			ID int64 `json:"id"`
		} `json:"questionnaire"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &created); err != nil || created.Questionnaire.ID < 1 {
		t.Fatalf("create response id=%d err=%v", created.Questionnaire.ID, err)
	}
	fixture.application, fixture.server, fixture.questionnaireID = application, server, created.Questionnaire.ID
	return fixture
}
