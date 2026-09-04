package http

import (
	"context"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
