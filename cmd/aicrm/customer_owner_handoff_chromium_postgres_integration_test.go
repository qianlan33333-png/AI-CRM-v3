package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLOwnerHandoffChromiumJourney drives both authorized Owner
// Migration modes through the real login, Host, HTTP handlers and PostgreSQL.
// OneID is read only: the WeCom customer uses an already verified external
// identity. Confirmation queues its EER intent but this API-role journey never
// starts a Provider worker or calls WeCom.
func TestPostgreSQLOwnerHandoffChromiumJourney(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	defer server.Close()
	origin := "https://" + server.Listener.Addr().String()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	application, err := compose(ctx, platformconfig.Runtime{Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: origin, ReleaseSHA: "owner-handoff-chromium", WorkerOwner: "owner-handoff-chromium", WorkerLimit: 1, Effects: platformconfig.Effects{ProviderEnabled: true}, WeCom: platformconfig.WeCom{Enabled: true, CorpID: "browser-corp", AgentID: "browser-agent", Secret: "browser-secret", ContactSecret: "browser-contact-secret", ContextSigningKey: "01234567890123456789012345678901"}, GroupOps: platformconfig.GroupOps{WebhookSecret: "owner-handoff-chromium-webhook"}, Survey: platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(key), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(key)}, Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "owner-browser", Password: "owner-browser-password", DisplayName: "Owner Browser"}})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "owner-browser", Password: "owner-browser-password", DisplayName: "Owner Browser"}); err != nil {
		t.Fatal(err)
	}
	var source, target, localCustomer, wecomCustomer int64
	if err = application.pool.Native().QueryRow(ctx, `SELECT id FROM admin_users WHERE username='owner-browser'`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `UPDATE admin_users SET wecom_userid='browser-source' WHERE id=$1`, source); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active) VALUES('owner-browser-target','$argon2id$fixture','Target','browser-target',true) RETURNING id`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&localCustomer); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&wecomCustomer); err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO customer_local_owners(customer_id,staff_id,source) VALUES($1,$3,'owner_handoff_local_only'),($2,$3,'owner_handoff_local_only')`, localCustomer, wecomCustomer, source); err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO wecom_follow_relationships(corp_id,employee_id,customer_id,active) VALUES('browser','browser-source',$1,true)`, wecomCustomer); err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'wecom_external_userid','wecom-corp:browser','browser-external','verified','chromium_fixture',1,clock_timestamp())`, wecomCustomer); err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = application.handler
	server.StartTLS()
	_, sourceFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate owner handoff Chromium journey")
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(filepath.Dir(sourceFile), "..", "..", "internal", "webshell", "owner_handoff_chromium.test.mjs"))
	command.Env = append(os.Environ(), "AICRM_OWNER_HANDOFF_TEST_URL="+server.URL, "AICRM_OWNER_HANDOFF_TEST_USERNAME=owner-browser", "AICRM_OWNER_HANDOFF_TEST_PASSWORD=owner-browser-password", "AICRM_OWNER_HANDOFF_TEST_SOURCE="+strconv.FormatInt(source, 10), "AICRM_OWNER_HANDOFF_TEST_TARGET="+strconv.FormatInt(target, 10), "AICRM_OWNER_HANDOFF_TEST_LOCAL_CUSTOMER="+strconv.FormatInt(localCustomer, 10), "AICRM_OWNER_HANDOFF_TEST_WECOM_CUSTOMER="+strconv.FormatInt(wecomCustomer, 10))
	output, err := command.CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "Chromium remote debugging did not become ready") {
			t.Skipf("Chromium cannot start in this local sandbox: %s", strings.TrimSpace(string(output)))
		}
		t.Fatalf("owner handoff Chromium journey: %v output=%s", err, strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), "owner_handoff_chromium: PASS") {
		t.Fatalf("owner handoff Chromium journey did not report success: %q", output)
	}
	var local, wecom int
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FILTER (WHERE mode='local_only'),count(*) FILTER (WHERE mode='wecom_then_crm') FROM customer_owner_handoff_batches WHERE state='accepted'`).Scan(&local, &wecom); err != nil || local != 1 || wecom != 1 {
		t.Fatalf("batches local/wecom=%d/%d err=%v", local, wecom, err)
	}
}
