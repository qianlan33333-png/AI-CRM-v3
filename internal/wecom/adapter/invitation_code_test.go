package adapter

import (
	"context"
	"encoding/json"
	w "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInvitationCodeBindsOneGroupAndPreservesUnknownReceipt(t *testing.T) {
	failRead := false
	adds := 0
	server := httptest.NewServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			out.Write([]byte(`{"errcode":0,"access_token":"fixture-token","expires_in":7200}`))
		case "/cgi-bin/externalcontact/groupchat/add_join_way":
			adds++
			var body struct {
				Scene int      `json:"scene"`
				Auto  int      `json:"auto_create_room"`
				IDs   []string `json:"chat_id_list"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Scene != 2 || body.Auto != 0 || len(body.IDs) != 1 || body.IDs[0] != "test-group" {
				t.Errorf("incorrect single-group request %+v %v", body, err)
			}
			out.Write([]byte(`{"errcode":0,"config_id":"config-1"}`))
		case "/cgi-bin/externalcontact/groupchat/get_join_way":
			if failRead {
				out.WriteHeader(503)
				return
			}
			out.Write([]byte(`{"errcode":0,"join_way":{"qr_code":"https://wework.qpic.cn/code"}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			out.WriteHeader(404)
		}
	}))
	defer server.Close()
	client, err := NewDirectory(Config{Enabled: true, CorpID: "corp", ContactSecret: "fixture-secret", APIBase: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	code, err := client.CreateInvitationCode(context.Background(), "test-group")
	if err != nil || code.ConfigID != "config-1" || code.QRCode == "" || adds != 1 {
		t.Fatal(code, err, adds)
	}
	failRead = true
	code, err = client.CreateInvitationCode(context.Background(), "test-group")
	if err == nil || !w.ProviderCallAttempted(err) || code.ConfigID != "config-1" || adds != 2 {
		t.Fatal("must preserve accepted config for reconciliation", code, err, adds)
	}
}
