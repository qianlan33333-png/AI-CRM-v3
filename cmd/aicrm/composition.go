package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identityhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/http"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	platformruntime "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/runtime"
	platformwebhook "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/webshell"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
)

type composedApplication struct {
	pool           *platformpostgres.Pool
	handler        http.Handler
	management     *accessapp.Management
	weComProcessor wecom.InboxProcessor
}

func compose(ctx context.Context, cfg platformconfig.Runtime) (*composedApplication, error) {
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: cfg.DatabaseURL, MaxConnections: 20, MinConnections: 1})
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*composedApplication, error) {
		pool.Close()
		return nil, err
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return fail(err)
	}
	auditService, err := platformaudit.NewService(platformaudit.NewPostgreSQLStore())
	if err != nil {
		return fail(err)
	}
	inboxService, err := platformwebhook.NewService(platformwebhook.NewPostgreSQLStore())
	if err != nil {
		return fail(err)
	}

	passwords := credential.PasswordHasher{}
	dummyHash, err := passwords.Hash("aicrm-dummy-password-never-valid")
	if err != nil {
		return fail(err)
	}
	accessRepository := accessstore.NewPostgreSQL()
	authentication, err := accessapp.NewAuthentication(accessRepository, uow, passwords, accessapp.AuthenticationConfig{DummyPHCHash: dummyHash})
	if err != nil {
		return fail(err)
	}
	management, err := accessapp.NewManagement(accessRepository, uow, passwords, nil)
	if err != nil {
		return fail(err)
	}

	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	queries := identityquery.NewPostgreSQL()
	requestSecurity := requestAccessSecurity{authentication: authentication}
	oneIDHandler, err := identityhttp.NewHandler(identityhttp.Config{
		UnitOfWork: uow, Authenticator: requestSecurity, CSRF: requestSecurity,
		OneID: oneID, Queries: queries, Audit: auditService,
	})
	if err != nil {
		return fail(err)
	}

	renderer, err := webshell.NewRenderer()
	if err != nil {
		return fail(err)
	}
	accessHandler, err := accesshttp.NewHandler(accesshttp.Config{
		Renderer: renderer, Auth: authentication, Management: management, CookieSecure: true, CookiePath: "/",
	})
	if err != nil {
		return fail(err)
	}
	shellHandler, err := webshell.NewHandler(webshell.HandlerOptions{Renderer: renderer})
	if err != nil {
		return fail(err)
	}

	providerClient, err := wecomadapter.New(wecomadapter.Config{
		Enabled: cfg.WeCom.Enabled, CorpID: cfg.WeCom.CorpID, AgentID: cfg.WeCom.AgentID, Secret: cfg.WeCom.Secret,
		AdminCallbackURI: cfg.PublicOrigin + "/auth/wecom/callback", SidebarCallbackURI: cfg.PublicOrigin + "/api/sidebar/oauth/callback",
	})
	if err != nil {
		return fail(err)
	}
	var callbackCrypto *wecom.CallbackCrypto
	if cfg.WeCom.Enabled {
		callbackCrypto, err = wecom.NewCallbackCrypto(cfg.WeCom.CallbackToken, cfg.WeCom.CallbackAESKey, cfg.WeCom.CorpID)
		if err != nil {
			return fail(err)
		}
	}
	relationships := wecom.NewPostgreSQLFollowRelationshipStore()
	oauthStates := wecom.NewPostgreSQLOAuthStateStore()
	weComIdentity := oneIDBridge{service: oneID}
	weComProcessor := wecom.InboxProcessor{
		Enabled: cfg.WeCom.Enabled, CorpID: cfg.WeCom.CorpID, Inbox: inboxService, UOW: uow,
		Identity: weComIdentity, Relationships: relationships, Audit: auditService,
	}
	weComHandler, err := wecom.NewHTTPHandler(wecom.HTTPHandlerOptions{
		Callback: wecom.CallbackHandler{Enabled: cfg.WeCom.Enabled, Crypto: callbackCrypto, Inbox: inboxService, UOW: uow},
		OAuth: wecom.OAuthService{Enabled: cfg.WeCom.Enabled, CorpID: cfg.WeCom.CorpID, StateStore: oauthStates, UOW: uow,
			Client: providerClient, AllowedPaths: allowedOAuthRedirects(), StateTTL: 10 * time.Minute},
		ContextTokens: wecom.ContextTokenService{CorpID: cfg.WeCom.CorpID, SigningKey: []byte(cfg.WeCom.ContextSigningKey),
			Relationships: relationships, UOW: uow, TTL: 5 * time.Minute},
		JSSDKSigner: providerClient, JSSDKOrigin: cfg.PublicOrigin,
		PrincipalResolver: sidebarPrincipalResolver{authentication: authentication, users: accessRepository, uow: uow, corpID: cfg.WeCom.CorpID},
		CustomerViewer:    sidebarCustomerViewer{queries: queries, uow: uow}, SessionIssuer: weComSessionIssuer{authentication: authentication},
		ExistingIdentity: existingWeComIdentityResolver{service: oneID, uow: uow, corpID: cfg.WeCom.CorpID}, CookieSecure: true,
	})
	if err != nil {
		return fail(err)
	}
	readiness := platformruntime.ReadinessFunc(func(readinessContext context.Context) error {
		if checkErr := pool.Check(readinessContext); checkErr != nil {
			return checkErr
		}
		var complete bool
		checkErr := pool.Native().QueryRow(readinessContext, `SELECT COUNT(*) = 4 FROM platform_schema_migrations WHERE version IN ('0001','0002','0003','0004')`).Scan(&complete)
		if checkErr != nil || !complete {
			return errors.New("database schema is not ready")
		}
		return nil
	})
	healthHandler, err := platformruntime.NewHandler(platformruntime.HandlerOptions{ReleaseSHA: cfg.ReleaseSHA, Readiness: readiness})
	if err != nil {
		return fail(err)
	}

	handler, err := routeApplication(healthHandler, accessHandler.Routes(), oneIDHandler.Routes(), weComHandler, shellHandler, authentication, cfg.PublicOrigin)
	if err != nil {
		return fail(err)
	}
	return &composedApplication{pool: pool, handler: handler, management: management, weComProcessor: weComProcessor}, nil
}

