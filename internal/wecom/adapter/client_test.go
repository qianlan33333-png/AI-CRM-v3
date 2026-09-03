package adapter

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

func TestClientOAuthURLsAndExchange(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seen = append(seen, request.URL.Path+"?"+request.URL.RawQuery)
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			if request.URL.Query().Get("corpid") != "wx corp" || request.URL.Query().Get("corpsecret") != "secret value" {
				t.Fatalf("token query=%q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"access-token","expires_in":120}`))
		case "/cgi-bin/user/getuserinfo":
			if request.URL.Query().Get("access_token") != "access-token" || request.URL.Query().Get("code") != "provider code" {
				t.Fatalf("userinfo query=%q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"UserId":"employee"}`))
		default:
			t.Fatal("unexpected endpoint")
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	qr, err := client.AuthorizationURL(context.Background(), wecom.OAuthAdmin, wecom.OAuthModeQR, "state+/", "")
	if err != nil {
		t.Fatal(err)
	}
	assertAuthorization(t, qr, "open.work.weixin.qq.com", "/wwopen/sso/qrConnect", map[string]string{"appid": "wx corp", "agentid": "10001", "redirect_uri": "https://crm.example/auth/wecom/callback", "state": "state+/"})
	web, err := client.AuthorizationURL(context.Background(), wecom.OAuthSidebar, wecom.OAuthModeWeb, "state+/", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(web, "#wechat_redirect") {
		t.Fatalf("web URL missing fragment: %s", web)
	}
	assertAuthorization(t, strings.TrimSuffix(web, "#wechat_redirect"), "open.weixin.qq.com", "/connect/oauth2/authorize", map[string]string{"appid": "wx corp", "redirect_uri": "https://crm.example/api/sidebar/oauth/callback", "state": "state+/", "response_type": "code", "scope": "snsapi_base"})
	identity, err := client.ExchangeCode(context.Background(), wecom.OAuthAdmin, wecom.OAuthModeQR, "provider code")
	if err != nil || identity != (wecom.OAuthIdentity{CorpID: "wx corp", EmployeeID: "employee"}) {
		t.Fatalf("identity=%+v err=%v", identity, err)
	}
	if len(seen) != 2 {
		t.Fatalf("calls=%v", seen)
	}
}

func TestClientSignsBothTicketsCachesAndRefreshes(t *testing.T) {
	now := testNow
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls[request.URL.Path]++
		query := request.URL.Query()
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			if query.Get("corpid") != "wx corp" || query.Get("corpsecret") != "secret value" {
				t.Fatalf("token query=%s", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"token","expires_in":120}`))
		case "/cgi-bin/get_jsapi_ticket":
			if query.Get("access_token") != "token" || query.Get("type") != "" {
				t.Fatalf("corp ticket query=%s", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"ticket":"corp-ticket","expires_in":120}`))
		case "/cgi-bin/ticket/get":
			if query.Get("access_token") != "token" || query.Get("type") != "agent_config" {
				t.Fatalf("agent ticket query=%s", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"ticket":"agent-ticket","expires_in":120}`))
		default:
			t.Fatalf("unexpected path=%s", request.URL.Path)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return now })
	config, err := client.ConfigForURL(context.Background(), "https://crm.example/sidebar?x=1")
	if err != nil {
		t.Fatal(err)
	}
	if config.CorpID != "wx corp" || config.AgentID != "10001" || config.Config.Timestamp != testNow.Unix() || config.Config.NonceStr != strings.Repeat("01", 16) || len(config.Config.JSAPIList) != 1 {
		t.Fatalf("config=%+v", config)
	}
	assertSignature(t, config.Config.Signature, "corp-ticket", config.Config.NonceStr, config.Config.Timestamp, "https://crm.example/sidebar?x=1")
	assertSignature(t, config.AgentConfig.Signature, "agent-ticket", config.AgentConfig.NonceStr, config.AgentConfig.Timestamp, "https://crm.example/sidebar?x=1")
	if _, err = client.ConfigForURL(context.Background(), "https://crm.example/sidebar?x=1"); err != nil {
		t.Fatal(err)
	}
	if calls["/cgi-bin/gettoken"] != 1 || calls["/cgi-bin/get_jsapi_ticket"] != 1 || calls["/cgi-bin/ticket/get"] != 1 {
		t.Fatalf("cache calls=%v", calls)
	}
	now = now.Add(61 * time.Second)
	if _, err = client.ConfigForURL(context.Background(), "https://crm.example/sidebar?x=1"); err != nil {
		t.Fatal(err)
	}
	if calls["/cgi-bin/gettoken"] != 2 || calls["/cgi-bin/get_jsapi_ticket"] != 2 || calls["/cgi-bin/ticket/get"] != 2 {
		t.Fatalf("refresh calls=%v", calls)
	}
}

func TestClientRejectsProviderErrorsOversizeAndDoesNotExposeSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/cgi-bin/gettoken" {
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"token-secret","expires_in":120}`))
			return
		}
		_, _ = writer.Write([]byte(strings.Repeat("x", maxResponseBody+1)))
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	_, err := client.ExchangeCode(context.Background(), wecom.OAuthAdmin, wecom.OAuthModeQR, "code-secret")
	if !errors.Is(err, ErrResponse) {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(err.Error(), "token-secret") || strings.Contains(err.Error(), "code-secret") || strings.Contains(err.Error(), "secret value") {
		t.Fatalf("secret leaked in error %q", err)
	}
	if _, err = New(Config{Enabled: true, CorpID: "c", AgentID: "a", Secret: "s", AdminCallbackURI: "http://bad", SidebarCallbackURI: "https://crm.example/callback"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("bad config err=%v", err)
	}
	providerError := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/cgi-bin/gettoken" {
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"token-secret","expires_in":120}`))
			return
		}
		_, _ = writer.Write([]byte(`{"errcode":40029,"errmsg":"invalid code-secret"}`))
	}))
	defer providerError.Close()
	client = newTestClient(t, providerError, func() time.Time { return testNow })
	_, err = client.ExchangeCode(context.Background(), wecom.OAuthAdmin, wecom.OAuthModeQR, "code-secret")
	if !errors.Is(err, ErrResponse) || strings.Contains(err.Error(), "code-secret") {
		t.Fatalf("provider error=%v", err)
	}
}

func TestCustomerDirectoryProviderDisabledMakesZeroCalls(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	client, err := New(Config{Enabled: false, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if client.DirectoryReady() {
		t.Fatal("disabled directory must not be ready")
	}
	if _, err = client.ListContactStaff(context.Background()); !errors.Is(err, wecomport.ErrDirectoryDisabled) {
		t.Fatalf("err=%v", err)
	}
	if _, err = client.BatchExternalContacts(context.Background(), "staff", "", 100); !errors.Is(err, wecomport.ErrDirectoryDisabled) {
		t.Fatalf("err=%v", err)
	}
	if calls != 0 {
		t.Fatalf("network calls=%d", calls)
	}
}

func TestCustomerDirectoryProviderListsStaffAndBatchPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			if request.URL.Query().Get("corpsecret") != "contact secret" {
				t.Fatalf("secret route=%q", request.URL.Query().Get("corpsecret"))
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":120}`))
		case "/cgi-bin/externalcontact/get_follow_user_list":
			_, _ = writer.Write([]byte(`{"errcode":0,"follow_user":["staff-1","staff-1","staff-2"]}`))
		case "/cgi-bin/externalcontact/batch/get_by_user":
			if request.Method != http.MethodPost || request.URL.Query().Get("access_token") != "contact-token" {
				t.Fatalf("request=%s %s", request.Method, request.URL.String())
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"next_cursor":"next-1","external_contact_list":[{"external_contact":{"external_userid":"ext-1","name":"Alice","avatar":"https://example/avatar","type":1,"gender":2,"corp_name":"Example","unionid":"union-ignored-here"}}]}`))
		default:
			t.Fatalf("unexpected path=%s", request.URL.Path)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	client.config.ContactSecret = "contact secret"
	staff, err := client.ListContactStaff(context.Background())
	if err != nil || len(staff) != 2 {
		t.Fatalf("staff=%v err=%v", staff, err)
	}
	page, err := client.BatchExternalContacts(context.Background(), "staff-1", "cursor-1", 100)
	if err != nil || page.NextCursor != "next-1" || len(page.Contacts) != 1 || page.Contacts[0].ExternalUserID != "ext-1" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

var testNow = time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)

func newTestClient(t *testing.T, server *httptest.Server, now func() time.Time) *Client {
	t.Helper()
	client, err := New(Config{Enabled: true, CorpID: "wx corp", AgentID: "10001", Secret: "secret value", AdminCallbackURI: "https://crm.example/auth/wecom/callback", SidebarCallbackURI: "https://crm.example/api/sidebar/oauth/callback", APIBase: server.URL, HTTPClient: server.Client(), Now: now, Random: func(value []byte) error {
		for i := range value {
			value[i] = 1
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func assertAuthorization(t *testing.T, raw, host, path string, want map[string]string) {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "https" || parsed.Host != host || parsed.Path != path {
		t.Fatalf("url=%s", raw)
	}
	for key, value := range want {
		if parsed.Query().Get(key) != value {
			t.Fatalf("%s=%q want %q URL=%s", key, parsed.Query().Get(key), value, raw)
		}
	}
}

func assertSignature(t *testing.T, signature, ticket, nonce string, timestamp int64, signedURL string) {
	t.Helper()
	plain := "jsapi_ticket=" + ticket + "&noncestr=" + nonce + "&timestamp=" + strconvFormat(timestamp) + "&url=" + signedURL
	sum := sha1.Sum([]byte(plain))
	if signature != hex.EncodeToString(sum[:]) {
		t.Fatalf("signature=%s want=%x", signature, sum)
	}
}

func strconvFormat(value int64) string { return strconv.FormatInt(value, 10) }
