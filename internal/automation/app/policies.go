package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

var (
	ErrRuntimeInvalid     = errors.New("invalid automation runtime request")
	ErrRuntimeNotFound    = errors.New("automation runtime record not found")
	ErrRuntimeConflict    = errors.New("automation runtime conflict")
	ErrRuntimeNotReady    = errors.New("automation runtime not ready")
	ErrRuntimeUnavailable = errors.New("automation runtime unavailable")
)

type RuntimeStore interface {
	ListPolicies(context.Context) ([]automationdomain.Policy, error)
	Policy(context.Context, int64) (automationdomain.Policy, error)
	CreatePolicy(context.Context, automationdomain.Policy) (automationdomain.Policy, error)
	LockPolicy(context.Context, int64) (automationdomain.Policy, error)
	NextPolicyVersion(context.Context, int64) (int64, error)
	CreatePolicyVersion(context.Context, automationdomain.PolicyVersion) (automationdomain.PolicyVersion, error)
	SetCurrentPolicyVersion(context.Context, int64, int64, int64, int64, time.Time) (automationdomain.Policy, error)
	CurrentPolicyVersion(context.Context, int64) (automationdomain.PolicyVersion, error)
	SetPolicyLifecycle(context.Context, int64, int64, int64, automationdomain.PolicyLifecycle, time.Time) (automationdomain.Policy, error)
	ActivePoliciesForPackage(context.Context, int64) ([]automationdomain.PolicyVersion, error)
	CreateEnrollment(context.Context, automationdomain.Enrollment) (automationdomain.Enrollment, bool, error)
	ReserveRuntime(context.Context, RuntimeReservation) (RuntimeReceipt, bool, error)
	CompleteRuntime(context.Context, int64, json.RawMessage, time.Time) error
	AppendRuntimeFact(context.Context, RuntimeFact) error
	CreatePreview(context.Context, automationdomain.RunPreview) (automationdomain.RunPreview, error)
	PreviewByDigest(context.Context, [32]byte) (automationdomain.RunPreview, error)
	CreateRun(context.Context, automationdomain.RuntimeRun, []automationdomain.RuntimeRecipient) (automationdomain.RuntimeRun, error)
	ListRuns(context.Context, int64, int) ([]automationdomain.RuntimeRun, string, error)
	Run(context.Context, int64) (automationdomain.RuntimeRun, error)
	RunRecipients(context.Context, int64, int64, int) ([]automationdomain.RuntimeRecipient, string, error)
	CancelRun(context.Context, int64, time.Time) (automationdomain.RuntimeRun, error)
}
type RuntimeService struct {
	uow       platformport.UnitOfWork
	store     RuntimeStore
	audiences segmentport.ExecutionConfigurationReader
	snapshots segmentport.SnapshotReader
	now       func() time.Time
}
type PolicyCommand struct {
	Code, Name                string
	PolicyID, ExpectedVersion int64
	PackageID                 segmentport.PackageID
	TriggerKind               automationport.TriggerKind
	ActionKind                automationport.ActionKind
	ActionConfig, QuietHours  json.RawMessage
	SingleRunLimit            int
	ApprovalStaffID           *int64
	Actor                     int64
	IdempotencyKey            string
}
type PolicyLifecycleCommand struct {
	PolicyID, ExpectedVersion, Actor int64
	Target                           automationdomain.PolicyLifecycle
	IdempotencyKey                   string
}

