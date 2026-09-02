package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

type accessAuthentication interface {
	Authenticate(context.Context, string) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, string, string, string) (accessdomain.Principal, error)
	LoginWithWeComUserID(context.Context, accessapp.WeComLoginCommand) (accessapp.IssuedSession, error)
}

type requestAccessSecurity struct{ authentication accessAuthentication }

func (adapter requestAccessSecurity) Authenticate(ctx context.Context, request *http.Request) (accessdomain.Principal, error) {
	return adapter.authentication.Authenticate(ctx, cookieValue(request, accesshttp.SessionCookieName))
}

func (adapter requestAccessSecurity) AuthorizeCSRF(ctx context.Context, request *http.Request) (accessdomain.Principal, error) {
	return adapter.authentication.AuthorizeCSRF(ctx,
		cookieValue(request, accesshttp.SessionCookieName), cookieValue(request, accesshttp.CSRFCookieName),
		strings.TrimSpace(request.Header.Get("X-CSRF-Token")))
}

type weComSessionIssuer struct{ authentication accessAuthentication }

func (adapter weComSessionIssuer) IssueWeComSession(ctx context.Context, purpose wecom.OAuthPurpose, identity wecom.OAuthIdentity) (wecom.BrowserCredentials, error) {
	if purpose != wecom.OAuthAdmin && purpose != wecom.OAuthSidebar {
		return wecom.BrowserCredentials{}, accessdomain.ErrAuthentication
	}
	issued, err := adapter.authentication.LoginWithWeComUserID(ctx, accessapp.WeComLoginCommand{WeComUserID: identity.EmployeeID, Remote: "wecom-oauth"})
	if err != nil {
		return wecom.BrowserCredentials{}, err
	}
	return wecom.BrowserCredentials{SessionToken: issued.SessionToken, CSRFToken: issued.CSRFToken, ExpiresAt: issued.ExpiresAt}, nil
}

type sidebarPrincipalResolver struct {
	authentication accessAuthentication
	users          accessUserReader
	uow            platformport.UnitOfWork
	corpID         string
}

type accessUserReader interface {
	UserByID(context.Context, int64, bool) (accessdomain.User, error)
}

func (adapter sidebarPrincipalResolver) SidebarPrincipal(ctx context.Context, sessionToken string) (wecom.SidebarPrincipal, error) {
	principal, err := adapter.authentication.Authenticate(ctx, sessionToken)
	if err != nil {
		return wecom.SidebarPrincipal{}, err
	}
	var user accessdomain.User
	err = adapter.uow.Within(ctx, func(txContext context.Context) error {
		var loadErr error
		user, loadErr = adapter.users.UserByID(txContext, principal.InternalID, false)
		return loadErr
	})
	if err != nil || !user.Active || user.WeComUserID == "" {
		return wecom.SidebarPrincipal{}, accessdomain.ErrAuthentication
	}
	return wecom.SidebarPrincipal{CorpID: adapter.corpID, EmployeeID: user.WeComUserID}, nil
}

type oneIDBridge struct{ service identityapp.OneIDService }

func (adapter oneIDBridge) ProvisionVerifiedWeComIdentity(ctx context.Context, fact identitydomain.VerifiedFact) (customerdomain.CustomerID, error) {
	result, err := adapter.service.ProvisionVerifiedIdentity(ctx, identityport.ProvisionCommand{Fact: fact})
	return result.CustomerID, err
}

func (adapter oneIDBridge) FindVerifiedWeComIdentity(ctx context.Context, fact identitydomain.VerifiedFact) (customerdomain.CustomerID, bool, error) {
	reference := fact.Reference()
	result, err := adapter.service.Resolve(ctx, identitydomain.Reference{Kind: reference.Kind, Scope: reference.Scope,
		Value: reference.NormalizedValue, Assurance: identitydomain.AssuranceVerified, Source: reference.Source})
	if err != nil {
		return 0, false, err
	}
	return result.CustomerID, result.Status == identityport.ResolveFound, nil
}

type existingWeComIdentityResolver struct {
	service identityapp.OneIDService
	uow     platformport.UnitOfWork
	corpID  string
}

func (adapter existingWeComIdentityResolver) ResolveExistingWeComIdentity(ctx context.Context, corpID, externalUserID string) (customerdomain.CustomerID, bool, error) {
	if corpID != adapter.corpID || strings.TrimSpace(externalUserID) != externalUserID || externalUserID == "" {
		return 0, false, identitydomain.ErrInvalidReference
	}
	var result identityport.ResolveResult
	err := adapter.uow.Within(ctx, func(txContext context.Context) error {
		var resolveErr error
		result, resolveErr = adapter.service.Resolve(txContext, identitydomain.Reference{
			Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:" + corpID,
			Value: externalUserID, Assurance: identitydomain.AssuranceDeclared, Source: "wecom.sidebar",
		})
		return resolveErr
	})
	if err != nil {
		return 0, false, err
	}
	return result.CustomerID, result.Status == identityport.ResolveFound, nil
}

type sidebarCustomerViewer struct {
	queries identityquery.Reader
	uow     platformport.UnitOfWork
}

func (adapter sidebarCustomerViewer) SidebarCustomer(ctx context.Context, customerID customerdomain.CustomerID) (wecom.SidebarCustomerView, error) {
	var detail identityquery.CustomerDetail
	err := adapter.uow.Within(ctx, func(txContext context.Context) error {
		var queryErr error
		detail, queryErr = adapter.queries.Customer(txContext, customerID)
		return queryErr
	})
	if errors.Is(err, identityquery.ErrNotFound) {
		return wecom.SidebarCustomerView{}, wecom.ErrCustomerNotFound
	}
	if err != nil {
		return wecom.SidebarCustomerView{}, err
	}
	return wecom.SidebarCustomerView{CustomerID: detail.CanonicalCustomerID, Status: string(detail.CanonicalStatus)}, nil
}

func requireAdminSession(authentication accessAuthentication, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, err := authentication.Authenticate(request.Context(), cookieValue(request, accesshttp.SessionCookieName)); err != nil {
			target := "/login?next=" + url.QueryEscape(request.URL.RequestURI())
			http.Redirect(writer, request, target, http.StatusSeeOther)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func cookieValue(request *http.Request, name string) string {
	cookie, err := request.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}
