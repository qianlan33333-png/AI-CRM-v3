package webshell

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAdminNavGroupsMirrorSourceMenu(t *testing.T) {
	if len(ADMIN_NAV_GROUPS) != 4 {
		t.Fatalf("group count=%d, want 4", len(ADMIN_NAV_GROUPS))
	}
	wantTitles := []string{"运营", "交易", "素材", "配置及后台"}
	wantCounts := []int{10, 4, 3, 5}
	for index, group := range ADMIN_NAV_GROUPS {
		if group.Title != wantTitles[index] || len(group.Items) != wantCounts[index] {
			t.Fatalf("group %d=%+v, want title=%q count=%d", index, group, wantTitles[index], wantCounts[index])
		}
		for _, item := range group.Items {
			if item.Key == "" || item.Label == "" || item.Endpoint == "" {
				t.Fatalf("incomplete nav item=%+v", item)
			}
			if got := AdminPathFor(item.Endpoint); !strings.HasPrefix(got, "/admin") {
				t.Fatalf("item %q path=%q", item.Label, got)
			}
		}
	}

	navigation := NavItems("api.admin_orders_page")
	if !navigation[1].Active || !navigation[1].Items[0].Active {
		t.Fatalf("transaction item is not active: %+v", navigation[1])
	}
	navigation[1].Items[0].Label = "mutated copy"
	if ADMIN_NAV_GROUPS[1].Items[0].Label == "mutated copy" {
		t.Fatal("NavItems returned mutable source item")
	}
}

func TestPathForEscapesDynamicSegmentsAndNextPath(t *testing.T) {
	got := PathFor("api.admin_console_customer_detail", map[string]string{"customer_id": "42"})
	if got != "/admin/customers/42" {
		t.Fatalf("customer detail path=%q", got)
	}
	if PathFor("api.admin_console_customer_detail", map[string]string{"customer_id": "contact/a"}) != "#" {
		t.Fatal("non-numeric customer detail id accepted")
	}
	if SafeNextPath("https://attacker.example") != AdminRootPath {
		t.Fatal("absolute next path accepted")
	}
	if SafeNextPath("//attacker.example") != AdminRootPath {
		t.Fatal("protocol-relative next path accepted")
	}
	if SafeNextPath("/admin/config?tab=access") != "/admin/config?tab=access" {
		t.Fatal("local next path was changed")
	}
}

