package adapter

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
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

func TestDirectoryClientDoesNotRequireOAuthConfiguration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			if request.URL.Query().Get("corpid") != "corp" || request.URL.Query().Get("corpsecret") != "contact-secret" {
				t.Fatalf("token query=%s", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":120}`))
		case "/cgi-bin/externalcontact/get_contact_way":
			_, _ = writer.Write([]byte(`{"errcode":0,"contact_way":{"config_id":"config-1","qr_code":"https://wework.qpic.cn/wwpic/example"}}`))
		default:
			t.Fatalf("unexpected path=%s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "contact-secret", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil || !client.DirectoryReady() {
		t.Fatalf("directory client ready=%v err=%v", client != nil && client.DirectoryReady(), err)
	}
	asset, err := client.GetContactWay(context.Background(), "config-1")
	if err != nil || asset.ProviderAssetRef != "config-1" || asset.URL == "" {
		t.Fatalf("asset=%+v err=%v", asset, err)
	}
	if _, err = client.AuthorizationURL(context.Background(), wecom.OAuthAdmin, wecom.OAuthModeQR, "state", ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("directory-only OAuth err=%v", err)
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
			// Production pages with 100 contacts can legitimately exceed the old
			// 64 KiB OAuth-oriented limit. Unknown Provider fields must remain
			// safely ignored without making the response unbounded.
			payload := `{"errcode":0,"next_cursor":"next-1","external_contact_list":[{"external_contact":{"external_userid":"ext-1","name":"Alice","avatar":"https://example/avatar","type":1,"gender":2,"corp_name":"Example","unionid":"union-ignored-here"},"follow_info":[{"userid":"staff-1","tags":[{"tag_id":"tag-1","tag_name":"重点客户","type":1}]}]}],"provider_padding":"` + strings.Repeat("x", 70<<10) + `"}`
			_, _ = writer.Write([]byte(payload))
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
	if len(page.Contacts[0].FollowInfo) != 1 || page.Contacts[0].FollowInfo[0].EmployeeID != "staff-1" || len(page.Contacts[0].FollowInfo[0].Tags) != 1 || page.Contacts[0].FollowInfo[0].Tags[0].ProviderTagID != "tag-1" {
		t.Fatalf("follow info=%+v", page.Contacts[0].FollowInfo)
	}
}

func TestCustomerDirectoryProviderClassifiesProviderReadFailures(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		body          string
		wantCode      string
		wantRetryable bool
	}{
		{name: "permission", status: http.StatusOK, body: `{"errcode":48001}`, wantCode: "provider_permission_denied"},
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{"errcode":45009}`, wantCode: "provider_rate_limited", wantRetryable: true},
		{name: "unavailable", status: http.StatusServiceUnavailable, body: `{"errcode":-1}`, wantCode: "provider_unavailable", wantRetryable: true},
		{name: "invalid response", status: http.StatusOK, body: `{"errcode":0,"external_contact_list":[{"external_contact":{"external_userid":""}}]}`, wantCode: "provider_response_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/cgi-bin/gettoken":
					_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":7200}`))
				case "/cgi-bin/externalcontact/batch/get_by_user":
					writer.WriteHeader(test.status)
					_, _ = writer.Write([]byte(test.body))
				default:
					t.Fatalf("unexpected path=%s", request.URL.Path)
				}
			}))
			defer server.Close()
			client := newTestClient(t, server, func() time.Time { return testNow })
			client.config.ContactSecret = "contact secret"
			_, err := client.BatchExternalContacts(context.Background(), "staff-1", "", 100)
			var failure wecomport.DirectoryFailure
			if err == nil || !errors.As(err, &failure) || failure.DirectoryFailureCode() != test.wantCode || failure.DirectoryFailureRetryable() != test.wantRetryable {
				t.Fatalf("err=%v failure=%v", err, failure)
			}
			if strings.Contains(err.Error(), "contact-token") {
				t.Fatalf("provider token leaked in error=%q", err)
			}
		})
	}
}

