package product

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductUIAllowlistUsesDonorTemplateAndDeniesDataPage(t *testing.T) {
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "admin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{"products", "productForm", "spProducts", "spProductForm"} {
		raw := "<!doctype html><body><div class=\"shell\"><template id=\"tpl\"><section data-page=\"" + page + "\">donor body</section></template></div></body>"
		if err := os.WriteFile(filepath.Join(dist, "admin", page+".html"), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := buildManifest{Entries: map[string]string{"tokens": "assets/tokens.css", "labs": "assets/labs.css", "admin": "assets/admin.js"}, Files: map[string]json.RawMessage{"assets/tokens.css": json.RawMessage("null"), "assets/labs.css": json.RawMessage("null"), "assets/admin.js": json.RawMessage("null")}}
	rawManifest, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dist, "asset-manifest.json"), rawManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"tokens.css": "tokens", "labs.css": "labs", "admin.js": "admin"} {
		if err = os.WriteFile(filepath.Join(dist, "assets", name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var rendered struct {
		page, body string
		assets     ProductAssets
	}
	handler := (&ModuleRegistration{}).UIBinding(dist, func(_ http.ResponseWriter, _ *http.Request, page, body string, assets ProductAssets) error {
		rendered.page, rendered.body, rendered.assets = page, body, assets
		return nil
	})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/wechat-pay/products.html", nil))
	if recorder.Code != http.StatusOK || rendered.page != "products" || !strings.Contains(rendered.body, "donor body") || rendered.assets.AdminJS != "/product-assets/admin.js" {
		t.Fatalf("status=%d page=%q body=%q assets=%+v", recorder.Code, rendered.page, rendered.body, rendered.assets)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/wechat-pay/products/7/edit", nil))
	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/admin/wechat-pay/productForm.html?id=7" {
		t.Fatalf("canonical edit status=%d location=%q", recorder.Code, recorder.Header().Get("Location"))
	}

	rendered.page = ""
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/service-period-products/spProductData.html", nil))
	if recorder.Code != http.StatusNotFound || rendered.page != "" {
		t.Fatalf("excluded data page status=%d page=%q", recorder.Code, rendered.page)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/product-assets/admin.js", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "admin" {
		t.Fatalf("asset status=%d body=%q", recorder.Code, recorder.Body.String())
	}

	for _, alias := range []string{"/admin/spProductData.html", "/admin/wechat-pay/spProductData.html", "/admin/wechat-pay/products/spProductData.html"} {
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, alias, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("alias=%s status=%d", alias, recorder.Code)
		}
	}
}

func TestProductPageRejectsUnexpectedQueriesAndNonPositiveIDs(t *testing.T) {
	for _, test := range []struct {
		path string
		want bool
	}{
		{path: "/admin/wechat-pay/products?view=form", want: false},
		{path: "/admin/productForm.html?id=0", want: false},
		{path: "/admin/service-period-products/0/edit", want: false},
		{path: "/admin/service-period-products/new", want: true},
	} {
		parsed, err := url.Parse(test.path)
		if err != nil {
			t.Fatal(err)
		}
		page, id, ok := productPage(parsed.Path)
		ok = ok && validProductUIQuery(httptest.NewRequest(http.MethodGet, test.path, nil), page, id)
		if test.want && !ok {
			t.Fatalf("path=%s not allowlisted", test.path)
		}
		if !test.want && ok {
			t.Fatalf("path=%s unexpectedly allowlisted", test.path)
		}
	}
}
