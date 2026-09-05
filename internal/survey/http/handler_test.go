package http

import (
	"context"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

type routeDefinitions struct {
	surveyport.DefinitionApplication
}

type routeSurvey struct {
	surveyport.PublicApplication
	surveyport.SubmissionApplication
	questionnaire surveyport.Questionnaire
}

func (s *routeSurvey) ReadPublic(_ context.Context, slug string) (surveyport.Questionnaire, error) {
	if slug != s.questionnaire.Slug {
		return surveyport.Questionnaire{}, surveyport.ErrNotFound
	}
	return s.questionnaire, nil
}

type routeSecurity struct{}

func (routeSecurity) Authenticate(context.Context, *nethttp.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, errors.New("unused")
}
func (routeSecurity) AuthorizeCSRF(context.Context, *nethttp.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, errors.New("unused")
}

type operationSecurity struct{ csrfErr error }

func (s operationSecurity) Authenticate(context.Context, *nethttp.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (s operationSecurity) AuthorizeCSRF(context.Context, *nethttp.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, s.csrfErr
}

type operationRouteSurvey struct {
	surveyport.PublicApplication
	surveyport.SubmissionApplication
	configuration surveyport.OperationConfiguration
	saveCalls     int
	testCalls     int
	testReceipt   surveyport.CompletionTestReceipt
	testErr       error
	receipts      []surveyport.OperationReceipt
}

func (s *operationRouteSurvey) GetOperationConfiguration(context.Context, surveyport.ID) (surveyport.OperationConfiguration, error) {
	return s.configuration, nil
}
func (s *operationRouteSurvey) ListOperationReceipts(context.Context, surveyport.ID, int32, int32) ([]surveyport.OperationReceipt, int64, error) {
	return s.receipts, int64(len(s.receipts)), nil
}
func (s *operationRouteSurvey) SaveOperationConfiguration(_ context.Context, value surveyport.OperationConfiguration, _ int64, _ string) (surveyport.OperationConfiguration, error) {
	s.saveCalls++
	s.configuration = value
	s.configuration.Version++
	return s.configuration, nil
}
func (s *operationRouteSurvey) QueueCompletionTest(context.Context, surveyport.ID, int64, string) (surveyport.CompletionTestReceipt, error) {
	s.testCalls++
	return s.testReceipt, s.testErr
}

type routeOAuth struct {
	enabled  bool
	identity surveyport.SubmissionIdentity
}

func (o routeOAuth) Enabled() bool { return o.enabled }
func (routeOAuth) Start(context.Context, string) (string, error) {
	return "https://open.weixin.qq.com/authorize", nil
}
func (routeOAuth) Complete(context.Context, string, string) (string, string, error) {
	return "session", "/h5/all.html?slug=growth", nil
}
func (o routeOAuth) ResolveSession(context.Context, string) (surveyport.SubmissionIdentity, error) {
	return o.identity, nil
}

func newRouteHandler(t *testing.T, oauth routeOAuth) *Handler {
	t.Helper()
	handler, err := NewHandler(&routeDefinitions{}, &routeSurvey{questionnaire: surveyport.Questionnaire{Slug: "growth", Status: surveyport.StatusPublished, AnswerDisplayMode: surveyport.DisplayAllInOne}}, routeSecurity{}, oauth)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestPublicSurveyCannotBypassOAuth(t *testing.T) {
	handler := newRouteHandler(t, routeOAuth{enabled: true})
	for _, path := range []string{"/api/public/questionnaires/growth", "/api/public/questionnaires/growth/submissions"} {
		method := nethttp.MethodGet
		if strings.HasSuffix(path, "/submissions") {
			method = nethttp.MethodPost
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
		if response.Code != nethttp.StatusUnauthorized || !strings.Contains(response.Body.String(), "survey_oauth_required") {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestOperationMetadataPUTRequiresCSRFAndCurrentConfigurationVersion(t *testing.T) {
	survey := &operationRouteSurvey{configuration: surveyport.OperationConfiguration{QuestionnaireID: 7, ExternalPushEnabled: false, ExternalPushConfigurationRef: "push.v2", ExternalPushMetadata: []byte(`{"remark":"changed-by-b"}`), Version: 2}}
	handler, err := NewHandler(&routeDefinitions{}, survey, operationSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(nethttp.MethodPut, "/api/admin/questionnaires/7/operations/external-push", strings.NewReader(`{"enabled":true,"configuration_reference":"push.v1","metadata":{"remark":"stale-a"},"configuration_version":1}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "survey-operation-http-cas-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusConflict || !strings.Contains(response.Body.String(), "configuration_conflict") || survey.saveCalls != 0 {
		t.Fatalf("stale response=%d body=%s saves=%d", response.Code, response.Body.String(), survey.saveCalls)
	}
	if survey.configuration.ExternalPushEnabled || survey.configuration.ExternalPushConfigurationRef != "push.v2" {
		t.Fatalf("stale HTTP request overwrote concurrent config: %+v", survey.configuration)
	}

	csrfHandler, err := NewHandler(&routeDefinitions{}, survey, operationSecurity{csrfErr: errors.New("csrf rejected")})
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(nethttp.MethodPut, "/api/admin/questionnaires/7/operations/external-push", strings.NewReader(`{"enabled":false,"configuration_reference":"push.v2","metadata":{},"configuration_version":2}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "survey-operation-http-csrf-0002")
	response = httptest.NewRecorder()
	csrfHandler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusForbidden || !strings.Contains(response.Body.String(), "csrf_invalid") || survey.saveCalls != 0 {
		t.Fatalf("csrf response=%d body=%s saves=%d", response.Code, response.Body.String(), survey.saveCalls)
	}
}

func TestLegacyQuestionnaireOpsSaveJourneyPreservesExternalPushMetadata(t *testing.T) {
	// This is the exact existing web save order: completion PUT, followed by an
	// external-push PUT that contains only the toggle and opaque reference.
	survey := &operationRouteSurvey{configuration: surveyport.OperationConfiguration{QuestionnaireID: 7, ExternalPushEnabled: true, ExternalPushConfigurationRef: "push.v1", ExternalPushMetadata: []byte(`{"remark":"keep-me","custom_params":{"campaign":"autumn"}}`), Version: 1}}
	handler, err := NewHandler(&routeDefinitions{}, survey, operationSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	completion := httptest.NewRequest(nethttp.MethodPut, "/api/admin/questionnaires/7/operations/completion", strings.NewReader(`{"navigation_target_id":"completion.done","channel_id":19}`))
	completion.Header.Set("Content-Type", "application/json")
	completion.Header.Set("Idempotency-Key", "survey-legacy-ops-completion-0001")
	completionResponse := httptest.NewRecorder()
	handler.ServeHTTP(completionResponse, completion)
	if completionResponse.Code != nethttp.StatusOK || survey.configuration.Version != 2 {
		t.Fatalf("legacy completion response=%d config=%+v", completionResponse.Code, survey.configuration)
	}

	push := httptest.NewRequest(nethttp.MethodPut, "/api/admin/questionnaires/7/operations/external-push", strings.NewReader(`{"enabled":true,"configuration_reference":"push.v2"}`))
	push.Header.Set("Content-Type", "application/json")
	push.Header.Set("Idempotency-Key", "survey-legacy-ops-push-0002")
	pushResponse := httptest.NewRecorder()
	handler.ServeHTTP(pushResponse, push)
	if pushResponse.Code != nethttp.StatusOK || survey.saveCalls != 2 || survey.configuration.Version != 3 || !survey.configuration.ExternalPushEnabled || survey.configuration.ExternalPushConfigurationRef != "push.v2" || string(survey.configuration.ExternalPushMetadata) != `{"remark":"keep-me","custom_params":{"campaign":"autumn"}}` {
		t.Fatalf("legacy save journey lost operations data: status=%d config=%+v saves=%d", pushResponse.Code, survey.configuration, survey.saveCalls)
	}

	newMetadataWithoutVersion := httptest.NewRequest(nethttp.MethodPut, "/api/admin/questionnaires/7/operations/external-push", strings.NewReader(`{"enabled":true,"configuration_reference":"push.v2","metadata":{"remark":"must-carry-version"}}`))
	newMetadataWithoutVersion.Header.Set("Content-Type", "application/json")
	newMetadataWithoutVersion.Header.Set("Idempotency-Key", "survey-legacy-ops-metadata-0003")
	metadataResponse := httptest.NewRecorder()
	handler.ServeHTTP(metadataResponse, newMetadataWithoutVersion)
	if metadataResponse.Code != nethttp.StatusBadRequest || !strings.Contains(metadataResponse.Body.String(), "configuration_version_required") || survey.saveCalls != 2 {
		t.Fatalf("metadata without version response=%d body=%s saves=%d", metadataResponse.Code, metadataResponse.Body.String(), survey.saveCalls)
	}
}

func TestExternalPushLogsPreserveSyntheticRunAndTerminalStatus(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	survey := &operationRouteSurvey{receipts: []surveyport.OperationReceipt{
		{ID: 8, QuestionnaireID: 7, SourcePK: "questionnaire-test-0123456789abcdef0123456789abcdef", Status: "queued", OccurrenceCount: 0, OccurredAt: now},
		{ID: 9, QuestionnaireID: 7, SourcePK: "questionnaire-test-fedcba9876543210fedcba9876543210", Status: "executed", OccurrenceCount: 1, RealEffectExecuted: true, OccurredAt: now},
		{ID: 10, QuestionnaireID: 7, SourcePK: "questionnaire-test-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: "outcome_unknown", OccurrenceCount: 1, RealEffectExecuted: true, OccurredAt: now},
		{ID: 11, QuestionnaireID: 7, Status: "queued", OccurrenceCount: 0, OccurredAt: now},
	}}
	handler, err := NewHandler(&routeDefinitions{}, survey, operationSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(nethttp.MethodGet, "/admin/questionnaires/external-push-logs", nil))
	body := response.Body.String()
	if response.Code != nethttp.StatusOK || !strings.Contains(body, `"test_run_id":"questionnaire-test-0123456789abcdef0123456789abcdef"`) || !strings.Contains(body, `"status":"executed"`) || !strings.Contains(body, `"status":"outcome_unknown"`) || !strings.Contains(body, `"test_run_id":11`) || strings.Contains(body, `"local_only":true`) {
		t.Fatalf("logs response=%d body=%s", response.Code, body)
	}
}

func TestExternalPushTestUsesSyntheticQueueAndFailsClosedWhenDisabled(t *testing.T) {
	survey := &operationRouteSurvey{testReceipt: surveyport.CompletionTestReceipt{QuestionnaireID: 7, TestRunID: "questionnaire-test-0123456789abcdef0123456789abcdef", EffectID: "eer_7", State: "queued"}}
	handler, err := NewHandler(&routeDefinitions{}, survey, operationSecurity{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(nethttp.MethodPost, "/api/admin/questionnaires/7/operations/external-push/test", nil)
	request.Header.Set("Idempotency-Key", "survey-completion-test-http-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusAccepted || survey.testCalls != 1 || !strings.Contains(response.Body.String(), "questionnaire-test-") || !strings.Contains(response.Body.String(), "synthetic_data") {
		t.Fatalf("test queue response=%d body=%s calls=%d", response.Code, response.Body.String(), survey.testCalls)
	}
	survey.testErr = surveyport.ErrEffectUnavailable
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusConflict || !strings.Contains(response.Body.String(), "provider_disabled") || survey.testCalls != 2 {
		t.Fatalf("disabled test response=%d body=%s calls=%d", response.Code, response.Body.String(), survey.testCalls)
	}
}

func TestExternalPushTestRequiresCSRFBeforeAcceptingSyntheticEffect(t *testing.T) {
	survey := &operationRouteSurvey{testReceipt: surveyport.CompletionTestReceipt{QuestionnaireID: 7, TestRunID: "questionnaire-test-0123456789abcdef0123456789abcdef", EffectID: "eer_7", State: "queued"}}
	handler, err := NewHandler(&routeDefinitions{}, survey, operationSecurity{csrfErr: errors.New("csrf rejected")})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(nethttp.MethodPost, "/api/admin/questionnaires/7/operations/external-push/test", nil)
	request.Header.Set("Idempotency-Key", "survey-completion-test-http-csrf-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusForbidden || !strings.Contains(response.Body.String(), "csrf_invalid") || survey.testCalls != 0 {
		t.Fatalf("csrf response=%d body=%s calls=%d", response.Code, response.Body.String(), survey.testCalls)
	}
}

func TestSessionEndpointFailsClosedAndHidesCustomerIdentity(t *testing.T) {
	disabled := newRouteHandler(t, routeOAuth{})
	response := httptest.NewRecorder()
	disabled.ServeHTTP(response, httptest.NewRequest(nethttp.MethodGet, "/api/h5/surveys/session?slug=growth", nil))
	if response.Code != nethttp.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "survey_oauth_unavailable") {
		t.Fatalf("disabled status=%d body=%s", response.Code, response.Body.String())
	}

	customerID := customerdomain.CustomerID(42)
	enabled := newRouteHandler(t, routeOAuth{enabled: true, identity: surveyport.SubmissionIdentity{State: surveyport.IdentityResolved, CustomerID: &customerID}})
	request := httptest.NewRequest(nethttp.MethodGet, "/api/h5/surveys/session?slug=growth", nil)
	request.AddCookie(&nethttp.Cookie{Name: "__Host-aicrm_survey_identity", Value: strings.Repeat("a", 43)})
	response = httptest.NewRecorder()
	enabled.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || strings.Contains(response.Body.String(), "42") || strings.Contains(response.Body.String(), "openid") || strings.Contains(response.Body.String(), "unionid") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPublicEntryUsesUnifiedOAuthGate(t *testing.T) {
	handler := newRouteHandler(t, routeOAuth{enabled: true})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(nethttp.MethodGet, "/q/growth", nil))
	if response.Code != nethttp.StatusSeeOther || response.Header().Get("Location") != "/h5/auth.html?slug=growth" {
		t.Fatalf("status=%d location=%q", response.Code, response.Header().Get("Location"))
	}

	request := httptest.NewRequest(nethttp.MethodGet, "/api/h5/surveys/oauth/start?slug=growth", nil)
	request.Header.Set("User-Agent", "Mozilla/5.0")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusBadRequest || !strings.Contains(response.Body.String(), "survey_wechat_required") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
