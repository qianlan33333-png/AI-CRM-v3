// Package webshell contains the v3 presentation shell for the admin console
// and the WeCom customer sidebar.
//
// The package intentionally stops at HTML, CSS and local shell state.  It does
// not own authentication, customer data, business APIs, or provider calls.
// A future composition root can mount the renderer behind the corresponding
// domain handlers once those capabilities are implemented.
package webshell

import (
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
)

const (
	LoginPath             = "/login"
	WeComAuthStartPath    = "/auth/wecom/start"
	AdminRootPath         = "/admin"
	LoginAccessPath       = "/admin/config/login-access"
	SidebarPagePath       = "/sidebar/bind-mobile"
	SidebarBindMobileAPI  = "/api/v3/sidebar/bind-mobile"
	SidebarWorkbenchPath  = "/api/v3/sidebar/workbench"
	SidebarProfilePath    = "/api/v3/sidebar/profile"
	SidebarJSSDKPath      = "/api/v3/sidebar/jssdk-config"
	SidebarContextPath    = "/api/v3/sidebar/context-token"
	SidebarQuestionnaires = "/api/v3/sidebar/questionnaires"
	SidebarProducts       = "/api/v3/sidebar/products"
	SidebarOrders         = "/api/v3/sidebar/orders"
	SidebarCoupons        = "/api/v3/sidebar/coupons"
	SidebarMaterials      = "/api/v3/sidebar/materials"
)

// AdminRoute is the stable path portion of a shell route.  Endpoint names
// retain the source shell's route vocabulary so later domain handlers can map
// the menu without copying the old application.
type AdminRoute struct {
	Endpoint string
	Path     string
}

// AdminNavItem is one link in the admin navigation.
type AdminNavItem struct {
	Key      string
	Label    string
	Endpoint string
	Href     string
	Active   bool
}

// AdminNavGroup is a labelled group of admin navigation links.
type AdminNavGroup struct {
	Title  string
	Items  []AdminNavItem
	Active bool
}

