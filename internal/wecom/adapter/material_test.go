package adapter

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

func TestMaterialUploaderClassifiesConfirmedRejectAndSuccess(t *testing.T) {
	for _, test := range []struct {
		name      string
		response  string
		wantError bool
		retryable bool
	}{
		{name: "success", response: fmt.Sprintf(`{"errcode":0,"media_id":"media-1","created_at":%d}`, testNow.Unix())},
		{name: "rate limited", response: `{"errcode":45009}`, wantError: true, retryable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/cgi-bin/gettoken":
					_, _ = w.Write([]byte(`{"errcode":0,"access_token":"token","expires_in":7200}`))
				case "/cgi-bin/media/upload":
					if r.Method != http.MethodPost || r.URL.Query().Get("access_token") != "token" || r.URL.Query().Get("type") != "image" {
						t.Fatalf("request=%s?%s", r.URL.Path, r.URL.RawQuery)
					}
					_, _ = w.Write([]byte(test.response))
				default:
					t.Fatalf("path=%s", r.URL.Path)
				}
			}))
			defer server.Close()
			client := newTestClient(t, server, func() time.Time { return testNow })
			uploader, err := NewMaterialUploader(client, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			if err != nil {
				t.Fatal(err)
			}
			content := []byte("image bytes")
			digest := sha256.Sum256(content)
			receipt, called, err := uploader.UploadMaterial(context.Background(), outboundport.MaterialSourceSnapshot{SourceRef: "image:1", SourceType: "image", ContentDigest: digest, FileName: "cover.png", MediaType: "image/png", SizeBytes: int64(len(content)), SnapshotVersion: 1}, outboundport.MaterialSourceContent{Bytes: content, FileName: "cover.png", MediaType: "image/png"}, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			if !test.wantError {
				if err != nil || !called || receipt.MediaID != "media-1" || !receipt.ProviderCreatedAt.Equal(testNow.UTC()) {
					t.Fatalf("receipt=%+v called=%t err=%v", receipt, called, err)
				}
				return
			}
			var classified outboundport.MaterialUploadError
			if !called || !errors.As(err, &classified) || classified.OutcomeUnknown() || classified.Retryable() != test.retryable || classified.FailureCode() != "wecom_errcode_45009" {
				t.Fatalf("called=%t err=%v classified=%v", called, err, classified)
			}
		})
	}
}

func TestMaterialUploaderUsesDedicatedTimeoutInsteadOfClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"token","expires_in":7200}`))
		case "/cgi-bin/media/upload":
			time.Sleep(30 * time.Millisecond)
			_, _ = w.Write([]byte(fmt.Sprintf(`{"media_id":"media-1","created_at":%d}`, testNow.Unix())))
		default:
			t.Fatalf("path=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, func() time.Time { return testNow })
	client.http.Timeout = 10 * time.Millisecond
	client.config.UploadTimeout = 100 * time.Millisecond
	uploader, err := NewMaterialUploader(client, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("image bytes")
	digest := sha256.Sum256(content)
	receipt, called, err := uploader.UploadMaterial(context.Background(), outboundport.MaterialSourceSnapshot{SourceRef: "image:1", SourceType: "image", ContentDigest: digest, FileName: "cover.png", MediaType: "image/png", SizeBytes: int64(len(content)), SnapshotVersion: 1}, outboundport.MaterialSourceContent{Bytes: content, FileName: "cover.png", MediaType: "image/png"}, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil || !called || receipt.MediaID != "media-1" {
		t.Fatalf("receipt=%+v called=%t err=%v", receipt, called, err)
	}
}
