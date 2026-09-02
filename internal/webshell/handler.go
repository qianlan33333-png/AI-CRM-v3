package webshell

import (
	"net/http"
	"strings"
)

// HandlerOptions configures the standalone shell handler.  Renderer is the
// only dependency; no Composition Root, store, provider, or domain service is
// accepted here by design.
type HandlerOptions struct {
	Renderer    *Renderer
	SidebarData SidebarPageData
}

// Handler serves the shell pages and embedded static assets.  Reserved data
// endpoints return a controlled not-implemented response until their domain
// owners are mounted by a future composition root.
type Handler struct {
	renderer    *Renderer
	sidebarData SidebarPageData
}

// NewHandler builds an independent httptest-friendly shell handler.  The
// optional form keeps the common no-configuration case concise while allowing
// callers to inject a renderer or fixed sidebar data in tests.
func NewHandler(options ...HandlerOptions) (http.Handler, error) {
	if len(options) > 1 {
		return nil, errTooManyHandlerOptions
	}
	var option HandlerOptions
	if len(options) == 1 {
		option = options[0]
	}
	renderer := option.Renderer
	if renderer == nil {
		var err error
		renderer, err = NewRenderer()
		if err != nil {
			return nil, err
		}
	}
	return &Handler{renderer: renderer, sidebarData: option.SidebarData}, nil
}

// MustHandler is a convenience for small local previews and tests.
func MustHandler() http.Handler {
	handler, err := NewHandler()
	if err != nil {
		panic(err)
	}
	return handler
}

// NewHandlerWithRenderer makes the dependency explicit for callers that
// already constructed a Renderer.
func NewHandlerWithRenderer(renderer *Renderer) (http.Handler, error) {
	return NewHandler(HandlerOptions{Renderer: renderer})
}

var errTooManyHandlerOptions = &handlerOptionsError{}

type handlerOptionsError struct{}

func (*handlerOptionsError) Error() string {
	return "webshell accepts at most one HandlerOptions value"
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if handler == nil || handler.renderer == nil {
		http.Error(writer, "webshell handler is not initialized", http.StatusInternalServerError)
		return
	}
	requestPath := request.URL.Path
	switch {
	case strings.HasPrefix(requestPath, "/static/"):
		handler.renderer.ServeStatic(writer, request)
	case requestPath == LoginPath:
		handler.serveLogin(writer, request)
	case requestPath == WeComAuthStartPath:
		handler.serveWeComAuthStart(writer, request)
	case requestPath == AdminRootPath || strings.HasPrefix(requestPath, AdminRootPath+"/"):
		handler.serveAdmin(writer, request)
	case requestPath == SidebarPagePath:
		handler.serveSidebar(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (handler *Handler) serveLogin(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodGet+", "+http.MethodHead+", "+http.MethodPost)
		return
	}
	data := DefaultLoginPage(request.URL.Query().Get("next"))
	status := http.StatusOK
	if request.Method == http.MethodPost {
		// Do not parse or log credentials.  Access owns authentication and will
		// replace this controlled response when its contract is implemented.
		data.PageError = "本地登录暂未接入，请使用企业微信登录入口。"
		status = http.StatusNotImplemented
	}
	if err := handler.renderer.RenderLoginStatus(writer, status, data); err != nil {
		http.Error(writer, "unable to render login shell", http.StatusInternalServerError)
	}
}

func (handler *Handler) serveWeComAuthStart(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, http.MethodGet+", "+http.MethodHead)
		return
	}
	data := DefaultLoginPage(request.URL.Query().Get("next"))
	data.PageError = "企业微信登录入口已预留；实际认证能力尚未接入。"
	if err := handler.renderer.RenderLoginStatus(writer, http.StatusNotImplemented, data); err != nil {
		http.Error(writer, "unable to render authentication shell", http.StatusInternalServerError)
	}
}

func (handler *Handler) serveAdmin(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, http.MethodGet+", "+http.MethodHead)
		return
	}
	spec := adminSpecForPath(request.URL.Path)
	data := AdminPageForRequest(request, spec.title, spec.summary, spec.activeEndpoint)
	if err := handler.renderer.RenderAdmin(writer, data); err != nil {
		http.Error(writer, "unable to render admin shell", http.StatusInternalServerError)
	}
}

func (handler *Handler) serveSidebar(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, http.MethodGet+", "+http.MethodHead)
		return
	}
	if err := handler.renderer.RenderSidebar(writer, handler.sidebarData); err != nil {
		http.Error(writer, "unable to render sidebar shell", http.StatusInternalServerError)
	}
}

func methodNotAllowed(writer http.ResponseWriter, allow string) {
	writer.Header().Set("Allow", allow)
	writer.WriteHeader(http.StatusMethodNotAllowed)
}

type adminSpec struct {
	title          string
	summary        string
	activeEndpoint string
}