// ADMIN_ROUTE_REGISTRY is the source shell's route registry plus the v3
// reserved authentication, access and sidebar paths.  Registry values are
// paths only; they do not imply that the corresponding business capability is
// implemented.
var ADMIN_ROUTE_REGISTRY = map[string]AdminRoute{
	"api.admin_console_dashboard":                      {"api.admin_console_dashboard", "/admin"},
	"api.admin_console_customers":                      {"api.admin_console_customers", "/admin/customers"},
	"api.admin_owner_migration_page":                   {"api.admin_owner_migration_page", "/admin/owner-migration"},
	"api.admin_owner_migration_action":                 {"api.admin_owner_migration_action", "/admin/owner-migration"},
	"api.admin_user_ops_ui":                            {"api.admin_user_ops_ui", "/admin/user-ops/ui"},
	"api.admin_hxc_dashboard_workspace":                {"api.admin_hxc_dashboard_workspace", "/admin/hxc-dashboard"},
	"api.admin_hxc_send_config_page":                   {"api.admin_hxc_send_config_page", "/admin/hxc-send-config"},
	"api.admin_cloud_orchestrator_workspace":           {"api.admin_cloud_orchestrator_workspace", "/admin/cloud-orchestrator/plans"},
	"api.admin_cloud_orchestrator_plans_workspace":     {"api.admin_cloud_orchestrator_plans_workspace", "/admin/cloud-orchestrator/plans"},
	"api.admin_cloud_orchestrator_campaigns_workspace": {"api.admin_cloud_orchestrator_campaigns_workspace", "/admin/cloud-orchestrator/campaigns"},
	"api.admin_cloud_orchestrator_observability":       {"api.admin_cloud_orchestrator_observability", "/admin/cloud-orchestrator/observability"},
	"api.admin_wecom_tags_page":                        {"api.admin_wecom_tags_page", "/admin/wecom-tags"},
	"api.admin_channels_page":                          {"api.admin_channels_page", "/admin/channels"},
	"api.admin_channel_new_page":                       {"api.admin_channel_new_page", "/admin/channels/new"},
	"api.admin_questionnaires":                         {"api.admin_questionnaires", "/admin/questionnaires"},
	"api.admin_console_questionnaires":                 {"api.admin_console_questionnaires", "/admin/questionnaires"},
	"api.admin_console_questionnaire_new":              {"api.admin_console_questionnaire_new", "/admin/questionnaires/new"},
	"api.admin_radar_links":                            {"api.admin_radar_links", "/admin/radar-links"},
	"api.admin_radar_link_new":                         {"api.admin_radar_link_new", "/admin/radar-links/new"},
	"api.admin_automation_conversion":                  {"api.admin_automation_conversion", "/admin/automation-conversion"},
	"api.admin_automation_agents_page":                 {"api.admin_automation_agents_page", "/admin/automation-agents"},
	"api.admin_group_ops_ui":                           {"api.admin_group_ops_ui", "/admin/automation-conversion/group-ops/ui"},
	"api.admin_group_ops_groups_ui":                    {"api.admin_group_ops_groups_ui", "/admin/automation-conversion/group-ops/groups/ui"},
	"api.admin_wechat_pay_transactions_page":           {"api.admin_wechat_pay_transactions_page", "/admin/wechat-pay/transactions"},
	"api.admin_orders_page":                            {"api.admin_orders_page", "/admin/orders"},
	"api.admin_wechat_pay_products_page":               {"api.admin_wechat_pay_products_page", "/admin/wechat-pay/products"},
	"api.admin_service_period_products_page":           {"api.admin_service_period_products_page", "/admin/service-period-products"},
	"api.admin_coupons_page":                           {"api.admin_coupons_page", "/admin/coupons"},
	"api.admin_alipay_transactions_page":               {"api.admin_alipay_transactions_page", "/admin/alipay/transactions"},
	"api.admin_image_library_workspace":                {"api.admin_image_library_workspace", "/admin/image-library"},
	"api.admin_miniprogram_library_workspace":          {"api.admin_miniprogram_library_workspace", "/admin/miniprogram-library"},
	"api.admin_attachment_library_workspace":           {"api.admin_attachment_library_workspace", "/admin/attachment-library"},
	"api.admin_config":                                 {"api.admin_config", "/admin/config"},
	"api.admin_config_app_settings":                    {"api.admin_config_app_settings", "/admin/config/app-settings"},
	"api.admin_config_login_access":                    {"api.admin_config_login_access", LoginAccessPath},
	"api.admin_api_docs":                               {"api.admin_api_docs", "/admin/api-docs"},
	"api.admin_console_api_docs":                       {"api.admin_console_api_docs", "/admin/api-docs"},
	"api.admin_operation_cycles_page":                  {"api.admin_operation_cycles_page", "/admin/operation-cycles"},
	"api.admin_login":                                  {"api.admin_login", LoginPath},
	"api.admin_login_submit":                           {"api.admin_login_submit", LoginPath},
	"api.auth_wecom_start":                             {"api.auth_wecom_start", WeComAuthStartPath},
	"api.admin_logout":                                 {"api.admin_logout", "/logout"},
	"api.sidebar_bind_mobile_page":                     {"api.sidebar_bind_mobile_page", SidebarPagePath},
	"api.sidebar_workbench":                            {"api.sidebar_workbench", SidebarWorkbenchPath},
	"api.sidebar_profile":                              {"api.sidebar_profile", SidebarProfilePath},
	"api.sidebar_jssdk_config":                         {"api.sidebar_jssdk_config", SidebarJSSDKPath},
	"api.sidebar_context_token":                        {"api.sidebar_context_token", SidebarContextPath},
}

