package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// OneID decision: the fixture inserts one already-verified canonical identity
// strictly to prove the read-only root-provenance counter. It never resolves
// or provisions through the overview. Persistence decision: the overview
// joins no owner tables and writes nothing; this PostgreSQL fixture only
// seeds owner facts needed to exercise the composed read path.
func TestPostgreSQLAdminOverviewCompositionPreflight(t *testing.T) {
	fixture := newAdminOverviewFixture(t)
	response := overviewAuthenticatedGET(t, fixture.application.handler, fixture.session, "/api/admin/overview?period=7d")
	assertAdminOverviewResponse(t, response)
	unauthenticated := httptest.NewRecorder()
	fixture.application.handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/admin/overview?period=7d", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("overview unauthenticated status=%d body=%s", unauthenticated.Code, unauthenticated.Body.String())
	}
}

func TestPostgreSQLAdminOverviewChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	fixture := newAdminOverviewFixture(t)
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate overview Chromium journey")
	}
	command := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(source), "overview_chromium_journey.mjs"))
	command.Env = append(os.Environ(),
		"AICRM_OVERVIEW_BROWSER_URL="+fixture.server.URL,
		"AICRM_OVERVIEW_BROWSER_USERNAME=overview-browser-admin",
		"AICRM_OVERVIEW_BROWSER_PASSWORD=overview-browser-admin-password",
		"AICRM_CHROMIUM_BINARY=/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "admin_overview_chromium: PASS") {
		t.Fatalf("admin overview Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}
}

type adminOverviewFixture struct {
	ctx         context.Context
	application *composedApplication
	server      *httptest.Server
	session     string
}

func newAdminOverviewFixture(t *testing.T) *adminOverviewFixture {
	t.Helper()
	// compose resolves the release manifest as web/dist relative to the running
	// service. Go package tests otherwise start in cmd/aicrm and silently take
	// the generic no-assets fallback, which cannot validate the V3 page Host.
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate overview composition fixture")
	}
	t.Chdir(filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..")))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	t.Cleanup(cleanup)
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	dataKey := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	application, err := compose(ctx, platformconfig.Runtime{
		Role:         platformconfig.RoleAPI,
		DatabaseURL:  databaseURL,
		PublicOrigin: "https://" + server.Listener.Addr().String(),
		ReleaseSHA:   "1111111111111111111111111111111111111111",
		WorkerOwner:  "admin-overview-chromium",
		WorkerLimit:  1,
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "admin-overview-chromium-webhook"},
		OpenPlatform: platformconfig.OpenPlatform{JWTSigningKey: "01234567890123456789012345678901"},
		Survey: platformconfig.Survey{
			DataKey: dataKey, IdentityPhoneDataKey: dataKey,
		},
		Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "overview-browser-admin", Password: "overview-browser-admin-password", DisplayName: "Overview Browser Admin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "overview-browser-admin", Password: "overview-browser-admin-password", DisplayName: "Overview Browser Admin"}); err != nil {
		t.Fatal(err)
	}
	seedAdminOverviewFacts(t, ctx, application)
	server.Config.Handler = application.handler
	server.StartTLS()
	session, _ := adminAccessLogin(t, application.handler, "overview-browser-admin", "overview-browser-admin-password")
	return &adminOverviewFixture{ctx: ctx, application: application, server: server, session: session}
}

func seedAdminOverviewFacts(t *testing.T, ctx context.Context, application *composedApplication) {
	t.Helper()
	pool := application.pool.Native()
	now := time.Now().UTC().Truncate(time.Microsecond)
	var customerID, identityID, orderID, paymentID int64
	if err := pool.QueryRow(ctx, `INSERT INTO customers(created_at,updated_at) VALUES($1,$1) RETURNING id`, now).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at,created_at,updated_at) VALUES($1,'mp_openid','wechat-app:overview-browser','overview-browser-payer','verified','wechat_miniprogram',1,$2,$2,$2) RETURNING id`, customerID, now).Scan(&identityID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at) VALUES('wechat_pay','overview-browser','overview-browser-order','M-OVERVIEW-BROWSER',$1,$1,1200,'CNY','paid','native',true,1,$2,$2) RETURNING id`, customerID, now).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,version,paid_confirmed_at,created_at,updated_at,historical) VALUES($1,'wechat_pay','mini_program','M-OVERVIEW-BROWSER',$2,$3,$3,1200,'CNY','paid',1,$4,$4,$4,false) RETURNING id`, orderID, identityID, customerID, now).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	var refundID int64
	if err := pool.QueryRow(ctx, `INSERT INTO payment_refunds(payment_id,provider,refund_no,amount_minor,reason,status,version,created_at,updated_at) VALUES($1,'wechat_pay','R-OVERVIEW-BROWSER',200,'overview fixture','completed',1,$2,$2) RETURNING id`, paymentID, now).Scan(&refundID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payment_audit_events(event_type,aggregate_id,actor_scope,payload,occurred_at) VALUES('payment.refund_settled',$1,'overview-browser','{}'::jsonb,$2)`, refundID, now); err != nil {
		t.Fatal(err)
	}
	var distributorID, policyID, credentialID, attributionID, commissionID int64
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_distributors(customer_id,public_no,agreement_version,enabled,registered_at,version,created_at,updated_at) VALUES($1,'DSTOVERVIEWBROWSER','v1',true,$2,1,$2,$2) RETURNING id`, customerID, now).Scan(&distributorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES(99001,'standard_product',true,1000,7,1,$1,$1) RETURNING id`, now).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_promotion_credentials(distributor_id,product_id,product_type,token_digest,status,created_at,expires_at) VALUES($1,99001,'standard_product',$2,'active',$3,$4) RETURNING id`, distributorID, make([]byte, 32), now, now.Add(24*time.Hour)).Scan(&credentialID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at) VALUES($1,1,'overview-browser-product','Overview Browser Product',$2,$3,'payment:overview-browser','eligible',$4,1,1000,7,$5) RETURNING id`, orderID, distributorID, credentialID, policyID, now).Scan(&attributionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at) VALUES($1,$2,1,$3,1200,0,120,120,0,1000,$4,$5,'pending','','','',1,$4,$4) RETURNING id`, attributionID, orderID, distributorID, now, now.AddDate(0, 0, 7)).Scan(&commissionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO distribution_exceptions(commission_id,kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at) VALUES($1,'settlement_unknown','open',120,0,120,'provider_result_pending','payment:overview-browser','overview-browser',1,$2,$2)`, commissionID, now); err != nil {
		t.Fatal(err)
	}
}