func TestCustomerDirectoryProviderRefreshesExpiredReadTokenOnce(t *testing.T) {
	for name, persistent := range map[string]bool{"recovers": false, "persistent": true} {
		t.Run(name, func(t *testing.T) {
			tokenCalls, readCalls := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/cgi-bin/gettoken":
					tokenCalls++
					_, _ = writer.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":7200}`))
				case "/cgi-bin/externalcontact/get_follow_user_list":
					readCalls++
					if readCalls == 1 || persistent {
						_, _ = writer.Write([]byte(`{"errcode":40014}`))
						return
					}
					_, _ = writer.Write([]byte(`{"errcode":0,"follow_user":["staff-1"]}`))
				default:
					t.Fatalf("unexpected path=%s", request.URL.Path)
				}
			}))
			defer server.Close()
			client := newTestClient(t, server, func() time.Time { return testNow })
			client.config.ContactSecret = "contact secret"
			staff, err := client.ListContactStaff(context.Background())
			if !persistent {
				if err != nil || len(staff) != 1 || tokenCalls != 2 || readCalls != 2 {
					t.Fatalf("staff=%v err=%v tokens=%d reads=%d", staff, err, tokenCalls, readCalls)
				}
				return
			}
			var failure wecomport.DirectoryFailure
			if err == nil || !errors.As(err, &failure) || failure.DirectoryFailureCode() != "provider_credentials_invalid" || failure.DirectoryFailureRetryable() || tokenCalls != 2 || readCalls != 2 {
				t.Fatalf("err=%v tokens=%d reads=%d", err, tokenCalls, readCalls)
			}
		})
	}
}

