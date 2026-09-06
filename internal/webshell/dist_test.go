package webshell

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newDistFixture builds a minimal built-frontend tree: one home document, one
// standalone screen, the sidebar workbench and one hashed runtime asset.
func newDistFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(relative, content string) {
		t.Helper()
		file := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("admin/index.html", `<!doctype html><title>dist home</title><body data-page="index">新壳首页</body>`)
	write("admin/customers.html", `<!doctype html><title>dist customers</title><body data-page="customers">新壳客户列表</body>`)
	write("admin/automation.html", `<!doctype html><title>dist automation</title><body data-page="automation">新壳自动化运营</body>`)
	write("admin/cycles.html", `<!doctype html><title>dist cycles</title><body data-page="cycles">新壳运营闭环</body>`)
	write("admin/channels.html", `<!doctype html><title>dist channels</title><body data-page="channels">新壳渠道码中心</body>`)
	write("admin/wecom-tags.html", `<!doctype html><title>dist tags</title><body data-page="tags">新壳企微标签</body>`)
	write("sidebar/index.html", `<link rel="stylesheet" href="../assets/sidebarStyles-test.css"><script type="module" src="../assets/sidebar-test.js"></script>新侧边栏`)
	write("assets/sidebar-test.js", `console.log("sidebar")`)
	write("assets/sidebarStyles-test.css", `.sidebar-shell{}`)
	return root
}

func TestDistAdminPagesReplacePlaceholderShell(t *testing.T) {
	handler, err := NewHandler(HandlerOptions{DistDir: newDistFixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	for path, marker := range map[string]string{
		"/admin/index.html":      "新壳首页",
		"/admin/automation.html": "新壳自动化运营",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), marker) {
			t.Fatalf("dist admin page %s status=%d body=%q", path, response.Code, response.Body.String())
		}
		if !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("dist admin page %s must stay uncached: %q", path, response.Header().Get("Cache-Control"))
		}
	}

	// Vanity aliases nested deeper than /admin/<name>.html redirect onto the
	// flat canonical document: the built pages resolve "../assets/…" and
	// sibling links against the request path, which breaks at deeper mounts.
	for path, target := range map[string]string{
		"/admin/operation-cycles": "/admin/cycles.html",
		"/admin/channels":         "/admin/channels.html",
		"/admin/wecom-tags":       "/admin/wecom-tags.html",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != target {
			t.Fatalf("deep alias %s status=%d location=%q, want 303 %q", path, response.Code, response.Header().Get("Location"), target)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/operation-cycles?view=detail&id=7", nil))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/cycles.html?view=detail&id=7" {
		t.Fatalf("deep alias must preserve the query string: status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/cycles.html", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "新壳运营闭环") {
		t.Fatalf("canonical deep-alias target must serve the document: status=%d", response.Code)
	}

	// The admin root canonicalizes onto the built home document instead of
	// relying on a meta refresh resolving against a slash-less base URL.
	for _, root := range []string{"/admin", "/admin/"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, root, nil))
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/customers.html" {
			t.Fatalf("admin root %s status=%d location=%q", root, response.Code, response.Header().Get("Location"))
		}
	}

	// Unknown and module-owned paths keep the template shell behavior.
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/orders", nil))
	if !strings.Contains(response.Body.String(), "功能待接入") {
		t.Fatalf("module-owned placeholder regressed: %q", response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/../secret.html", nil))
	if strings.Contains(response.Body.String(), "dist") && response.Code == http.StatusOK {
		t.Fatalf("traversal reached dist document: %q", response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/automation.html", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("dist admin page accepted POST: status=%d", response.Code)
	}
}

func TestDistSidebarReplacesLegacyWorkbench(t *testing.T) {
	handler, err := NewHandler(HandlerOptions{DistDir: newDistFixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, SidebarPagePath, nil))
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, "新侧边栏") {
		t.Fatalf("dist sidebar status=%d body=%q", response.Code, body)
	}
	if strings.Contains(body, "../assets/") || !strings.Contains(body, `"/sidebar-assets/sidebar-test.js"`) || !strings.Contains(body, `"/sidebar-assets/sidebarStyles-test.css"`) {
		t.Fatalf("sidebar asset URLs were not rewritten to the unauthenticated prefix: %q", body)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/sidebar-assets/sidebar-test.js", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "console.log") {
		t.Fatalf("sidebar asset status=%d body=%q", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("hashed sidebar asset must be immutably cacheable: %q", response.Header().Get("Cache-Control"))
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/sidebar-assets/../sidebar/index.html", nil))
	if response.Code == http.StatusOK {
		t.Fatalf("sidebar asset traversal escaped dist root")
	}
}

func TestDistAdminPageNameMapping(t *testing.T) {
	for path, expected := range map[string]string{
		"/admin/owner-migration":                    "ownerMig.html",
		"/admin/api-docs":                           "apidocs.html",
		"/admin/funnel.html":                        "funnel.html",
		"/admin/operation-cycles":                   "cycles.html",
		"/admin/channels/new":                       "channelForm.html",
		"/admin/wecom-tags":                         "wecom-tags.html",
		"/admin/wechat-pay/products":                "products.html",
		"/admin/service-period-products":            "spProducts.html",
		"/admin/cloud-orchestrator/plans":           "ai.html",
		"/admin/automation-conversion/group-ops/ui": "groupops.html",
	} {
		name, ok := DistAdminPageName(path)
		if !ok || name != expected {
			t.Fatalf("DistAdminPageName(%q) = %q, %t", path, name, ok)
		}
	}
	for _, path := range []string{"/admin/customers/7", "/admin/nested/deep.html", "/admin/..%2fsecret.html", "/api/admin/orders", "/admin/customers"} {
		if name, ok := DistAdminPageName(path); ok {
			t.Fatalf("DistAdminPageName(%q) unexpectedly resolved %q", path, name)
		}
	}
	if _, ok := DistAdminPageFile("", "/admin"); ok {
		t.Fatal("dist lookup must be disabled without a dist root")
	}
}
