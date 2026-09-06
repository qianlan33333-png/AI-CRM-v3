package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	config "github.com/qianlan33333-png/AI-CRM-v3/internal/config"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var (
	ErrRuntimeReleaseNotFound = configport.ErrRuntimeReleaseNotFound
	ErrRuntimeReleaseConflict = configport.ErrRuntimeReleaseConflict
	ErrRuntimeReleaseInvalid  = configport.ErrRuntimeReleaseInvalid
)

type runtimeReleaseRepository interface {
	ActiveRuntimeRevision(context.Context, bool) (int64, error)
	ActiveRuntimeRelease(context.Context) (configport.RuntimeRelease, bool, error)
	GetRuntimeRelease(context.Context, int64, bool) (configport.RuntimeRelease, error)
	ListRuntimeReleases(context.Context, int) ([]configport.RuntimeRelease, error)
	InsertRuntimeRelease(context.Context, configport.RuntimeRelease) (configport.RuntimeRelease, error)
	SetRuntimeReleaseValidation(context.Context, int64, configport.RuntimeReleaseState, []configport.RuntimeValidationIssue, time.Time) (configport.RuntimeRelease, error)
	PublishRuntimeRelease(context.Context, int64, int64, string, time.Time) (configport.RuntimeRelease, error)
	SetActiveRuntimeRelease(context.Context, int64, time.Time) error
	AppendRuntimeReleaseAudit(context.Context, int64, string, string, string, time.Time) error
	ReserveRuntimeReleaseCommand(context.Context, string, string, string, []byte, time.Time) (configport.RuntimeReleaseReceipt, bool, error)
	CompleteRuntimeReleaseCommand(context.Context, int64, int64, time.Time) error
	InsertRuntimeUsage(context.Context, configport.RuntimeUsage) error
	ListRuntimeUsage(context.Context, int64, int) ([]configport.RuntimeUsage, error)
}

// RuntimeReleaseService owns Config's draft/validation/publish/rollback state.
// Runtime values are immutable versions; changing active is a pointer update,
// never an environment-file or Provider operation.
type RuntimeReleaseService struct {
	uow          platformport.UnitOfWork
	repo         runtimeReleaseRepository
	events       configport.EventAppender
	defaultLimit int
	now          func() time.Time
}

func NewRuntimeReleaseService(uow platformport.UnitOfWork, repo runtimeReleaseRepository, events configport.EventAppender, defaultLimit int) (*RuntimeReleaseService, error) {
	if uow == nil || repo == nil || events == nil || defaultLimit < 1 || defaultLimit > 5000 {
		return nil, ErrRuntimeReleaseInvalid
	}
	return &RuntimeReleaseService{uow: uow, repo: repo, events: events, defaultLimit: defaultLimit, now: time.Now}, nil
}

func (s *RuntimeReleaseService) EffectiveSnapshot(ctx context.Context) (out configport.EffectiveSnapshot, err error) {
	if !s.ready() {
		return out, ErrRuntimeReleaseInvalid
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		out, e = s.effectiveFromActive(tx)
		return e
	})
	if err != nil {
		return configport.EffectiveSnapshot{}, classifyRuntimeRelease(err)
	}
	return out, nil
}

func (s *RuntimeReleaseService) effectiveFromActive(ctx context.Context) (configport.EffectiveSnapshot, error) {
	// Read the active pointer and its immutable release in one statement. A
	// concurrent publish may supersede the previous release immediately after
	// this read, but the returned revision remains a valid frozen snapshot.
	release, found, err := s.repo.ActiveRuntimeRelease(ctx)
	if err != nil {
		return configport.EffectiveSnapshot{}, err
	}
	if !found {
		return configport.EffectiveSnapshot{Revision: 0, Source: configport.RuntimeSourceEnvironmentDefault, AutomationMaxRecipients: s.defaultLimit}, nil
	}
	limit, err := runtimeLimit(release.Settings)
	if err != nil || release.State != configport.RuntimeReleasePublished {
		return configport.EffectiveSnapshot{}, ErrRuntimeReleaseConflict
	}
	at := release.PublishedAt.UTC()
	return configport.EffectiveSnapshot{Revision: release.ID, Source: configport.RuntimeSourcePublished, AutomationMaxRecipients: limit, PublishedAt: &at}, nil
}

