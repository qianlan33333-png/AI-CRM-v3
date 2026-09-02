package wecom

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

type JSSDKSigner interface {
	ConfigForURL(context.Context, string) (map[string]string, error)
}

type EmployeeSessionIssuer interface {
	IssueWeComSession(context.Context, OAuthPurpose, OAuthIdentity) error
}

type HTTPHandlerOptions struct {
	Callback          CallbackHandler
	OAuth             OAuthService
	ContextTokens     ContextTokenService
	JSSDKSigner       JSSDKSigner
	JSSDKOrigin       string
	PrincipalResolver SidebarPrincipalResolver
	CustomerViewer    SidebarCustomerViewer
	SessionIssuer     EmployeeSessionIssuer
}

// NewHTTPHandler creates the frozen WeCom routes. The caller mounts this
// handler in cmd/aicrm; this package never registers routes globally.
func NewHTTPHandler(options HTTPHandlerOptions) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /wecom/external-contact/callback", options.Callback)
	mux.Handle("POST /wecom/external-contact/callback", options.Callback)
	mux.HandleFunc("GET /auth/wecom/start", func(writer http.ResponseWriter, request *http.Request) {
		handleOAuthStart(writer, request, options.OAuth, OAuthAdmin)
	})
	mux.HandleFunc("GET /auth/wecom/callback", func(writer http.ResponseWriter, request *http.Request) {
		handleOAuthCallback(writer, request, options, OAuthAdmin)
	})
	mux.HandleFunc("GET /api/sidebar/oauth/start", func(writer http.ResponseWriter, request *http.Request) {
		handleOAuthStart(writer, request, options.OAuth, OAuthSidebar)
	})
	mux.HandleFunc("GET /api/sidebar/oauth/callback", func(writer http.ResponseWriter, request *http.Request) {
		handleOAuthCallback(writer, request, options, OAuthSidebar)
	})
	mux.HandleFunc("GET /api/sidebar/jssdk-config", func(writer http.ResponseWriter, request *http.Request) {
		handleJSSDK(writer, request, options)
	})
	mux.HandleFunc("POST /api/sidebar/context-token", func(writer http.ResponseWriter, request *http.Request) {
		handleContextIssue(writer, request, options)
	})
	mux.HandleFunc("GET /api/sidebar/v2/workbench", func(writer http.ResponseWriter, request *http.Request) {
		handleWorkbench(writer, request, options)
	})
	return mux
}

func handleOAuthStart(writer http.ResponseWriter, request *http.Request, service OAuthService, purpose OAuthPurpose) {
	start, err := service.Start(request.Context(), purpose, request.URL.Query().Get("next"))
	if err != nil {
		writeOAuthError(writer, err)
		return
	}
	http.Redirect(writer, request, start.AuthorizationURL, http.StatusFound)
}

func handleOAuthCallback(writer http.ResponseWriter, request *http.Request, options HTTPHandlerOptions, purpose OAuthPurpose) {
	if options.SessionIssuer == nil {
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	identity, state, err := options.OAuth.ConsumeAndExchange(request.Context(), purpose, request.URL.Query().Get("state"), request.URL.Query().Get("code"))
	if err != nil {
		writeOAuthError(writer, err)
		return
	}
	if err = options.SessionIssuer.IssueWeComSession(request.Context(), purpose, identity); err != nil {
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	http.Redirect(writer, request, state.Redirect, http.StatusFound)
}

func handleJSSDK(writer http.ResponseWriter, request *http.Request, options HTTPHandlerOptions) {
	if !options.OAuth.Enabled || options.JSSDKSigner == nil || !validJSSDKURL(request.URL.Query().Get("url"), options.JSSDKOrigin) {
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	config, err := options.JSSDKSigner.ConfigForURL(request.Context(), request.URL.Query().Get("url"))
	if err != nil {
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, config)
}

func validJSSDKURL(value, origin string) bool {
	parsed, err := url.Parse(value)
	base, baseErr := url.Parse(origin)
	return err == nil && baseErr == nil && base.IsAbs() && base.Host != "" && parsed.Scheme == base.Scheme && parsed.Host == base.Host && parsed.Fragment == "" && parsed.Path != ""
}

func handleContextIssue(writer http.ResponseWriter, request *http.Request, options HTTPHandlerOptions) {
	if !options.OAuth.Enabled || options.PrincipalResolver == nil {
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 4096)
	var input struct {
		CustomerID int64 `json:"customer_id"`
	}
	if json.NewDecoder(request.Body).Decode(&input) != nil || input.CustomerID < 1 {
		writeWeComError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	principal, err := options.PrincipalResolver.SidebarPrincipal(request.Context())
	if err != nil {
		writeWeComError(writer, http.StatusUnauthorized, "authentication_required")
		return
	}
	token, err := options.ContextTokens.Issue(request.Context(), principal, customerdomain.CustomerID(input.CustomerID))
	if err != nil {
		writeContextError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"context_token": token})
}

func handleWorkbench(writer http.ResponseWriter, request *http.Request, options HTTPHandlerOptions) {
	if !options.OAuth.Enabled || options.CustomerViewer == nil {
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	_, customerID, err := options.ContextTokens.Verify(request.Context(), bearerToken(request.Header.Get("Authorization")))
	if err != nil {
		writeContextError(writer, err)
		return
	}
	view, err := options.CustomerViewer.SidebarCustomer(request.Context(), customerID)
	if err != nil {
		if errors.Is(err, ErrCustomerNotFound) {
			writeWeComError(writer, http.StatusNotFound, "identity_not_found")
			return
		}
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

func writeOAuthError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrProviderDisabled):
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
	case errors.Is(err, ErrOpenRedirect), errors.Is(err, ErrInvalidOAuth):
		writeWeComError(writer, http.StatusBadRequest, "invalid_oauth_state")
	default:
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
	}
}

func writeContextError(writer http.ResponseWriter, err error) {
	if errors.Is(err, ErrRelationship) {
		writeWeComError(writer, http.StatusForbidden, "permission_denied")
		return
	}
	writeWeComError(writer, http.StatusUnauthorized, "authentication_required")
}

func bearerToken(value string) string {
	const prefix = "Bearer "
	if strings.HasPrefix(value, prefix) {
		return strings.TrimPrefix(value, prefix)
	}
	return ""
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
