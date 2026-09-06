package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLCustomerTagCommandChromiumJourney uses the actual Access
// session/CSRF path, the rendered customer Host, PostgreSQL/River and the
// Composition-owned WeCom Provider adapter. OneID resolves two seeded trusted
// external identities; the fixture only observes opaque test targets. The
// second write deliberately disconnects after the request so the durable
// result remains outcome_unknown rather than pretending it was rejected.
//
// OneID decision: involved through the scoped wecom_external_userid read Port;
// the journey seeds identities but never provisions or merges a customer.
// Persistence decision: command, EER receipt, River job and completion share
// PostgreSQL transactions; mark_tag/readback are controlled external fixture calls.
func TestPostgreSQLCustomerTagCommandChromiumJourney(t *testing.T) {
	if os.Getenv("AICRM_REQUIRE_CHROMIUM_JOURNEY") != "1" {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	provider := newCustomerTagChromiumProvider()
	defer provider.Close()
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	defer server.Close()
	origin := "https://" + server.Listener.Addr().String()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	application, err := compose(ctx, platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: origin,
		ReleaseSHA: "customer-tag-chromium-journey", WorkerOwner: "customer-tag-chromium-journey", WorkerLimit: 1,
		GroupOps:  platformconfig.GroupOps{WebhookSecret: "customer-tag-chromium-webhook-secret"},
		Survey:    platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(key), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(key)},
		Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "browser-owner", Password: "browser-owner-password", DisplayName: "Browser Owner"},
		Effects:   platformconfig.Effects{ProviderEnabled: true},
		WeCom:     platformconfig.WeCom{Enabled: true, CorpID: "fixture-corp", AgentID: "fixture-agent", Secret: "fixture-secret", ContactSecret: "fixture-contact-secret", ContextSigningKey: "customer-tag-chromium-context-key-32", CustomerTagProviderEnabled: true, APIBase: provider.URL(), HTTPClient: provider.Client()},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "browser-owner", Password: "browser-owner-password", DisplayName: "Browser Owner"}); err != nil {
		t.Fatal(err)
	}
	if err = seedCustomerTagChromiumJourney(ctx, application); err != nil {
		t.Fatal(err)
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() { workerDone <- application.effectsRuntime.Run(workerCtx) }()
	defer func() {
		stopWorker()
		select {
		case err := <-workerDone:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("effects runtime: %v", err)
			}
		case <-time.After(20 * time.Second):
			t.Error("effects runtime did not stop")
		}
	}()

	server.Config.Handler = application.handler
	server.StartTLS()
	_, source, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate customer tag Chromium script")
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(filepath.Dir(source), "customer_tag_command_chromium_journey.mjs"))
	command.Env = append(os.Environ(), "AICRM_CUSTOMER_TAG_TEST_URL="+server.URL, "AICRM_CUSTOMER_TAG_TEST_USERNAME=browser-owner", "AICRM_CUSTOMER_TAG_TEST_PASSWORD=browser-owner-password")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("customer tag Chromium journey: %v output=%s", err, strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), "customer_tag_command_chromium: PASS") {
		t.Fatalf("customer tag Chromium journey did not report success: %q", output)
	}
	var executed, unknown, observed, writes, reads int
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FILTER (WHERE state='executed'),count(*) FILTER (WHERE state='outcome_unknown') FROM customer_tag_command_lines`).Scan(&executed, &unknown); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM wecom_customer_tag_observations WHERE customer_id=1 AND observation_status='active'`).Scan(&observed); err != nil {
		t.Fatal(err)
	}
	writes, reads = provider.Counts()
	if executed != 1 || unknown != 1 || observed < 1 || writes != 2 || reads < 1 {
		t.Fatalf("durable outcomes executed=%d unknown=%d observed=%d provider_writes=%d provider_reads=%d", executed, unknown, observed, writes, reads)
	}
}

type customerTagChromiumProvider struct {
	server        *httptest.Server
	mu            sync.Mutex
	writes, reads int
}