func (application *composedApplication) Close() {
	if application != nil && application.pool != nil {
		application.pool.Close()
	}
}

func (application *composedApplication) bootstrap(ctx context.Context, config platformconfig.Bootstrap) error {
	if !config.Enabled {
		return nil
	}
	_, _, err := application.management.Bootstrap(ctx, accessapp.BootstrapInput{
		Username: config.Username, Password: config.Password, DisplayName: config.DisplayName,
	})
	return err
}

func allowedOAuthRedirects() map[string]struct{} {
	paths := map[string]struct{}{webshell.SidebarPagePath: {}}
	for _, route := range webshell.ADMIN_ROUTE_REGISTRY {
		if strings.HasPrefix(route.Path, webshell.AdminRootPath) {
			paths[route.Path] = struct{}{}
		}
	}
	return paths
}

func routeApplication(health, access, identity, weCom, shell http.Handler, authentication accessAuthentication, publicOrigin string) (http.Handler, error) {
	if health == nil || access == nil || identity == nil || weCom == nil || shell == nil || authentication == nil || canonicalOrigin(publicOrigin) == "" {
		return nil, errors.New("application HTTP dependencies are required")
	}
	mux := http.NewServeMux()
	mux.Handle("/healthz", health)
	mux.Handle("/readyz", health)
	mux.Handle("/login", access)
	mux.Handle("/logout", access)
	mux.Handle("/api/admin/access/", access)
	mux.Handle("/api/admin/oneid/", identity)
	mux.Handle("/wecom/external-contact/callback", weCom)
	mux.Handle("/auth/wecom/start", weCom)
	mux.Handle("/auth/wecom/callback", weCom)
	mux.Handle("/api/sidebar/", weCom)
	mux.Handle("/static/", shell)
	mux.Handle(webshell.SidebarPagePath, shell)
	mux.Handle("/admin", requireAdminSession(authentication, shell))
	mux.Handle("/admin/", requireAdminSession(authentication, shell))
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(writer, request)
			return
		}
		http.Redirect(writer, request, "/admin", http.StatusSeeOther)
	})
	return securityHeaders(rejectCrossSiteUnsafeRequests(mux, canonicalOrigin(publicOrigin))), nil
}

func rejectCrossSiteUnsafeRequests(next http.Handler, publicOrigin string) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if isUnsafeMethod(request.Method) {
			origin := request.Header.Get("Origin")
			blocked := false
			if origin != "" {
				// Origin is the authoritative browser signal. Fetch Metadata is
				// only a fallback because extensions and restored tabs can report
				// an inconsistent Sec-Fetch-Site for an otherwise same-origin form.
				blocked = canonicalOrigin(origin) != publicOrigin
			} else {
				blocked = strings.EqualFold(request.Header.Get("Sec-Fetch-Site"), "cross-site")
			}
			if blocked {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusForbidden)
				_, _ = writer.Write([]byte(`{"ok":false,"error":"cross_site_request"}`))
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func canonicalOrigin(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	if parsed.Scheme != "https" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		contentPolicy := "default-src 'self'; script-src 'self' https://res.wx.qq.com; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'"
		if request.URL.Path != webshell.SidebarPagePath && !strings.HasPrefix(request.URL.Path, "/api/sidebar/") {
			writer.Header().Set("X-Frame-Options", "SAMEORIGIN")
			contentPolicy += "; frame-ancestors 'self'"
		}
		writer.Header().Set("Content-Security-Policy", contentPolicy)
		next.ServeHTTP(writer, request)
	})
}
