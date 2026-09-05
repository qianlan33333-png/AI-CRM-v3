package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	aiassistantapp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/app"
	aiassistanthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/http"
	aiassistantstore "github.com/qianlan33333-png/AI-CRM-v3/internal/aiassistant/store"
	externaleffects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	groupopsapp "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/app"
	groupopsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identityquery "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/query"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	groupopsmaterial "github.com/qianlan33333-png/AI-CRM-v3/internal/media/groupopsmaterial"
	mediastore "github.com/qianlan33333-png/AI-CRM-v3/internal/media/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
	"github.com/riverqueue/river"
)

// TestAIAssistantAndGroupOpsShareRiverOutboundAndEffects exercises both real
// outbound leaves in one PostgreSQL schema. It starts River only after both
// acceptances commit, then repeats their command receipts across a restart.
func TestAIAssistantAndGroupOpsShareRiverOutboundAndEffects(t *testing.T) {
	native, cleanup := aiAssistantHTTPJourneyPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, migration := range []string{"0007_media.sql", "0012_group_ops.sql", "0016_media_content_packages.sql", "0078_group_ops_provider_tasks.sql", "0081_group_ops_webhook_unconfigured_reference.sql"} {
		if err := applyAIAssistantHTTPJourneyMigration(ctx, native, migration); err != nil {
			t.Fatalf("apply %s: %v", migration, err)
		}
	}
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	seedAIAssistantHTTPJourney(t, native)
	var groupActor int64
	if err = native.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active) VALUES('joint-group','$argon2id$joint','Joint Group','journey-sender',true) RETURNING id`).Scan(&groupActor); err != nil {
		t.Fatal(err)
	}

	var privateCalls, groupCalls, messageSequence atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			secret := r.URL.Query().Get("corpsecret")
			if secret != "journey-contact-secret" && secret != "runtime-contact-secret" {
				t.Errorf("unexpected contact secret")
				http.Error(w, "bad secret", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": "joint-token", "expires_in": 7200})
		case "/cgi-bin/externalcontact/add_msg_template":
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode joint WeCom request: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			sender, _ := request["sender"].(string)
			switch sender {
			case "staff-1":
				privateCalls.Add(1)
			case "journey-sender":
				if request["chat_type"] != "group" {
					t.Errorf("group request=%#v", request)
				}
				groupCalls.Add(1)
			default:
				t.Errorf("unknown sender=%q request=%#v", sender, request)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "msgid": fmt.Sprintf("joint-msg-%d", messageSequence.Add(1))})
		default:
			http.NotFound(w, r)
		}
	}))
	defer providerServer.Close()
	privateWeCom, err := wecomadapter.NewDirectory(wecomadapter.Config{Enabled: true, CorpID: "corp-1", ContactSecret: "journey-contact-secret", APIBase: providerServer.URL, HTTPClient: providerServer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	groupWeCom, err := wecomadapter.NewDirectory(wecomadapter.Config{Enabled: true, CorpID: "corp-1", ContactSecret: "runtime-contact-secret", APIBase: providerServer.URL, HTTPClient: providerServer.Client()})
	if err != nil {
		t.Fatal(err)
	}

	accessRepository := accessstore.NewPostgreSQL()
	identities := identityquery.NewPostgreSQL()
	aiStore, err := aiassistantstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	aiService, err := aiassistantapp.NewService(uow, aiStore, journeyCustomerReader{}, aiStaffSnapshotAdapter{repository: accessRepository}, journeyTextMaterials{}, identityapp.OneIDService{Store: identitystore.NewPostgresStore()}, identities)
	if err != nil {
		t.Fatal(err)
	}
	groupStore, err := groupopsstore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	mediaStore, err := mediastore.NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	freezer, err := groupopsmaterial.NewFreezer(mediaPreparedPlanReader{reader: mediaStore})
	if err != nil {
		t.Fatal(err)
	}
	materials, err := newGroupOpsMaterialAdapter(mediaStore, freezer)
	if err != nil {
		t.Fatal(err)
	}

	workers := river.NewWorkers()
	effectsModule := externaleffects.NewModuleRegistration()
	if err = effectsModule.RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	continuation := groupopsapp.NewContinuationWorker()
	if err = river.AddWorkerSafely[groupopsapp.ContinuationJobArgs](workers, continuation); err != nil {
		t.Fatal(err)
	}
	insertClient, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, insertClient)
	if err != nil {
		t.Fatal(err)
	}
	privateWriter, err := outbound.NewPrivateMessageRepository(native, effects)
	if err != nil {
		t.Fatal(err)
	}
	if err = aiService.BindOutbound(privateWriter, true); err != nil {
		t.Fatal(err)
	}
	if err = aiService.BindReconciler(effects); err != nil {
		t.Fatal(err)
	}
	staff := groupOpsStaffAdapter{access: accessRepository, owners: groupStore}
	groupRuntime := groupopsapp.NewRuntimeService(uow, groupStore, groupStore, effects, staff, nil, staff, nil, journeyReconciler{repository: effects}, materials)
	groupRuntime.SetDispatchEnabled(true)
	if err = continuation.Bind(groupRuntime); err != nil {
		t.Fatal(err)
	}
	privateProvider, err := outbound.NewPrivateMessageProvider(true, privateWriter, aiPrivateTargetResolver{uow: uow, identities: identities, access: accessRepository, relationships: wecom.NewPostgreSQLFollowRelationshipStore(), corpID: "corp-1"}, aiPrivatePayloadReader{content: aiStore}, privateWeCom)
	if err != nil {
		t.Fatal(err)
	}
	readiness := groupOpsMaterialReadinessAdapter{uow: uow, capturer: mediaStore, freezer: freezer}
	groupProvider, err := outbound.NewGroupMessageProvider(outbound.GroupMessageProviderConfig{Enabled: true, Executions: groupOpsDispatchReader{uow: uow, execution: groupStore, senders: staff}, Materials: readiness, Writer: groupWeCom})
	if err != nil {
		t.Fatal(err)
	}
	if err = effectsModule.SetProviderAdapter(outbound.NewProviderRouterWithPrivate(nil, groupProvider, privateProvider)); err != nil {
		t.Fatal(err)
	}
	groupCompletion, err := outbound.NewGroupMessageCompletionSink(groupStore, groupStore)
	if err != nil {
		t.Fatal(err)
	}
	continuations, err := groupopsapp.NewRiverContinuationEnqueuer(insertClient)
	if err != nil {
		t.Fatal(err)
	}
	groupCompletion.WithContinuation(continuations)
	privateCompletion, err := outbound.NewPrivateMessageCompletionSink(privateWriter, aiStore)
	if err != nil {
		t.Fatal(err)
	}
	completionRouter, err := outbound.NewCompletionRouterWithPrivate(nil, groupCompletion, privateCompletion)
	if err != nil {
		t.Fatal(err)
	}
	if err = effects.SetCompletionSink(completionRouter); err != nil {
		t.Fatal(err)
	}
	if _, err = effectsModule.Bind(effects, journeyHTTPSecurity{}); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
	handler, err := aiassistanthttp.NewHandler(aiassistanthttp.Config{Application: aiService, Security: journeyHTTPSecurity{}, Authorizer: accessapp.AIAssistantAuthorizer{}, Integration: aiassistanthttp.IntegrationConfig{Enabled: true, Key: "journey", Secret: "01234567890123456789012345678901", ActorID: 9, WeComCorpID: "corp-1", MaxSkew: 5 * time.Minute}, DispatchReady: true, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	routes := handler.Routes()
	created := journeySignedJSON(t, routes, now, "01234567890123456789012345678901", "joint-nonce-00000001", "joint-intake-key-00001", map[string]any{"external_userid": "external-1", "owner_userid": "staff-1", "content_package": map[string]any{"content_text": "joint private"}, "external_event_id": "joint-event-1"})
	var intake struct {
		Plan struct {
			ID int64 `json:"id"`
		} `json:"plan"`
	}
	if created.Code != http.StatusAccepted || json.Unmarshal(created.Body.Bytes(), &intake) != nil || intake.Plan.ID < 1 {
		t.Fatalf("AI intake=%d %s", created.Code, created.Body.String())
	}
	list := journeyAdminJSON(t, routes, http.MethodGet, "/api/admin/ai-assistant/plans/"+itoa(intake.Plan.ID)+"/recipients?limit=50", "", nil)
	var recipients struct {
		Items []struct {
			ID      int64 `json:"id"`
			Version int64 `json:"version"`
		} `json:"items"`
	}
	if err = json.Unmarshal(list.Body.Bytes(), &recipients); err != nil || len(recipients.Items) != 1 {
		t.Fatalf("AI recipients=%s err=%v", list.Body.String(), err)
	}
	recipient := recipients.Items[0]
	if reply := journeyAdminJSON(t, routes, http.MethodPost, "/api/admin/ai-assistant/plans/"+itoa(intake.Plan.ID)+"/recipients/"+itoa(recipient.ID)+"/review", "joint-review-1", map[string]any{"expected_version": recipient.Version, "decision": "approved"}); reply.Code != http.StatusOK {
		t.Fatalf("AI review=%d %s", reply.Code, reply.Body.String())
	}
	plan := journeyAdminJSON(t, routes, http.MethodGet, "/api/admin/ai-assistant/plans/"+itoa(intake.Plan.ID), "", nil)
	var planState struct {
		Plan struct {
			Version int64 `json:"version"`
		} `json:"plan"`
	}
	if err = json.Unmarshal(plan.Body.Bytes(), &planState); err != nil {
		t.Fatal(err)
	}
	preview := journeyAdminJSON(t, routes, http.MethodPost, "/api/admin/ai-assistant/plans/"+itoa(intake.Plan.ID)+"/preview-approval", "joint-preview-1", map[string]any{"expected_version": planState.Plan.Version})
	var previewBody struct {
		PreviewDigest string `json:"preview_digest"`
	}
	if err = json.Unmarshal(preview.Body.Bytes(), &previewBody); err != nil || previewBody.PreviewDigest == "" {
		t.Fatalf("AI preview=%s err=%v", preview.Body.String(), err)
	}
	approve := func() *httptest.ResponseRecorder {
		return journeyAdminJSON(t, routes, http.MethodPost, "/api/admin/ai-assistant/plans/"+itoa(intake.Plan.ID)+"/approve", "joint-approve-1", map[string]any{"expected_version": planState.Plan.Version, "preview_digest": previewBody.PreviewDigest})
	}
	if reply := approve(); reply.Code != http.StatusOK {
		t.Fatalf("AI approve=%d %s", reply.Code, reply.Body.String())
	}

	groupPlan := createRiverJourneyPlan(t, ctx, uow, groupStore, groupActor)
	if _, err = native.Exec(ctx, `INSERT INTO group_ops_directory_groups(chat_reference,owner_staff_id,display_name,member_count,source_digest,refreshed_at) VALUES ('chat-river-1',$1,'Joint one',1,'sha256:2a4d94c0173fb5ae03b9c51223a7e16b48f261aa2603ae1bd91a8d5de54b7ec5',clock_timestamp()),('chat-river-2',$1,'Joint two',1,'sha256:4679c40a8551974f87b357c95f2d3a9b7030ddd442a4e440fc1aa094624bca6e',clock_timestamp())`, groupActor); err != nil {
		t.Fatal(err)
	}
	accepted, err := groupRuntime.AcceptBroadcast(ctx, groupPlan, groupActor, "joint-group-accept-1")
	if err != nil || accepted.Accepted != 2 {
		t.Fatalf("group acceptance=%+v err=%v", accepted, err)
	}
	var effectsCount, fingerprints, groupEffect, privateEffect int
	if err = native.QueryRow(ctx, `SELECT count(*),count(DISTINCT envelope_fingerprint),count(*) FILTER (WHERE kind='group_message'),count(*) FILTER (WHERE kind='outbound_message') FROM external_effects`).Scan(&effectsCount, &fingerprints, &groupEffect, &privateEffect); err != nil || effectsCount != 3 || fingerprints != 3 || groupEffect != 2 || privateEffect != 1 {
		t.Fatalf("shared EER namespace effects=%d fingerprints=%d group=%d private=%d err=%v", effectsCount, fingerprints, groupEffect, privateEffect, err)
	}

	runtimeService, err := platformjobqueue.NewRuntime(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	start, stop := startGroupOpsRiver(t, runtimeService)
	start()
	deadline := time.Now().Add(12 * time.Second)
	for privateCalls.Load()+groupCalls.Load() != 3 && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	waitGroupOpsDelayedSuccessors(t, native, groupPlan, 2)
	var effectBaseline int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effectBaseline); err != nil || effectBaseline != 5 {
		stop()
		t.Fatalf("first group completion must persist two delayed successors: effects=%d err=%v", effectBaseline, err)
	}
	stop()
	if privateCalls.Load() != 1 || groupCalls.Load() != 2 {
		t.Fatalf("shared leaves private=%d group=%d", privateCalls.Load(), groupCalls.Load())
	}
	if replay, replayErr := groupRuntime.AcceptBroadcast(ctx, groupPlan, groupActor, "joint-group-accept-1"); replayErr != nil || replay.Run.ID != accepted.Run.ID {
		t.Fatalf("group replay=%+v err=%v", replay, replayErr)
	}
	if reply := approve(); reply.Code != http.StatusOK {
		t.Fatalf("AI approval replay=%d %s", reply.Code, reply.Body.String())
	}
	restarted, err := platformjobqueue.NewRuntime(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	start, stop = startGroupOpsRiver(t, restarted)
	start()
	time.Sleep(250 * time.Millisecond)
	stop()
	if privateCalls.Load() != 1 || groupCalls.Load() != 2 {
		t.Fatalf("replay/restart duplicated provider calls: private=%d group=%d", privateCalls.Load(), groupCalls.Load())
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effectsCount); err != nil || effectsCount != effectBaseline {
		t.Fatalf("replay/restart minted effects=%d baseline=%d err=%v", effectsCount, effectBaseline, err)
	}
}
