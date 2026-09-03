package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

type fakeSecurity struct {
	principal        accessdomain.Principal
	authErr, csrfErr error
}

func (f fakeSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return f.principal, f.authErr
}
func (f fakeSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return f.principal, f.csrfErr
}

type fakeApplication struct{}

func (fakeApplication) ListGroups(context.Context) ([]segmentdomain.Group, error) { return nil, nil }
func (fakeApplication) CreateGroup(context.Context, segmentapp.GroupCommand) (segmentdomain.Group, error) {
	return segmentdomain.Group{}, nil
}
func (fakeApplication) UpdateGroup(context.Context, segmentapp.GroupCommand) (segmentdomain.Group, error) {
	return segmentdomain.Group{}, nil
}
func (fakeApplication) DeleteGroup(context.Context, segmentapp.VersionCommand) error { return nil }
func (fakeApplication) ListPackages(context.Context, int, int, bool) (segmentapp.PackagePage, error) {
	return segmentapp.PackagePage{}, nil
}
func (fakeApplication) GetPackage(context.Context, int64) (segmentdomain.Package, error) {
	return segmentdomain.Package{}, nil
}
func (fakeApplication) CreatePackage(context.Context, segmentapp.PackageCreateCommand) (segmentdomain.Package, error) {
	return segmentdomain.Package{}, nil
}
func (fakeApplication) UpdatePackage(context.Context, segmentapp.PackageUpdateCommand) (segmentdomain.Package, error) {
	return segmentdomain.Package{}, nil
}
func (fakeApplication) CopyPackage(context.Context, segmentapp.VersionCommand) (segmentdomain.Package, error) {
	return segmentdomain.Package{}, nil
}
func (fakeApplication) TransitionPackage(context.Context, segmentapp.VersionCommand, segmentdomain.Lifecycle) (segmentdomain.Package, error) {
	return segmentdomain.Package{}, segmentapp.ErrNotReady
}
func (fakeApplication) PutConfiguration(context.Context, segmentapp.ConfigurationCommand) (segmentdomain.ConfigurationVersion, error) {
	return segmentdomain.ConfigurationVersion{}, nil
}
func (fakeApplication) CurrentConfiguration(context.Context, int64) (segmentdomain.ConfigurationVersion, error) {
	return segmentdomain.ConfigurationVersion{}, nil
}

func TestHandlerAuthRBACCSRFAndClosedTemplates(t *testing.T) {
	viewer := accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}
	admin := accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	tests := []struct {
		name, method, path, body string
		security                 fakeSecurity
		want                     int
	}{
		{"unauthenticated", http.MethodGet, "/api/admin/ai-audience/templates", "", fakeSecurity{authErr: errors.New("no")}, 401},
		{"viewer templates", http.MethodGet, "/api/admin/ai-audience/templates", "", fakeSecurity{principal: viewer}, 200},
		{"viewer cannot mutate", http.MethodPost, "/api/admin/ai-audience/packages", `{"name":"x","template_key":"active_contacts"}`, fakeSecurity{principal: viewer}, 403},
		{"csrf required", http.MethodPost, "/api/admin/ai-audience/packages", `{"name":"x","template_key":"active_contacts"}`, fakeSecurity{principal: admin, csrfErr: errors.New("csrf")}, 403},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, _ := NewHandler(fakeApplication{}, test.security)
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Idempotency-Key", "1234567890abcdef")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.name == "viewer templates" && !strings.Contains(response.Body.String(), `"active_contacts"`) {
				t.Fatalf("body=%s", response.Body.String())
			}
		})
	}
}

func TestHandlerRejectsUnknownAndOversizedBodiesAndFailsClosedActivation(t *testing.T) {
	admin := accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
	handler, _ := NewHandler(fakeApplication{}, fakeSecurity{principal: admin})
	for name, fixture := range map[string]struct {
		path, body string
		want       int
	}{
		"unknown field":        {"/api/admin/ai-audience/packages", `{"name":"x","template_key":"active_contacts","token":"secret"}`, 400},
		"oversized":            {"/api/admin/ai-audience/packages", `{"name":"` + strings.Repeat("x", 70<<10) + `","template_key":"active_contacts"}`, 413},
		"activation not ready": {"/api/admin/ai-audience/packages/1/activate", `{"expected_version":1}`, 503},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, fixture.path, strings.NewReader(fixture.body))
			request.Header.Set("Idempotency-Key", "1234567890abcdef")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != fixture.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
