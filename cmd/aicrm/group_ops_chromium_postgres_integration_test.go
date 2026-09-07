package main

import (
	"bytes"
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

// OneID decision: not involved. The browser fixture uses only the authenticated
// local operator and opaque group-plan records. Persistence decision: the Host
// saves the node through the normal Group Ops PostgreSQL UoW; dispatch remains
// disabled and the journey submits no Provider write.
type groupOpsChromiumFixture struct {
	ctx         context.Context
	application *composedApplication
	server      *httptest.Server
	script      string
	planID      int64
}

func TestPostgreSQLGroupOpsStandardHostCompositionPreflight(t *testing.T) {
	fixture := newGroupOpsChromiumFixture(t)
	session, _ := adminAccessLogin(t, fixture.application.handler, "groupops-browser-owner", "groupops-browser-owner-password")
	page := authenticatedAdminGet(t, fixture.application.handler, session, "/admin/automation-conversion/group-ops/plans/"+strconv.FormatInt(fixture.planID, 10))
	if page.Code != http.StatusOK || !bytes.Contains(page.Body.Bytes(), []byte(`data-group-ops-standard-host="true"`)) || !bytes.Contains(page.Body.Bytes(), []byte(`/groupops-assets/`)) {
		t.Fatalf("standard Group Ops page status=%d host=%t assets=%t", page.Code, bytes.Contains(page.Body.Bytes(), []byte(`data-group-ops-standard-host="true"`)), bytes.Contains(page.Body.Bytes(), []byte(`/groupops-assets/`)))
	}
	detail := authenticatedAdminGet(t, fixture.application.handler, session, "/api/admin/automation-conversion/group-ops/plans/"+strconv.FormatInt(fixture.planID, 10))
	if detail.Code != http.StatusOK || !bytes.Contains(detail.Body.Bytes(), []byte(`"plan"`)) || !bytes.Contains(detail.Body.Bytes(), []byte(`"plan_type":"standard"`)) {
		t.Fatalf("standard Group Ops detail status=%d plan=%t type=%t", detail.Code, bytes.Contains(detail.Body.Bytes(), []byte(`"plan"`)), bytes.Contains(detail.Body.Bytes(), []byte(`"plan_type":"standard"`)))
	}
}

func TestPostgreSQLGroupOpsStandardHostChromiumJourney(t *testing.T) {
	if goruntime.GOOS == "darwin" && os.Getenv("AICRM_ALLOW_LOCAL_CHROMIUM_JOURNEY") != "1" {
		t.Skip("Chromium CDP journey requires Linux CI; set AICRM_ALLOW_LOCAL_CHROMIUM_JOURNEY=1 for an explicit local run")
	}
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	fixture := newGroupOpsChromiumFixture(t)
	command := exec.CommandContext(fixture.ctx, "node", fixture.script)
	command.Env = append(os.Environ(), "AICRM_GROUPOPS_TEST_URL="+fixture.server.URL, "AICRM_GROUPOPS_TEST_USERNAME=groupops-browser-owner", "AICRM_GROUPOPS_TEST_PASSWORD=groupops-browser-owner-password", "AICRM_GROUPOPS_TEST_PLAN_ID="+strconv.FormatInt(fixture.planID, 10))
	output, err := command.CombinedOutput()
	if strings.Contains(string(output), "group_ops_chromium: SKIP_DEVTOOLS") {
		t.Fatalf("Group Ops Chromium DevTools unexpectedly unavailable: %s", strings.TrimSpace(string(output)))
	}
	if err != nil || !strings.Contains(string(output), "group_ops_chromium: PASS") {
		t.Fatalf("Group Ops Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}
	var matched int
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT count(*) FROM group_ops_plan_nodes WHERE plan_id=$1 AND day_index=2 AND scheduled_time='09:30' AND trigger_time_label='09:30' AND action_title='Chromium 日程动作' AND node_status='active'`, fixture.planID).Scan(&matched); err != nil || matched != 1 {
		t.Fatalf("browser node persistence count=%d err=%v", matched, err)
	}
}

func newGroupOpsChromiumFixture(t *testing.T) *groupOpsChromiumFixture {
	t.Helper()
	_, source, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate Group Ops Chromium journey")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	t.Chdir(repository)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	t.Cleanup(cleanup)
	prepareProductExternalPushChromiumArtifacts(t, repository)
	t.Cleanup(func() {
		for _, invocation := range [][]string{{"npm", "run", "build", "--silent"}, {"node", "scripts/build-v3-host-adapters.mjs"}} {
			command := exec.Command(invocation[0], invocation[1:]...)
			command.Dir = repository
			if output, rebuildErr := command.CombinedOutput(); rebuildErr != nil {
				t.Errorf("restore Group Ops browser build %s: %v output=%s", strings.Join(invocation, " "), rebuildErr, strings.TrimSpace(string(output)))
			}
		}
	})
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	application, err := compose(ctx, platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: "https://" + server.Listener.Addr().String(), ReleaseSHA: "groupops-standard-host-chromium", WorkerOwner: "groupops-standard-host-chromium", WorkerLimit: 1,
		GroupOps:    platformconfig.GroupOps{WebhookSecret: "groupops-browser-webhook-secret"},
		Survey:      platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(dataKey), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(dataKey)},
		AIAssistant: platformconfig.AIAssistant{UIEnabled: true},
		Bootstrap:   platformconfig.Bootstrap{Enabled: true, Username: "groupops-browser-owner", Password: "groupops-browser-owner-password", DisplayName: "Group Ops Browser Owner"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	bootstrap := platformconfig.Bootstrap{Enabled: true, Username: "groupops-browser-owner", Password: "groupops-browser-owner-password", DisplayName: "Group Ops Browser Owner"}
	if err = application.bootstrap(ctx, bootstrap); err != nil {
		t.Fatal(err)
	}
	var actorID, planID int64
	if err = application.pool.Native().QueryRow(ctx, `SELECT id FROM admin_users WHERE username='groupops-browser-owner'`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO group_ops_plans(name,status,revision,created_by,updated_by,created_at,updated_at,plan_type) VALUES('Chromium 群运营计划','draft',1,$1,$1,$2,$2,'standard') RETURNING id`, actorID, now).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO group_ops_plan_members(plan_id,staff_id) VALUES($1,$2)`, planID, actorID); err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = application.handler
	server.StartTLS()
	return &groupOpsChromiumFixture{ctx: ctx, application: application, server: server, script: filepath.Join(filepath.Dir(source), "group_ops_chromium_journey.mjs"), planID: planID}
}
