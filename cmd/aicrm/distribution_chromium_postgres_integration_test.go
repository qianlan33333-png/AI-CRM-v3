package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"math/big"
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

// TestPostgreSQLDistributionChromiumJourney is the Distribution acceptance
// fixture: real composition, PostgreSQL-owned Distribution facts, the actual
// release documents and a headless Chrome session.  The WeChat Pay channel is
// configured solely to open the scoped Distribution composition gate; no
// provider request or profit-sharing effect is enabled by this fixture.
func TestPostgreSQLDistributionChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate Distribution Chromium journey")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	t.Chdir(repository)
	prepareProductExternalPushChromiumArtifacts(t, repository)
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	key, cert := distributionFixturePaymentCredentials(t)
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	defer server.Close()
	dataKey := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	application, err := compose(ctx, platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: "https://" + server.Listener.Addr().String(), ReleaseSHA: "distribution-chromium", WorkerOwner: "distribution-chromium", WorkerLimit: 1,
		GroupOps:  platformconfig.GroupOps{WebhookSecret: "distribution-chromium-webhook"},
		Survey:    platformconfig.Survey{DataKey: dataKey, IdentityPhoneDataKey: dataKey},
		WeChatPay: platformconfig.WeChatPay{Enabled: true, AppID: "wx-distribution-browser", AppSecret: "fixture-secret", AppScope: "wechat-app:distribution-browser", MerchantID: "fixture-mch", MerchantSerial: "fixture-merchant", PrivateKeyPath: key, PlatformCertPath: cert, APIV3Key: "0123456789abcdef0123456789abcdef"},
		Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "distribution-admin", Password: "distribution-admin-password", DisplayName: "Distribution Admin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "distribution-admin", Password: "distribution-admin-password", DisplayName: "Distribution Admin"}); err != nil {
		t.Fatal(err)
	}
	seed := seedDistributionChromiumFacts(t, ctx, application)
	assertDistributionAdminDeadlineWarningReadModel(t, ctx, application, seed.commissionID)
	server.Config.Handler = application.handler
	server.StartTLS()
	journey := filepath.Join(repository, "cmd", "aicrm", "distribution_chromium_journey.mjs")
	command := exec.CommandContext(ctx, "node", journey)
	command.Env = append(os.Environ(), "AICRM_DISTRIBUTION_BROWSER_URL="+server.URL, "AICRM_DISTRIBUTION_BROWSER_SESSION="+seed.session, "AICRM_DISTRIBUTION_BROWSER_PROMOTION="+seed.promotion, "AICRM_DISTRIBUTION_BROWSER_PRODUCT="+seed.productCode, "AICRM_DISTRIBUTION_BROWSER_ADMIN=distribution-admin", "AICRM_DISTRIBUTION_BROWSER_PASSWORD=distribution-admin-password")
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "distribution_chromium: PASS") {
		t.Fatalf("Distribution Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}
}

type distributionChromiumSeed struct {
	session, promotion, productCode string
	commissionID                    int64
}

func seedDistributionChromiumFacts(t *testing.T, ctx context.Context, application *composedApplication) distributionChromiumSeed {
	t.Helper()
	pool := application.pool.Native()
	now := time.Now().UTC()
	const code = "distribution-browser-product"
	const token = "dpc_" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	session := "dist_" + "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	var customer, product, distributor, policy, credential, attribution int64
	if err := pool.QueryRow(ctx, "INSERT INTO customers DEFAULT VALUES RETURNING id").Scan(&customer); err != nil {
		t.Fatal(err)
	}
	projection := `{"schema_version":1,"status":"enabled","enabled":true,"buy_button_text":"立即购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"purchase_action_enabled":false,"purchase_action_mode":"","wecom_tagging":{},"slices":[]}`
	if err := pool.QueryRow(ctx, "INSERT INTO products(product_code,name,description,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES($1,'分销浏览器商品','真实分销浏览器夹具',9900,'CNY',10,1,$2::jsonb) RETURNING id", code, projection).Scan(&product); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO distribution_distributors(customer_id,public_no,agreement_version,enabled,receiver_reference,receiver_app_id,receiver_ready,receiver_reason,receiver_checked_at,registered_at,version,created_at,updated_at) VALUES($1,'DISTBROWSER01','v1',TRUE,'receiver-browser','wx-distribution-browser',TRUE,'',$2,$2,1,$2,$2) RETURNING id", customer, now).Scan(&distributor); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO distribution_product_policies(product_id,product_type,enabled,commission_rate_basis_points,wait_days,version,created_at,updated_at) VALUES($1,'standard_product',TRUE,1000,7,1,$2,$2) RETURNING id", product, now).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(token))
	if err := pool.QueryRow(ctx, "INSERT INTO distribution_promotion_credentials(distributor_id,product_id,product_type,token_digest,status,created_at,expires_at) VALUES($1,$2,'standard_product',$3,'active',$4,$5) RETURNING id", distributor, product, digest[:], now, now.Add(24*time.Hour)).Scan(&credential); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO distribution_order_attributions(order_id,order_item_line,product_code,product_name,distributor_id,promotion_credential_id,qualification_evidence_reference,qualification_state,policy_id,policy_version,commission_rate_basis_points,wait_days,attributed_at) VALUES(9001,1,$1,'分销浏览器商品',$2,$3,'order:9000:line:1','eligible',$4,1,1000,7,$5) RETURNING id", code, distributor, credential, policy, now).Scan(&attribution); err != nil {
		t.Fatal(err)
	}
	var commission int64
	if err := pool.QueryRow(ctx, "INSERT INTO distribution_commissions(attribution_id,order_id,order_item_line,distributor_id,original_item_paid_minor,successful_refund_minor,initial_minor,current_payable_minor,paid_minor,commission_rate_basis_points,paid_confirmed_at,due_at,status,hold_reason,cancel_reason,exception_reason,version,created_at,updated_at) VALUES($1,9001,1,$2,9900,0,990,990,0,1000,$3,$4,'pending','','','',1,$3,$3) RETURNING id", attribution, distributor, now, now.Add(7*24*time.Hour)).Scan(&commission); err != nil {
		t.Fatal(err)
	}
	sessionDigest := sha256.Sum256([]byte(session))
	if _, err := pool.Exec(ctx, "INSERT INTO distribution_browser_sessions(token_digest,customer_id,identity_id,channel,app_id,app_scope,expires_at,created_at) VALUES($1,$2,1,'mini_program','wx-distribution-browser','wechat-app:distribution-browser',$3,$4)", sessionDigest[:], customer, now.Add(8*time.Hour), now); err != nil {
		t.Fatal(err)
	}
	return distributionChromiumSeed{session: session, promotion: token, productCode: code, commissionID: commission}
}