var adminSpecs = map[string]adminSpec{
	"/admin": {
		title:          "快捷入口",
		summary:        "进入需要直接操作的业务模块。",
		activeEndpoint: "api.admin_automation_conversion",
	},
	"/admin/automation-conversion": {
		title:          "自动化运营",
		summary:        "自动化运营入口已预留。",
		activeEndpoint: "api.admin_automation_conversion",
	},
	"/admin/operation-cycles": {
		title:          "运营闭环",
		summary:        "运营闭环入口已预留。",
		activeEndpoint: "api.admin_operation_cycles_page",
	},
	"/admin/automation-conversion/group-ops/ui": {
		title:          "群运营计划",
		summary:        "群运营计划入口已预留。",
		activeEndpoint: "api.admin_group_ops_ui",
	},
	"/admin/channels": {
		title:          "渠道码中心",
		summary:        "渠道码中心入口已预留。",
		activeEndpoint: "api.admin_channels_page",
	},
	"/admin/cloud-orchestrator/plans": {
		title:          "AI 助手",
		summary:        "AI 助手入口已预留。",
		activeEndpoint: "api.admin_cloud_orchestrator_workspace",
	},
	"/admin/cloud-orchestrator/campaigns": {
		title:          "AI 助手 · 方案",
		summary:        "AI 助手方案工作区入口已预留。",
		activeEndpoint: "api.admin_cloud_orchestrator_workspace",
	},
	"/admin/cloud-orchestrator/observability": {
		title:          "AI 助手 · 观测",
		summary:        "AI 助手观测入口已预留。",
		activeEndpoint: "api.admin_cloud_orchestrator_workspace",
	},
	"/admin/customers": {
		title:          "客户激活 / 客户列表",
		summary:        "客户列表入口已预留。",
		activeEndpoint: "api.admin_console_customers",
	},
	"/admin/hxc-dashboard": {
		title:          "漏斗 / 数据看板",
		summary:        "数据看板入口已预留。",
		activeEndpoint: "api.admin_hxc_dashboard_workspace",
	},
	"/admin/questionnaires": {
		title:          "问卷",
		summary:        "问卷管理入口已预留。",
		activeEndpoint: "api.admin_questionnaires",
	},
	"/admin/radar-links": {
		title:          "内容雷达",
		summary:        "内容雷达入口已预留。",
		activeEndpoint: "api.admin_radar_links",
	},
	"/admin/wecom-tags": {
		title:          "企微标签管理",
		summary:        "企微标签管理入口已预留。",
		activeEndpoint: "api.admin_wecom_tags_page",
	},
	"/admin/orders": {
		title:          "交易管理",
		summary:        "交易管理入口已预留。",
		activeEndpoint: "api.admin_orders_page",
	},
	"/admin/wechat-pay/transactions": {
		title:          "交易管理",
		summary:        "微信支付交易入口已预留。",
		activeEndpoint: "api.admin_orders_page",
	},
	"/admin/wechat-pay/products": {
		title:          "商品管理",
		summary:        "商品管理入口已预留。",
		activeEndpoint: "api.admin_wechat_pay_products_page",
	},
	"/admin/service-period-products": {
		title:          "周期商品管理",
		summary:        "周期商品管理入口已预留。",
		activeEndpoint: "api.admin_service_period_products_page",
	},
	"/admin/coupons": {
		title:          "优惠券",
		summary:        "优惠券入口已预留。",
		activeEndpoint: "api.admin_coupons_page",
	},
	"/admin/image-library": {
		title:          "图片素材库",
		summary:        "图片素材库入口已预留。",
		activeEndpoint: "api.admin_image_library_workspace",
	},
	"/admin/miniprogram-library": {
		title:          "小程序素材库",
		summary:        "小程序素材库入口已预留。",
		activeEndpoint: "api.admin_miniprogram_library_workspace",
	},
	"/admin/attachment-library": {
		title:          "附件素材库",
		summary:        "附件素材库入口已预留。",
		activeEndpoint: "api.admin_attachment_library_workspace",
	},
	"/admin/automation-agents": {
		title:          "自动化话术",
		summary:        "自动化话术入口已预留。",
		activeEndpoint: "api.admin_automation_agents_page",
	},
	"/admin/owner-migration": {
		title:          "负责人迁移",
		summary:        "负责人迁移入口已预留。",
		activeEndpoint: "api.admin_owner_migration_page",
	},
	"/admin/config": {
		title:          "配置",
		summary:        "配置入口已预留。",
		activeEndpoint: "api.admin_config",
	},
	LoginAccessPath: {
		title:          "员工登录权限",
		summary:        "管理后台员工账号、角色与企微绑定。",
		activeEndpoint: "api.admin_config",
	},
	"/admin/api-docs": {
		title:          "API 文档",
		summary:        "API 文档入口已预留。",
		activeEndpoint: "api.admin_api_docs",
	},
}

func adminSpecForPath(requestPath string) adminSpec {
	if spec, ok := adminSpecs[requestPath]; ok {
		return spec
	}
	for route, spec := range adminSpecs {
		if route != AdminRootPath && strings.HasPrefix(requestPath, route+"/") {
			return spec
		}
	}
	return adminSpec{
		title:          "管理后台",
		summary:        "v3 管理后台壳已就绪，业务能力按模块逐项接入。",
		activeEndpoint: "",
	}
}
