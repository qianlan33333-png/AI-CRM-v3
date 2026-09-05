package app

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
)

func TestMessageNodeAcceptsTypedMaterialOnlyAndRejectsInvalidPlans(t *testing.T) {
	plan := groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{{Kind: "image", ID: 1}, {Kind: "miniprogram", ID: 2}}}
	if !validNode(groupopsport.NodeMessage, "", 0, "", plan) {
		t.Fatal("typed material-only message was rejected")
	}
	if validNode(groupopsport.NodeMessage, "", 0, "", groupopsport.MaterialPlan{}) {
		t.Fatal("empty message without materials was accepted")
	}
	if validNode(groupopsport.NodeMessage, "", 0, "", groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{{Kind: "image", ID: 1}, {Kind: "image", ID: 1}}}) {
		t.Fatal("duplicate material reference was accepted")
	}
}

func TestLocalPlanLifecycleUsesReceiptsEventsAndStrictDraftBoundary(t *testing.T) {
	service, store, events := newTestService()
	ctx := context.Background()
	detail, err := service.Create(ctx, groupopsport.CreatePlanCommand{Name: "Onboarding", Actor: 7, IdempotencyKey: "group-ops-create-0001"})
	if err != nil || detail.Plan.Status != groupopsport.PlanDraft || detail.Plan.Revision != 1 || detail.ProviderExecutionEligible || detail.RealExternalCallExecuted {
		t.Fatalf("create detail=%#v err=%v", detail, err)
	}
	detail, err = service.AddMember(ctx, groupopsport.MemberCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, StaffID: 19, Actor: 7, IdempotencyKey: "group-ops-member-0001"})
	if err != nil || len(detail.Members) != 1 || detail.Members[0].StaffID != 19 {
		t.Fatalf("member detail=%#v err=%v", detail, err)
	}
	detail, err = service.AddGroupAsset(ctx, groupopsport.GroupAssetCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, AssetRef: "local-group-18", Actor: 7, IdempotencyKey: "group-ops-asset-0001"})
	if err != nil || len(detail.GroupAssets) != 1 || detail.GroupAssets[0].ID < 1 {
		t.Fatalf("asset detail=%#v err=%v", detail, err)
	}
	detail, err = service.AddNode(ctx, groupopsport.NodeCreateCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Position: 1, Kind: groupopsport.NodeMessage, MessageText: "欢迎加入", Actor: 7, IdempotencyKey: "group-ops-node-0001"})
	if err != nil || len(detail.Nodes) != 1 || detail.Nodes[0].ID < 1 {
		t.Fatalf("node detail=%#v err=%v", detail, err)
	}
	preview, err := service.Preview(ctx, detail.Plan.ID)
	if err != nil || !preview.Valid || preview.ProviderExecutionEligible || preview.RealExternalCallExecuted || len(preview.PreviewLines) != 1 {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	detail, err = service.Activate(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Actor: 7, IdempotencyKey: "group-ops-activate-01"})
	if err != nil || detail.Plan.Status != groupopsport.PlanActive {
		t.Fatalf("activate detail=%#v err=%v", detail, err)
	}
	_, err = service.Update(ctx, groupopsport.UpdatePlanCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Name: "not allowed", Actor: 7, IdempotencyKey: "group-ops-update-0001"})
	if !errors.Is(err, ErrStateConflict) {
		t.Fatalf("update active err=%v", err)
	}
	detail, err = service.Pause(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Actor: 7, IdempotencyKey: "group-ops-pause-00001"})
	if err != nil || detail.Plan.Status != groupopsport.PlanPaused {
		t.Fatalf("pause detail=%#v err=%v", detail, err)
	}
	_, err = service.Activate(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision - 1, Actor: 7, IdempotencyKey: "group-ops-reactivate-stale"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale reactivate err=%v", err)
	}
	detail, err = service.Activate(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Actor: 7, IdempotencyKey: "group-ops-reactivate-001"})
	if err != nil || detail.Plan.Status != groupopsport.PlanActive {
		t.Fatalf("reactivate detail=%#v err=%v", detail, err)
	}
	replayedActivation, err := service.Activate(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision - 1, Actor: 7, IdempotencyKey: "group-ops-reactivate-001"})
	if err != nil || replayedActivation.Plan != detail.Plan {
		t.Fatalf("reactivate replay=%#v err=%v", replayedActivation, err)
	}
	detail, err = service.Archive(ctx, groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Actor: 7, IdempotencyKey: "group-ops-archive-001"})
	if err != nil || detail.Plan.Status != groupopsport.PlanArchived {
		t.Fatalf("archive detail=%#v err=%v", detail, err)
	}
	_, err = service.AddMember(ctx, groupopsport.MemberCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, StaffID: 20, Actor: 7, IdempotencyKey: "group-ops-member-0002"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("archive write err=%v", err)
	}
	completed := 0
	for _, receipt := range store.receipts {
		if receipt.State == "completed" {
			completed++
		}
	}
	if completed != 8 || len(events.items) != 8 {
		t.Fatalf("completed receipts/events=%d/%d", completed, len(events.items))
	}
	for _, event := range events.items {
		if event.Type != groupopsport.EvGroupOpsPlanUpdated {
			t.Fatalf("event=%#v", event)
		}
	}
}

