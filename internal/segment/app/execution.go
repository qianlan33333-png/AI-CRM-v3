package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

type ExecutionStore interface {
	LockPackage(context.Context, int64) (segmentdomain.Package, error)
	CurrentConfiguration(context.Context, int64) (segmentdomain.ConfigurationVersion, error)
	CurrentBinding(context.Context, int64) (segmentdomain.AutomationBinding, error)
	CreateBinding(context.Context, segmentdomain.AutomationBinding) (segmentdomain.AutomationBinding, error)
	SetCurrentBinding(context.Context, int64, int64, int64, int64, time.Time) (segmentdomain.Package, error)
	CurrentSenderSet(context.Context, int64) (segmentdomain.SenderSet, error)
	CreateSenderSet(context.Context, segmentdomain.SenderSet) (segmentdomain.SenderSet, error)
	SetCurrentSenderSet(context.Context, int64, int64, int64, int64, time.Time) (segmentdomain.Package, error)
	Reserve(context.Context, segmentstore.Reservation) (segmentstore.Receipt, bool, error)
	Complete(context.Context, int64, json.RawMessage, time.Time) (segmentstore.Receipt, error)
	AppendMutationFacts(context.Context, segmentstore.MutationFact) (int64, error)
	PublishedSnapshot(context.Context, segmentport.PackageID) (segmentport.Snapshot, bool, error)
}
type ExecutionService struct {
	uow           platformport.UnitOfWork
	store         ExecutionStore
	agents        automationport.PublishedAgentReader
	staff         accessport.AutomationOpsStaffReader
	providerReady bool
	now           func() time.Time
}
type BindingCommand struct {
	PackageID, ExpectedPackageVersion int64
	AgentID                           automationport.AgentID
	ExpectedPublishedVersion          int64
	ExpectedAgentDigest               string
	Actor                             int64
	IdempotencyKey                    string
}
type SendersCommand struct {
	PackageID, ExpectedPackageVersion int64
	ProviderMemberIDs                 []string
	Actor                             int64
	IdempotencyKey                    string
}
type Precheck struct {
	Ready                  bool                   `json:"ready"`
	Reasons                []string               `json:"reasons"`
	ConfigurationVersionID int64                  `json:"configuration_version_id,omitempty"`
	SnapshotID             segmentport.SnapshotID `json:"snapshot_id,omitempty"`
	BindingVersion         int64                  `json:"binding_version,omitempty"`
	SenderSetVersion       int64                  `json:"sender_set_version,omitempty"`
}

