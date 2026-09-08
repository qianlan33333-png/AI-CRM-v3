package main

import (
	"bytes"
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
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// OneID decision: not involved. The browser fixture uses only the authenticated
// local operator and opaque group-plan records. Persistence decision: the Host
// saves the node through the normal Group Ops PostgreSQL UoW; dispatch remains
// disabled and the journey submits no Provider write.
type groupOpsChromiumFixture struct {
	ctx                context.Context
	application        *composedApplication
	server             *httptest.Server
	script             string
	planID             int64
	ownerStaffID       int64
	replacementStaffID int64
}

func TestPostgreSQLGroupOpsStandardHostCompositionPreflight(t *testing.T) {
	fixture := newGroupOpsChromiumFixture(t)
	session, _ := adminAccessLogin(t, fixture.application.handler, "groupops-browser-owner", "groupops-browser-owner-password")
	page := authenticatedAdminGet(t, fixture.application.handler, session, "/admin/automation-conversion/group-ops/plans/"+strconv.FormatInt(fixture.planID, 10))
	if page.Code != http.StatusOK || !bytes.Contains(page.Body.Bytes(), []byte(`data-group-ops-standard-host="true"`)) || !bytes.Contains(page.Body.Bytes(), []byte(`data-group-ops-standard-stage`)) || !bytes.Contains(page.Body.Bytes(), []byte(`<h1 class="admin-page-title">群运营计划</h1>`)) || !bytes.Contains(page.Body.Bytes(), []byte(`/groupops-assets/`)) || !bytes.Contains(page.Body.Bytes(), []byte(`/groupops-assets/assets/standard-components/operation_member_picker.js`)) {
		t.Fatalf("standard Group Ops page status=%d host=%t native_stage=%t topbar=%t assets=%t", page.Code, bytes.Contains(page.Body.Bytes(), []byte(`data-group-ops-standard-host="true"`)), bytes.Contains(page.Body.Bytes(), []byte(`data-group-ops-standard-stage`)), bytes.Contains(page.Body.Bytes(), []byte(`<h1 class="admin-page-title">群运营计划</h1>`)), bytes.Contains(page.Body.Bytes(), []byte(`/groupops-assets/`)))
	}
	detail := authenticatedAdminGet(t, fixture.application.handler, session, "/api/admin/automation-conversion/group-ops/plans/"+strconv.FormatInt(fixture.planID, 10))
	if detail.Code != http.StatusOK || !bytes.Contains(detail.Body.Bytes(), []byte(`"plan"`)) || !bytes.Contains(detail.Body.Bytes(), []byte(`"plan_type":"standard"`)) {
		t.Fatalf("standard Group Ops detail status=%d plan=%t type=%t", detail.Code, bytes.Contains(detail.Body.Bytes(), []byte(`"plan"`)), bytes.Contains(detail.Body.Bytes(), []byte(`"plan_type":"standard"`)))
	}
	members := authenticatedAdminGet(t, fixture.application.handler, session, "/api/admin/common/operation-members?scope=group_ops&page_size=100")
	if members.Code != http.StatusOK {
		t.Fatalf("Group Ops operation-members status=%d body=%s", members.Code, members.Body.String())
	}
	var memberPayload struct {
		Scope string `json:"scope"`
		Items []struct {
			StaffID      int64  `json:"staff_id"`
			SenderUserID string `json:"sender_userid"`
			DisplayName  string `json:"display_name"`
		} `json:"items"`
	}
	if err := json.NewDecoder(members.Body).Decode(&memberPayload); err != nil {
		t.Fatalf("decode Group Ops operation-members: %v", err)
	}
	seen := map[int64]string{}
	for _, member := range memberPayload.Items {
		seen[member.StaffID] = member.SenderUserID
	}
	if memberPayload.Scope != "group_ops" || len(memberPayload.Items) != 2 || seen[fixture.ownerStaffID] != "chromium-owner" || seen[fixture.replacementStaffID] != "chromium-replacement" {
		t.Fatalf("Group Ops eligible member projection scope=%q items=%+v", memberPayload.Scope, memberPayload.Items)
	}
}

func TestPostgreSQLGroupOpsStandardHostChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	fixture := newGroupOpsChromiumFixture(t)
	command := exec.CommandContext(fixture.ctx, "node", fixture.script)
	command.Env = append(os.Environ(), "AICRM_GROUPOPS_TEST_URL="+fixture.server.URL, "AICRM_GROUPOPS_TEST_USERNAME=groupops-browser-owner", "AICRM_GROUPOPS_TEST_PASSWORD=groupops-browser-owner-password", "AICRM_GROUPOPS_TEST_PLAN_ID="+strconv.FormatInt(fixture.planID, 10), "AICRM_GROUPOPS_TEST_REPLACEMENT_STAFF_ID="+strconv.FormatInt(fixture.replacementStaffID, 10))
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
	var ownerCount, ownerID int64
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT count(*),coalesce(min(staff_id),0) FROM group_ops_plan_members WHERE plan_id=$1`, fixture.planID).Scan(&ownerCount, &ownerID); err != nil || ownerCount != 1 || ownerID != fixture.replacementStaffID {
		t.Fatalf("browser owner persistence count=%d owner=%d err=%v", ownerCount, ownerID, err)
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
	prepareGroupOpsChromiumArtifacts(t, repository)
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
	var actorID, replacementStaffID, planID int64
	if err = application.pool.Native().QueryRow(ctx, `SELECT id FROM admin_users WHERE username='groupops-browser-owner'`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	// Group Ops can select only active Access users with a verified, valid WeCom
	// sender binding. Bootstrap creates the local operator without one, so make
	// the fixture represent the same authorized local state as production.
	if _, err = application.pool.Native().Exec(ctx, `UPDATE admin_users SET wecom_userid='chromium-owner' WHERE id=$1`, actorID); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, "INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active) VALUES($1,$2,$3,$4,true) RETURNING id", "groupops-browser-replacement", "$argon2id$browser-replacement", "Chromium Replacement", "chromium-replacement").Scan(&replacementStaffID); err != nil {
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
	return &groupOpsChromiumFixture{ctx: ctx, application: application, server: server, script: filepath.Join(filepath.Dir(source), "group_ops_chromium_journey.mjs"), planID: planID, ownerStaffID: actorID, replacementStaffID: replacementStaffID}
}

// Group Ops runs against the same already-staged release closure as CI. The
// fixture must not rebuild or replace shared web/dist: packages run in
// parallel and other browser tests read the private PR01 documents there.
func prepareGroupOpsChromiumArtifacts(t *testing.T, repository string) {
	t.Helper()
	for _, relative := range []string{"asset-manifest.json"} {
		if _, err := os.Stat(filepath.Join(repository, "web", "dist", relative)); err != nil {
			t.Fatalf("Group Ops Chromium requires staged release artifact %s: %v", relative, err)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(repository, "web", "dist", "asset-manifest.json"))
	if err != nil || !bytes.Contains(manifest, []byte("\"groupopsHost\"")) || !bytes.Contains(manifest, []byte("\"groupopsStyles\"")) {
		t.Fatalf("Group Ops Chromium release manifest lacks Host closure: %v", err)
	}
}
