package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

type testSecurity struct{}

func (testSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (testSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 7}, nil
}

type testManager struct{ created radarport.CreateCommand }

func (m *testManager) List(context.Context, radarport.ListQuery) (radarport.LinkPage, error) {
	return radarport.LinkPage{Items: []radarport.LinkSummary{{Link: testLink()}}, Total: 1, Limit: 20}, nil
}
func (m *testManager) Get(context.Context, radar.RadarID) (radarport.LinkDetail, error) {
	return radarport.LinkDetail{Link: testLink()}, nil
}
func (m *testManager) Create(_ context.Context, c radarport.CreateCommand) (radarport.LinkDetail, error) {
	m.created = c
	return radarport.LinkDetail{Link: testLink()}, nil
}
func (m *testManager) Update(context.Context, radarport.UpdateCommand) (radarport.LinkDetail, error) {
	return radarport.LinkDetail{Link: testLink()}, nil
}
func (m *testManager) SetStatus(context.Context, radarport.SetStatusCommand) (radarport.LinkDetail, error) {
	return radarport.LinkDetail{Link: testLink()}, nil
}

type testQuery struct{}

func (testQuery) Stats(context.Context, radar.RadarID) (radarport.Stats, error) {
	return radarport.Stats{TotalEvents: 3, TotalLandings: 1, AuthorizedUsers: 1, ViewCount: 2}, nil
}
func (testQuery) Events(context.Context, radarport.EventQuery) (radarport.EventPage, error) {
	return radarport.EventPage{}, nil
}

type testPublic struct{ openErr error }

func (p testPublic) Open(context.Context, radar.PublicCode, string) (radarport.PublicAccess, error) {
	return radarport.PublicAccess{}, p.openErr
}
func (testPublic) CompleteOAuth(context.Context, string, string) (string, string, error) {
	return "", "", radarport.ErrUnavailable
}
func (testPublic) Content(context.Context, radar.PublicCode, string) (radarport.Content, error) {
	return radarport.Content{}, radarport.ErrNotFound
}
func (testPublic) Record(context.Context, radar.PublicCode, string, radarport.EventStage, string) (radarport.EventProjection, bool, error) {
	return radarport.EventProjection{}, false, radarport.ErrNotFound
}
func testLink() radar.Link {
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	return radar.Link{ID: 1, PublicCode: "rd_abcdefghijklmnopqrstuv", Name: "Guide", Title: "Guide", Content: radar.Content{Type: radar.ContentTypeLink, DestinationURL: "https://example.com"}, AuthPolicy: radar.AuthPolicyUnionIDRequired, Status: radar.StatusDraft, Version: 1, CreatedBy: 7, UpdatedBy: 7, CreatedAt: now, UpdatedAt: now}
}

func TestAdminCreateDefaultsToUnionIDAndEmitsNoExternalIdentity(t *testing.T) {
	manager := &testManager{}
	handler, err := NewHandler(manager, testQuery{}, testPublic{}, testSecurity{}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/radar-links", strings.NewReader(`{"expected_version":0,"name":"Guide","title":"Guide","destination_url":"https://example.com"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 201 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if manager.created.AuthPolicy != radar.AuthPolicyUnionIDRequired {
		t.Fatalf("policy=%s", manager.created.AuthPolicy)
	}
	body := strings.ToLower(response.Body.String())
	for _, forbidden := range []string{"unionid\"", "openid", "external_userid", "phone"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response leaked forbidden field %q: %s", forbidden, body)
		}
	}
	var payload map[string]any
	if json.Unmarshal(response.Body.Bytes(), &payload) != nil {
		t.Fatal("invalid JSON")
	}
}
func TestDisabledPublicLinkIsGone(t *testing.T) {
	handler, _ := NewHandler(&testManager{}, testQuery{}, testPublic{openErr: radarport.ErrGone}, testSecurity{}, "https://crm.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/r/rd_abcdefghijklmnopqrstuv", nil))
	if response.Code != http.StatusGone {
		t.Fatalf("status=%d", response.Code)
	}
}
