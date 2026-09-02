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
	return extractDonorTemplate(string(raw))
}

// extractDonorTemplate returns the inner HTML of the release page's outer
// <template id="tpl">. Frozen donor pages contain nested templates produced by
// sc-for/sc-if lowering, so stopping at the first closing tag silently removes
// dialogs and later interactions from the mounted workspace.
//
// The input is a verified release asset, not request data. This deliberately
// small tag scanner preserves the donor bytes between the outer tags while
// matching nested <template> elements. It handles quoted '>' characters and
// comments so the boundary cannot be confused by ordinary markup attributes.
func extractDonorTemplate(raw string) (string, error) {
	found := false
	depth := 0
	contentStart := 0
	for cursor := 0; cursor < len(raw); {
		tag, next, ok := nextHTMLTag(raw, cursor)
		if !ok {
			break
		}
		cursor = next
		if tag.name != "template" {
			continue
		}
		if !found {
			if !tag.closing && templateHasID(tag.raw, "tpl") {
				found = true
				depth = 1
				contentStart = tag.end
			}
			continue
		}
		if tag.closing {
			depth--
			if depth == 0 {
				return raw[contentStart:tag.start], nil
			}
			continue
		}
		depth++
	}
	if !found {
		return "", errors.New("donor template missing")
	}
	return "", errors.New("donor template incomplete")
}

type htmlTag struct {
	raw     string
	name    string
	closing bool
	start   int
	end     int
}

// nextHTMLTag finds one ordinary HTML tag without parsing or rewriting its
// contents. Comments are skipped and quoted attribute values may contain >.
func nextHTMLTag(raw string, cursor int) (htmlTag, int, bool) {
	for cursor < len(raw) {
		relative := strings.IndexByte(raw[cursor:], '<')
		if relative < 0 {
			return htmlTag{}, len(raw), false
		}
		start := cursor + relative
		if strings.HasPrefix(raw[start:], "<!--") {
			end := strings.Index(raw[start+4:], "-->")
			if end < 0 {
				return htmlTag{}, len(raw), false
			}
			cursor = start + 4 + end + 3
			continue
		}
		cursor = start + 1
		closing := false
		for cursor < len(raw) && isHTMLSpace(raw[cursor]) {
			cursor++
		}
		if cursor < len(raw) && raw[cursor] == '/' {
			closing = true
			cursor++
			for cursor < len(raw) && isHTMLSpace(raw[cursor]) {
				cursor++
			}
		}
		nameStart := cursor
		for cursor < len(raw) && isHTMLName(raw[cursor]) {
			cursor++
		}
		if nameStart == cursor {
			cursor = start + 1
			continue
		}
		name := strings.ToLower(raw[nameStart:cursor])
		quote := byte(0)
		for cursor < len(raw) {
			ch := raw[cursor]
			if quote != 0 {
				if ch == quote {
					quote = 0
				}
				cursor++
				continue
			}
			if ch == '\'' || ch == '"' {
				quote = ch
				cursor++
				continue
			}
			if ch == '>' {
				end := cursor + 1
				return htmlTag{raw: raw[start:end], name: name, closing: closing, start: start, end: end}, end, true
			}
			cursor++
		}
		return htmlTag{}, len(raw), false
	}
	return htmlTag{}, len(raw), false
}

func isHTMLSpace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\f'
}
func isHTMLName(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == ':'
}

func templateHasID(tag, expected string) bool {
	// The outer donor tag is generated as <template id="tpl">. Supporting
	// ordinary quoted and unquoted IDs keeps the release adapter tolerant of
	// formatting-only donor build changes without accepting a partial template.
	lower := strings.ToLower(tag)
	for offset := 0; ; {
		index := strings.Index(lower[offset:], "id")
		if index < 0 {
			return false
		}
		index += offset
		beforeOK := index == 0 || !isHTMLName(lower[index-1])
		after := index + len("id")
		afterOK := after >= len(lower) || !isHTMLName(lower[after])
		if !beforeOK || !afterOK {
			offset = after
			continue
		}
		for after < len(lower) && isHTMLSpace(lower[after]) {
			after++
		}
		if after >= len(lower) || lower[after] != '=' {
			offset = after
			continue
		}
		after++
		for after < len(lower) && isHTMLSpace(lower[after]) {
			after++
		}
		if after >= len(lower) {
			return false
		}
		valueStart := after
		valueEnd := after
		if lower[after] == '\'' || lower[after] == '"' {
			quote := lower[after]
			valueStart++
			valueEnd = strings.IndexByte(lower[valueStart:], quote)
			if valueEnd < 0 {
				return false
			}
			valueEnd += valueStart
		} else {
			for valueEnd < len(lower) && !isHTMLSpace(lower[valueEnd]) && lower[valueEnd] != '>' && lower[valueEnd] != '/' {
				valueEnd++
			}
		}
		return lower[valueStart:valueEnd] == strings.ToLower(expected)
	}
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
