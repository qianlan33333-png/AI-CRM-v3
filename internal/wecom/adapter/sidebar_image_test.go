package adapter

import (
	"context"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSidebarUploadUsesApplicationCredentialAndNeverSends(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			if r.URL.Query().Get("corpsecret") != "secret value" {
				t.Error("wrong application credential")
			}
			io.WriteString(w, `{"errcode":0,"access_token":"application-token","expires_in":7200}`)
		case "/cgi-bin/media/upload":
			if r.URL.Query().Get("access_token") != "application-token" || r.URL.Query().Get("type") != "image" {
				t.Error("wrong upload scope")
			}
			reader, err := r.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			part, err := reader.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(part)
			if err != nil {
				t.Fatal(err)
			}
			if part.FormName() != "media" || part.FileName() != "image.png" || string(data) != "image-bytes" {
				t.Error("unexpected upload body")
			}
			io.WriteString(w, `{"errcode":0,"media_id":"provider-image-id"}`)
		default:
			t.Errorf("unexpected provider operation %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	client.config.ContactSecret = "different-contact-secret"
	source := outboundport.SidebarImagePreparationSource{Scope: string(effectport.Hash("sidebar.image.scope.config.v1", "wx corp:10001")), Content: []byte("image-bytes"), FileName: "image.png", MediaType: "image/png"}
	receipt, attempted, err := client.UploadSidebarImage(context.Background(), source)
	if err != nil || !attempted || receipt.MediaID != "provider-image-id" || !receipt.ReadyUntil.Equal(testNow.Add(70*time.Hour)) || calls != 2 {
		t.Fatalf("upload receipt: attempted=%v err=%v calls=%d", attempted, err, calls)
	}
	source.Scope = "different-scope"
	_, attempted, err = client.UploadSidebarImage(context.Background(), source)
	if err == nil || attempted || calls != 2 {
		t.Fatal("scope mismatch reached provider")
	}
}
func TestSidebarUploadMissingReceiptIsNotSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/gettoken" {
			io.WriteString(w, `{"errcode":0,"access_token":"token","expires_in":7200}`)
			return
		}
		io.WriteString(w, `{"errcode":0}`)
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	receipt, attempted, err := client.UploadSidebarImage(context.Background(), outboundport.SidebarImagePreparationSource{Scope: string(effectport.Hash("sidebar.image.scope.config.v1", "wx corp:10001")), Content: []byte("image-bytes"), FileName: "image.png", MediaType: "image/png"})
	if err == nil || !attempted || receipt.MediaID != "" {
		t.Fatal("missing media receipt reported success")
	}
}
