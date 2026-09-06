package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLSidebarThumbnailChromiumJourney exercises the deployed sidebar
// shell rather than a DOM fixture: Access owns the authenticated browser
// session, the sidebar bootstrap resolves an existing scoped OneID relation,
// Media owns the enabled image variant, and Chromium loads it through a blob
// object URL under the sidebar-only CSP relaxation.
//
// OneID decision: involved only through the existing scoped
// wecom_external_userid read path; the journey never provisions or merges a
// customer. Persistence decision: local PostgreSQL reads only after fixture
// setup. External Effects decision: not involved; the fixture asserts no
// Provider request is made while rendering the thumbnail.
func TestPostgreSQLSidebarThumbnailChromiumJourney(t *testing.T) {
	// The local macOS sandbox cannot reliably expose Chrome's DevTools port
	// (Crashpad exits before DevToolsActivePort appears). Linux CI sets the
	// required flag and executes this exact script; do not turn that CI path
	// into a skip.
	if goruntime.GOOS == "darwin" {
		t.Skip("Darwin Chrome DevTools is unavailable in this sandbox; required Linux CI executes the journey")
	}
	if !platformconfig.ChromiumJourneyRequired() {
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
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal(err)
	}
	application, err := compose(ctx, platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: origin,
		ReleaseSHA: "sidebar-thumbnail-chromium-journey", WorkerOwner: "sidebar-thumbnail-chromium-journey", WorkerLimit: 1,
		GroupOps:  platformconfig.GroupOps{WebhookSecret: "sidebar-thumbnail-chromium-webhook-secret"},
		Survey:    platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(dataKey), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(dataKey)},
		Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "sidebar-browser-owner", Password: "sidebar-browser-owner-password", DisplayName: "Sidebar Browser Owner"},
		Effects:   platformconfig.Effects{ProviderEnabled: false},
		WeCom:     platformconfig.WeCom{Enabled: true, CorpID: "fixture-corp", AgentID: "fixture-agent", Secret: "fixture-secret", ContactSecret: "fixture-contact-secret", ContextSigningKey: "sidebar-thumbnail-context-key-32", APIBase: provider.URL(), HTTPClient: provider.Client()},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "sidebar-browser-owner", Password: "sidebar-browser-owner-password", DisplayName: "Sidebar Browser Owner"}); err != nil {
		t.Fatal(err)
	}
	if err = seedSidebarThumbnailChromiumJourney(ctx, application); err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = application.handler
	server.StartTLS()

	_, source, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate sidebar Chromium script")
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(filepath.Dir(source), "..", "..", "internal", "webshell", "sidebar_thumbnail_chromium.test.mjs"))
	command.Env = append(os.Environ(),
		"AICRM_SIDEBAR_THUMBNAIL_TEST_URL="+server.URL,
		"AICRM_SIDEBAR_THUMBNAIL_TEST_USERNAME=sidebar-browser-owner",
		"AICRM_SIDEBAR_THUMBNAIL_TEST_PASSWORD=sidebar-browser-owner-password",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("sidebar thumbnail Chromium journey: %v output=%s", err, strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), "sidebar_thumbnail_chromium: PASS") {
		t.Fatalf("sidebar thumbnail Chromium journey did not report success: %q", output)
	}
	writes, reads := provider.Counts()
	if writes != 0 || reads != 0 {
		t.Fatalf("thumbnail read must not call the WeCom provider: writes=%d reads=%d", writes, reads)
	}
}

func seedSidebarThumbnailChromiumJourney(ctx context.Context, application *composedApplication) error {
	pool := application.pool.Native()
	for _, statement := range []string{
		`UPDATE admin_users SET wecom_userid='fixture-staff' WHERE id=1`,
		`INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES(1,'active')`,
		`INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at) VALUES(1,'wecom_external_userid','wecom-corp:fixture-corp','sidebar-thumbnail-external','verified','sidebar_thumbnail_chromium_fixture',1,clock_timestamp())`,
		`INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,staff_ids,started_at,completed_at) VALUES('sidebar-thumbnail-chromium-seed','manual','succeeded','wecom-corp:fixture-corp',jsonb_build_array('fixture-staff'),clock_timestamp(),clock_timestamp())`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			return err
		}
	}
	var runID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM wecom_customer_sync_runs WHERE run_key='sidebar-thumbnail-chromium-seed'`).Scan(&runID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wecom_external_contact_profiles(customer_id,corp_scope,external_identity_id,display_name,activation_status,profile_digest,last_seen_run_id,fetched_at,primary_owner_userid,primary_owner_run_id) VALUES(1,'wecom-corp:fixture-corp',(SELECT id FROM customer_identities WHERE customer_id=1),'sidebar thumbnail customer','active',decode(repeat('00',32),'hex'),$1,clock_timestamp(),'fixture-staff',$1)`, runID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wecom_follow_relationships(corp_id,employee_id,customer_id,active) VALUES('fixture-corp','fixture-staff',1,true)`); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,oneid_label,activation_status,source,last_synced_at,updated_at) VALUES(1,'active','sidebar thumbnail customer','customer #1','active','sidebar_thumbnail_chromium_fixture',clock_timestamp(),clock_timestamp())`); err != nil {
		return err
	}
	content, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		return err
	}
	sum := sha256.Sum256(content)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if _, err = pool.Exec(ctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,'image/png',$2,$3)`, digest, len(content), content); err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `INSERT INTO media_images(blob_digest,file_name,name,description,tags,category,mime_type,byte_size,width,height,enabled,created_by,updated_by) VALUES($1,'sidebar-thumbnail.png','sidebar thumbnail','','fixture','sidebar-fixture','image/png',$2,1,1,true,1,1)`, digest, len(content))
	return err
}