func newCustomerTagChromiumProvider() *customerTagChromiumProvider {
	fixture := &customerTagChromiumProvider{}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"access_token":"fixture-token","expires_in":7200}`))
		case "/cgi-bin/externalcontact/mark_tag":
			var request struct {
				ExternalUserID string `json:"external_userid"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil {
				http.Error(w, "invalid", http.StatusBadRequest)
				return
			}
			fixture.mu.Lock()
			fixture.writes++
			fixture.mu.Unlock()
			if request.ExternalUserID == "fixture-external-two" {
				http.Error(w, "fixture disconnect", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0}`))
		case "/cgi-bin/externalcontact/get":
			fixture.mu.Lock()
			fixture.reads++
			fixture.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"external_contact":{"external_userid":"fixture-external-one","name":"fixture","type":1,"gender":0},"follow_user":[{"userid":"fixture-staff","tags":[{"tag_id":"fixture-provider-add","name":"fixture observed","type":1}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	return fixture
}
func (f *customerTagChromiumProvider) URL() string          { return f.server.URL }
func (f *customerTagChromiumProvider) Client() *http.Client { return f.server.Client() }
func (f *customerTagChromiumProvider) Close()               { f.server.Close() }
func (f *customerTagChromiumProvider) Counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes, f.reads
}

func seedCustomerTagChromiumJourney(ctx context.Context, application *composedApplication) error {
	pool := application.pool.Native()
	_, err := pool.Exec(ctx, `UPDATE admin_users SET wecom_userid='fixture-staff' WHERE id=1;
		INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active'),(2,'active');
		INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES
		(1,'wecom_external_userid','wecom-corp:fixture-corp','fixture-external-one','verified','chromium_fixture',1,clock_timestamp()),
		(2,'wecom_external_userid','wecom-corp:fixture-corp','fixture-external-two','verified','chromium_fixture',1,clock_timestamp());
		INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,staff_ids,started_at,completed_at) VALUES('customer-tag-chromium-seed','manual','succeeded','wecom-corp:fixture-corp',jsonb_build_array('fixture-staff'),clock_timestamp(),clock_timestamp());`)
	if err != nil {
		return err
	}
	var runID int64
	if err = pool.QueryRow(ctx, `SELECT id FROM wecom_customer_sync_runs WHERE run_key='customer-tag-chromium-seed'`).Scan(&runID); err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `INSERT INTO wecom_external_contact_profiles(customer_id,corp_scope,external_identity_id,display_name,activation_status,profile_digest,last_seen_run_id,fetched_at,primary_owner_userid,primary_owner_run_id) VALUES
		(1,'wecom-corp:fixture-corp',(SELECT id FROM customer_identities WHERE customer_id=1),'fixture one','active',decode(repeat('00',32),'hex'),$1,clock_timestamp(),'fixture-staff',$1),
		(2,'wecom-corp:fixture-corp',(SELECT id FROM customer_identities WHERE customer_id=2),'fixture two','active',decode(repeat('00',32),'hex'),$1,clock_timestamp(),'fixture-staff',$1)`, runID)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `INSERT INTO wecom_follow_relationships(corp_id,employee_id,customer_id,active) VALUES('fixture-corp','fixture-staff',1,true),('fixture-corp','fixture-staff',2,true);
		INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,oneid_label,activation_status,source,last_synced_at,updated_at) VALUES
		(1,'active','fixture one','customer #1','active','chromium_fixture',clock_timestamp(),clock_timestamp()),
		(2,'active','fixture two','customer #2','active','chromium_fixture',clock_timestamp(),clock_timestamp());
		INSERT INTO tag_groups(group_name,sort_order) VALUES('fixture group',0);
		INSERT INTO tag_catalog_tags(group_id,tag_name,sort_order) VALUES((SELECT id FROM tag_groups WHERE group_name='fixture group'),'fixture add',0),((SELECT id FROM tag_groups WHERE group_name='fixture group'),'fixture remove',1);
		INSERT INTO tag_provider_tag_bindings(provider_tag_id,tag_id) VALUES('fixture-provider-add',(SELECT id FROM tag_catalog_tags WHERE tag_name='fixture add')),('fixture-provider-remove',(SELECT id FROM tag_catalog_tags WHERE tag_name='fixture remove'));`)
	return err
}