// EffectiveSnapshotWithin reads through the caller's existing UoW. This keeps
// a consumer's frozen business record and Config usage fact atomic without
// exposing a database type through the stable Config Port.
func (s *RuntimeReleaseService) EffectiveSnapshotWithin(ctx context.Context) (configport.EffectiveSnapshot, error) {
	if !s.ready() {
		return configport.EffectiveSnapshot{}, ErrRuntimeReleaseInvalid
	}
	snapshot, err := s.effectiveFromActive(ctx)
	if err != nil {
		return configport.EffectiveSnapshot{}, classifyRuntimeRelease(err)
	}
	return snapshot, nil
}

func (s *RuntimeReleaseService) RecordRuntimeUsage(ctx context.Context, use configport.RuntimeUsage) error {
	if !s.ready() || !validUsage(use) {
		return ErrRuntimeReleaseInvalid
	}
	// The repository requires the caller's UoW transaction. Callers record a
	// usage only after the corresponding business record exists in that UoW.
	if err := s.repo.InsertRuntimeUsage(ctx, use); err != nil {
		return classifyRuntimeRelease(err)
	}
	return nil
}

func (s *RuntimeReleaseService) ListRuntimeReleases(ctx context.Context, limit int) (out configport.RuntimeReleasePage, err error) {
	if !s.ready() || limit < 1 || limit > 100 {
		return out, ErrRuntimeReleaseInvalid
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		// Read the active pointer together with its immutable values. A separate
		// pointer lookup followed by GetRuntimeRelease could see a supersede in
		// between under READ COMMITTED and turn an otherwise valid page into a
		// false conflict.
		out.Effective, e = s.effectiveFromActive(tx)
		if e != nil {
			return e
		}
		out.ActiveRevision = out.Effective.Revision
		out.Releases, e = s.repo.ListRuntimeReleases(tx, limit)
		return e
	})
	if err != nil {
		return configport.RuntimeReleasePage{}, classifyRuntimeRelease(err)
	}
	return out, nil
}

func (s *RuntimeReleaseService) RuntimeRelease(ctx context.Context, id int64) (out configport.RuntimeRelease, err error) {
	if !s.ready() || id < 1 {
		return out, ErrRuntimeReleaseInvalid
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		out, e = s.repo.GetRuntimeRelease(tx, id, false)
		return e
	})
	return out, classifyRuntimeRelease(err)
}

func (s *RuntimeReleaseService) CreateRuntimeReleaseDraft(ctx context.Context, command configport.RuntimeReleaseDraftCommand) (out configport.RuntimeRelease, err error) {
	if !s.ready() || !validReleaseActor(command.Actor) || !validRuntimeKey(command.IdempotencyKey) || command.ExpectedBaseRevision < 0 {
		return out, ErrRuntimeReleaseInvalid
	}
	settings, issues := config.ValidateRuntimeSettings(command.Settings)
	if len(issues) != 0 {
		return out, ErrRuntimeReleaseInvalid
	}
	payload := releasePayload("create", command.ExpectedBaseRevision, 0, settings)
	digest := sha256.Sum256(payload)
	now := s.now().UTC()
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := s.repo.ReserveRuntimeReleaseCommand(tx, "runtime_release.create", command.Actor, command.IdempotencyKey, digest[:], now)
		if e != nil {
			return e
		}
		if !owned {
			return s.replayRuntimeReceipt(tx, receipt, digest[:], &out)
		}
		active, e := s.repo.ActiveRuntimeRevision(tx, true)
		if e != nil {
			return e
		}
		if active != command.ExpectedBaseRevision {
			return ErrRuntimeReleaseConflict
		}
		out = configport.RuntimeRelease{State: configport.RuntimeReleaseDraft, BaseRevision: active, Settings: settings, Checksum: releaseChecksum(settings), CreatedBy: command.Actor, CreatedAt: now}
		out, e = s.repo.InsertRuntimeRelease(tx, out)
		if e != nil {
			return e
		}
		if e = s.repo.AppendRuntimeReleaseAudit(tx, out.ID, "created", command.Actor, command.IdempotencyKey, now); e != nil {
			return e
		}
		return s.repo.CompleteRuntimeReleaseCommand(tx, receipt.ID, out.ID, now)
	})
	return out, classifyRuntimeRelease(err)
}