// ADMIN_NAV_GROUPS is the complete source admin shell menu.  It deliberately
// contains links to reserved paths even while those feature pages display a
// controlled placeholder. No item is backed by sample data.
var ADMIN_NAV_GROUPS = []AdminNavGroup{
	{
		Title: "运营",
		Items: []AdminNavItem{
			{Key: "automation_conversion", Label: "自动化运营", Endpoint: "api.admin_automation_conversion"},
			{Key: "operation_cycles", Label: "运营闭环", Endpoint: "api.admin_operation_cycles_page"},
			{Key: "group_ops", Label: "群运营计划", Endpoint: "api.admin_group_ops_ui"},
			{Key: "channels", Label: "渠道码中心", Endpoint: "api.admin_channels_page"},
			{Key: "cloud_orchestrator", Label: "AI 助手", Endpoint: "api.admin_cloud_orchestrator_workspace"},
			{Key: "customers", Label: "客户激活 / 客户列表", Endpoint: "api.admin_console_customers"},
			{Key: "user_ops_funnel", Label: "漏斗 / 数据看板", Endpoint: "api.admin_hxc_dashboard_workspace"},
			{Key: "questionnaires", Label: "问卷", Endpoint: "api.admin_questionnaires"},
			{Key: "radar_links", Label: "内容雷达", Endpoint: "api.admin_radar_links"},
			{Key: "wecom_tags", Label: "企微标签管理", Endpoint: "api.admin_wecom_tags_page"},
		},
	},
	{
		Title: "交易",
		Items: []AdminNavItem{
			{Key: "wechat_pay_transactions", Label: "交易管理", Endpoint: "api.admin_orders_page"},
			{Key: "wechat_pay_products", Label: "商品管理", Endpoint: "api.admin_wechat_pay_products_page"},
			{Key: "service_period_products", Label: "周期商品管理", Endpoint: "api.admin_service_period_products_page"},
			{Key: "coupons", Label: "优惠券", Endpoint: "api.admin_coupons_page"},
		},
	},
	{
		Title: "素材",
		Items: []AdminNavItem{
			{Key: "image_library", Label: "图片素材库", Endpoint: "api.admin_image_library_workspace"},
			{Key: "miniprogram_library", Label: "小程序素材库", Endpoint: "api.admin_miniprogram_library_workspace"},
			{Key: "attachment_library", Label: "附件素材库", Endpoint: "api.admin_attachment_library_workspace"},
		},
	},
	{
		Title: "配置及后台",
		Items: []AdminNavItem{
			{Key: "automation_agents", Label: "自动化话术", Endpoint: "api.admin_automation_agents_page"},
			{Key: "owner_migration", Label: "负责人迁移", Endpoint: "api.admin_owner_migration_page"},
			{Key: "config", Label: "配置", Endpoint: "api.admin_config"},
			{Key: "api_docs", Label: "API 文档", Endpoint: "api.admin_api_docs"},
		},
	},
}

// AdminNavGroups is the idiomatic Go alias for callers that do not need to
// mirror the source Python constant name.
var AdminNavGroups = ADMIN_NAV_GROUPS

// Breadcrumb is one item in the admin shell breadcrumb trail.
type Breadcrumb struct {
	Label string
	Href  string
}

// PageAction is an optional topbar link.  It is intentionally link-only; a
// write action must be implemented by a domain command handler first.
type PageAction struct {
	Label   string
	Href    string
	Variant string
}

// HeaderTab is an optional topbar tab.
type HeaderTab struct {
	Label  string
	Href   string
	Active bool
}

// AdminUser is the non-sensitive display-only portion of an authenticated
// operator.  The shell never resolves or logs identity values.
type AdminUser struct {
	DisplayName string
}

// AdminPageData is the data contract for the base admin shell and placeholder
// pages.  The zero value is safe and is normalized by the renderer.
type AdminPageData struct {
	PageTitle         string
	PageSummary       string
	ActiveEndpoint    string
	RequestPath       string
	Breadcrumbs       []Breadcrumb
	NavItems          []AdminNavGroup
	CurrentAdminUser  *AdminUser
	PageNotice        string
	PageError         string
	PageActions       []PageAction
	HeaderTabs        []HeaderTab
	ShowPageHeader    bool
	AdminActionTokens map[string]string
}

