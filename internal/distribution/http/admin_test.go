package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
)

type adminHTTPSecurity struct {
	principal accessdomain.Principal
	csrfCalls int
	csrfErr   error
}

func (s *adminHTTPSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return s.principal, nil
}
func (s *adminHTTPSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	s.csrfCalls++
	return s.principal, s.csrfErr
}

type adminHTTPReader struct{}

func (adminHTTPReader) ListAdminDistributors(context.Context, string, int32) (distributionport.AdminPage[distributionport.AdminDistributor], error) {
	return distributionport.AdminPage[distributionport.AdminDistributor]{Items: []distributionport.AdminDistributor{{ID: 9, PublicNo: "D-9", CustomerReference: "customer:masked", AgreementVersion: "v1", Enabled: true, Version: 3}}}, nil
}
func (adminHTTPReader) ListAdminOrders(context.Context, string, int32) (distributionport.AdminPage[distributionport.AdminOrder], error) {
	return distributionport.AdminPage[distributionport.AdminOrder]{}, nil
}
func (adminHTTPReader) ListAdminExceptions(context.Context, string, int32) (distributionport.AdminPage[distributionport.AdminException], error) {
	return distributionport.AdminPage[distributionport.AdminException]{}, nil
}

type adminWarningReader struct{ adminHTTPReader }

func (adminWarningReader) ListAdminExceptions(context.Context, string, int32) (distributionport.AdminPage[distributionport.AdminException], error) {
	return distributionport.AdminPage[distributionport.AdminException]{Items: []distributionport.AdminException{{ExceptionID: 17, CommissionID: 9, Kind: "settlement_deadline_imminent", Status: "open", AmountMinor: 0, Reason: "split_deadline_within_24h", Version: 1, CanReconcile: false, CanRecordRecovery: false, CanRecordMerchantLiability: false}}}, nil
}

type adminHTTPCommands struct {
	enable   *distributionport.AdminDistributorCommand
	recovery *distributionport.AdminExceptionCommand
}

func (c *adminHTTPCommands) SetDistributorEnabled(_ context.Context, command distributionport.AdminDistributorCommand, enabled bool) error {
	if enabled {
		return errors.New("not expected")
	}
	c.enable = &command
	return nil
}
func (c *adminHTTPCommands) ReconcileException(context.Context, distributionport.AdminExceptionCommand) error {
	return nil
}
func (c *adminHTTPCommands) RecordRecovery(_ context.Context, command distributionport.AdminExceptionCommand) error {
	c.recovery = &command
	return nil
}
func (c *adminHTTPCommands) RecordMerchantLiability(context.Context, distributionport.AdminExceptionCommand) error {
	return nil
}

func TestAdminHandlerUsesAccessCSRFAndNeverExposesManualPaidEndpoint(t *testing.T) {
	security := &adminHTTPSecurity{principal: accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	commands := &adminHTTPCommands{}
	handler, err := NewAdminHandler(AdminConfig{Reader: adminHTTPReader{}, Commands: commands, Security: security})
	if err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/admin/distribution/distributors", nil))
	if get.Code != http.StatusOK || get.Header().Get("Cache-Control") != "no-store" || !strings.Contains(get.Body.String(), `"public_no":"D-9"`) || security.csrfCalls != 0 {
		t.Fatalf("read=%d body=%s csrf=%d", get.Code, get.Body.String(), security.csrfCalls)
	}
	write := httptest.NewRequest(http.MethodPost, "/api/admin/distribution/distributors/9/disable", strings.NewReader(`{"version":3,"reason":"policy breach"}`))
	write.Header.Set("Idempotency-Key", "distribution-admin-disable-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, write)
	if response.Code != http.StatusOK || security.csrfCalls != 1 || commands.enable == nil || commands.enable.ActorScope != "access:7" || commands.enable.Reason != "policy breach" {
		t.Fatalf("write=%d command=%+v csrf=%d", response.Code, commands.enable, security.csrfCalls)
	}
	paid := httptest.NewRecorder()
	handler.ServeHTTP(paid, httptest.NewRequest(http.MethodPost, "/api/admin/distribution/exceptions/8/paid", nil))
	if paid.Code != http.StatusNotFound || paid.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("manual paid route=%d cache=%q", paid.Code, paid.Header().Get("Cache-Control"))
	}
}

func TestAdminHandlerRecoveryRequiresAccessCSRFAndForwardsOnlyEvidence(t *testing.T) {
	security := &adminHTTPSecurity{principal: accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	commands := &adminHTTPCommands{}
	handler, _ := NewAdminHandler(AdminConfig{Reader: adminHTTPReader{}, Commands: commands, Security: security})
	request := httptest.NewRequest(http.MethodPost, "/api/admin/distribution/exceptions/8/recoveries", strings.NewReader(`{"version":4,"amount_minor":12,"evidence_reference":"receipt:9"}`))
	request.Header.Set("Idempotency-Key", "distribution-admin-recovery-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || commands.recovery == nil || commands.recovery.Reason != "manual_recovery" || commands.recovery.EvidenceReference != "receipt:9" || commands.recovery.AmountMinor != 12 {
		t.Fatalf("recovery=%d command=%+v", response.Code, commands.recovery)
	}
	security.csrfErr = errors.New("csrf")
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, request)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("csrf=%d", denied.Code)
	}
}

func TestAdminHandlerDeadlineWarningIsInformationalInDTO(t *testing.T) {
	security := &adminHTTPSecurity{principal: accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	handler, err := NewAdminHandler(AdminConfig{Reader: adminWarningReader{}, Commands: &adminHTTPCommands{}, Security: security})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/distribution/exceptions", nil))
	body := response.Body.String()
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(body, `"kind":"settlement_deadline_imminent"`) || !strings.Contains(body, `"can_reconcile":false`) || !strings.Contains(body, `"can_record_recovery":false`) || !strings.Contains(body, `"can_record_merchant_liability":false`) {
		t.Fatalf("informational warning dto status=%d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), body)
	}
}

func TestAdminHandlerRejectsMalformedCursorWithNoStoreRead(t *testing.T) {
	security := &adminHTTPSecurity{principal: accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	handler, err := NewAdminHandler(AdminConfig{Reader: adminHTTPReader{}, Commands: &adminHTTPCommands{}, Security: security})
	if err != nil {
		t.Fatal(err)
	}
	for _, cursor := range []string{"abc", "0", "01", "+1"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/distribution/distributors?cursor="+cursor, nil))
		if response.Code != http.StatusBadRequest || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("cursor=%q status=%d cache=%q", cursor, response.Code, response.Header().Get("Cache-Control"))
		}
	}
}
