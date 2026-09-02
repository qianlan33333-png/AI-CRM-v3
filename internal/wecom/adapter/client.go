// Package adapter contains the opt-in HTTP implementation of the WeCom OAuth
// and JSSDK contracts. It never enables a provider by itself.
package adapter

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

const (
	productionAPIBase = "https://qyapi.weixin.qq.com"
	maxResponseBody   = 64 << 10
)

var (
	ErrUnavailable = errors.New("wecom provider unavailable")
	ErrResponse    = errors.New("wecom provider response rejected")
)

// Config is injected by the composition root. Secrets are intentionally only
// held in memory and no method in this package logs request parameters.
type Config struct {
	Enabled            bool
	CorpID             string
	AgentID            string
	Secret             string
	AdminCallbackURI   string
	SidebarCallbackURI string
	APIBase            string
	HTTPClient         *http.Client // explicit test injection also permits httptest bases
	Now                func() time.Time
	Random             func([]byte) error
	JSAPIList          []string
}

type credential struct {
	value     string
	expiresAt time.Time
}

// Client implements both the OAuth client and the typed JSSDK signer.
// Cache reads/writes are mutex-protected; it has neither a ticker nor a
// background goroutine.
type Client struct {
	config  Config
	apiBase *url.URL
	http    *http.Client
	mu      sync.Mutex
	tokens  map[string]credential
}

func New(config Config) (*Client, error) {
	if !config.Enabled {
		return &Client{config: config, http: config.HTTPClient, tokens: map[string]credential{}}, nil
	}
	if invalid(config.CorpID) || invalid(config.AgentID) || invalid(config.Secret) || !validCallback(config.AdminCallbackURI) || !validCallback(config.SidebarCallbackURI) {
		return nil, ErrUnavailable
	}
	base, err := validatedAPIBase(config.APIBase, config.HTTPClient != nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	} else {
		// Do not follow an untrusted provider redirect to another host. Copying
		// keeps injected test transports intact without mutating a shared client.
		copy := *client
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &copy
	}
	return &Client{config: config, apiBase: base, http: client, tokens: map[string]credential{}}, nil
}

func (client *Client) Ready() bool {
	return client != nil && client.config.Enabled && client.apiBase != nil && client.http != nil
}

func (client *Client) AuthorizationURL(_ context.Context, purpose wecom.OAuthPurpose, mode wecom.OAuthMode, state, _ string) (string, error) {
	if !client.Ready() || state == "" {
		return "", ErrUnavailable
	}
	callback := client.config.AdminCallbackURI
	if purpose == wecom.OAuthSidebar {
		callback = client.config.SidebarCallbackURI
	}
	values := url.Values{"appid": {client.config.CorpID}, "redirect_uri": {callback}, "state": {state}}
	switch {
	case purpose == wecom.OAuthAdmin && mode == wecom.OAuthModeQR:
		values.Set("agentid", client.config.AgentID)
		return "https://open.work.weixin.qq.com/wwopen/sso/qrConnect?" + values.Encode(), nil
	case (purpose == wecom.OAuthAdmin || purpose == wecom.OAuthSidebar) && mode == wecom.OAuthModeWeb:
		values.Set("response_type", "code")
		values.Set("scope", "snsapi_base")
		return "https://open.weixin.qq.com/connect/oauth2/authorize?" + values.Encode() + "#wechat_redirect", nil
	default:
		return "", ErrUnavailable
	}
}

func (client *Client) ExchangeCode(ctx context.Context, _ wecom.OAuthPurpose, _ wecom.OAuthMode, code string) (wecom.OAuthIdentity, error) {
	if !client.Ready() || invalid(code) {
		return wecom.OAuthIdentity{}, ErrUnavailable
	}
	token, err := client.accessToken(ctx)
	if err != nil {
		return wecom.OAuthIdentity{}, err
	}
	payload, err := client.request(ctx, "/cgi-bin/user/getuserinfo", url.Values{"access_token": {token}, "code": {code}})
	userID := payload.UserID
	if userID == "" {
		userID = payload.UserIDLower
	}
	if err != nil || userID == "" {
		return wecom.OAuthIdentity{}, ErrResponse
	}
	return wecom.OAuthIdentity{CorpID: client.config.CorpID, EmployeeID: userID}, nil
}

func (client *Client) ConfigForURL(ctx context.Context, rawURL string) (wecom.JSSDKConfig, error) {
	if !client.Ready() {
		return wecom.JSSDKConfig{}, ErrUnavailable
	}
	signedURL, err := exactNoFragmentURL(rawURL)
	if err != nil {
		return wecom.JSSDKConfig{}, ErrResponse
	}
	token, err := client.accessToken(ctx)
	if err != nil {
		return wecom.JSSDKConfig{}, err
	}
	corpTicket, err := client.ticket(ctx, token, "corp")
	if err != nil {
		return wecom.JSSDKConfig{}, err
	}
	agentTicket, err := client.ticket(ctx, token, "agent")
	if err != nil {
		return wecom.JSSDKConfig{}, err
	}
	config, err := client.sign(signedURL, corpTicket)
	if err != nil {
		return wecom.JSSDKConfig{}, err
	}
	agentConfig, err := client.sign(signedURL, agentTicket)
	if err != nil {
		return wecom.JSSDKConfig{}, err
	}
	return wecom.JSSDKConfig{CorpID: client.config.CorpID, AgentID: client.config.AgentID, Config: config, AgentConfig: agentConfig}, nil
}