func TestStandaloneHandlerRendersAdminLoginSidebarAndAssets(t *testing.T) {
	handler, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		method     string
		path       string
		status     int
		contains   []string
		notContain []string
	}{
		{
			name:   "automation audience shell",
			method: http.MethodGet,
			path:   "/admin/automation-conversion",
			status: http.StatusOK,
			contains: []string{
				"AI 自动化运营",
				"class=\"aud-layout\"",
				"人群包分组",
				"共 0 个自定义分组",
				"当前分组暂无人群包",
				"admin_console.js",
				"admin_audience.css",
				"admin_audience_detail.js?v=automation-operations-v5",
			},
			notContain: []string{
				"功能待接入",
				"/api/admin/ai-audience/",
				"加载中",
			},
		},
		{
			name:   "automation audience secondary shell",
			method: http.MethodGet,
			path:   "/admin/automation-conversion/packages/42",
			status: http.StatusOK,
			contains: []string{
				"AI 自动化运营",
				"class=\"ai-page\"",
				"人群包配置维度",
				"基础配置",
				"自动化话术能力",
				"发送人白名单",
				"成员列表",
				"发送记录",
				"admin_audience_detail.css",
				"admin_audience_detail.js?v=automation-operations-v5",
				"template_parameter_form.js?v=dd8-frozen-ab63c644",
				"admin_audience_template_host.js?v=prd05-template-host-v1",
			},
			notContain: []string{
				"功能待接入",
				"/api/admin/ai-audience/",
				"external_userid",
				"加载中",
			},
		},
		{
			name:   "production admin console javascript",
			method: http.MethodGet,
			path:   "/static/admin_console/admin_console.js",
			status: http.StatusOK,
			contains: []string{
				"function bootLegacyFrames()",
				"function bootCopyButtons()",
				"window.AdminFmt",
			},
		},
		{
			name:   "audience secondary local javascript",
			method: http.MethodGet,
			path:   "/static/admin_console/admin_audience_detail.js",
			status: http.StatusOK,
			contains: []string{
				"/api/admin",
				"credentials: \"same-origin\"",
				"X-CSRF-Token",
				"Idempotency-Key",
				"broadcast-previews",
				"outcome_unknown",
			},
			notContain: []string{
				"sessionStorage",
				"localStorage",
				"mock.invalid",
				"external_userid",
			},
		},
		{
			name:   "admin placeholder",
			method: http.MethodGet,
			path:   "/admin/orders",
			status: http.StatusOK,
			contains: []string{
				"data-admin-shell-source=\"v3_webshell\"",
				"交易管理",
				"功能待接入",
				"客户激活 / 客户列表",
			},
			notContain: []string{"统计：0"},
		},
		{
			name:   "login",
			method: http.MethodGet,
			path:   "/login?next=/admin/orders",
			status: http.StatusOK,
			contains: []string{
				"method=\"post\"",
				"action=\"/login\"",
				"name=\"username\"",
				"name=\"password\"",
				"type=\"submit\">登录",
				"/auth/wecom/start?mode=qr&amp;next=%2Fadmin%2Forders",
				"/admin/config/login-access",
			},
			notContain: []string{"disabled"},
		},
		{
			name:   "wecom start blocked",
			method: http.MethodGet,
			path:   "/auth/wecom/start",
			status: http.StatusNotImplemented,
			contains: []string{
				"企业微信登录入口已预留",
			},
		},
		{
			name:   "sidebar shell",
			method: http.MethodGet,
			path:   SidebarPagePath,
			status: http.StatusOK,
			contains: []string{
				"核心画像",
				"问卷",
				"商品",
				"订单",
				"优惠券",
				"素材",
				"data-bind-mobile-url=\"/sidebar/bind-mobile\"",
				"data-jssdk-config-url=\"/api/sidebar/jssdk-config\"",
				"data-context-token-url=\"/api/sidebar/context-token\"",
				"data-workbench-url=\"/api/sidebar/v2/workbench\"",
				"data-profile-url=\"/api/sidebar/v2/profile\"",
				"data-questionnaires-url=\"/api/sidebar/v2/questionnaires\"",
				"data-send-intents-url=\"/api/sidebar/v2/send-intents\"",
				"https://res.wx.qq.com/open/js/jweixin-1.6.0.js",
			},
			notContain: []string{"/api/v3/sidebar/", "聊天", "标签", "跟进", "运营", "自动化", "XMLHttpRequest", "sendBeacon"},
		},
		{
			name:   "sidebar shell javascript",
			method: http.MethodGet,
			path:   "/static/sidebar_workbench/sidebar_workbench.js",
			status: http.StatusOK,
			contains: []string{
				"data-tab",
				"/api/sidebar/jssdk-config",
				"/api/sidebar/context-token",
				"/api/sidebar/v2/workbench",
				"/api/sidebar/v2/questionnaires",
				"/api/sidebar/v2/products",
				"/api/sidebar/v2/orders",
				"/api/sidebar/v2/coupons",
				"/api/sidebar/v2/materials",
				"sendChatMessage",
				"/api/sidebar/oauth/start?next=/sidebar/bind-mobile",
				"Authorization: \"Bearer \"",
			},
			notContain: []string{"/api/v3/sidebar/", "chat-activity", "other-staff-messages", "/tags", "/owners", "XMLHttpRequest", "sendBeacon"},
		},
		{
			name:   "access page",
			method: http.MethodGet,
			path:   LoginAccessPath,
			status: http.StatusOK,
			contains: []string{
				"data-admin-access-root",
				"/api/admin/access/users",
				"username",
				"display_name",
				"wecom_userid",
				"admin_access.js",
			},
			notContain: []string{"session_version", "password_hash", "digest"},
		},
		{
			name:   "access javascript",
			method: http.MethodGet,
			path:   "/static/admin_console/admin_access.js",
			status: http.StatusOK,
			contains: []string{
				"last_login_at",
				"aicrm_admin_csrf",
				"X-CSRF-Token",
				"/api/admin/access/users/",
			},
			notContain: []string{"session_version", "password_hash", "digest"},
		},
		{
			name:   "oneid page",
			method: http.MethodGet,
			path:   OneIDPagePath,
			status: http.StatusOK,
			contains: []string{
				"OneID 身份中心",
				"data-admin-oneid-root",
				"/api/admin/oneid/resolve",
				"/api/admin/oneid/customers/",
				"/api/admin/oneid/conflicts",
				"/api/admin/oneid/merge-candidates",
				"name=\"kind\"",
				"name=\"scope\"",
				"name=\"value\"",
				"admin_oneid.js",
			},
			notContain: []string{
				"name=\"assurance\"",
				"localStorage",
				"sessionStorage",
				"/api/v3/",
				"fixture",
			},
		},
		{
			name:   "oneid javascript",
			method: http.MethodGet,
			path:   "/static/admin_console/admin_oneid.js",
			status: http.StatusOK,
			contains: []string{
				"/api/admin/oneid/resolve",
				"JSON.stringify({ kind: kind, scope: scope, value: value })",
				"credentials: \"same-origin\"",
				"textContent",
			},
			notContain: []string{
				"assurance",
				"localStorage",
				"sessionStorage",
				"innerHTML",
				"console.",
			},
		},
		{
			name:       "customer directory page",
			method:     http.MethodGet,
			path:       "/admin/customers",
			status:     http.StatusOK,
			contains:   []string{"data-customer-directory-root", "/api/admin/customers", "/api/admin/customer-sync-runs", "客户查找", "admin-filter-bar admin-form-grid admin-form-grid--wide-filters", "手机号", "客户列表", "admin-table", "admin_customers.js"},
			notContain: []string{"type=\"password\"", "name=\"activation_status\"", "揭示理由", "临时揭示", "+8613812345678", "raw_external_userid", "unionid_value", "/api/v2/", "fixture", "data-profile-section"},
		},
		{
			name:       "customer directory javascript",
			method:     http.MethodGet,
			path:       "/static/admin_console/admin_customers.js",
			status:     http.StatusOK,
			contains:   []string{"credentials: \"same-origin\"", "cache: \"no-store\"", "X-CSRF-Token", "customer_id", "phone-reveal", "phone.startsWith(\"+86\") ? phone.slice(3)", "/360", "订单统计", "问卷统计", "风险摘要", "最近触点", "/admin/message-archive/customers/"},
			notContain: []string{"customer-avatar", "phone_assurance", "item.activation_status", "declared", "localStorage", "sessionStorage", "console.log", "/api/v2/", "chat-activity", "survey-answers"},
		},
		{
			name:       "message archive customer entry",
			method:     http.MethodGet,
			path:       "/admin/message-archive",
			status:     http.StatusOK,
			contains:   []string{"data-message-archive-entry", "选择客户", "href=\"/admin/customers\""},
			notContain: []string{"data-message-archive-root", "name=\"q\"", "Customer ID"},
		},
		{
			name:       "customer profile page",
			method:     http.MethodGet,
			path:       "/admin/customers/42",
			status:     http.StatusOK,
			contains:   []string{"客户档案", "admin-module-banner", "admin-profile-grid", "admin-split-grid admin-customer-detail-layout", "admin-customer-detail-main", "admin-customer-detail-sidebar", "customer-360-sections"},
			notContain: []string{"external_userid", "UnionID", "unionid", "declared", "verified", "+8613812345678", "揭示理由", "customer-list-filters", "customer-sync-start", "跟进成员", "聊天记录", "data-profile-section"},
		},
		{
			name:   "oneid css asset",
			method: http.MethodGet,
			path:   "/static/admin_console/admin_oneid.css",
			status: http.StatusOK,
			contains: []string{
				".admin-oneid-query-grid",
				".admin-oneid-detail",
			},
		},
		{
			name:   "admin shell javascript",
			method: http.MethodGet,
			path:   "/static/admin_console/admin_shell.js",
			status: http.StatusOK,
			contains: []string{
				"X-CSRF-Token",
				"method: \"POST\"",
				"credentials: \"same-origin\"",
			},
			notContain: []string{"localStorage", "sessionStorage", "console."},
		},
		{
			name:   "oneid nav icon asset",
			method: http.MethodGet,
			path:   "/static/admin_console/nav-icons/oneid.svg",
			status: http.StatusOK,
			contains: []string{
				"<svg",
			},
		},
		{
			name:   "css asset",
			method: http.MethodGet,
			path:   "/static/admin_console/admin_console.css",
			status: http.StatusOK,
			contains: []string{
				".admin-layout",
				"--brand: #3370ff",
			},
		},
		{
			name:   "sidebar css asset",
			method: http.MethodGet,
			path:   "/static/sidebar_workbench/sidebar_workbench.css",
			status: http.StatusOK,
			contains: []string{
				".profile-card",
				".modal",
			},
		},
		{
			name:   "nav icon asset",
			method: http.MethodGet,
			path:   "/static/admin_console/nav-icons/wechat_pay_transactions.svg",
			status: http.StatusOK,
			contains: []string{
				"<svg",
			},
		},
		{
			name:   "v3 sidebar api is not registered",
			method: http.MethodGet,
			path:   "/api/v3/sidebar/workbench",
			status: http.StatusNotFound,
		},
		{
			name:   "production workbench api is not registered",
			method: http.MethodGet,
			path:   SidebarWorkbenchPath,
			status: http.StatusNotFound,
		},
		{
			name:   "questionnaire api is not registered",
			method: http.MethodGet,
			path:   "/api/sidebar/v2/questionnaires",
			status: http.StatusNotFound,
		},
		{
			name:   "product api is not registered",
			method: http.MethodGet,
			path:   "/api/sidebar/v2/products",
			status: http.StatusNotFound,
		},
		{
			name:   "order api is not registered",
			method: http.MethodGet,
			path:   "/api/sidebar/v2/orders",
			status: http.StatusNotFound,
		},
		{
			name:   "coupon api is not registered",
			method: http.MethodGet,
			path:   "/api/sidebar/v2/coupons",
			status: http.StatusNotFound,
		},
		{
			name:   "material api is not registered",
			method: http.MethodGet,
			path:   "/api/sidebar/v2/materials",
			status: http.StatusNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.path, nil)
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			body := response.Body.String()
			for _, expected := range test.contains {
				if !strings.Contains(body, expected) {
					t.Errorf("body missing %q", expected)
				}
			}
			for _, forbidden := range test.notContain {
				if strings.Contains(body, forbidden) {
					t.Errorf("body contains forbidden %q", forbidden)
				}
			}
		})
	}
}