func TestListTagCatalogUsesNarrowPostAndPreservesProviderFacts(t *testing.T) {
	for name, response := range map[string]string{
		"missing":   `{"errcode":0}`,
		"null":      `{"errcode":0,"tag_group":null}`,
		"empty":     `{"errcode":0,"tag_group":[]}`,
		"tag-null":  `{"errcode":0,"tag_group":[{"group_id":"g","group_name":"group","order":-1,"tag":null}]}`,
		"raw-facts": `{"errcode":0,"tag_group":[{"group_id":"g","group_name":"group","order":1,"tag":[{"id":"dup","name":"one","order":2,"deleted":false},{"id":"dup","name":"gone","order":3,"deleted":true}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/cgi-bin/gettoken":
					if r.Method != http.MethodGet || r.URL.Query().Get("corpsecret") != "secret" {
						t.Fatal("unexpected token request")
					}
					_, _ = w.Write([]byte(`{"errcode":0,"access_token":"token","expires_in":7200}`))
				case "/cgi-bin/externalcontact/get_corp_tag_list":
					if r.Method != http.MethodPost || r.URL.Query().Get("access_token") != "token" {
						t.Fatal("unexpected catalog request")
					}
					body, _ := io.ReadAll(r.Body)
					if string(body) != `{}` {
						t.Fatalf("catalog body=%q", body)
					}
					_, _ = w.Write([]byte(response))
				default:
					t.Fatal("unexpected path")
				}
			}))
			defer server.Close()
			client, err := New(Config{Enabled: true, CorpID: "corp", AgentID: "1", Secret: "secret", AdminCallbackURI: "https://id-dev.youcangogogo.com/auth/wecom/callback", SidebarCallbackURI: "https://id-dev.youcangogogo.com/api/sidebar/oauth/callback", APIBase: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			groups, err := client.ListTagCatalog(context.Background())
			if name == "missing" || name == "null" {
				var failure *CatalogReadError
				if err == nil || !errors.As(err, &failure) || !failure.CallAttempted {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if name == "empty" && (groups == nil || len(groups) != 0) {
				t.Fatalf("groups=%#v", groups)
			}
			if name == "tag-null" && (len(groups) != 1 || groups[0].Tags == nil || len(groups[0].Tags) != 0 || groups[0].Order != -1) {
				t.Fatalf("groups=%#v", groups)
			}
			if name == "raw-facts" && (len(groups) != 1 || len(groups[0].Tags) != 2 || groups[0].Tags[1].Deleted != true) {
				t.Fatalf("groups=%#v", groups)
			}
		})
	}
}

func TestListTagCatalogTokenFailureIsPreCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cgi-bin/gettoken" {
			t.Fatal("catalog endpoint should not be called")
		}
		_, _ = w.Write([]byte(`{"errcode":40001}`))
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	_, err := client.ListTagCatalog(context.Background())
	var failure *CatalogReadError
	if err == nil || !errors.As(err, &failure) || failure.CallAttempted {
		t.Fatalf("err=%v failure=%+v", err, failure)
	}
}

func TestChannelContactWayLifecycleAndWelcomeAttachments(t *testing.T) {
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		if r.URL.Path == "/cgi-bin/gettoken" {
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":120}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/cgi-bin/externalcontact/get_contact_way":
			if !strings.Contains(string(body), `"config_id":"cw-1"`) {
				t.Fatalf("get body=%s", body)
			}
			_, _ = w.Write([]byte(`{"errcode":0,"contact_way":{"config_id":"cw-1","qr_code":"https://wework.qpic.cn/wwpic/1"}}`))
		case "/cgi-bin/externalcontact/update_contact_way":
			if !strings.Contains(string(body), `"config_id":"cw-1"`) || !strings.Contains(string(body), `"state":"campaign"`) {
				t.Fatalf("update body=%s", body)
			}
			_, _ = w.Write([]byte(`{"errcode":0}`))
		case "/cgi-bin/externalcontact/del_contact_way":
			_, _ = w.Write([]byte(`{"errcode":0}`))
		case "/cgi-bin/externalcontact/send_welcome_msg":
			if !strings.Contains(string(body), `"msgtype":"image"`) || !strings.Contains(string(body), `"media_id":"media-1"`) || !strings.Contains(string(body), `"msgtype":"link"`) || !strings.Contains(string(body), `"url":"https://work.weixin.qq.com/gm/`) {
				t.Fatalf("welcome body=%s", body)
			}
			_, _ = w.Write([]byte(`{"errcode":0}`))
		default:
			t.Fatalf("unexpected path=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	client.config.ContactSecret = "contact secret"
	request := wecomport.AcquisitionAssetRequest{Name: "Campaign", State: "campaign", StaffUserIDs: []string{"staff-1"}}
	updated, err := client.UpdateContactWay(context.Background(), "cw-1", request)
	if err != nil || updated.ProviderAssetRef != "cw-1" || calls["/cgi-bin/externalcontact/update_contact_way"] != 1 || calls["/cgi-bin/externalcontact/get_contact_way"] != 1 {
		t.Fatalf("updated=%+v calls=%v err=%v", updated, calls, err)
	}
	if err = client.SendWelcomeMessage(context.Background(), "welcome-code", "欢迎", []wecomport.WelcomeAttachment{{MsgType: "image", MediaID: "media-1"}, {MsgType: "link", Title: "入群", URL: "https://work.weixin.qq.com/gm/0123456789abcdef0123456789abcdef"}}); err != nil {
		t.Fatal(err)
	}
	if err = client.DeleteContactWay(context.Background(), "cw-1"); err != nil || calls["/cgi-bin/externalcontact/del_contact_way"] != 1 {
		t.Fatalf("calls=%v err=%v", calls, err)
	}
}

func TestPrivateMessageUsesSingleCustomerContractAndRejectsFailList(t *testing.T) {
	for name, providerResponse := range map[string]string{
		"accepted":        `{"errcode":0,"msgid":"msg-1","fail_list":[]}`,
		"target rejected": `{"errcode":0,"msgid":"msg-1","fail_list":["external-secret-id"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			calls := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Path)
				switch r.URL.Path {
				case "/cgi-bin/gettoken":
					if r.URL.Query().Get("corpsecret") != "contact secret" {
						t.Fatal("private message did not use contact secret")
					}
					_, _ = w.Write([]byte(`{"errcode":0,"access_token":"contact-token","expires_in":7200}`))
				case "/cgi-bin/media/upload":
					if r.URL.Query().Get("type") != "image" || !strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
						t.Fatalf("upload request=%s", r.URL.String())
					}
					raw, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(raw), "Content-Type: image/png") || !strings.Contains(string(raw), "Content-Disposition: form-data;") || !strings.Contains(string(raw), "filename=image.png") || !strings.Contains(string(raw), "name=media") {
						t.Fatalf("multipart headers=%q", raw)
					}
					_, _ = w.Write([]byte(`{"errcode":0,"media_id":"media-1"}`))
				case "/cgi-bin/externalcontact/add_msg_template":
					raw, _ := io.ReadAll(r.Body)
					var body map[string]any
					if json.Unmarshal(raw, &body) != nil || body["chat_type"] != "single" || body["sender"] != "staff-secret-id" {
						t.Fatalf("message body=%s", raw)
					}
					targets, _ := body["external_userid"].([]any)
					if len(targets) != 1 || targets[0] != "external-secret-id" {
						t.Fatalf("targets=%v", targets)
					}
					_, _ = w.Write([]byte(providerResponse))
				default:
					t.Fatalf("unexpected path=%s", r.URL.Path)
				}
			}))
			defer server.Close()
			client := newTestClient(t, server, func() time.Time { return testNow })
			client.config.ContactSecret = "contact secret"
			receipt, attempted, err := client.SendPrivateMessage(context.Background(), outboundport.PrivateMessageTarget{ExternalUserID: "external-secret-id", StaffUserID: "staff-secret-id"}, outboundport.PrivateMessagePayload{Text: "hello", Attachments: []outboundport.PrivateMessageAttachment{{Kind: "image", Content: []byte("png-image-bytes"), FileName: "image.png", MediaType: "image/png"}}})
			if !attempted || len(calls) != 3 {
				t.Fatalf("attempted=%v calls=%v err=%v", attempted, calls, err)
			}
			if name == "accepted" {
				if err != nil || receipt.MessageID != "msg-1" {
					t.Fatalf("receipt=%+v err=%v", receipt, err)
				}
				return
			}
			var failure outboundport.PrivateMessageSendError
			if err == nil || !errors.As(err, &failure) || failure.OutcomeUnknown() {
				t.Fatalf("known target rejection err=%v", err)
			}
		})
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