func NewExecutionService(uow platformport.UnitOfWork, store ExecutionStore, agents automationport.PublishedAgentReader, staff accessport.AutomationOpsStaffReader, providerReady bool) (*ExecutionService, error) {
	if uow == nil || store == nil || agents == nil || staff == nil {
		return nil, ErrNotReady
	}
	return &ExecutionService{uow, store, agents, staff, providerReady, time.Now}, nil
}
func (s *ExecutionService) PutBinding(ctx context.Context, command BindingCommand) (segmentdomain.AutomationBinding, error) {
	if !validExecutionMutation(command.PackageID, command.ExpectedPackageVersion, command.Actor, command.IdempotencyKey) || command.AgentID < 1 || command.ExpectedPublishedVersion < 1 || len(command.ExpectedAgentDigest) != 64 {
		return segmentdomain.AutomationBinding{}, ErrInvalid
	}
	published, found, err := s.agents.PublishedAgent(ctx, command.AgentID)
	if err != nil {
		return segmentdomain.AutomationBinding{}, ErrUnavailable
	}
	if !found || published.Status == automationport.AgentStatusArchived || published.PublishedVersion < 1 {
		return segmentdomain.AutomationBinding{}, ErrNotReady
	}
	combined := sha256.Sum256(append(append([]byte{}, published.ContentDigest[:]...), published.MaterialsDigest[:]...))
	if published.PublishedVersion != command.ExpectedPublishedVersion || hex.EncodeToString(combined[:]) != command.ExpectedAgentDigest {
		return segmentdomain.AutomationBinding{}, ErrConflict
	}
	now := s.now().UTC()
	payload, _ := json.Marshal(command)
	var output segmentdomain.AutomationBinding
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := s.store.Reserve(tx, segmentstore.Reservation{Operation: "put_binding", ActorScope: fmt.Sprintf("admin:%d", command.Actor), KeyDigest: sha256.Sum256([]byte(command.IdempotencyKey)), PayloadDigest: sha256.Sum256(payload), CreatedAt: now})
		if e != nil {
			return e
		}
		if !owned {
			return json.Unmarshal(receipt.ResultSnapshot, &output)
		}
		pkg, e := s.store.LockPackage(tx, command.PackageID)
		if e != nil {
			return e
		}
		if pkg.Version != command.ExpectedPackageVersion || pkg.Lifecycle != segmentdomain.Paused {
			return ErrConflict
		}
		output, e = s.store.CreateBinding(tx, segmentdomain.AutomationBinding{PackageID: command.PackageID, AgentID: published.AgentID, AutomationType: published.AutomationType, AgentPublishedVersion: published.PublishedVersion, ContentDigest: published.ContentDigest, MaterialsDigest: published.MaterialsDigest, CreatedBy: command.Actor, CreatedAt: now})
		if e != nil {
			return e
		}
		if _, e = s.store.SetCurrentBinding(tx, command.PackageID, output.ID, command.ExpectedPackageVersion, command.Actor, now); e != nil {
			return e
		}
		result, _ := json.Marshal(output)
		if _, e = s.store.AppendMutationFacts(tx, fact("binding", output.ID, "put", "audience.binding.created.v1", command.Actor, command.IdempotencyKey, now)); e != nil {
			return e
		}
		_, e = s.store.Complete(tx, receipt.ID, result, now)
		return e
	})
	return output, classify(err)
}
func (s *ExecutionService) CurrentBinding(ctx context.Context, packageID int64) (segmentdomain.AutomationBinding, error) {
	var out segmentdomain.AutomationBinding
	err := s.uow.Within(ctx, func(tx context.Context) error { var e error; out, e = s.store.CurrentBinding(tx, packageID); return e })
	return out, classify(err)
}
func (s *ExecutionService) ReplaceSenders(ctx context.Context, command SendersCommand) (segmentdomain.SenderSet, error) {
	if !validExecutionMutation(command.PackageID, command.ExpectedPackageVersion, command.Actor, command.IdempotencyKey) || len(command.ProviderMemberIDs) < 1 || len(command.ProviderMemberIDs) > 5 {
		return segmentdomain.SenderSet{}, ErrInvalid
	}
	members := make([]segmentdomain.Sender, 0, len(command.ProviderMemberIDs))
	seen := map[accessport.StaffID]struct{}{}
	for _, providerID := range command.ProviderMemberIDs {
		if providerID == "" || strings.TrimSpace(providerID) != providerID {
			return segmentdomain.SenderSet{}, ErrInvalid
		}
		eligibility, found, err := s.staff.ResolveAutomationSender(ctx, providerID)
		if err != nil {
			return segmentdomain.SenderSet{}, ErrUnavailable
		}
		if !found || !eligibility.Active || !eligibility.Eligible || eligibility.EligibilityVersion < 1 || eligibility.RefreshedAt.IsZero() {
			return segmentdomain.SenderSet{}, ErrNotReady
		}
		if _, exists := seen[eligibility.StaffID]; exists {
			return segmentdomain.SenderSet{}, ErrConflict
		}
		seen[eligibility.StaffID] = struct{}{}
		members = append(members, segmentdomain.Sender{StaffID: eligibility.StaffID, EligibilityVersion: eligibility.EligibilityVersion, EligibilityRefreshedAt: eligibility.RefreshedAt})
	}
	now := s.now().UTC()
	payload, _ := json.Marshal(struct {
		PackageID int64
		Expected  int64
		StaffIDs  []segmentdomain.Sender
	}{command.PackageID, command.ExpectedPackageVersion, members})
	var output segmentdomain.SenderSet
	err := s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := s.store.Reserve(tx, segmentstore.Reservation{Operation: "replace_senders", ActorScope: fmt.Sprintf("admin:%d", command.Actor), KeyDigest: sha256.Sum256([]byte(command.IdempotencyKey)), PayloadDigest: sha256.Sum256(payload), CreatedAt: now})
		if e != nil {
			return e
		}
		if !owned {
			return json.Unmarshal(receipt.ResultSnapshot, &output)
		}
		pkg, e := s.store.LockPackage(tx, command.PackageID)
		if e != nil {
			return e
		}
		if pkg.Version != command.ExpectedPackageVersion || pkg.Lifecycle != segmentdomain.Paused {
			return ErrConflict
		}
		output, e = s.store.CreateSenderSet(tx, segmentdomain.SenderSet{PackageID: command.PackageID, Members: members, CreatedBy: command.Actor, CreatedAt: now})
		if e != nil {
			return e
		}
		if _, e = s.store.SetCurrentSenderSet(tx, command.PackageID, output.ID, command.ExpectedPackageVersion, command.Actor, now); e != nil {
			return e
		}
		result, _ := json.Marshal(output)
		if _, e = s.store.AppendMutationFacts(tx, fact("sender_set", output.ID, "replace", "audience.senders.replaced.v1", command.Actor, command.IdempotencyKey, now)); e != nil {
			return e
		}
		_, e = s.store.Complete(tx, receipt.ID, result, now)
		return e
	})
	return output, classify(err)
}
func (s *ExecutionService) CurrentSenderSet(ctx context.Context, packageID int64) (segmentdomain.SenderSet, error) {
	var out segmentdomain.SenderSet
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		out, e = s.store.CurrentSenderSet(tx, packageID)
		return e
	})
	return out, classify(err)
}
func (s *ExecutionService) Precheck(ctx context.Context, packageID int64) (Precheck, error) {
	if s == nil || packageID < 1 {
		return Precheck{}, ErrInvalid
	}
	var pkg segmentdomain.Package
	var config segmentdomain.ConfigurationVersion
	var binding segmentdomain.AutomationBinding
	var senders segmentdomain.SenderSet
	var snapshot segmentport.Snapshot
	var snapshotFound bool
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		pkg, e = s.store.LockPackage(tx, packageID)
		if e != nil {
			return e
		}
		config, e = s.store.CurrentConfiguration(tx, packageID)
		if e != nil {
			return e
		}
		binding, e = s.store.CurrentBinding(tx, packageID)
		if e != nil {
			return e
		}
		senders, e = s.store.CurrentSenderSet(tx, packageID)
		if e != nil {
			return e
		}
		snapshot, snapshotFound, e = s.store.PublishedSnapshot(tx, segmentport.PackageID(packageID))
		return e
	})
	if err != nil {
		return Precheck{}, classify(err)
	}
	result := Precheck{ConfigurationVersionID: config.ID, BindingVersion: binding.Version, SenderSetVersion: senders.Version}
	if snapshotFound {
		result.SnapshotID = snapshot.ID
	} else {
		result.Reasons = append(result.Reasons, "published_snapshot_missing")
	}
	agent, found, readErr := s.agents.PublishedAgent(ctx, binding.AgentID)
	if readErr != nil || !found {
		result.Reasons = append(result.Reasons, "published_content_missing")
	} else {
		if agent.AutomationType != automationport.AutomationTypeFixedScript {
			result.Reasons = append(result.Reasons, "agent_execution_not_supported")
		}
		if agent.Status != automationport.AgentStatusActive {
			result.Reasons = append(result.Reasons, "content_not_active")
		}
		if agent.PublishedVersion != binding.AgentPublishedVersion || agent.ContentDigest != binding.ContentDigest || agent.MaterialsDigest != binding.MaterialsDigest {
			result.Reasons = append(result.Reasons, "content_version_drift")
		}
	}
	for _, sender := range senders.Members {
		current, found, e := s.staff.AutomationSender(ctx, sender.StaffID)
		if e != nil || !found || !current.Active || !current.Eligible {
			result.Reasons = append(result.Reasons, "sender_ineligible")
			break
		}
		if current.EligibilityVersion != sender.EligibilityVersion {
			result.Reasons = append(result.Reasons, "sender_version_drift")
			break
		}
	}
	if pkg.Lifecycle == segmentdomain.Archived {
		result.Reasons = append(result.Reasons, "package_archived")
	}
	if !s.providerReady {
		result.Reasons = append(result.Reasons, "provider_disabled")
	}
	result.Ready = len(result.Reasons) == 0
	return result, nil
}
func validExecutionMutation(packageID, expected, actor int64, key string) bool {
	return packageID > 0 && expected > 0 && actor > 0 && len(key) >= 16 && len(key) <= 128 && strings.TrimSpace(key) == key
}

