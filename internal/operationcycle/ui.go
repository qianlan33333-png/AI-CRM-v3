package operationcycle

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/donortemplate"
)

type UIAssets struct{ TokensCSS, LabsCSS, HostJS string }
type PageRenderer func(http.ResponseWriter, *http.Request, string, string, UIAssets) error
type uiHandler struct {
	dist   string
	render PageRenderer
}

func (m *ModuleRegistration) UIBinding(dist string, render PageRenderer) http.Handler {
	if m == nil || strings.TrimSpace(dist) == "" || render == nil {
		return http.NotFoundHandler()
	}
	return &uiHandler{dist: dist, render: render}
}

func (h *uiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != "/admin/operation-cycles" {
		http.NotFound(w, r)
		return
	}
	page := "cycles"
	query := r.URL.Query()
	if len(query) != 0 {
		if len(query) != 2 || len(query["view"]) != 1 || query.Get("view") != "detail" || len(query["id"]) != 1 || !validUIKey(query.Get("id")) {
			http.Redirect(w, r, "/admin/operation-cycles", http.StatusSeeOther)
			return
		}
		page = "cyclesDetail"
	}
	raw, err := os.ReadFile(filepath.Join(h.dist, "admin", page+".html"))
	if err != nil {
		http.Error(w, "operation cycle UI unavailable", http.StatusServiceUnavailable)
		return
	}
	templateBody, err := donortemplate.Extract(string(raw))
	if err != nil {
		http.Error(w, "operation cycle UI unavailable", http.StatusServiceUnavailable)
		return
	}
	assets, err := operationCycleAssets(h.dist)
	if err != nil {
		http.Error(w, "operation cycle UI unavailable", http.StatusServiceUnavailable)
		return
	}
	if err = h.render(w, r, page, templateBody, assets); err != nil {
		http.Error(w, "operation cycle UI unavailable", http.StatusInternalServerError)
	}
}

func validUIKey(value string) bool {
	if value == "" || len(value) > 160 || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' || r == ':') {
			return false
		}
	}
	return true
}

func operationCycleAssets(dist string) (UIAssets, error) {
	raw, err := os.ReadFile(filepath.Join(dist, "asset-manifest.json"))
	if err != nil {
		return UIAssets{}, err
	}
	var manifest struct {
		Entries map[string]string `json:"entries"`
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return UIAssets{}, err
	}
	asset := func(name string) (string, error) {
		value := manifest.Entries[name]
		if value == "" || strings.Contains(value, "..") || !strings.HasPrefix(value, "assets/") {
			return "", errors.New("operation cycle asset missing")
		}
		if _, statErr := os.Stat(filepath.Join(dist, filepath.FromSlash(value))); statErr != nil {
			return "", statErr
		}
		return "/" + value, nil
	}
	tokens, err := asset("tokens")
	if err != nil {
		return UIAssets{}, err
	}
	labs, err := asset("labs")
	if err != nil {
		return UIAssets{}, err
	}
	host, err := asset("operationCyclesHost")
	if err != nil {
		return UIAssets{}, err
	}
	return UIAssets{TokensCSS: tokens, LabsCSS: labs, HostJS: host}, nil
}
