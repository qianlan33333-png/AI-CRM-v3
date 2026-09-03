package outbound

import (
	"encoding/json"
	"testing"

	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
)

func TestWelcomeAttachmentsRequireValidatedFrozenMediaSnapshot(t *testing.T) {
	raw, err := json.Marshal(mediaport.GroupOpsMaterialSnapshot{SchemaVersion: 2, NodeKind: "message", Attachments: []mediaport.GroupOpsProviderReadyAttachment{
		{MsgType: "image", MediaID: "media-1"},
		{MsgType: "miniprogram", MediaID: "thumb-1", AppID: "wx-app", PagePath: "pages/home", Title: "课程"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	items, err := welcomeAttachments(raw)
	if err != nil || len(items) != 2 || items[1].AppID != "wx-app" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if _, err = welcomeAttachments(json.RawMessage(`{"schema_version":2,"node_kind":"message","attachments":[{"msgtype":"image","media_id":""}]}`)); err == nil {
		t.Fatal("invalid provider material snapshot was accepted")
	}
}
