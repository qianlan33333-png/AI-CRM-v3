package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMountSegmentAPIOwnsOnlyCanonicalPrefix(t *testing.T) {
	base := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(210) })
	audience := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(211) })
	handler, err := mountSegmentAPI(base, audience)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{
		"/api/admin/ai-audience/packages": 211,
		"/api/admin/customers":            210,
		"/admin/automation-conversion":    210,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != want {
			t.Fatalf("path=%s status=%d want=%d", path, response.Code, want)
		}
	}
}
