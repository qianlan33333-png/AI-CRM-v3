package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLReferralH5OAuthReturnRoundTrip exercises the composed Payment
// OAuth state store and callback rather than bypassing them with a fixture
// browser session. The Provider reads are constrained to the local test server;
// no real OAuth credential, state, code, or identity is emitted by this test.
func TestPostgreSQLReferralH5OAuthReturnRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	key, cert := distributionFixturePaymentCredentials(t)
	applicationServer := httptest.NewUnstartedServer(http.NotFoundHandler())
	defer applicationServer.Close()
	dataKey := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cfg := platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: "https://" + applicationServer.Listener.Addr().String(), ReleaseSHA: strings.Repeat("1", 40), WorkerOwner: "referral-h5-oauth", WorkerLimit: 1,
		Referral:  platformconfig.Referral{TokenDataKey: dataKey},
		GroupOps:  platformconfig.GroupOps{WebhookSecret: "referral-h5-oauth-webhook"},
		Survey:    platformconfig.Survey{DataKey: dataKey, IdentityPhoneDataKey: dataKey, OAuthEnabled: true, OAuthAppID: "wx-referral-h5", OAuthSecret: "referral-h5-oauth-secret", OAuthOpenPlatformID: "referral-h5-platform", OAuthScope: "snsapi_userinfo"},
		WeChatPay: platformconfig.WeChatPay{Enabled: true, AppID: "wx-referral-payment", AppSecret: "referral-payment-secret", AppScope: "wechat-app:referral-payment", H5OAuthEnabled: true, H5AppID: "wx-referral-h5", H5AppSecret: "referral-h5-oauth-secret", H5AppScope: "wechat-app:wx-referral-h5", OrderContactDataKey: dataKey, MerchantID: "referral-h5-mch", MerchantSerial: "referral-h5-serial", PrivateKeyPath: key, PlatformCertPath: cert, APIV3Key: strings.Repeat("k", 32)},
	}
	application, err := compose(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	applicationServer.Config.Handler = application.handler
	applicationServer.StartTLS()

	weChat := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/sns/oauth2/access_token":
			if request.Method != http.MethodGet || request.URL.Query().Get("grant_type") != "authorization_code" || request.URL.Query().Get("code") != "referral-roundtrip-code" {
				http.Error(writer, "unexpected OAuth exchange", http.StatusBadRequest)
				return
			}
			_, _ = writer.Write([]byte(`{"access_token":"referral-roundtrip-access","openid":"referral-roundtrip-openid","scope":"snsapi_userinfo"}`))
		case "/sns/userinfo":
			if request.Method != http.MethodGet || request.URL.Query().Get("access_token") != "referral-roundtrip-access" || request.URL.Query().Get("openid") != "referral-roundtrip-openid" {
				http.Error(writer, "unexpected OAuth userinfo", http.StatusBadRequest)
				return
			}
			_, _ = writer.Write([]byte(`{"openid":"referral-roundtrip-openid","unionid":"referral-roundtrip-unionid","nickname":"授权回跳测试"}`))
		default:
			http.Error(writer, "unexpected OAuth path", http.StatusBadRequest)
		}
	}))
	defer weChat.Close()
	weChatURL, err := url.Parse(weChat.URL)
	if err != nil {
		t.Fatal(err)
	}
	originalTransport := http.DefaultTransport
	transport := &referralH5OAuthTransport{base: originalTransport, target: weChatURL}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	invite := "rfi_" + strings.Repeat("A", 43)
	var consumedState string
	for _, returnPath := range []string{
		"/referral?campaign=1",
		"/referral?campaign=1&invite=" + invite,
	} {
		start := referralH5OAuthRequest(t, application.handler, "/api/h5/wechat-pay/oauth/start?return_url="+url.QueryEscape(returnPath), "MicroMessenger Referral OAuth")
		if start.Code != http.StatusFound {
			t.Fatalf("start return=%q status=%d body=%s", returnPath, start.Code, start.Body.String())
		}
		authorization, parseErr := url.Parse(start.Header().Get("Location"))
		if parseErr != nil || authorization.Scheme != "https" || authorization.Host != "open.weixin.qq.com" || authorization.Path != "/connect/oauth2/authorize" {
			t.Fatalf("unexpected authorization location=%q err=%v", start.Header().Get("Location"), parseErr)
		}
		state := authorization.Query().Get("state")
		if state == "" {
			t.Fatal("OAuth start omitted state")
		}
		if consumedState == "" {
			consumedState = state
		}
		callback := referralH5OAuthRequest(t, application.handler, "/api/h5/wechat-pay/oauth/callback?state="+url.QueryEscape(state)+"&code=referral-roundtrip-code", "")
		if callback.Code != http.StatusFound || callback.Header().Get("Location") != returnPath {
			t.Fatalf("callback return=%q status=%d actual=%q", returnPath, callback.Code, callback.Header().Get("Location"))
		}
		cookies := callback.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != "aicrm_payment_session" || !cookies[0].Secure || !cookies[0].HttpOnly || !strings.HasPrefix(cookies[0].Value, "pays_") || len(cookies[0].Value) != 48 {
			t.Fatalf("callback did not issue one valid trusted session cookie")
		}
	}
	if transport.calls != 4 {
		t.Fatalf("provider calls=%d want 4", transport.calls)
	}
	replayed := referralH5OAuthRequest(t, application.handler, "/api/h5/wechat-pay/oauth/callback?state="+url.QueryEscape(consumedState)+"&code=referral-roundtrip-code", "")
	if replayed.Code != http.StatusUnauthorized || transport.calls != 4 {
		t.Fatalf("OAuth state replay status=%d provider_calls=%d", replayed.Code, transport.calls)
	}
	var consumed int
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM payment_h5_oauth_states WHERE return_path LIKE '/referral?campaign=1%' AND consumed_at IS NOT NULL`).Scan(&consumed); err != nil || consumed != 2 {
		t.Fatalf("stored and consumed OAuth state count=%d err=%v", consumed, err)
	}

	for _, unsafeReturn := range []string{
		"https://evil.example/referral?campaign=1",
		"/referral?campaign=1&invite=" + invite + "&next=/admin",
		"/referral?invite=" + invite + "&campaign=1",
		"/referral?campaign=0",
		"/referral?campaign=9223372036854775808",
	} {
		response := referralH5OAuthRequest(t, application.handler, "/api/h5/wechat-pay/oauth/start?return_url="+url.QueryEscape(unsafeReturn), "MicroMessenger Referral OAuth")
		if response.Code != http.StatusBadRequest || response.Header().Get("Location") != "" {
			t.Fatalf("unsafe return=%q status=%d location=%q", unsafeReturn, response.Code, response.Header().Get("Location"))
		}
	}
	var states int
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM payment_h5_oauth_states`).Scan(&states); err != nil || states != 2 {
		t.Fatalf("unsafe returns persisted OAuth state count=%d err=%v", states, err)
	}
	var participations, relationshipChanges int
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM referral_participations`).Scan(&participations); err != nil || participations != 0 {
		t.Fatalf("OAuth callback unexpectedly joined a Referral campaign count=%d err=%v", participations, err)
	}
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM referral_relationship_history`).Scan(&relationshipChanges); err != nil || relationshipChanges != 0 {
		t.Fatalf("OAuth callback unexpectedly changed Referral attribution count=%d err=%v", relationshipChanges, err)
	}
}

type referralH5OAuthTransport struct {
	base   http.RoundTripper
	target *url.URL
	calls  int
}

func (transport *referralH5OAuthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" || request.URL.Host != "api.weixin.qq.com" || (request.URL.Path != "/sns/oauth2/access_token" && request.URL.Path != "/sns/userinfo") || request.Method != http.MethodGet {
		return nil, fmt.Errorf("unexpected H5 OAuth provider request")
	}
	transport.calls++
	forwarded := request.Clone(request.Context())
	endpoint := *transport.target
	endpoint.Path = request.URL.Path
	endpoint.RawQuery = request.URL.RawQuery
	forwarded.URL = &endpoint
	forwarded.Host = ""
	forwarded.RequestURI = ""
	return transport.base.RoundTrip(forwarded)
}

func referralH5OAuthRequest(t *testing.T, handler http.Handler, path, userAgent string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if userAgent != "" {
		request.Header.Set("User-Agent", userAgent)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
