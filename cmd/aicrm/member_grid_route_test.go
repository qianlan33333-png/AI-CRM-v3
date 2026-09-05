package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	producthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/http"
)

func TestProductDataEntryMountsEmbeddedMemberGridAndAssets(t *testing.T) {
	marker := http.NotFoundHandler()
	authentication := &fakeAccessAuthentication{principal: accessdomain.Principal{
		Kind: accessdomain.KindAdmin, InternalID: 7, Roles: []accessdomain.Role{accessdomain.RoleAdmin},
	}}
	productUI := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := producthttp.RenderMemberGridInternal(w, r, r.URL.Query().Get("id")); err != nil {
			http.NotFound(w, r)
		}
	})
	handler, err := routeApplicationWithProducts(
		marker, marker, marker, marker, marker, marker, marker, marker, marker, marker,
		marker, productUI, marker, marker, marker, authentication, "https://crm.example",
	)
	if err != nil {
		t.Fatal(err)
	}
	handler = mountMemberGridUI(handler, producthttp.NewMemberGridUI())

	request := httptest.NewRequest(http.MethodGet, "/admin/spProductData.html?id=7", nil)
	request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: "valid"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `id="spMemberGrid"`) || !strings.Contains(response.Body.String(), `data-service-product-id="7"`) || !strings.Contains(response.Body.String(), `member_grid_host.js`) {
		t.Fatalf("member-grid entry status=%d body=%s", response.Code, response.Body.String())
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