func TestRendererAccessContractConsumesNextAndMapsLoginErrors(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.Render(nil, response, http.StatusUnauthorized, "login", map[string]any{
		"next_path":        "/admin/config/login-access",
		"error":            "invalid_credentials",
		"login_csrf_token": "form-token",
		"password":         "must-not-render",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
	body := response.Body.String()
	for _, expected := range []string{"name=\"username\"", "value=\"/admin/config/login-access\"", "name=\"login_csrf_token\" value=\"form-token\"", "账号或密码不正确，请重试。"} {
		if !strings.Contains(body, expected) {
			t.Errorf("body missing %q", expected)
		}
	}
	if strings.Contains(body, "invalid_credentials") || strings.Contains(body, "must-not-render") {
		t.Fatal("renderer exposed raw error or ignored credential")
	}
}

func TestStaticAssetsUseBrowserApplicableContentType(t *testing.T) {
	handler := MustHandler()
	for _, test := range []struct {
		path        string
		contentType string
	}{
		{"/static/admin_console/admin_console.css", "text/css"},
		{"/static/admin_console/admin_audience.css", "text/css"},
		{"/static/admin_console/admin_audience_detail.css", "text/css"},
		{"/static/admin_console/automation_capability_selector.css", "text/css"},
		{"/static/admin_console/send_content_readonly_detail.css", "text/css"},
		{"/static/admin_console/ai_audience_send_records.css", "text/css"},
		{"/static/admin_console/admin_console.js", "text/javascript"},
		{"/static/admin_console/tag_sync_bridge.js", "text/javascript"},
		{"/static/admin_console/automation_create_code_adapter.js", "text/javascript"},
		{"/static/admin_console/survey_operations.js", "text/javascript"},
		{"/static/admin_console/config_adminops_bridge.js", "text/javascript"},
		{"/static/admin_console/template_parameter_form.js", "text/javascript"},
		{"/static/admin_console/admin_audience_template_host.js", "text/javascript"},
		{"/static/admin_console/nav-icons/automation_conversion.svg", "image/svg+xml"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d", test.path, response.Code)
		}
		if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, test.contentType) {
			t.Errorf("%s content-type=%q, want %s", test.path, got, test.contentType)
		}
	}
}

func TestAudienceTemplateControllerIsFrozenDD8Asset(t *testing.T) {
	contents, err := os.ReadFile("static/admin_console/template_parameter_form.js")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contents)
	if got := hex.EncodeToString(digest[:]); got != "ab63c6446f37fd94c3fc07439a89cf90e6114b1c4e962d60cfe96bd72b8bdc1c" {
		t.Fatalf("frozen template controller digest=%s", got)
	}
}

