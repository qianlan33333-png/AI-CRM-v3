package automation

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/donortemplate"
)

type AgentAssets struct{ TokensCSS, LabsCSS, AdminJS string }
type AgentPageRenderer func(http.ResponseWriter, *http.Request, string, string, AgentAssets) error
type agentUI struct {
	dist   string
	render AgentPageRenderer
}

func (m *ModuleRegistration) UIBinding(dist string, render AgentPageRenderer) http.Handler {
	if m == nil || strings.TrimSpace(dist) == "" || render == nil {
		return http.NotFoundHandler()
	}
	return &agentUI{dist, render}
}
func (h *agentUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", 405)
		return
	}
	page, ok := agentPage(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !validAgentQuery(page, r) {
		http.Redirect(w, r, "/admin/automation-agents", http.StatusSeeOther)
		return
	}
	raw, err := os.ReadFile(filepath.Join(h.dist, "admin", page+".html"))
	if err != nil {
		http.Error(w, "automation UI unavailable", 503)
		return
	}
	tpl, err := donortemplate.Extract(string(raw))
	if err != nil {
		http.Error(w, "automation UI unavailable", 503)
		return
	}
	assets, err := agentAssets(h.dist)
	if err != nil {
		http.Error(w, "automation UI unavailable", 503)
		return
	}
	if err = h.render(w, r, page, tpl, assets); err != nil {
		http.Error(w, "automation UI unavailable", 500)
	}
}
func agentPage(path string) (string, bool) {
	switch path {
	case "/admin/automation-agents", "/admin/agents.html":
		return "agents", true
	case "/admin/agentEdit.html":
		return "agentEdit", true
	default:
		return "", false
	}
}
func validAgentQuery(page string, r *http.Request) bool {
	q := r.URL.Query()
	if page == "agents" {
		if len(q) == 0 {
			return true
		}
		v, ok := q["type"]
		return ok && len(q) == 1 && len(v) == 1 && (v[0] == "agent" || v[0] == "fixed_script")
	}
	if len(q) != 1 {
		return false
	}
	v, ok := q["id"]
	if !ok || len(v) != 1 {
		return false
	}
	id, e := strconv.ParseInt(v[0], 10, 64)
	return e == nil && id > 0 && strconv.FormatInt(id, 10) == v[0]
}
func agentAssets(dist string) (AgentAssets, error) {
	raw, e := os.ReadFile(filepath.Join(dist, "asset-manifest.json"))
	if e != nil {
		return AgentAssets{}, e
	}
	var m struct {
		Entries map[string]string `json:"entries"`
	}
	if e = json.Unmarshal(raw, &m); e != nil {
		return AgentAssets{}, e
	}
	get := func(n string) (string, error) {
		v := m.Entries[n]
		if v == "" || !strings.HasPrefix(v, "assets/") || strings.Contains(v, "..") {
			return "", errors.New("automation bundle asset missing")
		}
		if _, e = os.Stat(filepath.Join(dist, v)); e != nil {
			return "", e
		}
		return "/" + v, nil
	}
	t, e := get("tokens")
	if e != nil {
		return AgentAssets{}, e
	}
	l, e := get("labs")
	if e != nil {
		return AgentAssets{}, e
	}
	a, e := get("admin")
	if e != nil {
		return AgentAssets{}, e
	}
	return AgentAssets{t, l, a}, nil
}
