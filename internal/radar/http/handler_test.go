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

type testManager struct {
	created radarport.CreateCommand
	page    radarport.LinkPage
}

func (m *testManager) List(context.Context, radarport.ListQuery) (radarport.LinkPage, error) {
	if m.page.Items != nil {
		return m.page, nil
	}
	return radarport.LinkPage{Items: []radarport.LinkSummary{{Link: testLink(), StatisticsStatus: radarport.LinkStatisticsReady}}, Total: 1, Limit: 20}, nil
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

type testQuery struct{ events radarport.EventPage }

func (testQuery) Stats(context.Context, radar.RadarID) (radarport.Stats, error) {
	return radarport.Stats{TotalEvents: 3, TotalLandings: 1, AuthorizedUsers: 1, ViewCount: 2}, nil
}
func (q testQuery) Events(context.Context, radarport.EventQuery) (radarport.EventPage, error) {
	return q.events, nil
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

func TestAdminListMapsMeasuredAndUnavailableStatisticsWithoutFallbacks(t *testing.T) {
	lastViewedAt := time.Date(2026, 9, 12, 9, 30, 0, 0, time.UTC)
	manager := &testManager{page: radarport.LinkPage{Items: []radarport.LinkSummary{
		{Link: testLink(), StatisticsStatus: radarport.LinkStatisticsReady, TotalLandings: 7, AuthorizedUsers: 3, AuthorizedViews: 2, ViewCount: 4, LastViewedAt: &lastViewedAt},
		{Link: radar.Link{ID: 2, PublicCode: "rd_zyxwvutsrqponmlkjihgfe", Name: "Unavailable", Title: "Unavailable", Content: radar.Content{Type: radar.ContentTypeLink, DestinationURL: "https://example.com/unavailable"}, AuthPolicy: radar.AuthPolicyAnonymous, Status: radar.StatusDraft, Version: 1, CreatedBy: 7, UpdatedBy: 7, CreatedAt: lastViewedAt, UpdatedAt: lastViewedAt}, StatisticsStatus: radarport.LinkStatisticsUnavailable},
	}, Total: 2, Limit: 20}}
	handler, err := NewHandler(manager, testQuery{}, testPublic{}, testSecurity{}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/radar-links", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Items []struct {
			LinkID           int64   `json:"link_id"`
			StatisticsStatus string  `json:"statistics_status"`
			TotalLandings    *int64  `json:"total_landings"`
			AuthorizedUsers  *int64  `json:"authorized_users"`
			AuthorizedViews  *int64  `json:"authorized_views"`
			ViewCount        *int64  `json:"view_count"`
			LastViewedAt     *string `json:"last_viewed_at"`
		} `json:"items"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 2 {
		t.Fatalf("items=%+v", payload.Items)
	}
	ready := payload.Items[0]
	if ready.StatisticsStatus != "ready" || ready.TotalLandings == nil || *ready.TotalLandings != 7 || ready.AuthorizedUsers == nil || *ready.AuthorizedUsers != 3 || ready.AuthorizedViews == nil || *ready.AuthorizedViews != 2 || ready.ViewCount == nil || *ready.ViewCount != 4 || ready.LastViewedAt == nil || *ready.LastViewedAt == "" {
		t.Fatalf("ready=%+v", ready)
	}
	unavailable := payload.Items[1]
	if unavailable.StatisticsStatus != "unavailable" || unavailable.TotalLandings != nil || unavailable.AuthorizedUsers != nil || unavailable.AuthorizedViews != nil || unavailable.ViewCount != nil || unavailable.LastViewedAt != nil {
		t.Fatalf("unavailable=%+v", unavailable)
	}
}

func TestEventExportFormatsBusinessTimestampsInShanghai(t *testing.T) {
	query := testQuery{events: radarport.EventPage{Items: []radarport.EventProjection{{
		ReceiptID: "rre_export", RadarID: 1, Stage: radarport.EventLanding,
		Attribution: radarport.AttributionResolved, CustomerRef: "customer:7",
		OccurredAt: time.Date(2026, time.September, 5, 0, 1, 2, 611265000, time.UTC),
	}}, Total: 1, Limit: 500}}
	handler, err := NewHandler(&testManager{}, query, testPublic{}, testSecurity{}, "https://crm.example")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/radar-links/1/events/export", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "2026-09-05 08:01:02") || strings.Contains(response.Body.String(), "2026-09-05T00:01:02") || !strings.Contains(response.Body.String(), "访问落地页") || !strings.Contains(response.Body.String(), "已关联客户") || strings.Contains(response.Body.String(), ",landing,resolved,") {
		t.Fatalf("business CSV did not use Shanghai display time: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestOAuthFailureOffersOnlyValidatedManualRetry(t *testing.T) {
	handler, _ := NewHandler(&testManager{}, testQuery{}, testPublic{}, testSecurity{}, "https://crm.example")
	for _, code := range []string{"rd_abcdefghijklmnopqrstuv", "//evil.example"} {
		request := httptest.NewRequest(http.MethodGet, "/api/public/radar/oauth/callback?code=failed&state=opaque", nil)
		request.AddCookie(&http.Cookie{Name: "radar_oauth_return", Value: code})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 503 || response.Header().Get("Location") != "" || !strings.Contains(response.Body.String(), "微信授权未完成") {
			t.Fatalf("unexpected failure page: %d", response.Code)
		}
		if strings.Contains(response.Body.String(), `href="/r/`) != (code == "rd_abcdefghijklmnopqrstuv") {
			t.Fatal("unsafe or missing retry link")
		}
	}
}
