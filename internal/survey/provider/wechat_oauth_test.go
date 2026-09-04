package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

func TestAuthorizationURLUsesInteractiveScopeAndExactCallback(t *testing.T) {
	provider, err := NewWeChatOAuth(true, "wx-app", "secret", "platform", "https://id-dev.youcangogogo.com/api/h5/surveys/oauth/callback", "snsapi_userinfo")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(provider.AuthorizationURL("state-token"))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "open.weixin.qq.com" || parsed.Query().Get("scope") != "snsapi_userinfo" || parsed.Query().Get("redirect_uri") != "https://id-dev.youcangogogo.com/api/h5/surveys/oauth/callback" || parsed.Query().Get("state") != "state-token" {
		t.Fatalf("authorization URL=%s", parsed.Redacted())
	}
}

func TestExchangePrefersUnionIDAndFallsBackToOfficialAccountOpenID(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		kind          identitydomain.Kind
		scope         string
	}{
		{"unionid", `{"access_token":"not-retained","openid":"oa-open","unionid":"union-one","scope":"snsapi_userinfo"}`, identitydomain.KindUnionID, "wechat-open-platform:platform"},
		{"openid", `{"access_token":"not-retained","openid":"oa-open","scope":"snsapi_base,snsapi_userinfo"}`, identitydomain.KindOAOpenID, "wechat-app:wx-app"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/sns/oauth2/access_token" || r.URL.Query().Get("code") != "provider-code" || r.URL.Query().Get("secret") != "secret" {
					t.Fatalf("request=%s", r.URL.Redacted())
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.payload))
			}))
			defer server.Close()
			provider, err := NewWeChatOAuth(true, "wx-app", "secret", "platform", "https://example.test/api/h5/surveys/oauth/callback", "snsapi_userinfo")
			if err != nil {
				t.Fatal(err)
			}
			provider.apiBase, provider.client = server.URL, server.Client()
			fact, err := provider.Exchange(context.Background(), "provider-code")
			if err != nil {
				t.Fatal(err)
			}
			if ref := fact.Reference(); ref.Kind != test.kind || ref.Scope != test.scope || ref.Assurance != identitydomain.AssuranceVerified {
				t.Fatalf("reference=%+v", ref)
			}
		})
	}
}

func TestExchangeRejectsWrongScopeProviderErrorsAndSnapshotIdentity(t *testing.T) {
	for _, payload := range []string{
		`{"openid":"oa-open","scope":"snsapi_base"}`,
		`{"errcode":40029,"errmsg":"invalid code"}`,
		`{"openid":"virtual-open","unionid":"virtual-union","scope":"snsapi_userinfo","is_snapshotuser":1}`,
		`{"openid":"snapshot-user","scope":"snsapi_userinfo"}`,
		`{"openid":" oa-open ","scope":"snsapi_userinfo"}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(payload)) }))
		provider, err := NewWeChatOAuth(true, "wx-app", "secret", "platform", "https://example.test/api/h5/surveys/oauth/callback", "snsapi_userinfo")
		if err != nil {
			t.Fatal(err)
		}
		provider.apiBase, provider.client = server.URL, server.Client()
		if _, err = provider.Exchange(context.Background(), "provider-code"); err == nil {
			server.Close()
			t.Fatalf("accepted payload shape %q", payload)
		}
		server.Close()
	}
}

func TestEnabledOAuthRequiresOpenPlatformScopeAndInteractiveScope(t *testing.T) {
	callback := "https://example.test/api/h5/surveys/oauth/callback"
	if _, err := NewWeChatOAuth(true, "wx-app", "secret", "", callback, "snsapi_userinfo"); err == nil {
		t.Fatal("missing open platform scope accepted")
	}
	if _, err := NewWeChatOAuth(true, "wx-app", "secret", "platform", callback, "snsapi_base"); err == nil {
		t.Fatal("non-interactive OAuth scope accepted")
	}
	if _, err := NewWeChatOAuth(true, "wx-app", "secret\n", "platform", callback, "snsapi_userinfo"); err == nil {
		t.Fatal("unsafe OAuth secret accepted")
	}
	if _, err := NewWeChatOAuth(true, "wx-app", "secret", "platform", "https://example.test/other", "snsapi_userinfo"); err == nil {
		t.Fatal("unexpected OAuth callback accepted")
	}
}
