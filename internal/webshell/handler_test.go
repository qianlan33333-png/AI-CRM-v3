package webshell

import (
	"net/http"
	"net/http/httptest"
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
	got := PathFor("api.admin_console_customer_detail", map[string]string{"external_userid": "contact/a"})
	if got != "/admin/customers/contact%2Fa" {
		t.Fatalf("customer detail path=%q", got)
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
				"https://res.wx.qq.com/open/js/jweixin-1.6.0.js",
			},
			notContain: []string{"/api/v3/sidebar/", "/api/sidebar/v2/questionnaires", "/api/sidebar/v2/products", "XMLHttpRequest", "sendBeacon"},
		},
		{
			name:   "sidebar shell javascript",
			method: http.MethodGet,
			path:   "/static/sidebar_workbench/sidebar_workbench.js",
			status: http.StatusOK,
			contains: []string{
				"data-tab",
				"功能待接入",
				"/api/sidebar/jssdk-config",
				"/api/sidebar/context-token",
				"/api/sidebar/v2/workbench",
				"/api/sidebar/oauth/start?next=/sidebar/bind-mobile",
				"Authorization: \"Bearer \"",
			},
			notContain: []string{"/api/v3/sidebar/", "/api/sidebar/v2/questionnaires", "/api/sidebar/v2/products", "/api/sidebar/v2/orders", "/api/sidebar/v2/coupons", "/api/sidebar/v2/materials", "workbenchURL.searchParams.set(\"external_userid\"", "XMLHttpRequest", "sendBeacon"},
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