func overviewAuthenticatedGET(t *testing.T, handler http.Handler, session, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertAdminOverviewResponse(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	var body struct {
		Range struct {
			Timezone string `json:"timezone"`
		} `json:"range"`
		Paid struct {
			Status     string `json:"status"`
			OrderCount int64  `json:"order_count"`
			Gross      []struct {
				AmountMinor int64  `json:"amount_minor"`
				Currency    string `json:"currency"`
			} `json:"gross"`
		} `json:"paid"`
		Customers struct {
			Status string `json:"status"`
			Count  int64  `json:"new_canonical_customers"`
		} `json:"customers"`
		Refunds struct {
			Status    string `json:"status"`
			Completed int64  `json:"completed_count"`
		} `json:"refunds"`
		Distribution struct {
			Status    string `json:"status"`
			Unsettled int64  `json:"current_unsettled_minor"`
		} `json:"distribution"`
		Todos struct {
			Status string `json:"status"`
			Items  []struct {
				Code  string `json:"code"`
				Count int64  `json:"count"`
				Href  string `json:"href"`
			} `json:"items"`
		} `json:"todos"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || body.Range.Timezone != "Asia/Shanghai" || body.Paid.Status != "ready" || body.Paid.OrderCount != 1 || len(body.Paid.Gross) != 1 || body.Paid.Gross[0].AmountMinor != 1200 || body.Paid.Gross[0].Currency != "CNY" || body.Customers.Status != "ready" || body.Customers.Count != 1 || body.Refunds.Status != "ready" || body.Refunds.Completed != 1 || body.Distribution.Status != "ready" || body.Distribution.Unsettled != 120 || body.Todos.Status != "ready" || len(body.Todos.Items) != 1 || body.Todos.Items[0].Code != "distribution_exceptions" || body.Todos.Items[0].Count != 1 || body.Todos.Items[0].Href != "/admin/distribution" {
		t.Fatalf("overview response status=%d body=%s", response.Code, response.Body.String())
	}
}
