package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveymodule "github.com/qianlan33333-png/AI-CRM-v3/internal/survey"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/http"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	surveyprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/provider"
	surveysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
	surveystore "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/store"
)

// TestPostgreSQLPublicSurveyPresentationChromiumJourney drives the actual
// public Survey Owner through /q/{slug}. It obtains the existing signed
// Survey OAuth session through the real OAuth service before Chromium starts;
// no browser-only identity shortcut or Provider call is introduced here.
func TestPostgreSQLPublicSurveyPresentationChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	fixture := newPublicSurveyPresentationChromiumFixture(t)
	command := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(fixture.script), "public_survey_chromium_journey.mjs"))
	command.Env = append(os.Environ(),
		"AICRM_PUBLIC_SURVEY_BROWSER_URL="+fixture.server.URL,
		"AICRM_PUBLIC_SURVEY_BROWSER_SESSION="+fixture.session,
		"AICRM_PUBLIC_SURVEY_BROWSER_SUCCESS_SLUG="+fixture.success.Slug,
		"AICRM_PUBLIC_SURVEY_BROWSER_FAILURE_SLUG="+fixture.failure.Slug,
		"AICRM_PUBLIC_SURVEY_SCREENSHOT_DIR="+fixture.screenshots,
	)
	output, err := command.CombinedOutput()
	if strings.Contains(string(output), "public_survey_chromium: SKIP_DEVTOOLS") && runtime.GOOS == "darwin" {
		t.Skip("local Chromium DevTools is unavailable; Linux CI runs the required journey")
	}
	if err != nil || !strings.Contains(string(output), "public_survey_chromium: PASS") {
		t.Fatalf("public Survey Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}
	var browser struct {
		SubmissionID         int64 `json:"submission_id"`
		RecoverySubmissionID int64 `json:"recovery_submission_id"`
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), `{"submission_id":`) {
			err = json.Unmarshal([]byte(line), &browser)
			break
		}
	}
	if err != nil || browser.SubmissionID < 1 || browser.RecoverySubmissionID < 1 {
		t.Fatalf("public Survey browser result is incomplete: success=%d recovery=%d err=%v", browser.SubmissionID, browser.RecoverySubmissionID, err)
	}
	stored, err := fixture.submissions.GetSubmission(fixture.ctx, surveyport.ID(browser.SubmissionID))
	if err != nil || stored.QuestionnaireID != fixture.success.ID || stored.DefinitionVersion != fixture.success.DefinitionVersion || len(stored.Answers) != 1 || len(stored.Answers[0].SelectedOptions) != 1 {
		t.Fatalf("Survey Owner success readback id=%d questionnaire=%d version=%d answers=%d err=%v", browser.SubmissionID, stored.QuestionnaireID, stored.DefinitionVersion, len(stored.Answers), err)
	}
	recovered, err := fixture.submissions.GetSubmission(fixture.ctx, surveyport.ID(browser.RecoverySubmissionID))
	if err != nil || recovered.QuestionnaireID != fixture.failure.ID || recovered.DefinitionVersion != fixture.failure.DefinitionVersion || len(recovered.Answers) != 1 || len(recovered.Answers[0].SelectedOptions) != 1 {
		t.Fatalf("Survey Owner recovery readback id=%d questionnaire=%d version=%d answers=%d err=%v", browser.RecoverySubmissionID, recovered.QuestionnaireID, recovered.DefinitionVersion, len(recovered.Answers), err)
	}
	var failureWrites int
	if err = fixture.native.QueryRow(fixture.ctx, `SELECT count(*) FROM survey_submissions WHERE questionnaire_id=$1`, fixture.failure.ID).Scan(&failureWrites); err != nil || failureWrites != 1 {
		t.Fatalf("controlled failure then recovery writes=%d err=%v", failureWrites, err)
	}
	for _, page := range []string{"auth", "oauth-error", "answer", "failure", "result"} {
		for _, width := range []string{"375", "390", "430"} {
			name := "public-survey-" + page + "-" + width + ".png"
			info, statErr := os.Stat(filepath.Join(fixture.screenshots, name))
			if statErr != nil || info.Size() < 512 {
				t.Fatalf("public Survey screenshot=%s exists=%t size=%d", name, statErr == nil, func() int64 {
					if info == nil {
						return 0
					}
					return info.Size()
				}())
			}
		}
	}
}

type publicSurveyPresentationChromiumFixture struct {
	ctx         context.Context
	native      *pgxpool.Pool
	submissions *surveyapp.SubmissionService
	server      *httptest.Server
	success     surveyport.Questionnaire
	failure     surveyport.Questionnaire
	session     string
	script      string
	screenshots string
}

