package groupops

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/donortemplate"
)

// GroupOpsPageRenderer is the v3-owned shell adapter. The donor template and
// manifest-derived asset URLs are read-only values from the built donor
// release; this module never edits or recompiles donor business files.
type GroupOpsPageRenderer func(http.ResponseWriter, *http.Request, string, string, GroupOpsAssets) error

type GroupOpsAssets struct{ TokensCSS, LabsCSS, AdminJS string }

type groupOpsUI struct {
	dist   string
	render GroupOpsPageRenderer
}

func (m *ModuleRegistration) UIBinding(dist string, render GroupOpsPageRenderer) http.Handler {
	if m == nil || strings.TrimSpace(dist) == "" || render == nil {
		return http.NotFoundHandler()
	}
	return &groupOpsUI{dist: dist, render: render}
}

func (h *groupOpsUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.render == nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/groupops-assets/") {
		h.asset(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page, ok := groupOpsPage(r.URL.Path)
	if !ok || !validGroupOpsQuery(r, page) {
		http.NotFound(w, r)
		return
	}
	// The v3 workspace contract is a carrying route. Keep the donor browser
	// path (including its query-driven history mode) as the single active
	// runtime entry, and carry dynamic plan IDs into the donor's unchanged
	// groupopsDetail.html?id=... convention.
	if r.URL.Path == "/admin/automation-conversion/group-ops/ui" || r.URL.Path == "/admin/automation-conversion/group-ops/groups/ui" {
		target := "/admin/groupops.html"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	const detailPrefix = "/admin/automation-conversion/group-ops/plans/"
	if strings.HasPrefix(r.URL.Path, detailPrefix) {
		target := "/admin/groupopsDetail.html?id=" + url.QueryEscape(strings.TrimPrefix(r.URL.Path, detailPrefix))
		if r.URL.RawQuery != "" {
			target += "&" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	filename := page + ".html"
	raw, err := os.ReadFile(filepath.Join(h.dist, "admin", filename))
	if err != nil {
		http.Error(w, "group ops UI unavailable", http.StatusServiceUnavailable)
		return
	}
	templateBody, err := donortemplate.Extract(string(raw))
	if err != nil {
		http.Error(w, "group ops UI unavailable", http.StatusServiceUnavailable)
		return
	}
	assets, err := h.assets()
	if err != nil {
		http.Error(w, "group ops UI unavailable", http.StatusServiceUnavailable)
		return
	}
	if err = h.render(w, r, page, templateBody, assets); err != nil {
		http.Error(w, "group ops UI unavailable", http.StatusInternalServerError)
	}
}

func groupOpsPage(path string) (string, bool) {
	switch path {
	case "/admin/automation-conversion/group-ops/ui", "/admin/automation-conversion/group-ops/groups/ui", "/admin/groupops.html":
		return "groupops", true
	case "/admin/groupopsDetail.html":
		return "groupopsDetail", true
	}
	const prefix = "/admin/automation-conversion/group-ops/plans/"
	if strings.HasPrefix(path, prefix) {
		id := strings.TrimPrefix(path, prefix)
		if _, err := strconv.ParseInt(id, 10, 64); err == nil && positiveCanonicalID(id) {
			return "groupopsDetail", true
		}
	}
	return "", false
}

func validGroupOpsQuery(r *http.Request, page string) bool {
	values := r.URL.Query()
	if len(values) == 0 {
		return true
	}
	if page == "groupops" {
		history, ok := values["history"]
		return ok && len(values) == 1 && len(history) == 1 && history[0] == "1"
	}
	allowed := map[string]bool{"id": true, "history": true}
	for key := range values {
		if !allowed[key] || len(values[key]) != 1 {
			return false
		}
	}
	if history, ok := values["history"]; ok && history[0] != "1" {
		return false
	}
	if raw, ok := values["id"]; ok {
		return positiveCanonicalID(raw[0])
	}
	return values.Get("history") == "1"
}

func positiveCanonicalID(value string) bool {
	if value == "" || len(value) > 19 || (len(value) > 1 && value[0] == '0') {
		return false
	}
	number, err := strconv.ParseInt(value, 10, 64)
	return err == nil && number > 0
}

func (h *groupOpsUI) assets() (GroupOpsAssets, error) {
	raw, err := os.ReadFile(filepath.Join(h.dist, "asset-manifest.json"))
	if err != nil {
		return GroupOpsAssets{}, err
	}
	var manifest struct {
		Entries map[string]string          `json:"entries"`
		Files   map[string]json.RawMessage `json:"files"`
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return GroupOpsAssets{}, err
	}
	get := func(name string) (string, error) {
		value := manifest.Entries[name]
		if value == "" || strings.Contains(value, "..") || !strings.HasPrefix(value, "assets/") {
			return "", errors.New("group ops bundle asset missing")
		}
		if _, ok := manifest.Files[value]; !ok {
			return "", errors.New("group ops bundle asset is not in manifest")
		}
		if _, err := os.Stat(filepath.Join(h.dist, value)); err != nil {
			return "", err
		}
		return "/groupops-assets/" + value, nil
	}
	tokens, err := get("tokens")
	if err != nil {
		return GroupOpsAssets{}, err
	}
	labs, err := get("labs")
	if err != nil {
		return GroupOpsAssets{}, err
	}
	admin, err := get("admin")
	if err != nil {
		return GroupOpsAssets{}, err
	}
	return GroupOpsAssets{TokensCSS: tokens, LabsCSS: labs, AdminJS: admin}, nil
}

func (h *groupOpsUI) asset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	relative := strings.TrimPrefix(r.URL.Path, "/groupops-assets/")
	if relative == "" || strings.Contains(relative, "..") || strings.HasPrefix(relative, "/") {
		http.NotFound(w, r)
		return
	}
	raw, err := os.ReadFile(filepath.Join(h.dist, "asset-manifest.json"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var manifest struct {
		Files map[string]json.RawMessage `json:"files"`
	}
	if json.Unmarshal(raw, &manifest) != nil {
		http.NotFound(w, r)
		return
	}
	if _, ok := manifest.Files[relative]; !ok {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(filepath.Join(h.dist, relative))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	contentType := mime.TypeByExtension(filepath.Ext(info.Name()))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, file)
}
