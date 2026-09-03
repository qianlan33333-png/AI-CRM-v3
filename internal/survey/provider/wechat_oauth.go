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
	scope                                   string
	apiBase                                 string
	client                                  *http.Client
}

func NewWeChatOAuth(enabled bool, appID, secret, openPlatformID, callback, scope string) (*WeChatOAuth, error) {
	if enabled {
		parsedCallback, parseErr := url.ParseRequestURI(callback)
		if !validConfigValue(appID, 128) || !validConfigValue(secret, 4096) || !validConfigValue(openPlatformID, 256) ||
			parseErr != nil || parsedCallback.Scheme != "https" || parsedCallback.Host == "" || parsedCallback.Path != "/api/h5/surveys/oauth/callback" || parsedCallback.RawQuery != "" || parsedCallback.Fragment != "" || scope != "snsapi_userinfo" {
			return nil, errors.New("survey OAuth configuration incomplete")
		}
	}
	return &WeChatOAuth{enabled: enabled, appID: appID, secret: secret, openPlatformID: openPlatformID, callback: callback, scope: scope, apiBase: "https://api.weixin.qq.com", client: &http.Client{Timeout: 10 * time.Second}}, nil
}
func (p *WeChatOAuth) Enabled() bool { return p != nil && p.enabled }
func (p *WeChatOAuth) AuthorizationURL(state string) string {
	values := url.Values{"appid": {p.appID}, "redirect_uri": {p.callback}, "response_type": {"code"}, "scope": {p.scope}, "state": {state}}
	return "https://open.weixin.qq.com/connect/oauth2/authorize?" + values.Encode() + "#wechat_redirect"
}
func (p *WeChatOAuth) Exchange(ctx context.Context, code string) (identitydomain.VerifiedFact, error) {
	if !p.Enabled() || strings.TrimSpace(code) != code || code == "" || len(code) > 512 {
		return identitydomain.VerifiedFact{}, errors.New("survey OAuth unavailable")
	}
	values := url.Values{"appid": {p.appID}, "secret": {p.secret}, "code": {code}, "grant_type": {"authorization_code"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiBase+"/sns/oauth2/access_token?"+values.Encode(), nil)
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
		OpenID         string `json:"openid"`
		UnionID        string `json:"unionid"`
		ErrorCode      int    `json:"errcode"`
		Scope          string `json:"scope"`
		IsSnapshotUser int    `json:"is_snapshotuser"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.ErrorCode != 0 || payload.IsSnapshotUser != 0 || !validProviderValue(payload.OpenID) || payload.UnionID != "" && !validProviderValue(payload.UnionID) || !containsScope(payload.Scope, "snsapi_userinfo") || strings.HasPrefix(strings.ToLower(payload.OpenID), "snapshot") {
		return identitydomain.VerifiedFact{}, errors.New("survey OAuth unavailable")
	}
	input := identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:" + p.appID, Value: payload.OpenID, Source: "wechat.survey.oauth"}
	if payload.UnionID != "" {
		input.Kind = identitydomain.KindUnionID
		input.Scope = "wechat-open-platform:" + p.openPlatformID
		input.Value = payload.UnionID
	}
	return identitydomain.NewVerifiedFact(input)
}

func containsScope(raw, expected string) bool {
	for _, value := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		if value == expected {
			return true
		}
	}
	return false
}

func validProviderValue(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= 512
}

func validConfigValue(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\x00")
}
