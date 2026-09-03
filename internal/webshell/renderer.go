package webshell

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

// The shell ships its presentation assets so an httptest or a future
// composition root can mount it without depending on the donor repository.
// Source commits and the verified production release are recorded in README.md.
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
	Content          template.HTML
	AudienceList     bool
	AudienceDetail   bool
	Customers        bool
	ExternalEffects  bool
	ExternalAssets   ExternalEffectsAssets
	Media            bool
	MediaPage        string
	MediaAssets      MediaAssets
	Tags             bool
	TagsAssets       TagsAssets
	Product          bool
	ProductPage      string
	ProductAssets    ProductAssets
	Coupons          bool
	CouponPage       string
	CouponAssets     CouponAssets
	GroupOps         bool
	GroupOpsPage     string
	GroupOpsAssets   GroupOpsAssets
	Automation       bool
	AutomationPage   string
	AutomationAssets AutomationAssets
	// AutomationCreateCode is a v3 host binding for the frozen create form.
	// It is absent for existing records, whose immutable code stays donor-owned.
	AutomationCreateCode string
	Survey              bool
	SurveyPage          string
	SurveyAssets        SurveyAssets
	OperationCycles     bool
	OperationPage       string
	OperationAssets     OperationCycleAssets
}

// ExternalEffectsAssets are manifest-derived URLs for the frozen donor bundle.
// They are data-only paths supplied by the composition adapter, never HTML.
type ExternalEffectsAssets struct {
	TokensCSS string
	LabsCSS   string
	AdminJS   string
}

// MediaAssets are manifest-derived URLs for the immutable Media donor bundle.
// They are supplied by the Media module's release-only UI adapter.
type MediaAssets struct{ TokensCSS, LabsCSS, AdminJS string }

// TagsAssets are manifest-derived frozen donor bundle paths. The tag page is
// mounted in admin_base and never publishes the donor's own shell/sidebar.
type TagsAssets struct{ TokensCSS, LabsCSS, AdminJS string }

// ProductAssets are manifest-derived URLs for the frozen donor Product
// bundle. They are passed by the Product UI adapter and contain no markup.
type ProductAssets struct{ TokensCSS, LabsCSS, AdminJS string }

// CouponAssets are verified manifest paths for the frozen coupon workspaces.
type CouponAssets struct{ TokensCSS, LabsCSS, AdminJS string }

// GroupOpsAssets are manifest-derived URLs for the immutable donor Group Ops
// bundle. The v3 shell owns the sidebar; the donor supplies only its stage
// template and runtime assets.
type GroupOpsAssets struct{ TokensCSS, LabsCSS, AdminJS string }

// AutomationAssets are manifest-derived frozen Agent bundle paths. The v3
// shell supplies only URLs; donor markup remains the extracted template.
type AutomationAssets struct{ TokensCSS, LabsCSS, AdminJS string }

type SurveyAssets struct{ TokensCSS, LabsCSS, AdminJS, EditorJS, EditorCSS string }

// OperationCycleAssets keep the immutable donor presentation separate from
// the minimal v3 host binding that supplies real data and commands.
type OperationCycleAssets struct{ TokensCSS, LabsCSS, HostJS string }

