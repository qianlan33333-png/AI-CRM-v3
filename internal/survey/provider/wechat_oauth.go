package provider

import (
	"context"
	"encoding/json"
	"errors"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type WeChatOAuth struct {
	enabled                                 bool
	appID, secret, openPlatformID, callback string
	client                                  *http.Client
}

func NewWeChatOAuth(enabled bool, appID, secret, openPlatformID, callback string) (*WeChatOAuth, error) {
	if enabled && (appID == "" || secret == "" || openPlatformID == "" || !strings.HasPrefix(callback, "https://")) {
		return nil, errors.New("survey OAuth configuration incomplete")
	}
	return &WeChatOAuth{enabled: enabled, appID: appID, secret: secret, openPlatformID: openPlatformID, callback: callback, client: &http.Client{Timeout: 10 * time.Second}}, nil
}
func (p *WeChatOAuth) Enabled() bool { return p != nil && p.enabled }
func (p *WeChatOAuth) AuthorizationURL(state string) string {
	values := url.Values{"appid": {p.appID}, "redirect_uri": {p.callback}, "response_type": {"code"}, "scope": {"snsapi_base"}, "state": {state}}
	return "https://open.weixin.qq.com/connect/oauth2/authorize?" + values.Encode() + "#wechat_redirect"
}
func (p *WeChatOAuth) Exchange(ctx context.Context, code string) (identitydomain.VerifiedFact, error) {
	if !p.Enabled() || strings.TrimSpace(code) != code || code == "" || len(code) > 512 {
		return identitydomain.VerifiedFact{}, errors.New("survey OAuth unavailable")
	}
	values := url.Values{"appid": {p.appID}, "secret": {p.secret}, "code": {code}, "grant_type": {"authorization_code"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.weixin.qq.com/sns/oauth2/access_token?"+values.Encode(), nil)
	if err != nil {
		return identitydomain.VerifiedFact{}, errors.New("survey OAuth unavailable")
	}
	response, err := p.client.Do(request)
	if err != nil {
		return identitydomain.VerifiedFact{}, errors.New("survey OAuth unavailable")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil || response.StatusCode != 200 {
		return identitydomain.VerifiedFact{}, errors.New("survey OAuth unavailable")
	}
	var payload struct {
		OpenID    string `json:"openid"`
		UnionID   string `json:"unionid"`
		ErrorCode int    `json:"errcode"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.ErrorCode != 0 {
		return identitydomain.VerifiedFact{}, errors.New("survey OAuth unavailable")
	}
	input := identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:" + p.appID, Value: payload.OpenID, Source: "wechat.survey.oauth"}
	if payload.UnionID != "" {
		input.Kind = identitydomain.KindUnionID
		input.Scope = "wechat-open-platform:" + p.openPlatformID
		input.Value = payload.UnionID
	}
	return identitydomain.NewVerifiedFact(input)
}
