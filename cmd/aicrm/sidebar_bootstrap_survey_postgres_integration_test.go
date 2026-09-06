package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLSidebarBootstrapUsesBoundedSurveyReadAndActualTotal proves the
// outer Composition route accepts an authenticated, already-bound sidebar
// viewer even when Survey has more entries than its profile window permits.
// OneID is used only to resolve the existing scoped external identity. The
// journey performs no Provider write or customer provisioning.
func TestPostgreSQLSidebarBootstrapUsesBoundedSurveyReadAndActualTotal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	provider := newCustomerTagChromiumProvider()
	defer provider.Close()
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal(err)
	}
	application, err := compose(ctx, platformconfig.Runtime{
		Role:         platformconfig.RoleAPI,
		DatabaseURL:  databaseURL,
		PublicOrigin: "https://sidebar-bootstrap.example.test",
		ReleaseSHA:   "sidebar-bootstrap-survey-total",
		WorkerOwner:  "sidebar-bootstrap-survey-total",
		WorkerLimit:  1,
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "sidebar-bootstrap-survey-total-webhook-secret"},
		Survey: platformconfig.Survey{
			DataKey:              base64.RawStdEncoding.EncodeToString(dataKey),
			IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(dataKey),
		},
		Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "sidebar-survey-owner", Password: "sidebar-survey-owner-password", DisplayName: "Sidebar Survey Owner"},
		Effects:   platformconfig.Effects{ProviderEnabled: false},
		WeCom:     platformconfig.WeCom{Enabled: true, CorpID: "fixture-corp", AgentID: "fixture-agent", Secret: "fixture-secret", ContactSecret: "fixture-contact-secret", ContextSigningKey: "sidebar-bootstrap-context-key-32", APIBase: provider.URL(), HTTPClient: provider.Client()},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "sidebar-survey-owner", Password: "sidebar-survey-owner-password", DisplayName: "Sidebar Survey Owner"}); err != nil {
		t.Fatal(err)
	}
	if err = seedSidebarThumbnailChromiumJourney(ctx, application); err != nil {
		t.Fatal(err)
	}
	if err = seedSidebarBootstrapSurveySubmissions(ctx, application); err != nil {
		t.Fatal(err)
	}

	issued, err := application.authentication.LoginWithWeComUserID(ctx, accessapp.WeComLoginCommand{WeComUserID: "fixture-staff", Remote: "sidebar-bootstrap-survey-total"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/sidebar/v2/bootstrap", strings.NewReader(`{"external_userid":"sidebar-thumbnail-external"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "aicrm_sidebar_session", Value: issued.SessionToken})
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("sidebar bootstrap status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		State     string `json:"state"`
		Workbench struct {
			QuestionnaireCount int64 `json:"questionnaire_count"`
		} `json:"workbench"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.State != "ready" || body.Workbench.QuestionnaireCount != 102 {
		t.Fatalf("sidebar bootstrap=%+v", body)
	}
	writes, reads := provider.Counts()
	if writes != 0 || reads != 0 {
		t.Fatalf("sidebar bootstrap must not call WeCom provider: writes=%d reads=%d", writes, reads)
	}
}

func seedSidebarBootstrapSurveySubmissions(ctx context.Context, application *composedApplication) error {
	pool := application.pool.Native()
	now := time.Now().UTC().Add(-time.Minute)
	var questionnaireID, versionID int64
	if err := pool.QueryRow(ctx, `INSERT INTO survey_questionnaires(
		name,title,description,mode,answer_display_mode,slug,status,created_by,updated_by,created_at,updated_at
	) VALUES(
		'sidebar-bootstrap-count','Sidebar bootstrap count','','survey','all_in_one','sidebar-bootstrap-count','published',1,1,$1,$1
	) RETURNING id`, now).Scan(&questionnaireID); err != nil {
		return err
	}
	if err := pool.QueryRow(ctx, `INSERT INTO survey_definition_versions(
		questionnaire_id,version_number,mode,answer_display_mode,title_snapshot,description_snapshot,assessment_config,definition_digest,is_immutable,published_at,created_by,created_at
	) VALUES($1,1,'survey','all_in_one','Sidebar bootstrap count','', '{}'::jsonb,decode(repeat('5a',32),'hex'),true,$2,1,$2) RETURNING id`, questionnaireID, now).Scan(&versionID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `UPDATE survey_questionnaires SET active_definition_version_id=$2 WHERE id=$1`, questionnaireID, versionID); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, `INSERT INTO survey_submissions(
		questionnaire_id,definition_version_id,definition_version_number,customer_id,identity_state,
		submission_key_digest,payload_digest,questionnaire_slug_snapshot,title_snapshot,mode_snapshot,
		result_snapshot,submitted_at,created_at
	) SELECT $1,$2,1,1,'resolved',
		decode(lpad(to_hex(series),64,'0'),'hex'),decode(lpad(to_hex(series + 1024),64,'0'),'hex'),
		'sidebar-bootstrap-count','Sidebar bootstrap count','survey','{}'::jsonb,$3 - make_interval(secs => series),$3
	FROM generate_series(1,102) AS series`, questionnaireID, versionID, now)
	return err
}
