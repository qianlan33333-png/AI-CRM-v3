package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
)

// TestPostgreSQLOwnerHandoffChromiumJourney drives both authorized Owner
// Migration modes through the real login, Host, HTTP handlers and PostgreSQL.
// OneID is read only: the WeCom customer uses an already verified external
// identity. The separate test Provider is injected at composition and the
// River runtime is started by this journey; no production endpoint is used.
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
	var providerCalls atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			_ = json.NewEncoder(writer).Encode(map[string]any{"errcode": 0, "access_token": "owner-handoff-test-token", "expires_in": 7200})
		case "/cgi-bin/externalcontact/transfer_customer":
			providerCalls.Add(1)
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(writer, "bad transfer", http.StatusBadRequest)
				return
			}
			ids, ok := body["external_userid"].([]any)
			if !ok || len(ids) != 1 {
				http.Error(writer, "bad customers", http.StatusBadRequest)
				return
			}
			if body["transfer_success_msg"] != "您好，后续将由新的服务同事继续为您服务。" {
				http.Error(writer, "uninitialized welcome message", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"errcode": 0, "customer": []map[string]any{{"external_userid": ids[0], "errcode": 0}}})
		case "/cgi-bin/externalcontact/transfer_result":
			_ = json.NewEncoder(writer).Encode(map[string]any{"errcode": 0, "customer": []map[string]any{{"external_userid": "browser-external", "status": 1, "takeover_time": 1}}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer providerServer.Close()
	application, err := composeWithWeComClientFactory(ctx, platformconfig.Runtime{Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: origin, ReleaseSHA: "owner-handoff-chromium", WorkerOwner: "owner-handoff-chromium", WorkerLimit: 1, Effects: platformconfig.Effects{ProviderEnabled: true}, WeCom: platformconfig.WeCom{Enabled: true, CorpID: "browser-corp", AgentID: "browser-agent", Secret: "browser-secret", ContactSecret: "browser-contact-secret", ContextSigningKey: "01234567890123456789012345678901"}, GroupOps: platformconfig.GroupOps{WebhookSecret: "owner-handoff-chromium-webhook"}, Survey: platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(key), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(key)}, Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "owner-browser", Password: "owner-browser-password", DisplayName: "Owner Browser"}}, func(config wecomadapter.Config) (*wecomadapter.Client, error) {
		config.APIBase = providerServer.URL
		config.HTTPClient = providerServer.Client()
		return wecomadapter.New(config)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	effectsCtx, stopEffects := context.WithCancel(ctx)
	effectsDone := make(chan error, 1)
	go func() { effectsDone <- application.effectsRuntime.Run(effectsCtx) }()
	defer func() {
		stopEffects()
		if runtimeErr := <-effectsDone; runtimeErr != nil {
			t.Errorf("owner handoff River runtime: %v", runtimeErr)
		}
	}()
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "owner-browser", Password: "owner-browser-password", DisplayName: "Owner Browser"}); err != nil {
		t.Fatal(err)
	}
	var source, target, localCustomer, wecomCustomer, primaryOnlyCustomer, locallyReassignedCustomer, mixedCustomer int64
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active) VALUES('owner-browser-source','$argon2id$fixture','Inactive Source','browser-source',false) RETURNING id`).Scan(&source); err != nil {
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
	for _, destination := range []*int64{&primaryOnlyCustomer, &locallyReassignedCustomer, &mixedCustomer} {
		if err = application.pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO customer_local_owners(customer_id,staff_id,source) VALUES($1,$2,'owner_handoff_local_only')`, localCustomer, source); err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO customer_local_owners(customer_id,staff_id,source) VALUES($1,$2,'owner_handoff_local_only'),($3,$4,'owner_handoff_local_only')`, locallyReassignedCustomer, target, mixedCustomer, source); err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO wecom_follow_relationships(corp_id,employee_id,customer_id,active) VALUES('browser-corp','browser-source',$1,true)`, wecomCustomer); err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'wecom_external_userid','wecom-corp:browser-corp','browser-external','verified','chromium_fixture',1,clock_timestamp())`, wecomCustomer); err != nil {
		t.Fatal(err)
	}
	var primaryRun int64
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,completed_at) VALUES('owner-handoff-primary-fixture','manual','succeeded','wecom-corp:browser-corp',clock_timestamp()) RETURNING id`).Scan(&primaryRun); err != nil {
		t.Fatal(err)
	}
	for index, customerID := range []int64{primaryOnlyCustomer, locallyReassignedCustomer, mixedCustomer} {
		var identityID int64
		externalID := "browser-local-primary-" + strconv.Itoa(index+1)
		if err = application.pool.Native().QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES($1,'wecom_external_userid','wecom-corp:browser-corp',$2,'verified','chromium_fixture',1,clock_timestamp()) RETURNING id`, customerID, externalID).Scan(&identityID); err != nil {
			t.Fatal(err)
		}
		if _, err = application.pool.Native().Exec(ctx, `INSERT INTO wecom_external_contact_profiles(customer_id,corp_scope,external_identity_id,profile_digest,last_seen_run_id,fetched_at,primary_owner_userid,primary_owner_run_id) VALUES($1,'wecom-corp:browser-corp',$2,decode(repeat('01',32),'hex'),$3,clock_timestamp(),'browser-source',$3)`, customerID, identityID, primaryRun); err != nil {
			t.Fatal(err)
		}
		if _, err = application.pool.Native().Exec(ctx, `INSERT INTO wecom_customer_owner_observations(customer_id,corp_scope,employee_id,relationship_status,last_seen_run_id,observed_at,primary_owner_userid) VALUES($1,'wecom-corp:browser-corp','browser-source','active',$2,clock_timestamp(),'browser-source')`, customerID, primaryRun); err != nil {
			t.Fatal(err)
		}
	}
	server.Config.Handler = application.handler
	server.StartTLS()
	// Exercise the fully composed outer router before Chromium. The frozen
	// shared picker asks for an inactive source and an active target on the
	// exact compatibility URL; a second outer registration must not shadow the
	// Customer scope dispatcher. Group Ops retains its normal exact read through
	// that same dispatcher, while its /sync subtree remains separately owned.
	session, _ := adminAccessLogin(t, application.handler, "owner-browser", "owner-browser-password")
	operationMembers := func(rawQuery string) []struct {
		UserID string `json:"user_id"`
		Active bool   `json:"active"`
	} {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/admin/common/operation-members?"+rawQuery, nil)
		request.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: session})
		response := httptest.NewRecorder()
		application.handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("operation members query=%q status=%d body=%s", rawQuery, response.Code, response.Body.String())
		}
		var payload struct {
			Items []struct {
				UserID string `json:"user_id"`
				Active bool   `json:"active"`
			} `json:"items"`
		}
		if decodeErr := json.NewDecoder(response.Body).Decode(&payload); decodeErr != nil {
			t.Fatalf("operation members query=%q decode: %v", rawQuery, decodeErr)
		}
		return payload.Items
	}
	containsMember := func(items []struct {
		UserID string `json:"user_id"`
		Active bool   `json:"active"`
	}, userID string, active bool) bool {
		for _, item := range items {
			if item.UserID == userID && item.Active == active {
				return true
			}
		}
		return false
	}
	if !containsMember(operationMembers("scope=owner_migration&include_inactive=true"), "browser-source", false) {
		t.Fatal("fully composed owner-migration picker did not return the inactive source")
	}
	if containsMember(operationMembers("scope=owner_migration&include_inactive=false"), "browser-source", false) || !containsMember(operationMembers("scope=owner_migration&include_inactive=false"), "browser-target", true) {
		t.Fatal("fully composed owner-migration picker did not enforce source/target active visibility")
	}
	groupRequest := httptest.NewRequest(http.MethodGet, "/api/admin/common/operation-members?scope=group_ops", nil)
	groupRequest.AddCookie(&http.Cookie{Name: "aicrm_admin_session", Value: session})
	groupResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(groupResponse, groupRequest)
	if groupResponse.Code != http.StatusOK {
		t.Fatalf("fully composed Group Ops operation-member query status=%d body=%s", groupResponse.Code, groupResponse.Body.String())
	}
	var groupPayload struct {
		Scope string `json:"scope"`
	}
	if decodeErr := json.NewDecoder(groupResponse.Body).Decode(&groupPayload); decodeErr != nil || groupPayload.Scope != "group_ops" {
		t.Fatalf("fully composed Group Ops operation-member response scope=%q err=%v", groupPayload.Scope, decodeErr)
	}
	_, sourceFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate owner handoff Chromium journey")
	}
	runJourney := func(mode string, readback bool, scope string) {
		command := exec.CommandContext(ctx, "node", filepath.Join(filepath.Dir(sourceFile), "..", "..", "internal", "webshell", "owner_handoff_chromium.test.mjs"))
		command.Env = append(os.Environ(), "AICRM_OWNER_HANDOFF_TEST_URL="+server.URL, "AICRM_OWNER_HANDOFF_TEST_USERNAME=owner-browser", "AICRM_OWNER_HANDOFF_TEST_PASSWORD=owner-browser-password", "AICRM_OWNER_HANDOFF_TEST_SOURCE="+strconv.FormatInt(source, 10), "AICRM_OWNER_HANDOFF_TEST_TARGET="+strconv.FormatInt(target, 10), "AICRM_OWNER_HANDOFF_TEST_SOURCE_USERID=browser-source", "AICRM_OWNER_HANDOFF_TEST_TARGET_USERID=browser-target", "AICRM_OWNER_HANDOFF_TEST_MODE="+mode, "AICRM_OWNER_HANDOFF_TEST_SCOPE="+scope, "AICRM_OWNER_HANDOFF_TEST_READ_TRANSFER="+strconv.FormatBool(readback))
		output, runErr := command.CombinedOutput()
		if runErr != nil {
			if goruntime.GOOS == "darwin" && strings.Contains(string(output), "Chromium remote debugging did not become ready") {
				t.Skipf("Chromium cannot start in this local sandbox: %s", strings.TrimSpace(string(output)))
			}
			t.Fatalf("owner handoff Chromium %s journey: %v output=%s", mode, runErr, strings.TrimSpace(string(output)))
		}
		if !strings.Contains(string(output), "owner_handoff_chromium: PASS") {
			t.Fatalf("owner handoff Chromium %s did not report success: %q", mode, output)
		}
	}
	waitOwner := func(customerID int64, want int64, message string) {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			var got int64
			err = application.pool.Native().QueryRow(ctx, `SELECT staff_id FROM customer_local_owners WHERE customer_id=$1`, customerID).Scan(&got)
			if err == nil && got == want {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("%s", message)
	}
	runJourney("local_only", false, "all")
	waitOwner(localCustomer, target, "local_only did not update the local owner through River")
	waitOwner(primaryOnlyCustomer, target, "local_only did not include the only-WeCom-primary customer")
	waitOwner(mixedCustomer, target, "local_only did not retain Customer local-owner precedence for the mixed customer")
	var locallyReassignedVersion int64
	if err = application.pool.Native().QueryRow(ctx, `SELECT version FROM customer_local_owners WHERE customer_id=$1`, locallyReassignedCustomer).Scan(&locallyReassignedVersion); err != nil || locallyReassignedVersion != 1 {
		t.Fatalf("local-only reselected a customer already assigned to another staff: version=%d err=%v", locallyReassignedVersion, err)
	}
	var localRangeLines int
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM customer_owner_handoff_lines line JOIN customer_owner_handoff_batches batch ON batch.id=line.batch_id WHERE batch.mode='local_only'`).Scan(&localRangeLines); err != nil || localRangeLines != 3 {
		t.Fatalf("local all-range lines=%d want=3 (local source + only-primary + mixed; no re-assigned row), err=%v", localRangeLines, err)
	}
	if providerCalls.Load() != 0 {
		t.Fatalf("local_only unexpectedly called test Provider: %d", providerCalls.Load())
	}
	runJourney("wecom_then_crm", true, "excel_include")
	waitOwner(wecomCustomer, target, "provider_accepted WeCom line did not update the local owner")
	if providerCalls.Load() != 1 {
		t.Fatalf("test Provider calls=%d want=1", providerCalls.Load())
	}
	var local, wecom, accepted int
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FILTER (WHERE mode='local_only'), count(*) FILTER (WHERE mode='wecom_then_crm'), count(*) FILTER (WHERE state='provider_accepted') FROM customer_owner_handoff_batches b LEFT JOIN customer_owner_handoff_lines l ON l.batch_id=b.id`).Scan(&local, &wecom, &accepted); err != nil || local < 1 || wecom < 1 || accepted < 1 {
		t.Fatalf("batches local/wecom/accepted=%d/%d/%d err=%v", local, wecom, accepted, err)
	}
}