type RuntimeFacade struct {
	*Service
	*SnapshotService
	Execution *ExecutionService
}

func NewRuntimeFacade(configuration *Service, snapshot *SnapshotService, execution *ExecutionService) *RuntimeFacade {
	if configuration != nil {
		configuration.allowActivation = true
	}
	return &RuntimeFacade{configuration, snapshot, execution}
}
func (f *RuntimeFacade) PutBinding(ctx context.Context, c BindingCommand) (segmentdomain.AutomationBinding, error) {
	return f.Execution.PutBinding(ctx, c)
}
func (f *RuntimeFacade) CurrentBinding(ctx context.Context, id int64) (segmentdomain.AutomationBinding, error) {
	return f.Execution.CurrentBinding(ctx, id)
}
func (f *RuntimeFacade) ReplaceSenders(ctx context.Context, c SendersCommand) (segmentdomain.SenderSet, error) {
	return f.Execution.ReplaceSenders(ctx, c)
}
func (f *RuntimeFacade) CurrentSenderSet(ctx context.Context, id int64) (segmentdomain.SenderSet, error) {
	return f.Execution.CurrentSenderSet(ctx, id)
}
func (f *RuntimeFacade) Precheck(ctx context.Context, id int64) (Precheck, error) {
	return f.Execution.Precheck(ctx, id)
}
func (f *RuntimeFacade) TransitionPackage(ctx context.Context, c VersionCommand, target segmentdomain.Lifecycle) (segmentdomain.Package, error) {
	if target == segmentdomain.Active {
		check, err := f.Execution.Precheck(ctx, c.ID)
		if err != nil {
			return segmentdomain.Package{}, err
		}
		if !check.Ready {
			return segmentdomain.Package{}, ErrNotReady
		}
	}
	return f.Service.TransitionPackage(ctx, c, target)
}

