package http

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const publicCommerceAssetPrefix = "/product-public-assets/"

// PublicPresentationAssets is the manifest-verified, anonymous-safe V3
// presentation closure for Product's four public commerce routes. It is
// intentionally limited to browser resources: no catalog, identity, payment,
// coupon, or Provider dependency crosses this boundary.
type PublicPresentationAssets struct {
	StylesheetURL string
	HostURL       string
	handler       *publicPresentationAssetHandler
}

func (assets PublicPresentationAssets) configured() bool {
	return assets.handler != nil && assets.StylesheetURL != "" && assets.HostURL != ""
}

func (assets PublicPresentationAssets) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	if !assets.configured() {
		http.NotFound(writer, request)
		return
	}
	assets.handler.ServeHTTP(writer, request)
}

func publicCommerceContentSecurityPolicy() string {
	return "default-src 'self'; img-src 'self' https: data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
}

// NewPublicPresentationAssets resolves the narrowly declared V3 public
// presentation closure. Missing, altered, non-release, or unrelated assets
// fail composition rather than broadening anonymous access to /assets.
func NewPublicPresentationAssets(dist string) (PublicPresentationAssets, error) {
	if strings.TrimSpace(dist) == "" {
		return PublicPresentationAssets{}, errors.New("public presentation dist is required")
	}
	raw, err := os.ReadFile(filepath.Join(dist, "asset-manifest.json"))
	if err != nil {
		return PublicPresentationAssets{}, err
	}
	var manifest publicPresentationManifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return PublicPresentationAssets{}, err
	}
	if len(manifest.Entries) == 0 || len(manifest.Files) == 0 || len(manifest.ReleaseFiles) == 0 {
		return PublicPresentationAssets{}, errors.New("public presentation release manifest is incomplete")
	}
	handler := &publicPresentationAssetHandler{dist: dist, assets: map[string]publicPresentationAsset{}}
	var stylesheet, host string
	for _, entry := range []struct {
		name   string
		suffix string
		assign func(string)
	}{
		{name: "publicCommerceStyles", suffix: ".css", assign: func(value string) { stylesheet = value }},
		{name: "publicCommerceHost", suffix: ".js", assign: func(value string) { host = value }},
	} {
		value := manifest.Entries[entry.name]
		if !validPublicPresentationPath(value, entry.suffix) {
			return PublicPresentationAssets{}, errors.New("public presentation asset entry is invalid: " + entry.name)
		}
		if err = handler.include(value, manifest); err != nil {
			return PublicPresentationAssets{}, err
		}
		entry.assign(value)
	}
	return PublicPresentationAssets{
		StylesheetURL: publicCommerceAssetPrefix + strings.TrimPrefix(stylesheet, "assets/"),
		HostURL:       publicCommerceAssetPrefix + strings.TrimPrefix(host, "assets/"),
		handler:       handler,
	}, nil
}

type publicPresentationManifest struct {
	Entries      map[string]string                         `json:"entries"`
	Files        map[string]publicPresentationManifestFile `json:"files"`
	ReleaseFiles map[string]publicPresentationMetadata     `json:"release_files"`
}

type publicPresentationManifestFile struct {
	SHA256  string `json:"sha256"`
	Imports []struct {
		Path string `json:"path"`
	} `json:"imports"`
}

type publicPresentationMetadata struct {
	SHA256 string `json:"sha256"`
}

type publicPresentationAsset struct {
	sha256 string
	etag   string
}

type publicPresentationAssetHandler struct {
	dist   string
	assets map[string]publicPresentationAsset
}

func (handler *publicPresentationAssetHandler) include(releasePath string, manifest publicPresentationManifest) error {
	if _, exists := handler.assets[releasePath]; exists {
		return nil
	}
	if !validPublicPresentationPath(releasePath, "") {
		return errors.New("public presentation closure has an invalid asset path")
	}
	entry, hasEntry := manifest.Files[releasePath]
	released, hasRelease := manifest.ReleaseFiles[releasePath]
	if !hasEntry || !hasRelease || !validPublicPresentationSHA(entry.SHA256) || entry.SHA256 != released.SHA256 {
		return errors.New("public presentation asset is outside its verified release closure")
	}
	filename := filepath.Join(handler.dist, filepath.FromSlash(releasePath))
	info, err := os.Stat(filename)
	if err != nil || info.IsDir() || !publicPresentationAssetMatches(filename, entry.SHA256) {
		return errors.New("public presentation asset is missing or altered")
	}
	handler.assets[releasePath] = publicPresentationAsset{sha256: entry.SHA256, etag: "\"" + entry.SHA256 + "\""}
	for _, imported := range entry.Imports {
		if err = handler.include(imported.Path, manifest); err != nil {
			return err
		}
	}
	return nil
}

func validPublicPresentationPath(value, suffix string) bool {
	if !strings.HasPrefix(value, "assets/") || strings.Contains(value, "\\") || path.Clean(value) != value || strings.Contains(value, "//") {
		return false
	}
	if suffix != "" && !strings.HasSuffix(value, suffix) {
		return false
	}
	switch strings.ToLower(filepath.Ext(value)) {
	case ".css", ".js", ".mjs":
		return true
	default:
		return false
	}
}

func validPublicPresentationSHA(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func publicPresentationAssetMatches(filename, expected string) bool {
	file, err := os.Open(filename)
	if err != nil {
		return false
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return false
	}
	return hex.EncodeToString(hash.Sum(nil)) == expected
}

func (handler *publicPresentationAssetHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if handler == nil || request == nil || (request.Method != http.MethodGet && request.Method != http.MethodHead) || !strings.HasPrefix(request.URL.Path, publicCommerceAssetPrefix) {
		http.NotFound(writer, request)
		return
	}
	relative := strings.TrimPrefix(request.URL.Path, publicCommerceAssetPrefix)
	if relative == "" || strings.Contains(relative, "\\") || path.Clean(relative) != relative {
		http.NotFound(writer, request)
		return
	}
	releasePath := "assets/" + relative
	asset, ok := handler.assets[releasePath]
	if !ok {
		http.NotFound(writer, request)
		return
	}
	file, err := os.Open(filepath.Join(handler.dist, filepath.FromSlash(releasePath)))
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() || !publicPresentationAssetMatchesOpenFile(file, asset.sha256) {
		http.NotFound(writer, request)
		return
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(info.Name())))
	if contentType == "" {
		http.NotFound(writer, request)
		return
	}
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	writer.Header().Set("ETag", asset.etag)
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(writer, request, info.Name(), info.ModTime(), file)
}

func publicPresentationAssetMatchesOpenFile(file *os.File, expected string) bool {
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return false
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false
	}
	return hex.EncodeToString(hash.Sum(nil)) == expected
}
