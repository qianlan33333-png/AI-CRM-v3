package webshell

import (
	"bytes"
	"embed"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"strings"
	"time"
)

// The shell ships its presentation assets so an httptest or a future
// composition root can mount it without depending on the donor repository.
// The source commit is recorded in the asset provenance note below.
// Source: AI-CRM@69c5282fb38058f2cc9872b6feb3f0f54bfad64b.
//
//go:embed templates static
var embeddedWebAssets embed.FS

// Renderer renders only local shell templates.  It is safe for concurrent
// requests after construction because html/template execution is read-only.
type Renderer struct {
	templates *template.Template
	staticFS  fs.FS
}

// NewRenderer parses the embedded shell templates and prepares the embedded
// static filesystem.  No network, database, or configuration lookup occurs.
func NewRenderer() (*Renderer, error) {
	templates, err := template.New("webshell").ParseFS(embeddedWebAssets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	staticFS, err := fs.Sub(embeddedWebAssets, "static")
	if err != nil {
		return nil, err
	}
	return &Renderer{templates: templates, staticFS: staticFS}, nil
}

// AdminShellView is the fully rendered admin view returned by the renderer.
// It is exported for callers that want to prebuild a view, while the embedded
// templates remain the package's stable presentation boundary.
type AdminShellView struct {
	AdminPageData
	Content template.HTML
}

// RenderAdmin renders the standard admin base shell around the neutral
// placeholder body.  The body contains no business data.
func (renderer *Renderer) RenderAdmin(writer http.ResponseWriter, data AdminPageData) error {
	return renderer.RenderAdminStatus(writer, http.StatusOK, data)
}

// RenderAdminStatus is the status-aware variant used by controlled blocked
// routes.  The body is rendered before headers are written so template errors
// cannot produce a partial document.
func (renderer *Renderer) RenderAdminStatus(writer http.ResponseWriter, status int, data AdminPageData) error {
	if renderer == nil || renderer.templates == nil {
		return errors.New("webshell renderer is not initialized")
	}
	normalizeAdminPage(&data)
	content, err := executeTemplate(renderer.templates, "admin_placeholder", data)
	if err != nil {
		return err
	}
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{
		AdminPageData: data,
		Content:       template.HTML(content), // child template already escaped all data
	})
	if err != nil {
		return err
	}
	return writeHTML(writer, status, body)
}

// RenderLogin renders the login shell.  It does not authenticate or issue a
// session; POST handling is intentionally owned by Access in a later slice.
func (renderer *Renderer) RenderLogin(writer http.ResponseWriter, data LoginPageData) error {
	return renderer.RenderLoginStatus(writer, http.StatusOK, data)
}

// RenderLoginStatus renders a login page with a controlled HTTP status, useful
// for the reserved WeCom start and local POST routes.
func (renderer *Renderer) RenderLoginStatus(writer http.ResponseWriter, status int, data LoginPageData) error {
	if renderer == nil || renderer.templates == nil {
		return errors.New("webshell renderer is not initialized")
	}
	normalizeLoginPage(&data)
	body, err := executeTemplate(renderer.templates, "login_page", data)
	if err != nil {
		return err
	}
	return writeHTML(writer, status, body)
}

// RenderSidebar renders the WeCom sidebar shell with reserved data URLs.  It
// never resolves a customer, contacts a provider, or sends a write request.
func (renderer *Renderer) RenderSidebar(writer http.ResponseWriter, data SidebarPageData) error {
	return renderer.RenderSidebarStatus(writer, http.StatusOK, data)
}

// RenderSidebarStatus is the status-aware variant for a future adapter to use
// when the shell is mounted behind a capability gate.
func (renderer *Renderer) RenderSidebarStatus(writer http.ResponseWriter, status int, data SidebarPageData) error {
	if renderer == nil || renderer.templates == nil {
		return errors.New("webshell renderer is not initialized")
	}
	normalizeSidebarPage(&data)
	body, err := executeTemplate(renderer.templates, "sidebar_page", data)
	if err != nil {
		return err
	}
	return writeHTML(writer, status, body)
}

