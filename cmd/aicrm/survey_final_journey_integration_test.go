package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/http"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	surveyprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/provider"
	surveysecure "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
	surveystore "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/store"
)

// TestSurveyOAuthSubmissionResultJourneyPostgreSQL exercises the composition
// boundary with actual Survey and OneID PostgreSQL owners. The OAuth adapter
// itself is real; only the WeChat HTTPS exchange is strictly forwarded to this
// local httptest server, so the test cannot reach any other network endpoint.
func TestSurveyOAuthSubmissionResultJourneyPostgreSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()

	var actorID int64
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name) VALUES('survey-oauth-journey','$argon2id$test','Survey OAuth Journey') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	wrapper, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapper.Close()
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
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	definitions := surveyapp.NewService(uow, repository)
	submissions := surveyapp.NewSubmissionService(uow, repository, cipher)
	projection := customerstore.NewPostgreSQL()
	if err = submissions.BindDeclaredPhone(oneID, projection); err != nil {
		t.Fatal(err)
	}
	if err = submissions.BindCustomerTimeline(projection); err != nil {
		t.Fatal(err)
	}
	oauthProvider, err := surveyprovider.NewWeChatOAuth(true, "survey-journey-app", "survey-journey-secret", "survey-journey-platform", "https://journey.example.test/api/h5/surveys/oauth/callback", "snsapi_userinfo")
	if err != nil {
		t.Fatal(err)
	}
	oauth := surveyapp.NewOAuthService(uow, repository, oauthProvider, oneID)
	handler, err := surveyhttp.NewHandler(definitions, submissions, surveyJourneySecurity{actorID: actorID}, oauth)
	if err != nil {
		t.Fatal(err)
	}

	createBody := []byte(`{"name":"OAuth journey","title":"OAuth journey","description":"","answer_display_mode":"all_in_one","assessment_enabled":false,"assessment_config":{},"slug":"oauth-journey","questions":[{"type":"single_choice","title":"Continue?","required":true,"sort_order":0,"options":[{"option_text":"Yes","score":0,"tag_codes":[],"is_other":false,"sort_order":0},{"option_text":"No","score":0,"tag_codes":[],"is_other":false,"sort_order":1}]}],"score_rules":[]}`)
	created := surveyJourneyServe(t, handler, http.MethodPost, "/api/admin/questionnaires", createBody, "survey-oauth-create-0001", nil, "")
	if created.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var createResult struct {
		Questionnaire surveyport.Questionnaire `json:"questionnaire"`
	}
	mustSurveyJourneyJSON(t, created, &createResult)
	if createResult.Questionnaire.ID < 1 || len(createResult.Questionnaire.Questions) != 1 || len(createResult.Questionnaire.Questions[0].Options) != 2 {
		t.Fatalf("created questionnaire=%+v", createResult.Questionnaire)
	}

	published := surveyJourneyServe(t, handler, http.MethodPost, fmt.Sprintf("/api/admin/questionnaires/%d/public-publish", createResult.Questionnaire.ID), nil, "survey-oauth-publish-0001", nil, "")
	if published.Code != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", published.Code, published.Body.String())
	}
	var publishResult struct {
		Questionnaire surveyport.Questionnaire `json:"questionnaire"`
	}
	mustSurveyJourneyJSON(t, published, &publishResult)
	if publishResult.Questionnaire.Status != surveyport.StatusPublished || publishResult.Questionnaire.DefinitionVersion != 1 {
		t.Fatalf("published questionnaire=%+v", publishResult.Questionnaire)
	}

	start := surveyJourneyServe(t, handler, http.MethodGet, "/api/h5/surveys/oauth/start?slug=oauth-journey", nil, "", nil, "MicroMessenger Survey Journey")
	if start.Code != http.StatusSeeOther {
		t.Fatalf("OAuth start status=%d body=%s", start.Code, start.Body.String())
	}
	authorization := surveyFinalJourneyLocation(t, start)
	state := authorization.Query().Get("state")
	if authorization.Host != "open.weixin.qq.com" || state == "" {
		t.Fatalf("OAuth authorization=%s", authorization.String())
	}

	weChat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/sns/oauth2/access_token" || r.URL.Query().Get("grant_type") != "authorization_code" || r.URL.Query().Get("code") != "journey-code" {
			http.Error(w, "unexpected OAuth exchange", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openid":"journey-openid","unionid":"journey-unionid","scope":"snsapi_userinfo"}`))
	}))
	defer weChat.Close()
	weChatURL, err := url.Parse(weChat.URL)
	if err != nil {
		t.Fatal(err)
	}
	originalTransport := http.DefaultTransport
	transport := &surveyOAuthAllowlistTransport{base: originalTransport, target: weChatURL}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	callback := surveyJourneyServe(t, handler, http.MethodGet, "/api/h5/surveys/oauth/callback?state="+url.QueryEscape(state)+"&code=journey-code", nil, "", nil, "")
	if callback.Code != http.StatusSeeOther || transport.calls != 1 {
		t.Fatalf("OAuth callback status=%d calls=%d body=%s", callback.Code, transport.calls, callback.Body.String())
	}
	cookies := callback.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-aicrm_survey_identity" || !cookies[0].HttpOnly {
		t.Fatalf("OAuth callback cookie=%+v", cookies)
	}

	question := publishResult.Questionnaire.Questions[0]
	answer := map[string]any{"version": publishResult.Questionnaire.DefinitionVersion, "submission_key": "survey-oauth-submission-0001", "answers": []map[string]any{{"question_id": question.ID, "option_ids": []surveyport.ID{question.Options[0].ID}}}}
	submitBody, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	submitted := surveyJourneyServe(t, handler, http.MethodPost, "/api/public/questionnaires/oauth-journey/submissions", submitBody, "", cookies, "")
	if submitted.Code != http.StatusCreated {
		t.Fatalf("submit status=%d body=%s", submitted.Code, submitted.Body.String())
	}
	var submissionResult struct {
		Receipt     surveyport.SubmissionReceipt `json:"receipt"`
		ResultToken string                       `json:"result_token"`
	}
	mustSurveyJourneyJSON(t, submitted, &submissionResult)
	if submissionResult.Receipt.SubmissionID < 1 || submissionResult.ResultToken == "" || submissionResult.Receipt.ResultToken != submissionResult.ResultToken {
		t.Fatalf("submission receipt=%+v token=%q", submissionResult.Receipt, submissionResult.ResultToken)
	}

	resultBody, err := json.Marshal(map[string]string{"result_token": submissionResult.ResultToken})
	if err != nil {
		t.Fatal(err)
	}
	result := surveyJourneyServe(t, handler, http.MethodPost, "/api/public/survey-submission-results/query", resultBody, "", cookies, "")
	if result.Code != http.StatusOK {
		t.Fatalf("result status=%d body=%s", result.Code, result.Body.String())
	}
	var publicResult struct {
		SubmissionID surveyport.ID `json:"submission_id"`
	}
	mustSurveyJourneyJSON(t, result, &publicResult)
	if publicResult.SubmissionID != submissionResult.Receipt.SubmissionID {
		t.Fatalf("result submission=%d receipt=%d", publicResult.SubmissionID, submissionResult.Receipt.SubmissionID)
	}

	anonymous := surveyJourneyServe(t, handler, http.MethodPost, "/api/public/survey-submission-results/query", resultBody, "", nil, "")
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous result status=%d body=%s", anonymous.Code, anonymous.Body.String())
	}

	var customers, identities, storedSubmissions, storedTokens, customerBoundSubmissions int
	if err = native.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM customers),
		(SELECT count(*) FROM customer_identities WHERE assurance='verified'),
		(SELECT count(*) FROM survey_submissions),
		(SELECT count(*) FROM survey_result_tokens),
		(SELECT count(*) FROM survey_submissions WHERE customer_id=(SELECT id FROM customers))`).Scan(&customers, &identities, &storedSubmissions, &storedTokens, &customerBoundSubmissions); err != nil {
		t.Fatal(err)
	}
	if customers != 1 || identities != 1 || storedSubmissions != 1 || storedTokens != 1 || customerBoundSubmissions != 1 {
		t.Fatalf("customers=%d identities=%d submissions=%d result_tokens=%d customer_bound_submissions=%d", customers, identities, storedSubmissions, storedTokens, customerBoundSubmissions)
	}
}

type surveyJourneySecurity struct{ actorID int64 }

func (s surveyJourneySecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: s.actorID, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (s surveyJourneySecurity) AuthorizeCSRF(ctx context.Context, r *http.Request) (accessdomain.Principal, error) {
	return s.Authenticate(ctx, r)
}

type surveyOAuthAllowlistTransport struct {
	base   http.RoundTripper
	target *url.URL
	calls  int
}

func (t *surveyOAuthAllowlistTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" || request.URL.Host != "api.weixin.qq.com" || request.URL.Path != "/sns/oauth2/access_token" {
		return nil, fmt.Errorf("unexpected network request %s %s", request.Method, request.URL.String())
	}
	if request.Method != http.MethodGet {
		return nil, fmt.Errorf("unexpected OAuth method %s", request.Method)
	}
	t.calls++
	forwarded := request.Clone(request.Context())
	endpoint := *t.target
	endpoint.Path = request.URL.Path
	endpoint.RawQuery = request.URL.RawQuery
	forwarded.URL = &endpoint
	forwarded.Host = ""
	forwarded.RequestURI = ""
	return t.base.RoundTrip(forwarded)
}

func surveyJourneyServe(t *testing.T, handler http.Handler, method, path string, body []byte, key string, cookies []*http.Cookie, userAgent string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	if userAgent != "" {
		request.Header.Set("User-Agent", userAgent)
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func surveyFinalJourneyLocation(t *testing.T, recorder *httptest.ResponseRecorder) *url.URL {
	t.Helper()
	response := recorder.Result()
	location, err := response.Location()
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func mustSurveyJourneyJSON(t *testing.T, recorder *httptest.ResponseRecorder, value any) {
	t.Helper()
	if err := json.Unmarshal(recorder.Body.Bytes(), value); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
}