func (s *RuntimeReleaseService) ValidateRuntimeRelease(ctx context.Context, command configport.RuntimeReleaseMutationCommand) (out configport.RuntimeRelease, err error) {
	if !s.ready() || command.ReleaseID < 1 || !validReleaseActor(command.Actor) || !validRuntimeKey(command.IdempotencyKey) {
		return out, ErrRuntimeReleaseInvalid
	}
	payload := releasePayload("validate", 0, command.ReleaseID, nil)
	digest := sha256.Sum256(payload)
	now := s.now().UTC()
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := s.repo.ReserveRuntimeReleaseCommand(tx, "runtime_release.validate", command.Actor, command.IdempotencyKey, digest[:], now)
		if e != nil {
			return e
		}
		if !owned {
			return s.replayRuntimeReceipt(tx, receipt, digest[:], &out)
		}
		current, e := s.repo.GetRuntimeRelease(tx, command.ReleaseID, true)
		if e != nil {
			return e
		}
		if current.State != configport.RuntimeReleaseDraft && current.State != configport.RuntimeReleaseValidationFailed && current.State != configport.RuntimeReleaseValidated {
			return ErrRuntimeReleaseConflict
		}
		_, issues := config.ValidateRuntimeSettings(current.Settings)
		state, action := configport.RuntimeReleaseValidated, "validated"
		if len(issues) != 0 {
			state, action = configport.RuntimeReleaseValidationFailed, "validation_failed"
		}
		out, e = s.repo.SetRuntimeReleaseValidation(tx, current.ID, state, issues, now)
		if e != nil {
			return e
		}
		if e = s.repo.AppendRuntimeReleaseAudit(tx, out.ID, action, command.Actor, command.IdempotencyKey, now); e != nil {
			return e
		}
		return s.repo.CompleteRuntimeReleaseCommand(tx, receipt.ID, out.ID, now)
	})
	return out, classifyRuntimeRelease(err)
}

func (s *RuntimeReleaseService) PublishRuntimeRelease(ctx context.Context, command configport.RuntimeReleasePublishCommand) (out configport.RuntimeRelease, err error) {
	if !s.ready() || command.ReleaseID < 1 || command.ExpectedBaseRevision < 0 || !validReleaseActor(command.Actor) || !validRuntimeKey(command.IdempotencyKey) || !validChecksum(command.ExpectedChecksum) {
		return out, ErrRuntimeReleaseInvalid
	}
	payload := releasePayload("publish", command.ExpectedBaseRevision, command.ReleaseID, nil)
	payload = append(payload, []byte(command.ExpectedChecksum)...)
	digest := sha256.Sum256(payload)
	now := s.now().UTC()
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := s.repo.ReserveRuntimeReleaseCommand(tx, "runtime_release.publish", command.Actor, command.IdempotencyKey, digest[:], now)
		if e != nil {
			return e
		}
		if !owned {
			return s.replayRuntimeReceipt(tx, receipt, digest[:], &out)
		}
		active, e := s.repo.ActiveRuntimeRevision(tx, true)
		if e != nil {
			return e
		}
		current, e := s.repo.GetRuntimeRelease(tx, command.ReleaseID, true)
		if e != nil {
			return e
		}
		if active != command.ExpectedBaseRevision || current.BaseRevision != active || current.State != configport.RuntimeReleaseValidated || current.Checksum != command.ExpectedChecksum {
			return ErrRuntimeReleaseConflict
		}
		if _, issues := config.ValidateRuntimeSettings(current.Settings); len(issues) != 0 {
			return ErrRuntimeReleaseConflict
		}
		out, e = s.repo.PublishRuntimeRelease(tx, current.ID, active, command.Actor, now)
		if e != nil {
			return e
		}
		if e = s.repo.SetActiveRuntimeRelease(tx, out.ID, now); e != nil {
			return e
		}
		if e = s.repo.AppendRuntimeReleaseAudit(tx, out.ID, "published", command.Actor, command.IdempotencyKey, now); e != nil {
			return e
		}
		payload, e := json.Marshal(struct {
			ReleaseID  int64  `json:"release_id"`
			Revision   int64  `json:"revision"`
			RollbackOf *int64 `json:"rollback_of_release_id,omitempty"`
		}{out.ID, out.ID, out.RollbackOfReleaseID})
		if e != nil {
			return e
		}
		if _, e = s.events.Append(tx, configport.Event{Type: "runtime_release.published", Payload: payload, OccurredAt: now, IdempotencyKey: fmt.Sprintf("runtime_release.published:release:%d", out.ID)}); e != nil {
			return e
		}
		return s.repo.CompleteRuntimeReleaseCommand(tx, receipt.ID, out.ID, now)
	})
	return out, classifyRuntimeRelease(err)
}

