package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	automationapp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/app"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
)

type testSecurity struct {
	p   accessdomain.Principal
	err error
}

func (s testSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.p, s.err
}
func (s testSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.p, s.err
}

type testAgents struct {
	item   automationport.Agent
	create automationport.CreateCommand
	err    error
}

func (s *testAgents) List(context.Context, automationport.AutomationType) (automationport.Page, error) {
	return automationport.Page{Items: []automationport.Agent{s.item}, Total: 1}, s.err
}
func (s *testAgents) Get(context.Context, automationport.AgentID) (automationport.Agent, error) {
	return s.item, s.err
}
func (s *testAgents) Create(_ context.Context, c automationport.CreateCommand) (automationport.Agent, error) {
	s.create = c
	return s.item, s.err
}
func (s *testAgents) Update(context.Context, automationport.UpdateCommand) (automationport.Agent, error) {
	return s.item, s.err
}
func (s *testAgents) Copy(context.Context, automationport.MutationCommand) (automationport.Agent, error) {
	return s.item, s.err
}
func (s *testAgents) Publish(context.Context, automationport.MutationCommand) (automationport.Agent, error) {
	return s.item, s.err
}
func (s *testAgents) SetStatus(context.Context, automationport.MutationCommand, automationport.AgentStatus) (automationport.Agent, error) {
	return s.item, s.err
}
func (s *testAgents) SaveFixedContent(context.Context, automationport.FixedContentCommand) (automationport.Agent, error) {
	return s.item, s.err
}
func principal() accessdomain.Principal {
	return accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}
}
func agent() automationport.Agent {
	return automationport.Agent{ID: 1, AgentName: "a", AgentCode: "a", AutomationType: automationport.AutomationTypeAgent, Status: automationport.AgentStatusPaused, DraftVersion: 1, PublishedVersion: 1, CreatedBy: 7, UpdatedBy: 7, CreatedAt: time.Now(), UpdatedAt: time.Now(), LegacyConfiguration: []byte(`{}`)}
}
func TestCreateUsesPrincipalAndIdempotencyHeader(t *testing.T) {
	s := &testAgents{item: agent()}
	h, e := NewHandler(s, testSecurity{p: principal()})
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/admin/automation-agents", strings.NewReader(`{"agent_name":"a","agent_code":"a","automation_type":"agent"}`))
	r.Header.Set("Idempotency-Key", "1234567890123456")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || s.create.Actor != 7 || s.create.IdempotencyKey != "1234567890123456" {
		t.Fatalf("status=%d command=%+v", w.Code, s.create)
	}
}
func TestActivateFailsClosed(t *testing.T) {
	h, _ := NewHandler(&testAgents{item: agent()}, testSecurity{p: principal()})
	r := httptest.NewRequest(http.MethodPost, "/api/admin/automation-agents/1/activate", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 410 {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
}
func TestErrorMapsNotFound(t *testing.T) {
	h, _ := NewHandler(&testAgents{err: automationapp.ErrAgentNotFound}, testSecurity{p: principal()})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/automation-agents/1", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}

var _ = errors.New
