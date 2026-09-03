package channel

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestChannelUIUsesDedicatedHostAdapterAndCanonicalRoutes(t *testing.T) {
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"tokens.css", "labs.css", "admin.js", "channel-host.js"} {
		if err := os.WriteFile(filepath.Join(dist, "assets", file), []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `{"entries":{"tokens":"assets/tokens.css","labs":"assets/labs.css","admin":"assets/admin.js","channelCenterHost":"assets/channel-host.js"}}`
	if err := os.WriteFile(filepath.Join(dist, "asset-manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	assets, err := channelAssets(dist)
	if err != nil {
		t.Fatal(err)
	}
	if assets.AdminJS != "/assets/channel-host.js" {
		t.Fatalf("channel UI loaded shared donor entry instead of host adapter: %+v", assets)
	}

	tests := []struct{ path, page, id string }{
		{"/admin/channels", "channels", ""},
		{"/admin/channels/new", "channelForm", ""},
		{"/admin/channels/49/edit", "channelForm", "49"},
		{"/admin/channelForm.html?id=49", "channelForm", "49"},
	}
	for _, test := range tests {
		page, id, ok := channelPage(httptest.NewRequest("GET", test.path, nil))
		if !ok || page != test.page || id != test.id {
			t.Fatalf("channelPage(%q)=(%q,%q,%v)", test.path, page, id, ok)
		}
	}
	if _, _, ok := channelPage(httptest.NewRequest("GET", "/admin/channels/049/edit", nil)); ok {
		t.Fatal("non-canonical channel resource id was accepted")
	}
}