func NewRuntimeService(uow platformport.UnitOfWork, store RuntimeStore, audiences segmentport.ExecutionConfigurationReader, snapshots segmentport.SnapshotReader) (*RuntimeService, error) {
	if uow == nil || store == nil || audiences == nil || snapshots == nil {
		return nil, ErrRuntimeNotReady
	}
	return &RuntimeService{uow, store, audiences, snapshots, time.Now}, nil
}
func (s *RuntimeService) ListPolicies(ctx context.Context) ([]automationdomain.Policy, error) {
	var out []automationdomain.Policy
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; out, e = s.store.ListPolicies(tx); return e })
	return out, runtimeClassify(err)
}
func (s *RuntimeService) Policy(ctx context.Context, id int64) (automationdomain.Policy, automationdomain.PolicyVersion, error) {
	if id < 1 {
		return automationdomain.Policy{}, automationdomain.PolicyVersion{}, ErrRuntimeInvalid
	}
	var p automationdomain.Policy
	var v automationdomain.PolicyVersion
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		p, e = s.store.Policy(tx, id)
		if e == nil {
			v, e = s.store.CurrentPolicyVersion(tx, id)
		}
		return e
	})
	return p, v, runtimeClassify(err)
}
func (s *RuntimeService) CreatePolicy(ctx context.Context, c PolicyCommand) (automationdomain.Policy, error) {
	if !validRuntimeMutation(c.Actor, c.IdempotencyKey) {
		return automationdomain.Policy{}, ErrRuntimeInvalid
	}
	now := s.now().UTC()
	p, err := automationdomain.NewPolicy(c.Code, c.Name, c.Actor, now)
	if err != nil {
		return p, ErrRuntimeInvalid
	}
	payload, _ := json.Marshal(c)
	var output automationdomain.Policy
	err = s.runtimeMutation(ctx, "create_policy", c.Actor, c.IdempotencyKey, payload, func(tx context.Context) (any, RuntimeFact, error) {
		created, e := s.store.CreatePolicy(tx, p)
		if e != nil {
			return created, RuntimeFact{}, e
		}
		v, e := automationdomain.NewPolicyVersion(created.ID, 1, c.PackageID, c.TriggerKind, c.ActionKind, c.ActionConfig, c.QuietHours, c.SingleRunLimit, c.ApprovalStaffID, c.Actor, now)
		if e != nil {
			return created, RuntimeFact{}, e
		}
		v, e = s.store.CreatePolicyVersion(tx, v)
		if e != nil {
			return created, RuntimeFact{}, e
		}
		created, e = s.store.SetCurrentPolicyVersion(tx, created.ID, v.ID, created.Version, c.Actor, now)
		return created, runtimeFact("policy", created.ID, "create", "automation.policy.created.v1", c.Actor, c.IdempotencyKey, now), e
	}, &output)
	return output, runtimeClassify(err)
}
func (s *RuntimeService) PutPolicyVersion(ctx context.Context, c PolicyCommand) (automationdomain.PolicyVersion, error) {
	if c.PolicyID < 1 || c.ExpectedVersion < 1 || !validRuntimeMutation(c.Actor, c.IdempotencyKey) {
		return automationdomain.PolicyVersion{}, ErrRuntimeInvalid
	}
	now := s.now().UTC()
	payload, _ := json.Marshal(c)
	var output automationdomain.PolicyVersion
	err := s.runtimeMutation(ctx, "put_policy_version", c.Actor, c.IdempotencyKey, payload, func(tx context.Context) (any, RuntimeFact, error) {
		p, e := s.store.LockPolicy(tx, c.PolicyID)
		if e != nil {
			return output, RuntimeFact{}, e
		}
		if p.Version != c.ExpectedVersion || p.Lifecycle != automationdomain.PolicyPaused {
			return output, RuntimeFact{}, ErrRuntimeConflict
		}
		version, e := s.store.NextPolicyVersion(tx, p.ID)
		if e != nil {
			return output, RuntimeFact{}, e
		}
		output, e = automationdomain.NewPolicyVersion(p.ID, version, c.PackageID, c.TriggerKind, c.ActionKind, c.ActionConfig, c.QuietHours, c.SingleRunLimit, c.ApprovalStaffID, c.Actor, now)
		if e != nil {
			return output, RuntimeFact{}, e
		}
		output, e = s.store.CreatePolicyVersion(tx, output)
		if e != nil {
			return output, RuntimeFact{}, e
		}
		_, e = s.store.SetCurrentPolicyVersion(tx, p.ID, output.ID, p.Version, c.Actor, now)
		return output, runtimeFact("policy", p.ID, "version", "automation.policy.versioned.v1", c.Actor, c.IdempotencyKey, now), e
	}, &output)
	return output, runtimeClassify(err)
}
func (s *RuntimeService) TransitionPolicy(ctx context.Context, c PolicyLifecycleCommand) (automationdomain.Policy, error) {
	if c.PolicyID < 1 || c.ExpectedVersion < 1 || !validRuntimeMutation(c.Actor, c.IdempotencyKey) || (c.Target != automationdomain.PolicyActive && c.Target != automationdomain.PolicyPaused && c.Target != automationdomain.PolicyArchived) {
		return automationdomain.Policy{}, ErrRuntimeInvalid
	}
	if c.Target == automationdomain.PolicyActive {
		_, version, e := s.Policy(ctx, c.PolicyID)
		if e != nil {
			return automationdomain.Policy{}, e
		}
		configuration, e := s.audiences.AudienceExecutionConfiguration(ctx, version.PackageID)
		if e != nil {
			return automationdomain.Policy{}, ErrRuntimeUnavailable
		}
		if !configuration.Ready {
			return automationdomain.Policy{}, ErrRuntimeNotReady
		}
	}
	now := s.now().UTC()
	payload, _ := json.Marshal(c)
	var output automationdomain.Policy
	err := s.runtimeMutation(ctx, "transition_policy", c.Actor, c.IdempotencyKey, payload, func(tx context.Context) (any, RuntimeFact, error) {
		p, e := s.store.LockPolicy(tx, c.PolicyID)
		if e != nil {
			return p, RuntimeFact{}, e
		}
		if p.Version != c.ExpectedVersion {
			return p, RuntimeFact{}, ErrRuntimeConflict
		}
		if c.Target == automationdomain.PolicyActive {
			v, loadErr := s.store.CurrentPolicyVersion(tx, p.ID)
			if loadErr != nil {
				return p, RuntimeFact{}, loadErr
			}
			if !v.TriggerEnabled {
				return p, RuntimeFact{}, ErrRuntimeNotReady
			}
		}
		p, e = s.store.SetPolicyLifecycle(tx, p.ID, p.Version, c.Actor, c.Target, now)
		return p, runtimeFact("policy", p.ID, "transition", "automation.policy.transitioned.v1", c.Actor, c.IdempotencyKey, now), e
	}, &output)
	return output, runtimeClassify(err)
}
func (s *RuntimeService) EnrollAudienceMember(ctx context.Context, event segmentport.MemberEnteredV1) ([]automationdomain.Enrollment, error) {
	if s == nil || event.PackageID < 1 || event.CustomerID < 1 || event.EventID == "" || event.OccurredAt.IsZero() {
		return nil, ErrRuntimeInvalid
	}
	eventDigest := sha256.Sum256([]byte(event.EventID))
	output := []automationdomain.Enrollment{}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		versions, e := s.store.ActivePoliciesForPackage(tx, int64(event.PackageID))
		if e != nil {
			return e
		}
		for _, v := range versions {
			snapshot, _ := json.Marshal(map[string]any{"action_kind": v.ActionKind, "action_config": json.RawMessage(v.ActionConfig), "package_id": event.PackageID, "snapshot_id": event.SnapshotID, "configuration_version_id": event.ConfigurationVersionID, "customer_id": event.CustomerID})
			actionDigest := sha256.Sum256(snapshot)
			state := "pending"
			if v.ActionKind == automationport.ActionRecord {
				state = "recorded"
			}
			enrollment, owned, e := s.store.CreateEnrollment(tx, automationdomain.Enrollment{PolicyID: v.PolicyID, PolicyVersionID: v.ID, SourceEventDigest: eventDigest, CustomerID: int64(event.CustomerID), ActionKind: v.ActionKind, ActionSnapshot: snapshot, ActionDigest: actionDigest, State: state, CreatedAt: event.OccurredAt})
			if e != nil {
				return e
			}
			output = append(output, enrollment)
			if owned {
				payload, _ := json.Marshal(map[string]any{"enrollment_id": enrollment.ID, "policy_id": v.PolicyID, "customer_id": event.CustomerID, "action_kind": v.ActionKind})
				if e = s.store.AppendRuntimeFact(tx, runtimeFact("enrollment", enrollment.ID, "enroll", "automation.enrollment.created.v1", 1, hex.EncodeToString(eventDigest[:])+fmt.Sprint(v.ID), event.OccurredAt, payload)); e != nil {
					return e
				}
			}
		}
		return nil
	})
	return output, runtimeClassify(err)
}
func (s *RuntimeService) runtimeMutation(ctx context.Context, operation string, actor int64, key string, payload json.RawMessage, apply func(context.Context) (any, RuntimeFact, error), target any) error {
	if s == nil || !validRuntimeMutation(actor, key) {
		return ErrRuntimeInvalid
	}
	now := s.now().UTC()
	return s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := s.store.ReserveRuntime(tx, RuntimeReservation{Operation: operation, ActorScope: fmt.Sprintf("admin:%d", actor), KeyDigest: sha256.Sum256([]byte(key)), PayloadDigest: sha256.Sum256(payload), CreatedAt: now})
		if e != nil {
			return e
		}
		if !owned {
			if receipt.State != "completed" || len(receipt.Result) == 0 {
				return ErrRuntimeConflict
			}
			return json.Unmarshal(receipt.Result, target)
		}
		value, fact, e := apply(tx)
		if e != nil {
			return e
		}
		result, e := json.Marshal(value)
		if e != nil {
			return e
		}
		if e = s.store.AppendRuntimeFact(tx, fact); e != nil {
			return e
		}
		if e = s.store.CompleteRuntime(tx, receipt.ID, result, now); e != nil {
			return e
		}
		return json.Unmarshal(result, target)
	})
}
func runtimeFact(kind string, id int64, operation, event string, actor int64, key string, at time.Time, payload ...json.RawMessage) RuntimeFact {
	body := json.RawMessage(nil)
	if len(payload) > 0 {
		body = payload[0]
	} else {
		body, _ = json.Marshal(map[string]any{"resource_id": id})
	}
	return RuntimeFact{Kind: kind, ID: id, Operation: operation, EventType: event, Actor: actor, Payload: body, Key: operation + ":" + key, At: at}
}
func validRuntimeMutation(actor int64, key string) bool {
	return actor > 0 && len(key) >= 16 && len(key) <= 128 && strings.TrimSpace(key) == key
}
func runtimeClassify(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrRuntimeInvalid), errors.Is(err, ErrRuntimeNotFound), errors.Is(err, ErrRuntimeConflict), errors.Is(err, ErrRuntimeNotReady):
		return err
	case errors.Is(err, automationdomain.ErrInvalidPolicy):
		return ErrRuntimeInvalid
	default:
		return ErrRuntimeUnavailable
	}
}
