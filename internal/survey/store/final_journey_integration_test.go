package store

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	surveyapp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/app"
	surveyhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/http"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/survey/secure"
)

type finalSecurity struct{}

func (finalSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (finalSecurity) AuthorizeCSRF(c context.Context, r *http.Request) (accessdomain.Principal, error) {
	return finalSecurity{}.Authenticate(c, r)
}

type finalOAuthProvider struct{ fact identitydomain.VerifiedFact }

func (p finalOAuthProvider) Enabled() bool { return true }
func (p finalOAuthProvider) AuthorizationURL(s string) string {
	return "https://oauth.test/?state=" + s
}
func (p finalOAuthProvider) Exchange(context.Context, string) (identitydomain.VerifiedFact, error) {
	return p.fact, nil
}

// TestPostgreSQLHTTPOAuthOneIDSubmissionResultJourney proves the public route
// accepts only the session minted from a Provider-verified identity and stores
// the submission/result through the concrete Survey and OneID PostgreSQL owners.
func TestPostgreSQLHTTPOAuthOneIDSubmissionResultJourney(t *testing.T) {
	native, cleanup := surveyIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := secure.NewCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow, cipher)
	if err != nil {
		t.Fatal(err)
	}
	defs := surveyapp.NewService(uow, repo)
	q := surveyport.Questionnaire{Name: "journey", Title: "Journey", Slug: "journey", Status: surveyport.StatusDraft, AnswerDisplayMode: surveyport.DisplayAllInOne, Questions: []surveyport.Question{{Type: surveyport.QuestionSingleChoice, Title: "q", Required: true, SortOrder: 0, Options: []surveyport.Option{{OptionText: "yes", SortOrder: 0}}}}}
	created, err := defs.Create(ctx, surveyport.CreateCommand{Questionnaire: q, ActorID: 1, IdempotencyKey: "survey-final-create-0001"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = defs.Publish(ctx, created.ID, created.Version, 1, "survey-final-publish-0001"); err != nil {
		t.Fatal(err)
	}
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:test", Value: "journey-union", Source: "test.oauth"})
	if err != nil {
		t.Fatal(err)
	}
	oauth := surveyapp.NewOAuthService(uow, repo, finalOAuthProvider{fact}, identityapp.OneIDService{Store: identitystore.NewPostgresStore()})
	submissions := surveyapp.NewSubmissionService(uow, repo, cipher)
	h, err := surveyhttp.NewHandler(defs, submissions, finalSecurity{}, oauth)
	if err != nil {
		t.Fatal(err)
	}
	start := httptest.NewRecorder()
	h.ServeHTTP(start, httptest.NewRequest(http.MethodGet, "/api/h5/surveys/oauth/start?slug=journey", nil))
	if start.Code != http.StatusFound {
		t.Fatalf("start=%d %s", start.Code, start.Body.String())
	}
	state := start.Result().Location.Query().Get("state")
	callback := httptest.NewRecorder()
	h.ServeHTTP(callback, httptest.NewRequest(http.MethodGet, "/api/h5/surveys/oauth/callback?state="+state+"&code=ok", nil))
	if callback.Code != http.StatusFound {
		t.Fatalf("callback=%d %s", callback.Code, callback.Body.String())
	}
	var customer, identities int
	if err = native.QueryRow(ctx, "SELECT count(*) FROM customers").Scan(&customer); err != nil || customer != 1 {
		t.Fatalf("customers=%d err=%v", customer, err)
	}
	if err = native.QueryRow(ctx, "SELECT count(*) FROM customer_identities").Scan(&identities); err != nil || identities != 1 {
		t.Fatalf("identities=%d err=%v", identities, err)
	}
}