func TestRenderTagsKeepsPR10AsTheOnlyAdminShell(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.RenderTags(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/wecom-tags", nil), "企微标签管理", "", "api.admin_wecom_tags_page"), `<section data-page="tags">frozen donor fragment</section>`, TagsAssets{TokensCSS: "/assets/tokens.css", LabsCSS: "/assets/labs.css", AdminJS: "/assets/admin.js"})
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Count(body, `<main`) != 1 || strings.Count(body, `<aside`) != 1 || strings.Contains(body, `class="side"`) || !strings.Contains(body, `<template id="tpl"><section data-page="tags">frozen donor fragment</section></template>`) || !strings.Contains(body, `data-admin-shell-source="v3_webshell"`) || !strings.Contains(body, `/static/admin_console/tag_sync_bridge.js`) {
		t.Fatalf("tags shell mismatch status=%d body=%q", response.Code, body)
	}
}

func TestRenderHXCMountsLiveDashboardInTheV3Shell(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.RenderHXC(
		response,
		AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/hxc-dashboard", nil), "漏斗 / 数据看板", "HXC 当前全量投影", "api.admin_hxc_dashboard_workspace"),
		HXCAssets{TokensCSS: "/hxc-dashboard-assets/tokens.css", LabsCSS: "/hxc-dashboard-assets/labs.css", AdminJS: "/hxc-dashboard-assets/admin.js"},
	)
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Count(body, `<main`) != 1 || strings.Count(body, `<aside`) != 1 || !strings.Contains(body, `data-admin-shell-source="v3_webshell" data-page="funnel"`) || !strings.Contains(body, `<main id="stage" class="stage rich"></main>`) || !strings.Contains(body, `/hxc-dashboard-assets/admin.js`) || strings.Contains(body, "功能待接入") {
		t.Fatalf("HXC shell mismatch status=%d body=%q", response.Code, body)
	}
}

func TestRenderProductsKeepsPR10AsTheOnlyAdminShell(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.RenderProducts(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/wechat-pay/products", nil), "普通商品", "", "api.admin_products_page"), "products", `<section data-page="products">frozen donor product fragment</section>`, ProductAssets{TokensCSS: "/product-assets/tokens.css", LabsCSS: "/product-assets/labs.css", HostJS: "/product-assets/product-host.js"})
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Count(body, `<main`) != 1 || strings.Count(body, `<aside`) != 1 || strings.Contains(body, `class="side"`) || strings.Contains(body, `class="shell"`) || !strings.Contains(body, `<template id="tpl"><section data-page="products">frozen donor product fragment</section></template>`) || !strings.Contains(body, `data-admin-shell-source="v3_webshell"`) || !strings.Contains(body, `src="/product-assets/product-host.js"`) {
		t.Fatalf("product shell mismatch status=%d body=%q", response.Code, body)
	}
}

func TestRenderOrdersMountsFrozenTransactionPageAndHostImportControl(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.RenderOrders(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/orders", nil), "交易管理", "", "api.admin_orders_page"), "orders", `<section data-page="orders">frozen donor orders</section>`, OrderAssets{TokensCSS: "/order-assets/tokens.css", LabsCSS: "/order-assets/labs.css", AdminJS: "/order-assets/admin.js"})
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Count(body, `<aside`) != 1 || strings.Contains(body, `class="side"`) || !strings.Contains(body, `<template id="tpl"><section data-page="orders">frozen donor orders</section></template>`) || !strings.Contains(body, `data-order-import`) || !strings.Contains(body, `/static/admin_console/order_import.js`) {
		t.Fatalf("order shell mismatch status=%d body=%q", response.Code, body)
	}
}

func TestRenderCouponsKeepsPR10AsTheOnlyAdminShell(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.RenderCoupons(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/coupons", nil), "优惠券", "", "api.admin_coupons_page"), "coupons", `<section data-page="coupons">frozen donor coupon fragment</section>`, CouponAssets{TokensCSS: "/assets/tokens.css", LabsCSS: "/assets/labs.css", AdminJS: "/assets/admin.js"})
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Count(body, `<main`) != 1 || strings.Count(body, `<aside`) != 1 || strings.Contains(body, `class="side"`) || strings.Contains(body, `class="shell"`) || !strings.Contains(body, `<template id="tpl"><section data-page="coupons">frozen donor coupon fragment</section></template>`) || !strings.Contains(body, `data-page="coupons"`) {
		t.Fatalf("coupon shell mismatch status=%d body=%q", response.Code, body)
	}
}

func TestRenderAutomationUsesOnlyV3CreateCodeHostBinding(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	assets := AutomationAssets{TokensCSS: "/assets/tokens.css", LabsCSS: "/assets/labs.css", AdminJS: "/assets/admin.js"}
	response := httptest.NewRecorder()
	err = renderer.RenderAutomation(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/agentEdit.html?type=agent", nil), "自动化话术", "", "api.admin_automation_agents"), "agentEdit", `<section data-page="agentEdit">frozen donor fragment</section>`, assets, "agent_0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Count(body, `<main`) != 1 || strings.Count(body, `<aside`) != 1 || strings.Contains(body, `class="side"`) || !strings.Contains(body, `<template id="tpl"><section data-page="agentEdit">frozen donor fragment</section></template>`) || !strings.Contains(body, `data-automation-create-code="agent_0123456789abcdef0123456789abcdef"`) || !strings.Contains(body, `<script defer src="/static/admin_console/automation_create_code_adapter.js?v=automation-create-code-v2"></script>`) {
		t.Fatalf("automation create shell mismatch status=%d body=%q", response.Code, body)
	}

	response = httptest.NewRecorder()
	err = renderer.RenderAutomation(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/agentEdit.html?id=7", nil), "自动化话术", "", "api.admin_automation_agents"), "agentEdit", `<section data-page="agentEdit">frozen donor fragment</section>`, assets, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Body.String(), `data-automation-create-code=`) {
		t.Fatal("existing automation editor received a create-code binding")
	}
}

func TestAutomationCreateCodeAdapterBrowserTiming(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate webshell test")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(repo, "web", "dist", "admin", "agentEdit.html")); err != nil {
		t.Skip("browser bundle is not staged")
	}
	command := exec.Command("node", "internal/webshell/static/admin_console/automation_create_code_adapter.test.mjs")
	command.Dir = repo
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("browser timing contract failed: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "automation-create-code-adapter-browser: PASS") {
		t.Fatalf("browser timing contract did not report success: %q", output.String())
	}
}

func TestAudienceActivationReadinessBrowserContract(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate webshell test")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	command := exec.Command("node", "internal/webshell/static/admin_console/admin_audience_detail.test.mjs")
	command.Dir = repo
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("audience readiness browser contract failed: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "admin-audience-activation-readiness-browser: PASS") {
		t.Fatalf("audience readiness browser contract did not report success: %q", output.String())
	}
}

func TestAudienceFrozenTemplateHostBrowserContract(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate webshell test")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	command := exec.Command("node", "internal/webshell/static/admin_console/admin_audience_template_host.test.mjs")
	command.Dir = repo
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("audience template Host browser contract failed: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "admin-audience-template-host-browser: PASS") {
		t.Fatalf("audience template Host browser contract did not report success: %q", output.String())
	}
}

// TestOperationCyclesHostShellJourney is the release-facing host Journey:
// an authenticated v3 route mounts one frozen fragment in the sole sidebar
// shell and exposes only the v3 binding that starts the donor runtime.
func TestOperationCyclesHostShellJourney(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.RenderOperationCycles(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/operation-cycles/cyclesDetail.html?id=1", nil), "运营闭环", "", "api.admin_operation_cycles_page"), "cyclesDetail", `<section data-proof="frozen">原版页面</section>`, OperationCycleAssets{TokensCSS: "/assets/tokens.css", LabsCSS: "/assets/labs.css", HostJS: "/assets/operationCyclesHost.js"})
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Count(body, `<aside`) != 1 || strings.Contains(body, `class="side"`) || strings.Contains(body, `class="shell"`) || !strings.Contains(body, `<base href="/admin/operation-cycles/">`) || !strings.Contains(body, `data-page="cyclesDetail"`) || !strings.Contains(body, `src="/assets/operationCyclesHost.js"`) || !strings.Contains(body, `<template id="tpl"><section data-proof="frozen">原版页面</section></template>`) {
		t.Fatalf("operation-cycle host shell mismatch status=%d body=%q", response.Code, body)
	}
}

func TestSurveyEditorUsesFullWidthDonorWorkspaceInsideAdminShell(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.RenderSurvey(
		response,
		AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/questionnaireDetail.html?id=11", nil), "问卷编辑", "", "api.admin_questionnaires"),
		"questionnaireDetail",
		`<div class="shell"><header class="topbar">问卷工具栏</header><div class="workspace">编辑区</div></div><div id="questionnaire-editor-config" hidden>{}</div>`,
		SurveyAssets{TokensCSS: "/assets/tokens.css", LabsCSS: "/assets/labs.css", AdminJS: "/assets/admin.js", EditorJS: "/assets/editor.js", EditorCSS: "/assets/editor.css"},
	)
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Contains(body, `<main class="admin-page">`) || strings.Contains(body, `<template id="tpl">`) || strings.Contains(body, `href="/assets/tokens.css"`) || strings.Contains(body, `href="/assets/labs.css"`) || strings.Contains(body, `src="/assets/admin.js"`) || !strings.Contains(body, `<div class="admin-main-wrap">`) || !strings.Contains(body, `<div class="shell"><header class="topbar">问卷工具栏</header><div class="workspace">编辑区</div></div>`) || !strings.Contains(body, `data-page="questionnaireDetail"`) || !strings.Contains(body, `href="/assets/editor.css"`) || !strings.Contains(body, `src="/assets/editor.js"`) || !strings.Contains(body, `survey_operations.js?v=survey-bridge-v2`) {
		t.Fatalf("survey editor host layout mismatch status=%d body=%q", response.Code, body)
	}
}

func TestSurveyQRBridgeBrowserFallback(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is unavailable")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	command := exec.Command("node", "internal/webshell/static/admin_console/survey_qr_bridge.test.mjs")
	command.Dir = repo
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("survey QR bridge browser contract failed: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "survey-qr-bridge-browser: PASS") {
		t.Fatalf("survey QR bridge browser contract did not report success: %q", output.String())
	}
}

func TestMessageArchiveBrowserPrivateImageContract(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is unavailable")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	command := exec.Command("node", "internal/webshell/static/admin_console/message_archive.test.mjs")
	command.Dir = repo
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("message archive browser contract failed: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "message-archive-browser: PASS") {
		t.Fatalf("message archive browser contract did not report success: %q", output.String())
	}
}

func TestAdminCustomersBrowserMessageArchiveEntry(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is unavailable")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	command := exec.Command("node", "internal/webshell/static/admin_console/admin_customers.test.mjs")
	command.Dir = repo
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("admin customers browser contract failed: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "admin-customers-browser: PASS") {
		t.Fatalf("admin customers browser contract did not report success: %q", output.String())
	}
}

func TestLoginPostNeverIssuesSession(t *testing.T) {
	handler := MustHandler()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, LoginPath, strings.NewReader("account=admin&password=secret"))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d", response.Code)
	}
	if response.Header().Get("Set-Cookie") != "" {
		t.Fatal("login shell issued a session cookie")
	}
	if strings.Contains(response.Body.String(), "secret") {
		t.Fatal("login shell echoed credential input")
	}
}
func TestMessageArchiveRendersAsSeparateArchiveHost(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/message-archive/customers/7", nil)
	if err = renderer.RenderAdminStatus(response, http.StatusOK, AdminPageForRequest(request, "会话存档", "", "")); err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `data-message-archive-root`) || !strings.Contains(body, `admin-profile-message-list`) || strings.Contains(body, `customer-profile-root`) || strings.Contains(body, `customer-chat-activity`) {
		t.Fatalf("archive host boundary mismatch: %s", body)
	}
}

func TestRenderGroupOpsInjectsManifestVerifiedReadonlyContentRenderer(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	assets := GroupOpsAssets{TokensCSS: "/groupops-assets/assets/tokens.css", LabsCSS: "/groupops-assets/assets/labs.css", AdminJS: "/groupops-assets/assets/admin.js", ReadonlyCSS: "/groupops-assets/aiassistant/send_content_readonly_detail.css", ReadonlyJS: "/groupops-assets/aiassistant/send_content_readonly_detail.js"}
	response := httptest.NewRecorder()
	err = renderer.RenderGroupOps(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/groupopsDetail.html?history=1&id=9", nil), "群运营计划", "", "api.admin_group_ops_plan_detail"), "groupopsDetail", `<section data-page="groupopsDetail">frozen donor fragment</section>`, assets)
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `href="/groupops-assets/aiassistant/send_content_readonly_detail.css"`) || !strings.Contains(body, `<script defer src="/groupops-assets/aiassistant/send_content_readonly_detail.js"></script>`) || !strings.Contains(body, `<script defer src="/static/admin_console/groupops_history_readonly_bridge.js"></script>`) || !strings.Contains(body, `<template id="tpl"><section data-page="groupopsDetail">frozen donor fragment</section></template>`) {
		t.Fatalf("group ops read-only shell mismatch status=%d body=%q", response.Code, body)
	}
	if err := renderer.RenderGroupOps(httptest.NewRecorder(), AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/groupops.html", nil), "群运营计划", "", "api.admin_group_ops_ui"), "groupops", `<section></section>`, GroupOpsAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS}); err == nil {
		t.Fatal("group ops shell accepted missing read-only content assets")
	}
}
