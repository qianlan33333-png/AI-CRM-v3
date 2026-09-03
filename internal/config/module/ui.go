package module

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/donortemplate"
)

type UIAssets struct{ TokensCSS, LabsCSS, AdminJS string }
type PageRenderer func(http.ResponseWriter, *http.Request, string, string, UIAssets) error
type ui struct {
	dist   string
	render PageRenderer
}

// UIBinding is intentionally a tiny v3 host adapter. The donor runtime and
// complete generated fragments remain release inputs and never become routes.
func (m *Registration) UIBinding(dist string, render PageRenderer) http.Handler {
	if m == nil || strings.TrimSpace(dist) == "" || render == nil {
		return http.NotFoundHandler()
	}
	return &ui{dist: dist, render: render}
}
func (h *ui) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", 405)
		return
	}
	page, ok := configPage(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	raw, e := os.ReadFile(filepath.Join(h.dist, "admin", page+".html"))
	if e != nil {
		http.Error(w, "config UI unavailable", 503)
		return
	}
	body, e := donortemplate.Extract(string(raw))
	if e != nil {
		http.Error(w, "config UI unavailable", 503)
		return
	}
	assets, e := configAssets(h.dist)
	if e != nil {
		http.Error(w, "config UI unavailable", 503)
		return
	}
	if e = h.render(w, r, page, body, assets); e != nil {
		http.Error(w, "config UI unavailable", 500)
	}
}
func configPage(r *http.Request) (string, bool) {
	switch r.URL.Path {
	case "/admin/config", "/admin/config/", "/admin/config.html":
		return "config", len(r.URL.Query()) == 0
	case "/admin/configDetail.html":
		values := r.URL.Query()
		cats := values["cat"]
		if len(values) != 1 || len(cats) != 1 {
			return "", false
		}
		switch cats[0] {
		case "app-settings", "push-capabilities", "releases", "runtime-diagnostics":
			return "configDetail", true
		default:
			return "", false
		}
	case "/admin/api-docs", "/admin/apidocs.html":
		return "apidocs", len(r.URL.Query()) == 0
	default:
		return "", false
	}
}
func configAssets(dist string) (UIAssets, error) {
	raw, e := os.ReadFile(filepath.Join(dist, "asset-manifest.json"))
	if e != nil {
		return UIAssets{}, e
	}
	var manifest struct {
		Entries map[string]string `json:"entries"`
	}
	if e = json.Unmarshal(raw, &manifest); e != nil {
		return UIAssets{}, e
	}
	get := func(name string) (string, error) {
		v := manifest.Entries[name]
		if v == "" || strings.Contains(v, "..") || !strings.HasPrefix(v, "assets/") {
			return "", errors.New("config bundle asset missing")
		}
		if _, e = os.Stat(filepath.Join(dist, v)); e != nil {
			return "", e
		}
		return "/" + v, nil
	}
	tokens, e := get("tokens")
	if e != nil {
		return UIAssets{}, e
	}
	labs, e := get("labs")
	if e != nil {
		return UIAssets{}, e
	}
	admin, e := get("admin")
	if e != nil {
		return UIAssets{}, e
	}
	return UIAssets{tokens, labs, admin}, nil
}