// Render implements the small presentation contract consumed by the Access
// HTTP handler. Keeping this adapter in webshell avoids a concrete import
// from Access into the UI package while still letting Access own login
// authentication, cookies, redirects, and error status codes.
//
// Only the reserved "login" view is accepted here. In particular, this
// method does not turn arbitrary names or request data into business pages.
// Values unrelated to the login shell (including credentials) are ignored.
func (renderer *Renderer) Render(_ context.Context, writer http.ResponseWriter, status int, name string, values map[string]any) error {
	if name != "login" {
		return errors.New("webshell renderer does not support view " + name)
	}

	data := DefaultLoginPage(stringValue(values, "next_path"))
	data.PageNotice = stringValue(values, "notice")
	data.PageError = friendlyLoginError(stringValue(values, "error"))
	data.LoginCSRFToken = stringValue(values, "login_csrf_token")
	if title := stringValue(values, "page_title"); title != "" {
		data.PageTitle = title
	}
	if summary := stringValue(values, "page_summary"); summary != "" {
		data.PageSummary = summary
	}
	return renderer.RenderLoginStatus(writer, status, data)
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
	contentTemplate := "admin_placeholder"
	audienceList := data.RequestPath == "/admin/automation-conversion"
	audienceDetail := strings.HasPrefix(data.RequestPath, "/admin/automation-conversion/packages/")
	customers := data.RequestPath == "/admin/customers" || strings.HasPrefix(data.RequestPath, "/admin/customers/")
	if audienceList {
		contentTemplate = "admin_audience"
	} else if audienceDetail {
		contentTemplate = "admin_audience_detail"
	} else if data.RequestPath == LoginAccessPath {
		contentTemplate = "admin_access"
	} else if data.RequestPath == OneIDPagePath {
		contentTemplate = "admin_oneid"
	} else if customers {
		contentTemplate = "admin_customers"
	}
	content, err := executeTemplate(renderer.templates, contentTemplate, data)
	if err != nil {
		return err
	}
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{
		AdminPageData:  data,
		Content:        template.HTML(content), // child template already escaped all data
		AudienceList:   audienceList,
		AudienceDetail: audienceDetail,
		Customers:      customers,
	})
	if err != nil {
		return err
	}
	return writeHTML(writer, status, body)
}

