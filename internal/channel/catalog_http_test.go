package channel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
)

func TestCatalogHTTPRoleCSRFStrictJSONAndCAS(t *testing.T) {
	now := time.Date(2026, 9, 3, 14, 0, 0, 0, time.UTC)
	app := &catalogHTTPApplication{channel: catalogHTTPChannel(now)}
	security := &catalogHTTPSecurity{principal: accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}
	handler, err := NewCatalogHTTPHandler(CatalogHTTPConfig{Application: app, Security: security, CursorSigningKey: []byte("01234567890123456789012345678901"), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	response := catalogHTTPRequest(handler, http.MethodGet, "/api/admin/channels?limit=1", "", nil)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), `"next_cursor"`) {
		t.Fatalf("list status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	response = catalogHTTPRequest(handler, http.MethodGet, "/api/admin/channels/3", "", nil)
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"4"` {
		t.Fatalf("get status=%d etag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body.String())
	}

	body := catalogHTTPBody()
	response = catalogHTTPRequest(handler, http.MethodPost, "/api/admin/channels", body, map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {"create-key-0001"}, "X-CSRF-Token": {"valid"}})
	if response.Code != http.StatusForbidden {
		t.Fatalf("viewer write status=%d body=%s", response.Code, response.Body.String())
	}
	security.principal.Roles = []accessdomain.Role{accessdomain.RoleAdmin}
	response = catalogHTTPRequest(handler, http.MethodPost, "/api/admin/channels", body, map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {"create-key-0001"}, "X-CSRF-Token": {"valid"}})
	if response.Code != http.StatusCreated || app.created.ActorID != 7 {
		t.Fatalf("admin create status=%d command=%#v body=%s", response.Code, app.created, response.Body.String())
	}
	response = catalogHTTPRequest(handler, http.MethodPatch, "/api/admin/channels/3", body, map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {"update-key-0001"}, "If-Match": {`"4"`}, "X-CSRF-Token": {"valid"}})
	if response.Code != http.StatusOK || app.updated.Update.ExpectedVersion != 4 {
		t.Fatalf("patch status=%d command=%#v body=%s", response.Code, app.updated, response.Body.String())
	}
	response = catalogHTTPRequest(handler, http.MethodPatch, "/api/admin/channels/3", body, map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {"update-key-0002"}, "X-CSRF-Token": {"valid"}})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing If-Match status=%d", response.Code)
	}
	response = catalogHTTPRequest(handler, http.MethodPost, "/api/admin/channels", strings.TrimSuffix(body, "}")+`,"unexpected":true}`, map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {"create-key-0002"}, "X-CSRF-Token": {"valid"}})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown JSON status=%d body=%s", response.Code, response.Body.String())
	}
	response = catalogHTTPRequest(handler, http.MethodPost, "/api/admin/channels", body, map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {"create-key-0003", "create-key-0004"}, "X-CSRF-Token": {"valid"}})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate key status=%d", response.Code)
	}
}

func TestCatalogHTTPTamperedCursorAndApplicationErrors(t *testing.T) {
	now := time.Now().UTC()
	app := &catalogHTTPApplication{channel: catalogHTTPChannel(now)}
	security := &catalogHTTPSecurity{principal: accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}}
	handler, _ := NewCatalogHTTPHandler(CatalogHTTPConfig{Application: app, Security: security, CursorSigningKey: []byte("01234567890123456789012345678901"), Now: func() time.Time { return now }})
	response := catalogHTTPRequest(handler, http.MethodGet, "/api/admin/channels?cursor=bad.signature", "", nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("tampered cursor status=%d", response.Code)
	}
	app.err = ErrCatalogConflict
	response = catalogHTTPRequest(handler, http.MethodPatch, "/api/admin/channels/3", catalogHTTPBody(), map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {"update-key-conflict"}, "If-Match": {"4"}, "X-CSRF-Token": {"valid"}})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "VERSION_CONFLICT") {
		t.Fatalf("conflict status=%d body=%s", response.Code, response.Body.String())
	}
}

func catalogHTTPBody() string {
	value := map[string]any{"channel_type": "qrcode", "carrier_type": "qrcode", "channel_name": "Campaign", "channel_code": "campaign", "scene_value": "", "qr_url": "", "status": "active", "owner_staff_id": "7", "customer_channel": "", "link_url": "", "final_url": "", "welcome_message": "", "welcome_image_library_ids": []int64{}, "welcome_miniprogram_library_ids": []int64{}, "welcome_attachment_library_ids": []int64{}, "welcome_group_invite_library_ids": []int64{}, "auto_accept_friend": false, "entry_tag_id": "", "entry_tag_name": "", "entry_tag_group_name": "", "assignment_mode": "single_owner", "assignment_strategy": "ratio", "overflow_policy": "", "assignment_config_json": map[string]any{}}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func catalogHTTPChannel(now time.Time) channeldomain.Channel {
	return channeldomain.Channel{ID: 3, Code: "campaign", Status: channeldomain.StatusActive, ConfigVersion: 2, Version: 4, CreatedAt: now.Add(-time.Hour), UpdatedAt: now, Config: channeldomain.Config{Type: channeldomain.ChannelQRCode, Carrier: channeldomain.CarrierQRCode, Name: "Campaign", Assignment: channeldomain.Assignment{Mode: channeldomain.AssignmentSingle, Strategy: channeldomain.StrategyRatio, Assignees: []channeldomain.Assignee{{StaffID: 7, Priority: 1, Ratio: 100}}}}}
}

type catalogHTTPApplication struct {
	channel channeldomain.Channel
	created CatalogMutation
	updated CatalogMutation
	err     error
}

func (app *catalogHTTPApplication) Get(context.Context, int64) (channeldomain.Channel, error) {
	return app.channel, app.err
}
func (app *catalogHTTPApplication) List(context.Context, channelport.CatalogFilter) (channelport.CatalogPage, error) {
	return channelport.CatalogPage{Items: []channeldomain.Channel{app.channel}, NextCursor: "3", Total: 1}, app.err
}
func (app *catalogHTTPApplication) Create(_ context.Context, command CatalogMutation) (channeldomain.Channel, error) {
	app.created = command
	return app.channel, app.err
}
func (app *catalogHTTPApplication) Update(_ context.Context, _ int64, command CatalogMutation) (channeldomain.Channel, error) {
	app.updated = command
	return app.channel, app.err
}

type catalogHTTPSecurity struct {
	principal accessdomain.Principal
	authErr   error
	csrfErr   error
}

func (security *catalogHTTPSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return security.principal, security.authErr
}
func (security *catalogHTTPSecurity) AuthorizeCSRF(_ context.Context, request *http.Request) (accessdomain.Principal, error) {
	if security.csrfErr != nil {
		return accessdomain.Principal{}, security.csrfErr
	}
	if request.Header.Get("X-CSRF-Token") != "valid" {
		return accessdomain.Principal{}, errors.New("csrf")
	}
	return security.principal, nil
}

func catalogHTTPRequest(handler http.Handler, method, target, body string, headers map[string][]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