func (client *Client) accessToken(ctx context.Context) (string, error) {
	if value, ok := client.cached("access_token"); ok {
		return value, nil
	}
	payload, err := client.request(ctx, "/cgi-bin/gettoken", url.Values{"corpid": {client.config.CorpID}, "corpsecret": {client.config.Secret}})
	if err != nil || payload.AccessToken == "" || payload.ExpiresIn <= 0 {
		return "", ErrResponse
	}
	client.store("access_token", payload.AccessToken, payload.ExpiresIn)
	return payload.AccessToken, nil
}

func (client *Client) ticket(ctx context.Context, token, kind string) (string, error) {
	key, path := "jsapi_ticket", "/cgi-bin/get_jsapi_ticket"
	query := url.Values{"access_token": {token}}
	if kind == "agent" {
		key, path = "agent_jsapi_ticket", "/cgi-bin/ticket/get"
		query.Set("type", "agent_config")
	}
	if value, ok := client.cached(key); ok {
		return value, nil
	}
	payload, err := client.request(ctx, path, query)
	if err != nil || payload.Ticket == "" || payload.ExpiresIn <= 0 {
		return "", ErrResponse
	}
	client.store(key, payload.Ticket, payload.ExpiresIn)
	return payload.Ticket, nil
}

func (client *Client) sign(signedURL, ticket string) (wecom.JSSDKSignature, error) {
	bytes := make([]byte, 16)
	if err := client.random()(bytes); err != nil {
		return wecom.JSSDKSignature{}, ErrUnavailable
	}
	nonce := hex.EncodeToString(bytes)
	timestamp := client.now().Unix()
	plain := "jsapi_ticket=" + ticket + "&noncestr=" + nonce + "&timestamp=" + itoa(timestamp) + "&url=" + signedURL
	sum := sha1.Sum([]byte(plain))
	apis := append([]string(nil), client.config.JSAPIList...)
	if len(apis) == 0 {
		apis = []string{"getCurExternalContact"}
	}
	return wecom.JSSDKSignature{Timestamp: timestamp, NonceStr: nonce, Signature: hex.EncodeToString(sum[:]), JSAPIList: apis}, nil
}

type response struct {
	ErrCode     json.RawMessage `json:"errcode"`
	AccessToken string          `json:"access_token"`
	UserID      string          `json:"UserId"`
	UserIDLower string          `json:"userid"`
	Ticket      string          `json:"ticket"`
	ExpiresIn   int64           `json:"expires_in"`
}

func (client *Client) request(ctx context.Context, path string, query url.Values) (response, error) {
	endpoint := *client.apiBase
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawQuery = query.Encode()
	requestCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return response{}, ErrUnavailable
	}
	resp, err := client.http.Do(req)
	if err != nil {
		return response{}, ErrUnavailable
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxResponseBody+1)
	body, err := io.ReadAll(limited)
	if err != nil || len(body) > maxResponseBody || resp.StatusCode < 200 || resp.StatusCode > 299 {
		return response{}, ErrResponse
	}
	var payload response
	if json.Unmarshal(body, &payload) != nil || !successErrCode(payload.ErrCode) {
		return response{}, ErrResponse
	}
	payload.AccessToken = strings.TrimSpace(payload.AccessToken)
	payload.UserID = strings.TrimSpace(payload.UserID)
	payload.UserIDLower = strings.TrimSpace(payload.UserIDLower)
	payload.Ticket = strings.TrimSpace(payload.Ticket)
	return payload, nil
}

func (client *Client) cached(key string) (string, bool) {
	client.mu.Lock()
	defer client.mu.Unlock()
	entry, ok := client.tokens[key]
	return entry.value, ok && entry.value != "" && entry.expiresAt.After(client.now())
}

func (client *Client) store(key, value string, expiresIn int64) {
	validFor := time.Duration(expiresIn)*time.Second - time.Minute
	if validFor < 0 {
		validFor = 0
	}
	client.mu.Lock()
	client.tokens[key] = credential{value: value, expiresAt: client.now().Add(validFor)}
	client.mu.Unlock()
}

func (client *Client) now() time.Time {
	if client.config.Now != nil {
		return client.config.Now().UTC()
	}
	return time.Now().UTC()
}

func (client *Client) random() func([]byte) error {
	if client.config.Random != nil {
		return client.config.Random
	}
	return func(value []byte) error { _, err := rand.Read(value); return err }
}

func invalid(value string) bool { return value == "" || strings.TrimSpace(value) != value }

func validCallback(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Fragment == ""
}

func validatedAPIBase(raw string, testTransport bool) (*url.URL, error) {
	if raw == "" {
		raw = productionAPIBase
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Host == "" {
		return nil, ErrUnavailable
	}
	if parsed.Scheme == "https" && parsed.Host == "qyapi.weixin.qq.com" {
		return parsed, nil
	}
	if testTransport && parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost" || parsed.Hostname() == "::1") {
		return parsed, nil
	}
	return nil, ErrUnavailable
}

func exactNoFragmentURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", ErrResponse
	}
	parsed.Fragment = ""
	return parsed.String(), nil
}

func successErrCode(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var number int64
	if json.Unmarshal(raw, &number) == nil {
		return number == 0
	}
	var text string
	return json.Unmarshal(raw, &text) == nil && text == "0"
}

func itoa(value int64) string { return strconv.FormatInt(value, 10) }

var _ wecom.OAuthClient = (*Client)(nil)
var _ wecom.JSSDKSigner = (*Client)(nil)