func (s *RuntimeReleaseService) RollbackRuntimeRelease(ctx context.Context, command configport.RuntimeReleaseRollbackCommand) (out configport.RuntimeRelease, err error) {
	if !s.ready() || command.ReleaseID < 1 || command.ExpectedBaseRevision < 0 || !validReleaseActor(command.Actor) || !validRuntimeKey(command.IdempotencyKey) {
		return out, ErrRuntimeReleaseInvalid
	}
	payload := releasePayload("rollback", command.ExpectedBaseRevision, command.ReleaseID, nil)
	digest := sha256.Sum256(payload)
	now := s.now().UTC()
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, e := s.repo.ReserveRuntimeReleaseCommand(tx, "runtime_release.rollback", command.Actor, command.IdempotencyKey, digest[:], now)
		if e != nil {
			return e
		}
		if !owned {
			return s.replayRuntimeReceipt(tx, receipt, digest[:], &out)
		}
		active, e := s.repo.ActiveRuntimeRevision(tx, true)
		if e != nil {
			return e
		}
		if active != command.ExpectedBaseRevision {
			return ErrRuntimeReleaseConflict
		}
		target, e := s.repo.GetRuntimeRelease(tx, command.ReleaseID, false)
		if e != nil {
			return e
		}
		if target.State != configport.RuntimeReleasePublished && target.State != configport.RuntimeReleaseSuperseded {
			return ErrRuntimeReleaseConflict
		}
		settings, issues := config.ValidateRuntimeSettings(target.Settings)
		if len(issues) != 0 {
			return ErrRuntimeReleaseConflict
		}
		rollbackOf := target.ID
		candidate := configport.RuntimeRelease{State: configport.RuntimeReleaseValidated, BaseRevision: active, RollbackOfReleaseID: &rollbackOf, Settings: settings, Checksum: releaseChecksum(settings), CreatedBy: command.Actor, CreatedAt: now, ValidatedAt: &now}
		candidate, e = s.repo.InsertRuntimeRelease(tx, candidate)
		if e != nil {
			return e
		}
		out, e = s.repo.PublishRuntimeRelease(tx, candidate.ID, active, command.Actor, now)
		if e != nil {
			return e
		}
		if e = s.repo.SetActiveRuntimeRelease(tx, out.ID, now); e != nil {
			return e
		}
		if e = s.repo.AppendRuntimeReleaseAudit(tx, candidate.ID, "rolled_back", command.Actor, command.IdempotencyKey, now); e != nil {
			return e
		}
		payload, e := json.Marshal(struct {
			ReleaseID  int64 `json:"release_id"`
			Revision   int64 `json:"revision"`
			RollbackOf int64 `json:"rollback_of_release_id"`
		}{out.ID, out.ID, target.ID})
		if e != nil {
			return e
		}
		if _, e = s.events.Append(tx, configport.Event{Type: "runtime_release.rolled_back", Payload: payload, OccurredAt: now, IdempotencyKey: fmt.Sprintf("runtime_release.rolled_back:release:%d", out.ID)}); e != nil {
			return e
		}
		return s.repo.CompleteRuntimeReleaseCommand(tx, receipt.ID, out.ID, now)
	})
	return out, classifyRuntimeRelease(err)
}

func (s *RuntimeReleaseService) ListRuntimeUsage(ctx context.Context, revision int64, limit int) (out []configport.RuntimeUsage, err error) {
	if !s.ready() || revision < 0 || limit < 1 || limit > 100 {
		return nil, ErrRuntimeReleaseInvalid
	}
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var e error
		out, e = s.repo.ListRuntimeUsage(tx, revision, limit)
		return e
	})
	return out, classifyRuntimeRelease(err)
}

