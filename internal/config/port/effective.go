package port

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// RuntimeSettingKey is a closed business-runtime setting catalog. Deployment,
// identity and secret inputs are deliberately absent.
type RuntimeSettingKey string

const AutomationOperationsMaxRecipientsPerRun RuntimeSettingKey = "automation.operations.max_recipients_per_run"

var (
	ErrRuntimeReleaseNotFound = errors.New("runtime configuration release not found")
	ErrRuntimeReleaseConflict = errors.New("runtime configuration release conflict")
	ErrRuntimeReleaseInvalid  = errors.New("invalid runtime configuration release")
)

type RuntimeSetting struct {
	Key   RuntimeSettingKey `json:"key"`
	Value json.RawMessage   `json:"value"`
}

type RuntimeSource string

const (
	RuntimeSourceEnvironmentDefault RuntimeSource = "environment_default"
	RuntimeSourcePublished          RuntimeSource = "published"
)

// EffectiveSnapshot is immutable once returned. Revision 0 identifies the
// process environment default; positive revisions are immutable Config releases.
type EffectiveSnapshot struct {
	Revision                int64         `json:"revision"`
	Source                  RuntimeSource `json:"source"`
	AutomationMaxRecipients int           `json:"automation_max_recipients_per_run"`
	PublishedAt             *time.Time    `json:"published_at,omitempty"`
}

type EffectiveReader interface {
	// EffectiveSnapshot starts a short Config-owned read transaction for a
	// request boundary.
	EffectiveSnapshot(context.Context) (EffectiveSnapshot, error)
	// EffectiveSnapshotWithin uses the caller's already-open PostgreSQL UoW so
	// a consumer can freeze its business record and usage fact atomically.
	EffectiveSnapshotWithin(context.Context) (EffectiveSnapshot, error)
}

type RuntimeUsage struct {
	Snapshot    EffectiveSnapshot `json:"snapshot"`
	Consumer    string            `json:"consumer"`
	Role        string            `json:"role"`
	Operation   string            `json:"operation"`
	SubjectKind string            `json:"subject_kind"`
	SubjectID   int64             `json:"subject_id"`
	UsedAt      time.Time         `json:"used_at"`
}

// UsageRecorder records only an actual business boundary that consumed an
// immutable snapshot. It must participate in the caller's PostgreSQL UoW.
type UsageRecorder interface {
	RecordRuntimeUsage(context.Context, RuntimeUsage) error
}

type RuntimeReleaseReceipt struct {
	ID            int64
	PayloadDigest []byte
	ReleaseID     int64
	State         string
}

type RuntimeReleaseState string

const (
	RuntimeReleaseDraft            RuntimeReleaseState = "draft"
	RuntimeReleaseValidated        RuntimeReleaseState = "validated"
	RuntimeReleaseValidationFailed RuntimeReleaseState = "validation_failed"
	RuntimeReleasePublished        RuntimeReleaseState = "published"
	RuntimeReleaseSuperseded       RuntimeReleaseState = "superseded"
)

type RuntimeValidationIssue struct {
	Key   RuntimeSettingKey `json:"key"`
	Error string            `json:"error"`
}

type RuntimeRelease struct {
	ID                  int64                    `json:"id"`
	State               RuntimeReleaseState      `json:"state"`
	BaseRevision        int64                    `json:"base_revision"`
	RollbackOfReleaseID *int64                   `json:"rollback_of_release_id,omitempty"`
	Settings            []RuntimeSetting         `json:"settings"`
	Checksum            string                   `json:"checksum"`
	ValidationErrors    []RuntimeValidationIssue `json:"validation_errors"`
	CreatedBy           string                   `json:"created_by"`
	CreatedAt           time.Time                `json:"created_at"`
	ValidatedAt         *time.Time               `json:"validated_at,omitempty"`
	PublishedBy         string                   `json:"published_by,omitempty"`
	PublishedAt         *time.Time               `json:"published_at,omitempty"`
}

type RuntimeReleaseDraftCommand struct {
	ExpectedBaseRevision int64
	Settings             []RuntimeSetting
	Actor                string
	IdempotencyKey       string
}
type RuntimeReleaseMutationCommand struct {
	ReleaseID      int64
	Actor          string
	IdempotencyKey string
}
type RuntimeReleasePublishCommand struct {
	ReleaseID            int64
	ExpectedBaseRevision int64
	ExpectedChecksum     string
	Actor                string
	IdempotencyKey       string
}
type RuntimeReleaseRollbackCommand struct {
	ReleaseID            int64
	ExpectedBaseRevision int64
	Actor                string
	IdempotencyKey       string
}

type RuntimeReleasePage struct {
	ActiveRevision int64             `json:"active_revision"`
	Effective      EffectiveSnapshot `json:"effective"`
	Releases       []RuntimeRelease  `json:"releases"`
}

type RuntimeReleaseApplication interface {
	ListRuntimeReleases(context.Context, int) (RuntimeReleasePage, error)
	RuntimeRelease(context.Context, int64) (RuntimeRelease, error)
	CreateRuntimeReleaseDraft(context.Context, RuntimeReleaseDraftCommand) (RuntimeRelease, error)
	ValidateRuntimeRelease(context.Context, RuntimeReleaseMutationCommand) (RuntimeRelease, error)
	PublishRuntimeRelease(context.Context, RuntimeReleasePublishCommand) (RuntimeRelease, error)
	RollbackRuntimeRelease(context.Context, RuntimeReleaseRollbackCommand) (RuntimeRelease, error)
	ListRuntimeUsage(context.Context, int64, int) ([]RuntimeUsage, error)
}
