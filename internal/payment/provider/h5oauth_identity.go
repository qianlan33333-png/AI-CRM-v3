package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

var ErrH5OAuthIdentity = errors.New("wechat H5 OAuth identity verification failed")

type H5OAuthIdentity struct {
	enabled                 bool
	appID, secret, appScope string
	callbackURL, apiBase    string
	client                  *http.Client
}

func NewH5OAuthIdentity(enabled bool, appID, secret, appScope, callbackURL string) (*H5OAuthIdentity, error) {
	if enabled {
		parsed, err := url.ParseRequestURI(callbackURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "/api/h5/wechat-pay/oauth/callback" || parsed.RawQuery != "" || parsed.Fragment != "" || !safeH5OAuthValue(appID, 128) || !safeH5OAuthValue(secret, 4096) || appScope != "wechat-app:"+appID {
			return nil, ErrH5OAuthIdentity
		}
	}
	return &H5OAuthIdentity{enabled: enabled, appID: appID, secret: secret, appScope: appScope, callbackURL: callbackURL, apiBase: "https://api.weixin.qq.com", client: &http.Client{Timeout: 10 * time.Second}}, nil
}

func (p *H5OAuthIdentity) Enabled() bool { return p != nil && p.enabled }

func (p *H5OAuthIdentity) AuthorizationURL(state string) string {
	values := url.Values{"appid": {p.appID}, "redirect_uri": {p.callbackURL}, "response_type": {"code"}, "scope": {"snsapi_base"}, "state": {state}}
	return "https://open.weixin.qq.com/connect/oauth2/authorize?" + values.Encode() + "#wechat_redirect"
}

func (p *H5OAuthIdentity) Exchange(ctx context.Context, code string) (identitydomain.VerifiedFact, error) {
	if !p.Enabled() || !safeH5OAuthValue(code, 512) {
		return identitydomain.VerifiedFact{}, ErrH5OAuthIdentity
	}
	values := url.Values{"appid": {p.appID}, "secret": {p.secret}, "code": {code}, "grant_type": {"authorization_code"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiBase+"/sns/oauth2/access_token?"+values.Encode(), nil)
	if err != nil {
		return identitydomain.VerifiedFact{}, ErrH5OAuthIdentity
	}
	response, err := p.client.Do(request)
	if err != nil {
		return identitydomain.VerifiedFact{}, ErrH5OAuthIdentity
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	var payload struct {
		OpenID    string `json:"openid"`
		ErrorCode int    `json:"errcode"`
		Scope     string `json:"scope"`
	}
	if err != nil || response.StatusCode != http.StatusOK || json.Unmarshal(body, &payload) != nil || payload.ErrorCode != 0 || !safeH5OAuthValue(payload.OpenID, 512) || payload.Scope != "snsapi_base" && payload.Scope != "snsapi_userinfo" {
		return identitydomain.VerifiedFact{}, ErrH5OAuthIdentity
	}
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: p.appScope, Value: payload.OpenID, Source: "wechat.payment.h5_oauth"})
	if err != nil {
		return identitydomain.VerifiedFact{}, ErrH5OAuthIdentity
	}
	return fact, nil
}

func safeH5OAuthValue(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\x00")
}