func newPublicSurveyPresentationChromiumFixture(t *testing.T) *publicSurveyPresentationChromiumFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	t.Chdir(root)
	preparePublicSurveyPresentationChromiumArtifacts(t, root)
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	t.Cleanup(cleanup)
	native, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(native.Close)
	actorID := surveyJourneyGovernedActor(t, ctx, native, "public-survey-browser", "Public Survey Browser")
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wrapper.Close)
	uow, err := platformpostgres.NewUnitOfWork(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := surveysecure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	repository, err := surveystore.NewPostgreSQL(native, uow, cipher)
	if err != nil {
		t.Fatal(err)
	}
	definitions := surveyapp.NewService(uow, repository)
	submissions := surveyapp.NewSubmissionService(uow, repository, cipher)
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	projection := customerstore.NewPostgreSQL()
	if err = submissions.BindDeclaredPhone(oneID, projection); err != nil {
		t.Fatal(err)
	}
	if err = submissions.BindCustomerTimeline(projection); err != nil {
		t.Fatal(err)
	}
	oauthStore := &surveyJourneyOAuthStore{OAuthStore: repository}

	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	origin := "https://" + server.Listener.Addr().String()
	oauthProvider, err := surveyprovider.NewWeChatOAuth(true, "public-survey-browser-app", "public-survey-browser-secret", "public-survey-browser-platform", origin+"/api/h5/surveys/oauth/callback", "snsapi_userinfo")
	if err != nil {
		t.Fatal(err)
	}
	oauth := surveyapp.NewOAuthService(uow, oauthStore, oauthProvider, oneID)
	api, err := surveyhttp.NewHandler(definitions, submissions, surveyJourneySecurity{actorID: actorID}, oauth)
	if err != nil {
		t.Fatal(err)
	}
	success := publicSurveyPresentationDefinition("public-survey-ui", surveyport.DisplayAllInOne, "公开问卷体验", "请完成以下必答题后提交。")
	success, err = definitions.Create(ctx, surveyport.CreateCommand{Questionnaire: success, ActorID: actorID, IdempotencyKey: "public-survey-ui-create-0001"})
	if err != nil {
		t.Fatal(err)
	}
	success, err = definitions.Publish(ctx, success.ID, success.Version, actorID, "public-survey-ui-publish-0001")
	if err != nil {
		t.Fatal(err)
	}
	failure := publicSurveyPresentationDefinition("public-survey-ui-retry", surveyport.DisplayOneByOne, "逐题问卷体验", "此页面验证提交失败后仍可保留答案。")
	failure, err = definitions.Create(ctx, surveyport.CreateCommand{Questionnaire: failure, ActorID: actorID, IdempotencyKey: "public-survey-ui-retry-create-0001"})
	if err != nil {
		t.Fatal(err)
	}
	failure, err = definitions.Publish(ctx, failure.ID, failure.Version, actorID, "public-survey-ui-retry-publish-0001")
	if err != nil {
		t.Fatal(err)
	}

	weChat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/sns/oauth2/access_token":
			if r.Method != http.MethodGet || r.URL.Query().Get("code") != "public-survey-browser-code" {
				http.Error(w, "unexpected OAuth exchange", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"public-survey-browser-token","openid":"public-survey-browser-openid","scope":"snsapi_userinfo"}`))
		case "/sns/userinfo":
			if r.Method != http.MethodGet || r.URL.Query().Get("access_token") != "public-survey-browser-token" {
				http.Error(w, "unexpected OAuth userinfo", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"openid":"public-survey-browser-openid","unionid":"public-survey-browser-unionid"}`))
		default:
			http.Error(w, "unexpected OAuth path", http.StatusBadRequest)
		}
	}))
	t.Cleanup(weChat.Close)
	weChatURL, err := url.Parse(weChat.URL)
	if err != nil {
		t.Fatal(err)
	}
	originalTransport := http.DefaultTransport
	transport := &surveyOAuthAllowlistTransport{base: originalTransport, target: weChatURL}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	start := surveyJourneyServe(t, api, http.MethodGet, "/api/h5/surveys/oauth/start?slug="+success.Slug, nil, "", nil, "MicroMessenger Public Survey Browser")
	if start.Code != http.StatusSeeOther {
		t.Fatalf("Survey OAuth start status=%d", start.Code)
	}
	state := surveyFinalJourneyLocation(t, start).Query().Get("state")
	if state == "" {
		t.Fatal("Survey OAuth start omitted state")
	}
	callback := surveyJourneyServe(t, api, http.MethodGet, "/api/h5/surveys/oauth/callback?state="+url.QueryEscape(state)+"&code=public-survey-browser-code", nil, "", nil, "")
	if callback.Code != http.StatusSeeOther || callback.Header().Get("Location") != "/h5/all.html?slug="+success.Slug || transport.calls != 2 {
		t.Fatalf("Survey OAuth callback status=%d redirect=%q calls=%d", callback.Code, callback.Header().Get("Location"), transport.calls)
	}
	cookies := callback.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-aicrm_survey_identity" || len(cookies[0].Value) != 43 {
		t.Fatal("Survey OAuth callback did not issue one valid session cookie")
	}

	publicUI := surveymodule.NewModuleRegistration().PublicUIBinding(filepath.Join(root, "web", "dist"))
	mux := http.NewServeMux()
	mux.Handle("/h5/", publicUI)
	mux.Handle("/survey-assets/", publicUI)
	mux.Handle("/q/", api)
	mux.Handle("/api/h5/surveys/oauth/", api)
	mux.Handle("/api/h5/surveys/session", api)
	mux.Handle("/api/public/survey-submission-results/query", api)
	var controlledFailureAttempts atomic.Int32
	mux.Handle("/api/public/questionnaires/"+failure.Slug+"/submissions", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This local transport failure is reachable only after the browser has
		// entered through the Owner's authenticated /q route with its existing
		// session cookie. It neither invokes a Provider nor calls the Owner, so
		// the UI can prove draft preservation distinctly from the successful
		// PostgreSQL Owner submission above.
		if cookie, cookieErr := r.Cookie("__Host-aicrm_survey_identity"); cookieErr != nil || len(cookie.Value) != 43 {
			api.ServeHTTP(w, r)
			return
		}
		if controlledFailureAttempts.Add(1) != 1 {
			// The same client submission key now reaches the real Owner. Its
			// receipt and the PostgreSQL readback prove recovery did not create
			// a duplicate submission after the transport failure.
			api.ServeHTTP(w, r)
			return
		}
		// Keep the first request pending long enough for the one-by-one
		// template's structural submitting feedback to render in Chromium.
		select {
		case <-time.After(180 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"survey_temporarily_unavailable"}`))
	}))
	mux.Handle("/api/public/questionnaires/", api)
	server.Config.Handler = mux
	server.StartTLS()
	t.Cleanup(server.Close)

	screenshots := t.TempDir()
	if configured := platformconfig.PublicSurveyScreenshotDirectory(); configured != "" {
		if !filepath.IsAbs(configured) {
			t.Fatal("AICRM_PUBLIC_SURVEY_SCREENSHOT_DIR must be absolute")
		}
		if err = os.MkdirAll(configured, 0o700); err != nil {
			t.Fatal(err)
		}
		screenshots = configured
	}
	return &publicSurveyPresentationChromiumFixture{ctx: ctx, native: native, submissions: submissions, server: server, success: success, failure: failure, session: cookies[0].Value, script: source, screenshots: screenshots}
}

func publicSurveyPresentationDefinition(slug string, mode surveyport.AnswerDisplayMode, title, description string) surveyport.Questionnaire {
	maximum := 1
	questions := []surveyport.Question{{
		Type: surveyport.QuestionSingleChoice, Title: "你希望优先改善哪一项？", Required: true, SortOrder: 0,
		Validation: surveyport.Validation{MaximumSelections: &maximum},
		Options:    []surveyport.Option{{Text: "客户转化", TagCodes: []string{}, SortOrder: 0}, {Text: "内容运营", TagCodes: []string{}, SortOrder: 1}},
	}}
	if mode == surveyport.DisplayOneByOne {
		questions = append(questions, surveyport.Question{
			Type: surveyport.QuestionTextarea, Title: "补充你的目标", Required: false, SortOrder: 1,
			Validation: surveyport.Validation{}, Placeholder: "可选填写", Options: []surveyport.Option{},
		})
	}
	return surveyport.Questionnaire{Name: title, Title: title, Description: description, Mode: surveyport.ModeSurvey, AnswerDisplayMode: mode, AssessmentConfig: json.RawMessage(`{}`), Slug: slug, Status: surveyport.StatusDraft, Questions: questions}
}

func preparePublicSurveyPresentationChromiumArtifacts(t *testing.T, root string) {
	t.Helper()
	manifest, err := os.ReadFile(filepath.Join(root, "web", "dist", "asset-manifest.json"))
	if err != nil {
		t.Fatalf("public Survey Chromium requires built asset manifest: %v", err)
	}
	for _, entry := range []string{"h5", "h5AuthHost", "surveyPublicHost", "surveyPublicStyles", "sharedVisualTokens"} {
		if !strings.Contains(string(manifest), `"`+entry+`"`) {
			t.Fatalf("public Survey Chromium manifest lacks %s", entry)
		}
	}
}
