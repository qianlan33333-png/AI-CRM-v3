package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	producthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/http"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/webshell"
)

func TestProductDataEntryIsServedByTheShell(t *testing.T) {
	marker := http.NotFoundHandler()
	authentication := &fakeAccessAuthentication{principal: accessdomain.Principal{
		Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin},
	}}
	handler, err := routeApplicationWithProducts(
		marker, marker, marker, marker, marker, marker, marker, marker, marker, marker,
		marker, marker, marker, marker, webshell.MustHandler(), authentication, "https://crm.example",
	)
	if err != nil {
		t.Fatal(err)
	}
	handler = mountMemberGridUI(handler, producthttp.NewMemberGridUI())

	// The member-grid data page is a shell-served built document now: the new
	// shell embeds the member-grid controls, so the frozen product host bundle
	// is retired.  In this harness (no dist) it renders the shell placeholder.
	request := httptest.NewRequest(http.MethodGet, "/admin/spProductData.html?id=7", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `data-admin-shell-source="v3_webshell"`) || strings.Contains(body, "member_grid_host.js") {
		t.Fatalf("member-grid entry status=%d body=%s", response.Code, body)
	}

	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/static/service-period/icons/funnel.svg", nil))
	if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), "<svg") {
		t.Fatalf("embedded member-grid icon status=%d body=%s", asset.Code, asset.Body.String())
	}

	authentication.err = accessdomain.ErrAuthentication
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/admin/spProductData.html?id=7", nil))
	if unauthenticated.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated member-grid entry status=%d", unauthenticated.Code)
	}
}
