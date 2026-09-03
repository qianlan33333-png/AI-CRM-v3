package automation

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestAgentUIExtractsPrivateTemplateAndPreservesAliases(t *testing.T) {
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "admin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"agents", "agentEdit"} {
		if err := os.WriteFile(filepath.Join(dist, "admin", p+".html"), []byte(`<div class="shell"><aside class="side"></aside><template id="tpl"><section data-page="`+p+`">frozen</section></template></div>`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"tokens.css", "labs.css", "admin.js"} {
		if err := os.WriteFile(filepath.Join(dist, "assets", p), []byte(p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dist, "asset-manifest.json"), []byte(`{"entries":{"tokens":"assets/tokens.css","labs":"assets/labs.css","admin":"assets/admin.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotPage, gotTemplate string
	h := NewModuleRegistration().UIBinding(dist, func(_ http.ResponseWriter, _ *http.Request, page, tpl string, _ AgentAssets) error {
		gotPage, gotTemplate = page, tpl
		return nil
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/agents.html?type=agent", nil))
	if w.Code != 200 || gotPage != "agents" || gotTemplate != `<section data-page="agents">frozen</section>` {
		t.Fatalf("code=%d page=%q template=%q", w.Code, gotPage, gotTemplate)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/agentEdit.html?id=7", nil))
	if w.Code != 200 || gotPage != "agentEdit" {
		t.Fatalf("detail alias code=%d page=%q", w.Code, gotPage)
	}
	for _, target := range []string{"/admin/agentEdit.html", "/admin/agentEdit.html?type=fixed_script", "/admin/agentEdit.html?id=7&saved=1"} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusOK || gotPage != "agentEdit" {
			t.Fatalf("editor target %s code=%d page=%q", target, w.Code, gotPage)
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/agentEdit.html?type=agent", nil))
	if w.Code != http.StatusSeeOther {
		t.Fatal(w.Code)
	}
}