// RenderExternalEffects mounts the immutable donor runtime inside the one v3
// admin shell. It deliberately renders only the original stage mount point;
// donor navigation and HTML are never embedded.
func (renderer *Renderer) RenderExternalEffects(writer http.ResponseWriter, data AdminPageData, assets ExternalEffectsAssets) error {
	if renderer == nil || renderer.templates == nil || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.AdminJS == "" {
		return errors.New("external effects shell assets are required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = false
	content, err := executeTemplate(renderer.templates, "admin_external_effects", data)
	if err != nil {
		return err
	}
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{
		AdminPageData:   data,
		Content:         template.HTML(content),
		ExternalEffects: true,
		ExternalAssets:  assets,
	})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderMedia mounts one immutable Media template in the v3 shell. The
// caller supplies only a verified template extracted from web/dist; it never
// receives an arbitrary request-controlled HTML fragment.
func (renderer *Renderer) RenderMedia(writer http.ResponseWriter, data AdminPageData, page, donorTemplate string, assets MediaAssets) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.AdminJS == "" || (page != "images" && page != "attach" && page != "mpLib") {
		return errors.New("media shell assets are required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = false
	content := `<main id="stage" class="stage rich"></main><template id="tpl">` + donorTemplate + `</template>`
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), Media: true, MediaPage: page, MediaAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderTags mounts the complete, byte-frozen tags workspace into the one v3
// sidebar shell.  The supplied template was extracted from a verified release
// asset by the tag module, not from request input.
func (renderer *Renderer) RenderTags(writer http.ResponseWriter, data AdminPageData, donorTemplate string, assets TagsAssets) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.AdminJS == "" {
		return errors.New("tags shell assets are required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = false
	content := `<main id="stage" class="stage rich"></main><template id="tpl">` + donorTemplate + `</template>`
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), Tags: true, TagsAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderProducts mounts one allowlisted Product template into the existing
// PR10 shell. The donor template is the release-built template#tpl fragment;
// this method never renders the donor document or a second sidebar.
func (renderer *Renderer) RenderProducts(writer http.ResponseWriter, data AdminPageData, page, donorTemplate string, assets ProductAssets) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.AdminJS == "" || (page != "products" && page != "productForm" && page != "spProducts" && page != "spProductForm") {
		return errors.New("product shell assets are required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = false
	content := `<main id="stage" class="stage rich"></main><template id="tpl">` + donorTemplate + `</template>`
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), Product: true, ProductPage: page, ProductAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderCoupons mounts a verified coupons/couponForm donor template inside
// the only v3 admin shell; it never serves the donor's outer HTML document.
func (renderer *Renderer) RenderCoupons(writer http.ResponseWriter, data AdminPageData, page, donorTemplate string, assets CouponAssets) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.AdminJS == "" || (page != "coupons" && page != "couponForm") {
		return errors.New("coupon shell assets are required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = false
	content := `<main id="stage" class="stage rich"></main><template id="tpl">` + donorTemplate + `</template>`
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), Coupons: true, CouponPage: page, CouponAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderGroupOps mounts the active donor plan list/detail templates into the
// single v3 admin_base sidebar. It accepts only the page names selected by
// the Group Ops UI adapter and never receives request-controlled HTML.
func (renderer *Renderer) RenderGroupOps(writer http.ResponseWriter, data AdminPageData, page, donorTemplate string, assets GroupOpsAssets) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.AdminJS == "" || (page != "groupops" && page != "groupopsDetail") {
		return errors.New("Group Ops shell assets are required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = false
	content := `<main id="stage" class="stage rich"></main><template id="tpl">` + donorTemplate + `</template>`
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), GroupOps: true, GroupOpsPage: page, GroupOpsAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderAutomation mounts one verified Agent template into the existing v3
// admin shell. The outer donor document is never independently served.
func (renderer *Renderer) RenderAutomation(writer http.ResponseWriter, data AdminPageData, page, donorTemplate string, assets AutomationAssets, createCode string) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.AdminJS == "" || (page != "agents" && page != "agentEdit") {
		return errors.New("automation shell assets are required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = false
	content := `<main id="stage" class="stage rich"></main><template id="tpl">` + donorTemplate + `</template>`
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), Automation: true, AutomationPage: page, AutomationAssets: assets, AutomationCreateCode: createCode})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderSurvey mounts only the frozen question workspace fragment into the
// v3 admin shell. The editor bootstrap contains no record data; it directs the
// frozen controller to the v3 API adapter.
func (renderer *Renderer) RenderSurvey(writer http.ResponseWriter, data AdminPageData, page, donorTemplate string, assets SurveyAssets) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.AdminJS == "" || assets.EditorJS == "" || assets.EditorCSS == "" || (page != "questionnaires" && page != "questionnaireDetail" && page != "questionnaireOps") {
		return errors.New("survey shell assets are required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = false
	content := `<main id="stage" class="stage rich"></main><template id="tpl">` + donorTemplate + `</template>`
	if page == "questionnaireDetail" {
		content += `<div id="questionnaire-editor-config" hidden>{"mode":"new","heading":"问卷编辑","backHref":"questionnaires.html","defaultAssessment":false,"initialQuestionnaire":null,"initialQuestionnaireId":null}</div>`
	}
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), Survey: true, SurveyPage: page, SurveyAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderOperationCycles mounts one byte-frozen donor template inside the one
// v3 sidebar. Its host binding starts the frozen donor main -> legacy ->
// AdminController runtime after supplying only the operation-cycle read DTO.
func (renderer *Renderer) RenderOperationCycles(writer http.ResponseWriter, data AdminPageData, page, donorTemplate string, assets OperationCycleAssets) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.HostJS == "" || (page != "cycles" && page != "cyclesDetail") {
		return errors.New("operation-cycle shell assets are required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = false
	content := `<base href="/admin/operation-cycles/"><main id="stage" class="stage rich"></main><template id="tpl">` + donorTemplate + `</template>`
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), OperationCycles: true, OperationPage: page, OperationAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
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

// RenderSidebar renders the WeCom sidebar shell with reserved bootstrap URLs.
// The renderer itself never resolves a customer or contacts a provider; its
// browser asset may invoke only the explicitly reserved domain endpoints.
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
	contentType := mime.TypeByExtension(path.Ext(strings.ToLower(info.Name())))
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

func stringValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func friendlyLoginError(code string) string {
	switch strings.TrimSpace(code) {
	case "":
		return ""
	case "invalid_credentials", "authentication_required":
		return "账号或密码不正确，请重试。"
	case "csrf_required":
		return "页面安全令牌已失效，请刷新后重试。"
	case "invalid_request":
		return "请输入有效的账号和密码。"
	case "rate_limited":
		return "尝试次数过多，请稍后再试。"
	case "permission_denied":
		return "当前账号没有登录权限，请联系管理员。"
	case "not_found", "conflict", "internal_error":
		return "登录服务暂时不可用，请稍后重试。"
	default:
		return "登录失败，请稍后重试。"
	}
}