// LoginLinks contains the two reserved WeCom entry modes.
type LoginLinks struct {
	QR    string
	OAuth string
}

// LoginPageData is deliberately capability-neutral.  It can render a login
// form without accepting credentials or creating a session.
type LoginPageData struct {
	PageTitle     string
	PageSummary   string
	PageNotice    string
	PageError     string
	NextPath      string
	FormAction    string
	LoginLinks    LoginLinks
	AuthModeLabel string
}

// SidebarPageData supplies only data attributes and initial shell state.  The
// sidebar JavaScript does not consume these URLs until a future domain-owned
// implementation replaces the placeholder handlers.
type SidebarPageData struct {
	DebugEnabled      bool
	WorkbenchURL      string
	ProfileURL        string
	QuestionnairesURL string
	ProductsURL       string
	OrdersURL         string
	CouponsURL        string
	MaterialsURL      string
	BindMobileURL     string
	JSSDKConfigURL    string
	ContextTokenURL   string
}

// AdminPathFor resolves a known route without parameters.  Unknown routes
// return # so a missing capability cannot accidentally become an external URL.
func AdminPathFor(endpoint string) string {
	return PathFor(endpoint, nil)
}

// PathFor resolves a route and appends optional query parameters.  Dynamic
// route segments can be added by a later domain adapter; the shell itself does
// not interpolate untrusted identifiers into paths.
func PathFor(endpoint string, query map[string]string) string {
	if endpoint == "static" {
		filename := strings.TrimLeft(query["filename"], "/")
		if filename == "" {
			return "/static/"
		}
		return "/static/" + filename
	}
	switch endpoint {
	case "api.admin_console_customer_detail":
		return "/admin/customers/" + escapedSegment(query["external_userid"])
	case "api.admin_cloud_orchestrator_plan_detail":
		return "/admin/cloud-orchestrator/plans/" + escapedSegment(query["plan_id"])
	case "api.admin_channel_edit_page":
		return "/admin/channels/" + escapedSegment(query["channel_id"]) + "/edit"
	case "api.admin_radar_link_edit":
		return "/admin/radar-links/" + escapedSegment(query["link_id"]) + "/edit"
	case "api.admin_radar_link_detail":
		return "/admin/radar-links/" + escapedSegment(query["link_id"]) + "/detail"
	case "api.admin_group_ops_plan_detail":
		return "/admin/automation-conversion/group-ops/plans/" + escapedSegment(query["plan_id"])
	case "api.admin_wechat_pay_transaction_detail_page":
		return "/admin/wechat-pay/transactions/" + escapedSegment(query["order_id"])
	case "api.admin_wechat_shop_transaction_detail_page":
		return "/admin/wechat-shop/transactions/" + escapedSegment(query["order_id"])
	case "api.admin_console_questionnaire_detail":
		return "/admin/questionnaires/" + escapedSegment(query["questionnaire_id"])
	case "api.admin_operation_cycle_strategy_page":
		return "/admin/operation-cycles/" + escapedSegment(query["strategy_key"])
	case "api.admin_operation_cycle_run_page":
		return "/admin/operation-cycles/" + escapedSegment(query["strategy_key"]) + "/runs/" + escapedSegment(query["run_key"])
	}
	route, ok := ADMIN_ROUTE_REGISTRY[endpoint]
	if !ok {
		return "#"
	}
	if len(query) == 0 {
		return route.Path
	}
	values := url.Values{}
	keys := make([]string, 0, len(query))
	for key, value := range query {
		if key == "" || value == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		values.Set(key, query[key])
	}
	if encoded := values.Encode(); encoded != "" {
		return route.Path + "?" + encoded
	}
	return route.Path
}

func escapedSegment(value string) string {
	return url.PathEscape(strings.TrimSpace(value))
}