// ServeStatic serves one embedded shell asset under /static.  It intentionally
// avoids a directory listing and rejects traversal outside the embedded tree.
func (renderer *Renderer) ServeStatic(writer http.ResponseWriter, request *http.Request) {
	if renderer == nil || renderer.staticFS == nil {
		http.Error(writer, "webshell assets are not initialized", http.StatusInternalServerError)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, http.MethodGet+", "+http.MethodHead)
		return
	}
	relative := strings.TrimPrefix(request.URL.Path, "/static/")
	relative = cleanStaticPath(relative)
	if relative == "" || relative == "." {
		http.NotFound(writer, request)
		return
	}
	file, err := renderer.staticFS.Open(relative)
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
	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(writer, "unable to read webshell asset", http.StatusInternalServerError)
		return
	}
	contentType := mime.TypeByExtension(strings.ToLower(info.Name()))
	if contentType == "" {
		contentType = http.DetectContentType(content)
	}
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(writer, request, info.Name(), time.Time{}, bytes.NewReader(content))
}

func executeTemplate(templates *template.Template, name string, data any) ([]byte, error) {
	var buffer bytes.Buffer
	if err := templates.ExecuteTemplate(&buffer, name, data); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func writeHTML(writer http.ResponseWriter, status int, body []byte) error {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "private, no-store")
	writer.WriteHeader(status)
	_, err := writer.Write(body)
	return err
}

func normalizeAdminPage(data *AdminPageData) {
	if data.PageTitle == "" {
		data.PageTitle = "管理后台"
	}
	if data.PageSummary == "" {
		data.PageSummary = "v3 管理后台壳已就绪，业务能力按模块逐项接入。"
	}
	if data.Breadcrumbs == nil {
		data.Breadcrumbs = []Breadcrumb{{Label: "客户管理后台", Href: AdminRootPath}}
	}
	if data.NavItems == nil {
		data.NavItems = NavItems(data.ActiveEndpoint)
	}
	if data.AdminActionTokens == nil {
		data.AdminActionTokens = map[string]string{}
	}
	// The shell has a single page-level title/header.  A future caller may
	// introduce a richer layout only by adding an explicit template contract.
	data.ShowPageHeader = true
}

func normalizeLoginPage(data *LoginPageData) {
	if data.PageTitle == "" {
		data.PageTitle = "后台登录"
	}
	if data.PageSummary == "" {
		data.PageSummary = "企业微信负责“你是谁”，客户管理后台负责“你能做什么”。"
	}
	data.NextPath = SafeNextPath(data.NextPath)
	if data.FormAction == "" {
		data.FormAction = LoginPath
	}
	if data.LoginLinks.QR == "" || data.LoginLinks.OAuth == "" {
		defaults := DefaultLoginPage(data.NextPath)
		if data.LoginLinks.QR == "" {
			data.LoginLinks.QR = defaults.LoginLinks.QR
		}
		if data.LoginLinks.OAuth == "" {
			data.LoginLinks.OAuth = defaults.LoginLinks.OAuth
		}
	}
	if data.AuthModeLabel == "" {
		data.AuthModeLabel = "企业微信登录（待接入）"
	}
}

func normalizeSidebarPage(data *SidebarPageData) {
	defaults := DefaultSidebarPage()
	if data.WorkbenchURL == "" {
		data.WorkbenchURL = defaults.WorkbenchURL
	}
	if data.BindMobileURL == "" {
		data.BindMobileURL = defaults.BindMobileURL
	}
	if data.JSSDKConfigURL == "" {
		data.JSSDKConfigURL = defaults.JSSDKConfigURL
	}
	if data.ContextTokenURL == "" {
		data.ContextTokenURL = defaults.ContextTokenURL
	}
}