func assertDistributionAdminDeadlineWarningReadModel(t *testing.T, ctx context.Context, application *composedApplication, commissionID int64) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := application.pool.Native().Exec(ctx, `INSERT INTO distribution_exceptions(commission_id,settlement_id,kind,status,unpaid_due_minor,already_paid_minor,amount_minor,reason,evidence_reference,actor_scope,version,created_at,updated_at) VALUES($1,NULL,'settlement_deadline_imminent','open',0,0,0,'split_deadline_within_24h','deadline:fixture','worker:distribution-due',1,$2,$2)`, commissionID, now); err != nil {
		t.Fatal(err)
	}
	session, _ := adminAccessLogin(t, application.handler, "distribution-admin", "distribution-admin-password")
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/distribution/exceptions", nil)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	application.handler.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `"kind":"settlement_deadline_imminent"`) || !strings.Contains(body, `"can_reconcile":false`) || !strings.Contains(body, `"can_record_recovery":false`) || !strings.Contains(body, `"can_record_merchant_liability":false`) {
		t.Fatalf("deadline warning admin read-model status=%d body=%s", response.Code, body)
	}
}

func distributionFixturePaymentCredentials(t *testing.T) (string, string) {
	t.Helper()
	merchant, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	platform, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serial := big.NewInt(42)
	certDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: serial, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, BasicConstraintsValid: true}, &x509.Certificate{SerialNumber: serial}, &platform.PublicKey, merchant)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	keyPath, certPath := filepath.Join(dir, "merchant.pem"), filepath.Join(dir, "platform.pem")
	keyBytes, err := x509.MarshalPKCS8PrivateKey(merchant)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), 0600); err != nil {
		t.Fatal(err)
	}
	return keyPath, certPath
}
