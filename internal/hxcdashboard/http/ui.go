package http

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const dashboardPagePath = "/admin/hxc-dashboard"
const dashboardAssetPrefix = "/hxc-dashboard-assets/"

// PageRenderer is the composition-owned bridge into the shared v3 admin shell.
// Only verified release asset URLs cross this boundary.
type PageRenderer func(http.ResponseWriter, *http.Request, PageAssets) error

type PageAssets struct {
	TokensCSS string
	LabsCSS   string
	AdminJS   string
}

type UIHandler struct {
	dist     string
	renderer PageRenderer
}

func NewUIHandler(dist string, renderer PageRenderer) http.Handler {
	if strings.TrimSpace(dist) == "" || renderer == nil {
		return http.NotFoundHandler()
	}
	return &UIHandler{dist: dist, renderer: renderer}
}

func (h *UIHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if request.URL.Path == dashboardPagePath {
		if request.URL.RawQuery != "" {
			http.NotFound(writer, request)
			return
		}
		assets, err := h.pageAssets()
		if err != nil {
			http.Error(writer, "HXC dashboard UI unavailable", http.StatusServiceUnavailable)
			return
		}
		writer.Header().Set("Cache-Control", "private, no-store")
		if err = h.renderer(writer, request, assets); err != nil {
			http.Error(writer, "HXC dashboard UI unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	if !strings.HasPrefix(request.URL.Path, dashboardAssetPrefix) {
		http.NotFound(writer, request)
		return
	}
	h.serveAsset(writer, request)
}

func (h *UIHandler) pageAssets() (PageAssets, error) {
	manifest, err := h.manifest()
	if err != nil {
		return PageAssets{}, err
	}
	asset := func(name string) (string, error) {
		value := manifest.Entries[name]
		if value == "" || strings.Contains(value, "..") || !strings.HasPrefix(value, "assets/") {
			return "", errors.New("invalid dashboard asset")
		}
		if _, ok := manifest.ReleaseFiles[value]; !ok {
			return "", errors.New("dashboard asset is outside release closure")
		}
		if _, statErr := os.Stat(filepath.Join(h.dist, filepath.FromSlash(value))); statErr != nil {
			return "", statErr
		}
		return dashboardAssetPrefix + strings.TrimPrefix(value, "assets/"), nil
	}
	tokens, err := asset("tokens")
	if err != nil {
		return PageAssets{}, err
	}
	labs, err := asset("labs")
	if err != nil {
		return PageAssets{}, err
	}
	admin, err := asset("admin")
	if err != nil {
		return PageAssets{}, err
	}
	return PageAssets{TokensCSS: tokens, LabsCSS: labs, AdminJS: admin}, nil
}

func (h *UIHandler) serveAsset(writer http.ResponseWriter, request *http.Request) {
	relative := strings.TrimPrefix(request.URL.Path, dashboardAssetPrefix)
	clean := path.Clean(relative)
	if relative == "" || clean != relative || clean == "." || strings.Contains(relative, "\\") {
		http.NotFound(writer, request)
		return
	}
	releasePath := "assets/" + relative
	manifest, err := h.manifest()
	if err != nil {
		http.Error(writer, "HXC dashboard asset unavailable", http.StatusServiceUnavailable)
		return
	}
	if _, ok := manifest.ReleaseFiles[releasePath]; !ok {
		http.NotFound(writer, request)
		return
	}
	file, err := os.Open(filepath.Join(h.dist, filepath.FromSlash(releasePath)))
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(writer, request)
		return
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(info.Name())))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(writer, request, info.Name(), time.Time{}, file)
}

type releaseManifest struct {
	Entries      map[string]string          `json:"entries"`
	ReleaseFiles map[string]json.RawMessage `json:"release_files"`
}

func (h *UIHandler) manifest() (releaseManifest, error) {
	file, err := os.Open(filepath.Join(h.dist, "asset-manifest.json"))
	if err != nil {
		return releaseManifest{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 4<<20))
	var manifest releaseManifest
	if err = decoder.Decode(&manifest); err != nil {
		return releaseManifest{}, err
	}
	if len(manifest.Entries) == 0 || len(manifest.ReleaseFiles) == 0 {
		return releaseManifest{}, errors.New("dashboard release manifest is incomplete")
	}
	return manifest, nil
}
