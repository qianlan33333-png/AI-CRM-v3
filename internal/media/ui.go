package media

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// MediaPageRenderer is implemented by the v3 webshell adapter in composition.
// The immutable donor template is read only from the release build directory.
type MediaPageRenderer func(http.ResponseWriter, *http.Request, string, string, MediaAssets) error
type MediaAssets struct{ TokensCSS, LabsCSS, AdminJS string }
type mediaUI struct {
	dist   string
	render MediaPageRenderer
}

func (m *ModuleRegistration) UIBinding(dist string, render MediaPageRenderer) http.Handler {
	if m == nil || strings.TrimSpace(dist) == "" || render == nil {
		return http.NotFoundHandler()
	}
	return &mediaUI{dist: dist, render: render}
}

func (h *mediaUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.render == nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/media-assets/") {
		h.asset(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page, _, ok := mediaPage(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if len(r.URL.Query()) != 0 {
		http.Redirect(w, r, canonicalMediaPath(page), http.StatusSeeOther)
		return
	}
	templateBody, err := h.template(page)
	if err != nil {
		http.Error(w, "media UI unavailable", http.StatusServiceUnavailable)
		return
	}
	assets, err := h.assets()
	if err != nil {
		http.Error(w, "media UI unavailable", http.StatusServiceUnavailable)
		return
	}
	if err = h.render(w, r, page, templateBody, assets); err != nil {
		http.Error(w, "media UI unavailable", http.StatusInternalServerError)
	}
}
func mediaPage(path string) (string, string, bool) {
	switch path {
	case "/admin/image-library":
		return "images", "图片素材库", true
	case "/admin/miniprogram-library":
		return "mpLib", "小程序素材库", true
	case "/admin/attachment-library":
		return "attach", "附件素材库", true
	default:
		return "", "", false
	}
}
func canonicalMediaPath(page string) string {
	switch page {
	case "images":
		return "/admin/image-library"
	case "mpLib":
		return "/admin/miniprogram-library"
	default:
		return "/admin/attachment-library"
	}
}
func (h *mediaUI) template(page string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(h.dist, "admin", page+".html"))
	if err != nil {
		return "", err
	}
	const start = `<template id="tpl">`
	from := strings.Index(string(raw), start)
	if from < 0 {
		return "", errors.New("donor template missing")
	}
	from += len(start)
	to := strings.Index(string(raw)[from:], "</template>")
	if to < 0 {
		return "", errors.New("donor template incomplete")
	}
	return string(raw)[from : from+to], nil
}

type buildManifest struct {
	Entries map[string]string          `json:"entries"`
	Files   map[string]json.RawMessage `json:"files"`
}

func (h *mediaUI) assets() (MediaAssets, error) {
	raw, err := os.ReadFile(filepath.Join(h.dist, "asset-manifest.json"))
	if err != nil {
		return MediaAssets{}, err
	}
	var manifest buildManifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return MediaAssets{}, err
	}
	for _, name := range []string{"tokens", "labs", "admin"} {
		if manifest.Entries[name] == "" {
			return MediaAssets{}, errors.New("media bundle asset missing")
		}
	}
	return MediaAssets{TokensCSS: "/media-assets/" + manifest.Entries["tokens"], LabsCSS: "/media-assets/" + manifest.Entries["labs"], AdminJS: "/media-assets/" + manifest.Entries["admin"]}, nil
}
func (h *mediaUI) asset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	relative := strings.TrimPrefix(r.URL.Path, "/media-assets/")
	if relative == "" || strings.Contains(relative, "..") || strings.HasPrefix(relative, "/") {
		http.NotFound(w, r)
		return
	}
	raw, err := os.ReadFile(filepath.Join(h.dist, "asset-manifest.json"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var manifest buildManifest
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