// SafeNextPath accepts only a local absolute path and prevents protocol
// relative redirects.  The value is used in a hidden form field and in the
// WeCom start link, but never causes a redirect in this shell package.
func SafeNextPath(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return AdminRootPath
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return AdminRootPath
	}
	return value
}

// NavItems returns a deep copy of the complete menu with stable hrefs and
// active flags.  Callers may safely customize the returned slice for one
// request without mutating the package contract.
func NavItems(activeEndpoint string) []AdminNavGroup {
	groups := make([]AdminNavGroup, 0, len(ADMIN_NAV_GROUPS))
	for _, sourceGroup := range ADMIN_NAV_GROUPS {
		group := AdminNavGroup{Title: sourceGroup.Title, Items: make([]AdminNavItem, 0, len(sourceGroup.Items))}
		for _, sourceItem := range sourceGroup.Items {
			item := sourceItem
			item.Active = item.Endpoint == activeEndpoint
			item.Href = AdminPathFor(item.Endpoint)
			group.Items = append(group.Items, item)
			group.Active = group.Active || item.Active
		}
		groups = append(groups, group)
	}
	return groups
}

// AdminPageForRequest constructs the standard shell context for a request.
// It contains no business data and no sample statistics.
func AdminPageForRequest(request *http.Request, title, summary, activeEndpoint string) AdminPageData {
	requestPath := ""
	if request != nil && request.URL != nil {
		requestPath = request.URL.Path
	}
	if title == "" {
		title = "管理后台"
	}
	if summary == "" {
		summary = "v3 管理后台壳已就绪，业务能力按模块逐项接入。"
	}
	return AdminPageData{
		PageTitle:      title,
		PageSummary:    summary,
		ActiveEndpoint: activeEndpoint,
		RequestPath:    requestPath,
		Breadcrumbs: []Breadcrumb{
			{Label: "客户管理后台", Href: AdminPathFor("api.admin_console_dashboard")},
		},
		NavItems:          NavItems(activeEndpoint),
		ShowPageHeader:    true,
		AdminActionTokens: map[string]string{},
	}
}

// DefaultLoginPage returns the neutral login shell context for a local next
// path.  The path is sanitized before being included in links or form fields.
func DefaultLoginPage(nextPath string) LoginPageData {
	nextPath = SafeNextPath(nextPath)
	return LoginPageData{
		PageTitle:   "后台登录",
		PageSummary: "企业微信负责“你是谁”，客户管理后台负责“你能做什么”。",
		NextPath:    nextPath,
		FormAction:  LoginPath,
		LoginLinks: LoginLinks{
			QR:    PathFor("api.auth_wecom_start", map[string]string{"mode": "qr", "next": nextPath}),
			OAuth: PathFor("api.auth_wecom_start", map[string]string{"mode": "oauth", "next": nextPath}),
		},
		AuthModeLabel: "企业微信登录（待接入）",
	}
}

// DefaultSidebarPage returns the future v3 data URL contract.  It is safe to
// render in any environment because no URL is fetched by the shell script.
func DefaultSidebarPage() SidebarPageData {
	return SidebarPageData{
		WorkbenchURL:      SidebarWorkbenchPath,
		ProfileURL:        SidebarProfilePath,
		QuestionnairesURL: SidebarQuestionnaires,
		ProductsURL:       SidebarProducts,
		OrdersURL:         SidebarOrders,
		CouponsURL:        SidebarCoupons,
		MaterialsURL:      SidebarMaterials,
		BindMobileURL:     SidebarPagePath,
		JSSDKConfigURL:    SidebarJSSDKPath,
		ContextTokenURL:   SidebarContextPath,
	}
}

// cleanStaticPath keeps the static file server below /static and prevents a
// path traversal from reaching the embedded filesystem.
func cleanStaticPath(raw string) string {
	cleaned := path.Clean("/" + strings.TrimPrefix(raw, "/"))
	return strings.TrimPrefix(cleaned, "/")
}