func (s *RuntimeReleaseService) effectiveWithin(ctx context.Context, revision int64) (configport.EffectiveSnapshot, error) {
	if revision == 0 {
		return configport.EffectiveSnapshot{Revision: 0, Source: configport.RuntimeSourceEnvironmentDefault, AutomationMaxRecipients: s.defaultLimit}, nil
	}
	release, err := s.repo.GetRuntimeRelease(ctx, revision, false)
	if err != nil || release.State != configport.RuntimeReleasePublished {
		return configport.EffectiveSnapshot{}, ErrRuntimeReleaseConflict
	}
	limit, err := runtimeLimit(release.Settings)
	if err != nil {
		return configport.EffectiveSnapshot{}, ErrRuntimeReleaseConflict
	}
	at := release.PublishedAt.UTC()
	return configport.EffectiveSnapshot{Revision: release.ID, Source: configport.RuntimeSourcePublished, AutomationMaxRecipients: limit, PublishedAt: &at}, nil
}

func (s *RuntimeReleaseService) replayRuntimeReceipt(ctx context.Context, receipt configport.RuntimeReleaseReceipt, digest []byte, out *configport.RuntimeRelease) error {
	if !bytes.Equal(receipt.PayloadDigest, digest) || receipt.State != "completed" || receipt.ReleaseID < 1 {
		return ErrRuntimeReleaseConflict
	}
	value, err := s.repo.GetRuntimeRelease(ctx, receipt.ReleaseID, false)
	if err != nil {
		return err
	}
	*out = value
	return nil
}

func runtimeLimit(settings []configport.RuntimeSetting) (int, error) {
	canonical, issues := config.ValidateRuntimeSettings(settings)
	if len(issues) != 0 || len(canonical) != 1 {
		return 0, ErrRuntimeReleaseInvalid
	}
	var value int
	if err := json.Unmarshal(canonical[0].Value, &value); err != nil || value < 1 || value > 5000 {
		return 0, ErrRuntimeReleaseInvalid
	}
	return value, nil
}
func releaseChecksum(settings []configport.RuntimeSetting) string {
	copySettings := append([]configport.RuntimeSetting(nil), settings...)
	sort.Slice(copySettings, func(i, j int) bool { return copySettings[i].Key < copySettings[j].Key })
	payload, _ := json.Marshal(copySettings)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
func releasePayload(action string, base, id int64, settings []configport.RuntimeSetting) []byte {
	payload, _ := json.Marshal(struct {
		Action   string `json:"action"`
		Base, ID int64
		Settings []configport.RuntimeSetting `json:"settings,omitempty"`
	}{action, base, id, settings})
	return payload
}
func validReleaseActor(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= 200
}
func validRuntimeKey(value string) bool {
	return len(value) >= 8 && len(value) <= 200 && strings.TrimSpace(value) == value
}
func validChecksum(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size && hex.EncodeToString(raw) == value
}
func validUsage(use configport.RuntimeUsage) bool {
	return (use.Snapshot.Source == configport.RuntimeSourceEnvironmentDefault || use.Snapshot.Source == configport.RuntimeSourcePublished) && use.Snapshot.Revision >= 0 && use.Snapshot.AutomationMaxRecipients >= 1 && use.Snapshot.AutomationMaxRecipients <= 5000 && use.Consumer == string(configport.AutomationOperationsMaxRecipientsPerRun) && (use.Role == "api" || use.Role == "worker" || use.Role == "effects-worker") && (use.Operation == "preview" || use.Operation == "confirm" || use.Operation == "execution") && (use.SubjectKind == "automation_preview" || use.SubjectKind == "automation_run") && use.SubjectID > 0 && !use.UsedAt.IsZero()
}
func (s *RuntimeReleaseService) ready() bool {
	return s != nil && s.uow != nil && s.repo != nil && s.events != nil && s.defaultLimit >= 1 && s.defaultLimit <= 5000 && s.now != nil
}
func classifyRuntimeRelease(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrRuntimeReleaseInvalid) || errors.Is(err, ErrRuntimeReleaseConflict) || errors.Is(err, ErrRuntimeReleaseNotFound) {
		return err
	}
	return err
}

var _ configport.EffectiveReader = (*RuntimeReleaseService)(nil)
var _ configport.UsageRecorder = (*RuntimeReleaseService)(nil)
var _ configport.RuntimeReleaseApplication = (*RuntimeReleaseService)(nil)
