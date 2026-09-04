package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

func TestH5OAuthIdentityExchangesOnlyTrustedProviderResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("code") != "trusted-code" || request.URL.Query().Get("secret") != "secret" {
			t.Fatalf("unexpected exchange query=%s", request.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"openid":"oa-openid","scope":"snsapi_base"}`))
	}))
	defer server.Close()
	provider, err := NewH5OAuthIdentity(true, "wx-oa", "secret", "wechat-app:wx-oa", "https://crm.example.test/api/h5/wechat-pay/oauth/callback")
	if err != nil {
		t.Fatal(err)
	}
	provider.apiBase, provider.client = server.URL, server.Client()
	fact, err := provider.Exchange(context.Background(), "trusted-code")
	if err != nil || fact.Reference().Kind != identitydomain.KindOAOpenID || fact.Reference().Scope != "wechat-app:wx-oa" {
		t.Fatalf("fact=%+v err=%v", fact.Reference(), err)
	}
	if authorization := provider.AuthorizationURL("state-token"); !strings.Contains(authorization, "scope=snsapi_base") || !strings.Contains(authorization, "state=state-token") {
		t.Fatalf("authorization=%q", authorization)
	}
}
