package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMountReferralSeparatesPublicAndAdminPrefixes(t *testing.T) {
	public := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	admin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	fallback := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	handler := mountReferral(fallback, public, admin)

	for path, expected := range map[string]int{
		"/referral/invite/rfi_" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA": http.StatusNoContent,
		"/api/v1/referral/campaigns":             http.StatusNoContent,
		"/api/admin/referral/campaigns":          http.StatusAccepted,
		"/referral":                              http.StatusTeapot,
		"/api/v1/referrals":                      http.StatusTeapot,
		"/r/rd_remaining_pages123":               http.StatusTeapot,
		"/r/not-a-referral-token":                http.StatusTeapot,
		"/referral/invite/rfi_not-a-valid-token": http.StatusNoContent,
		"/r/rfi_not-a-valid-token":               http.StatusTeapot,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != expected {
			t.Fatalf("path=%s status=%d want=%d", path, response.Code, expected)
		}
	}
}

func TestMountReferralFailsClosedWhenAHandlerIsAbsent(t *testing.T) {
	response := httptest.NewRecorder()
	mountReferral(http.NotFoundHandler(), nil, nil).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/referral/campaigns", nil))
	if response.Code != http.StatusServiceUnavailable || response.Body.String() != "{\"error\":\"referral_unavailable\"}" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}
