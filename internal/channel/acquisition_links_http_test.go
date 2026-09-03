package channel

import (
	"context"
	"net/http"
	"strings"
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

func TestAcquisitionLinkHTTPFrozenContractAndRoles(t *testing.T) {
	app := &acquisitionLinkHTTPApplication{link: wecomport.CustomerAcquisitionLink{LinkID: "link-1", LinkName: "Campaign", URL: "https://work.weixin.qq.com/link", UserIDs: []string{"staff-1"}, DepartmentIDs: []int64{}, SkipVerify: true}}
	security := &catalogHTTPSecurity{principal: accessdomain.Principal{InternalID: 7, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleViewer}}}
	handler, err := NewAcquisitionLinkHTTPHandler(app, security)
	if err != nil {
		t.Fatal(err)
	}
	response := catalogHTTPRequest(handler, http.MethodGet, acquisitionLinksPath+"?limit=50", "", nil)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || response.Body.String() != "{\"items\":[{\"link_id\":\"link-1\"}],\"next_cursor\":\"\"}\n" {
		t.Fatalf("list status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	response = catalogHTTPRequest(handler, http.MethodGet, acquisitionLinksPath+"/link-1", "", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"url":"https://work.weixin.qq.com/link"`) {
		t.Fatalf("get status=%d body=%s", response.Code, response.Body.String())
	}
	body := `{"link_name":"Campaign","user_ids":["staff-1"],"department_ids":[],"skip_verify":true}`
	response = catalogHTTPRequest(handler, http.MethodPost, acquisitionLinksPath, body, map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {"channel-link-create-0001"}, "X-CSRF-Token": {"valid"}})
	if response.Code != http.StatusForbidden {
		t.Fatalf("viewer mutation status=%d body=%s", response.Code, response.Body.String())
	}
	security.principal.Roles = []accessdomain.Role{accessdomain.RoleSuperAdmin}
	response = catalogHTTPRequest(handler, http.MethodPost, acquisitionLinksPath, body, map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {"channel-link-create-0001"}, "X-CSRF-Token": {"valid"}})
	if response.Code != http.StatusAccepted || app.command.Operation != "create" || app.command.ActorID != 7 || !strings.Contains(response.Body.String(), `"state":"accepted"`) {
		t.Fatalf("create status=%d command=%+v body=%s", response.Code, app.command, response.Body.String())
	}
	response = catalogHTTPRequest(handler, http.MethodPatch, acquisitionLinksPath+"/link-1", strings.TrimSuffix(body, "}")+`,"unknown":true}`, map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {"channel-link-update-0001"}, "X-CSRF-Token": {"valid"}})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown JSON status=%d body=%s", response.Code, response.Body.String())
	}
	evidence := strings.Repeat("a", 64)
	response = catalogHTTPRequest(handler, http.MethodPost, acquisitionLinksPath+"/link-1/reconcile", `{"receipt_id":9,"resolution":"provider_applied","evidence_digest":"`+evidence+`"}`, map[string][]string{"Content-Type": {"application/json"}, "Idempotency-Key": {"channel-link-reconcile-0001"}, "X-CSRF-Token": {"valid"}})
	if response.Code != http.StatusOK || app.reconcile.EvidenceDigest != "sha256:"+evidence {
		t.Fatalf("reconcile status=%d command=%+v body=%s", response.Code, app.reconcile, response.Body.String())
	}
}

type acquisitionLinkHTTPApplication struct {
	link      wecomport.CustomerAcquisitionLink
	command   AcquisitionLinkCommand
	reconcile AcquisitionLinkReconcileCommand
}

func (app *acquisitionLinkHTTPApplication) List(context.Context, string, int) ([]wecomport.CustomerAcquisitionLink, string, error) {
	return []wecomport.CustomerAcquisitionLink{app.link}, "", nil
}
func (app *acquisitionLinkHTTPApplication) Get(context.Context, string) (wecomport.CustomerAcquisitionLink, error) {
	return app.link, nil
}
func (app *acquisitionLinkHTTPApplication) Mutate(_ context.Context, command AcquisitionLinkCommand) (AcquisitionLinkReceipt, error) {
	app.command = command
	return AcquisitionLinkReceipt{ID: 8, State: "accepted"}, nil
}
func (app *acquisitionLinkHTTPApplication) Reconcile(_ context.Context, command AcquisitionLinkReconcileCommand) (AcquisitionLinkReceipt, error) {
	app.reconcile = command
	link := app.link
	return AcquisitionLinkReceipt{ID: command.ReceiptID, State: "reconciled", OutcomeDigest: command.EvidenceDigest, Resolution: command.Resolution, BusinessEndpointDispatched: true, RealExternalCallExecuted: true, Link: &link}, nil
}

var _ AcquisitionLinkApplication = (*acquisitionLinkHTTPApplication)(nil)