func (s *ExecutionService) AudienceExecutionConfiguration(ctx context.Context, packageID segmentport.PackageID) (segmentport.ExecutionConfiguration, error) {
	check, err := s.Precheck(ctx, int64(packageID))
	if err != nil {
		return segmentport.ExecutionConfiguration{}, err
	}
	var pkg segmentdomain.Package
	var binding segmentdomain.AutomationBinding
	var senders segmentdomain.SenderSet
	var snapshot segmentport.Snapshot
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		pkg, e = s.store.LockPackage(tx, int64(packageID))
		if e != nil {
			return e
		}
		binding, e = s.store.CurrentBinding(tx, int64(packageID))
		if e != nil {
			return e
		}
		senders, e = s.store.CurrentSenderSet(tx, int64(packageID))
		if e != nil {
			return e
		}
		snapshot, _, e = s.store.PublishedSnapshot(tx, packageID)
		return e
	})
	if err != nil {
		return segmentport.ExecutionConfiguration{}, classify(err)
	}
	staffIDs := make([]int64, len(senders.Members))
	for i, item := range senders.Members {
		staffIDs[i] = int64(item.StaffID)
	}
	return segmentport.ExecutionConfiguration{PackageID: packageID, PackageVersion: pkg.Version, ConfigurationVersionID: segmentport.ConfigurationVersionID(check.ConfigurationVersionID), Snapshot: snapshot, AgentID: int64(binding.AgentID), AgentPublishedVersion: binding.AgentPublishedVersion, ContentDigest: binding.ContentDigest, MaterialsDigest: binding.MaterialsDigest, BindingVersion: binding.Version, SenderSetVersion: senders.Version, SenderStaffIDs: staffIDs, Ready: check.Ready, Reasons: append([]string(nil), check.Reasons...)}, nil
}

var _ segmentport.ExecutionConfigurationReader = (*ExecutionService)(nil)