func TestPausedActivationRejectsIncompleteDefinition(t *testing.T) {
	service, _, _ := newTestService()
	detail, err := service.Create(context.Background(), groupopsport.CreatePlanCommand{Name: "incomplete", Actor: 7, IdempotencyKey: "group-ops-incomplete-001"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Activate(context.Background(), groupopsport.TransitionCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Actor: 7, IdempotencyKey: "group-ops-incomplete-activate"})
	if !errors.Is(err, ErrStateConflict) {
		t.Fatalf("incomplete activation err=%v", err)
	}
}

func TestCreateReplayAndDescriptorAreOpaqueOnly(t *testing.T) {
	service, store, _ := newTestService()
	first, err := service.Create(context.Background(), groupopsport.CreatePlanCommand{Name: "local", Actor: 7, IdempotencyKey: "group-ops-create-replay"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(context.Background(), groupopsport.CreatePlanCommand{Name: "local", Actor: 7, IdempotencyKey: "group-ops-create-replay"})
	if err != nil || second.Plan.ID != first.Plan.ID || len(store.details) != 1 {
		t.Fatalf("replay=%#v err=%v details=%d", second, err, len(store.details))
	}
	_, err = service.Create(context.Background(), groupopsport.CreatePlanCommand{Name: "different", Actor: 7, IdempotencyKey: "group-ops-create-replay"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("replay conflict err=%v", err)
	}
	for _, ref := range []string{"https://example.invalid", "secret-value", "token-value"} {
		_, err = service.PutWebhookDescriptor(context.Background(), groupopsport.WebhookDescriptorCommand{PlanID: first.Plan.ID, ExpectedRevision: first.Plan.Revision, Reference: ref, Actor: 7, IdempotencyKey: "group-ops-webhook-0001"})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("ref=%q err=%v", ref, err)
		}
	}
	detail, err := service.PutWebhookDescriptor(context.Background(), groupopsport.WebhookDescriptorCommand{PlanID: first.Plan.ID, ExpectedRevision: first.Plan.Revision, Reference: "local-webhook-7", Actor: 7, IdempotencyKey: "group-ops-webhook-0002"})
	if err != nil || detail.WebhookDescriptor.Reference != "local-webhook-7" || detail.WebhookDescriptor.Path != "/api/automation/group-ops/webhooks/local-webhook-7" || detail.WebhookDescriptor.SignatureAlgorithm != "HMAC-SHA256" || detail.WebhookDescriptor.NonceHeader != "X-AICRM-Event-Id" {
		t.Fatalf("descriptor=%#v err=%v", detail.WebhookDescriptor, err)
	}
	read, err := service.GetWebhookDescriptor(context.Background(), first.Plan.ID)
	if err != nil || read != detail.WebhookDescriptor {
		t.Fatalf("read descriptor=%#v err=%v", read, err)
	}
	encoded, err := json.Marshal(read)
	if err != nil || !strings.Contains(string(encoded), `"url":"/api/automation/group-ops/webhooks/local-webhook-7"`) || !strings.Contains(string(encoded), `"signature_header":"X-AICRM-Signature"`) || strings.Contains(string(encoded), `"secret"`) || strings.Contains(string(encoded), `"token"`) || strings.Contains(string(encoded), `"receipt"`) {
		t.Fatalf("descriptor JSON=%s err=%v", encoded, err)
	}
}

func TestListsAreDeterministicAndBounded(t *testing.T) {
	service, _, _ := newTestService()
	for _, name := range []string{"one", "two", "three"} {
		if _, err := service.Create(context.Background(), groupopsport.CreatePlanCommand{Name: name, Actor: 7, IdempotencyKey: "group-ops-list-key-" + name}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := service.List(context.Background(), 2, 0)
	if err != nil || page.Total != 3 || len(page.Items) != 2 || !page.HasMore || page.Items[0].ID <= page.Items[1].ID || page.ProviderExecutionEligible || page.RealExternalCallExecuted {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	encoded, err := json.Marshal(page)
	if err != nil || !strings.Contains(string(encoded), `"queue_count":0`) {
		t.Fatalf("encoded=%s err=%v", encoded, err)
	}
	if _, err := service.List(context.Background(), 101, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bounds err=%v", err)
	}
}

func TestMutationRejectsMismatchedStrictReadback(t *testing.T) {
	service, store, _ := newTestService()
	detail, err := service.Create(context.Background(), groupopsport.CreatePlanCommand{Name: "readback", Actor: 7, IdempotencyKey: "group-ops-readback-001"})
	if err != nil {
		t.Fatal(err)
	}
	store.corruptReadback = true
	_, err = service.Update(context.Background(), groupopsport.UpdatePlanCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, Name: "changed", Actor: 7, IdempotencyKey: "group-ops-readback-002"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
}

func TestAddMemberRequiresActiveLocalStaff(t *testing.T) {
	service, _, _ := newTestService()
	detail, err := service.Create(context.Background(), groupopsport.CreatePlanCommand{Name: "local staff", Actor: 7, IdempotencyKey: "group-ops-staff-create-001"})
	if err != nil {
		t.Fatal(err)
	}
	service.staff = testStaff{active: false}
	_, err = service.AddMember(context.Background(), groupopsport.MemberCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, StaffID: 19, Actor: 7, IdempotencyKey: "group-ops-staff-invalid-01"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("inactive staff err=%v", err)
	}
	service.staff = testStaff{err: errors.New("staff reader unavailable")}
	_, err = service.AddMember(context.Background(), groupopsport.MemberCommand{PlanID: detail.Plan.ID, ExpectedRevision: detail.Plan.Revision, StaffID: 19, Actor: 7, IdempotencyKey: "group-ops-staff-unavail-1"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("staff reader err=%v", err)
	}
}

type testUOW struct{}

func (testUOW) Within(_ context.Context, fn func(context.Context) error) error {
	return fn(context.Background())
}

type testEvents struct{ items []groupopsport.Event }

type testStaff struct {
	active bool
	err    error
}

var _ groupopsport.ActiveStaffReader = testStaff{}

func (s testStaff) IsActiveStaff(context.Context, int64) (bool, error) { return s.active, s.err }

func (e *testEvents) Append(_ context.Context, event groupopsport.Event) (groupopsport.EventID, error) {
	e.items = append(e.items, event)
	return groupopsport.EventID(len(e.items)), nil
}

type testStore struct {
	details         map[int64]groupopsport.Detail
	receipts        map[string]Receipt
	nextPlan        int64
	nextAsset       int64
	nextNode        int64
	corruptReadback bool
}

func newTestService() (*Service, *testStore, *testEvents) {
	store := &testStore{details: map[int64]groupopsport.Detail{}, receipts: map[string]Receipt{}, nextPlan: 1, nextAsset: 1, nextNode: 1}
	events := &testEvents{}
	service := NewService(testUOW{}, store, testStaff{active: true}, events)
	service.now = func() time.Time { return time.Date(2026, time.August, 23, 8, 0, 0, 0, time.UTC) }
	return service, store, events
}

func (s *testStore) List(_ context.Context, limit, offset int32) ([]groupopsport.PlanListItem, error) {
	items := make([]groupopsport.PlanListItem, 0, len(s.details))
	for _, detail := range s.details {
		items = append(items, groupopsport.PlanListItem{Plan: detail.Plan})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt) || items[i].UpdatedAt.Equal(items[j].UpdatedAt) && items[i].ID > items[j].ID
	})
	if int(offset) >= len(items) {
		return []groupopsport.PlanListItem{}, nil
	}
	end := int(offset + limit)
	if end > len(items) {
		end = len(items)
	}
	return append([]groupopsport.PlanListItem{}, items[offset:end]...), nil
}
func (s *testStore) Count(context.Context) (int64, error) { return int64(len(s.details)), nil }
func (s *testStore) Get(_ context.Context, id int64) (groupopsport.Detail, error) {
	detail, ok := s.details[id]
	if !ok {
		return groupopsport.Detail{}, ErrNotFound
	}
	return cloneDetail(detail), nil
}
func (s *testStore) Lock(ctx context.Context, id int64) (groupopsport.Detail, error) {
	return s.Get(ctx, id)
}
func (s *testStore) Create(_ context.Context, plan groupopsport.Plan) (int64, error) {
	plan.ID = s.nextPlan
	s.nextPlan++
	s.details[plan.ID] = groupopsport.Detail{Plan: plan, Members: []groupopsport.Member{}, GroupAssets: []groupopsport.GroupAsset{}, Nodes: []groupopsport.Node{}, WebhookDescriptor: descriptor(""), Safety: groupopsport.LocalSafety()}
	return plan.ID, nil
}
func (s *testStore) Save(_ context.Context, detail groupopsport.Detail) error {
	for i := range detail.GroupAssets {
		if detail.GroupAssets[i].ID == 0 {
			detail.GroupAssets[i].ID = s.nextAsset
			s.nextAsset++
		}
	}
	for i := range detail.Nodes {
		if detail.Nodes[i].ID == 0 {
			detail.Nodes[i].ID = s.nextNode
			s.nextNode++
		}
	}
	sort.Slice(detail.GroupAssets, func(i, j int) bool { return detail.GroupAssets[i].AssetRef < detail.GroupAssets[j].AssetRef })
	if s.corruptReadback {
		detail.Plan.Name = "corrupt"
	}
	s.details[detail.Plan.ID] = cloneDetail(detail)
	return nil
}
func receiptKey(operation, scope string, key [32]byte) string {
	return operation + "/" + scope + "/" + string(key[:])
}
func (s *testStore) Reserve(_ context.Context, operation string, input Reservation) (Receipt, bool, error) {
	key := receiptKey(operation, input.ActorScope, input.KeyDigest)
	if value, ok := s.receipts[key]; ok {
		return value, false, nil
	}
	value := Receipt{ID: int64(len(s.receipts) + 1), Operation: operation, ActorScope: input.ActorScope, KeyDigest: input.KeyDigest, PayloadDigest: input.PayloadDigest, State: "in_progress"}
	s.receipts[key] = value
	return value, true, nil
}
func (s *testStore) Complete(_ context.Context, id int64, snapshot json.RawMessage, _ time.Time) (Receipt, error) {
	for key, value := range s.receipts {
		if value.ID == id {
			value.State = "completed"
			value.ResultSnapshot = append([]byte{}, snapshot...)
			s.receipts[key] = value
			return value, nil
		}
	}
	return Receipt{}, ErrNotFound
}
