package media

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestMediaUIRendersEveryFrozenWorkspaceWithStablePageKey(t *testing.T) {
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "admin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{"images", "attach", "mpLib"} {
		if err := os.WriteFile(filepath.Join(dist, "admin", page+".html"), []byte(`<template id="tpl"><section data-page="`+page+`"></section></template>`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tokens.css", "labs.css", "admin.js"} {
		if err := os.WriteFile(filepath.Join(dist, "assets", name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `{"entries":{"tokens":"assets/tokens.css","labs":"assets/labs.css","admin":"assets/admin.js"},"files":{"assets/tokens.css":{},"assets/labs.css":{},"assets/admin.js":{}}}`
	if err := os.WriteFile(filepath.Join(dist, "asset-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	handler := NewModuleRegistration().UIBinding(dist, func(w http.ResponseWriter, _ *http.Request, page, donor string, assets MediaAssets) error {
		got = append(got, page)
		if donor == "" || assets.AdminJS != "/media-assets/assets/admin.js" {
			t.Fatalf("bad render input page=%q donor=%q assets=%+v", page, donor, assets)
		}
		w.WriteHeader(http.StatusOK)
		return nil
	})
	for _, path := range []string{"/admin/image-library", "/admin/miniprogram-library", "/admin/attachment-library"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d", path, response.Code)
		}
	}
	want := []string{"images", "mpLib", "attach"}
	if len(got) != len(want) {
		t.Fatalf("pages=%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pages=%v want=%v", got, want)
		}
	}
}
