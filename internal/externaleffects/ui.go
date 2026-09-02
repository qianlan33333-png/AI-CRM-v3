package externaleffects

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// UIHandler serves only the frozen V2 hidden External Effects workspace. The
// donor bundle is not given a campaign URL: every request is normalized to
// view=external-effects and only its HTML plus hashed assets are reachable.
type UIHandler struct{ dist string }

func NewUIHandler(dist string) *UIHandler { return &UIHandler{dist: dist} }
func (h *UIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.dist == "" {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/admin/external-effects" {
		if r.URL.Query().Get("view") != "external-effects" || len(r.URL.Query()) != 1 {
			http.Redirect(w, r, "/admin/external-effects?view=external-effects", http.StatusSeeOther)
			return
		}
		h.serve(w, r, "admin/campaigns.html", "text/html; charset=utf-8")
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/assets/") {
		http.NotFound(w, r)
		return
	}
	relative := strings.TrimPrefix(r.URL.Path, "/")
	if strings.Contains(relative, "..") {
		http.NotFound(w, r)
		return
	}
	h.serve(w, r, relative, "")
}
func (h *UIHandler) serve(w http.ResponseWriter, r *http.Request, relative, mime string) {
	file := filepath.Join(h.dist, filepath.FromSlash(relative))
	clean := filepath.Clean(file)
	root := filepath.Clean(h.dist) + string(filepath.Separator)
	if !strings.HasPrefix(clean, root) {
		http.NotFound(w, r)
		return
	}
	if mime != "" {
		w.Header().Set("Content-Type", mime)
	}
	w.Header().Set("Cache-Control", "no-store")
	data, err := os.ReadFile(clean)
	if err != nil {
		http.Error(w, "external effects UI unavailable", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write(data)
}
