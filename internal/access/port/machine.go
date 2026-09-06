package port

import (
	"context"
	"net/netip"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

// MachineRepository is a separate Access-owned seam so existing session/RBAC
// repositories are not widened by machine-auth implementation details.
type MachineRepository interface {
	MachineClientByID(context.Context, string, bool) (domain.MachineClient, error)
	ListMachineClients(context.Context) ([]domain.MachineClient, error)
	CreateMachineClient(context.Context, domain.MachineClient) (domain.MachineClient, error)
	ReplaceMachineClient(context.Context, domain.MachineClient) error
	SetMachineClientLastUsed(context.Context, int64, time.Time) error
	AppendMachineAudit(context.Context, domain.MachineAudit) error
}

// The following DTOs are the stable Access boundary consumed by the machine
// HTTP host. They contain no secret hash or administrator role.
type CreateMachineClientInput struct {
	ClientID        string            `json:"client_id"`
	DisplayName     string            `json:"display_name"`
	Purpose         string            `json:"purpose"`
	Audiences       []string          `json:"audiences"`
	Scopes          []string          `json:"scopes"`
	Capabilities    []string          `json:"capabilities"`
	AllowedCIDRs    []string          `json:"allowed_cidrs"`
	OwnerScope      domain.OwnerScope `json:"owner_scope,omitempty"`
	TokenTTLSeconds int               `json:"token_ttl_seconds"`
	ExpiresAt       *time.Time        `json:"expires_at"`
}

type IssuedMachineClient struct {
	Client MachineClientSummary `json:"client"`
	Secret string               `json:"secret"`
}

type MachineClientSummary struct {
	ClientID        string            `json:"client_id"`
	DisplayName     string            `json:"display_name"`
	Purpose         string            `json:"purpose"`
	CredentialHint  string            `json:"credential_hint"`
	Audiences       []string          `json:"audiences"`
	Scopes          []string          `json:"scopes"`
	Capabilities    []string          `json:"capabilities"`
	AllowedCIDRs    []string          `json:"allowed_cidrs"`
	CorpID          string            `json:"corp_id,omitempty"`
	OwnerScope      domain.OwnerScope `json:"owner_scope,omitempty"`
	TokenTTLSeconds int               `json:"token_ttl_seconds"`
	ExpiresAt       *time.Time        `json:"expires_at,omitempty"`
	Enabled         bool              `json:"enabled"`
	ReissueRequired bool              `json:"reissue_required"`
	AuthVersion     int64             `json:"auth_version"`
	LastUsedAt      *time.Time        `json:"last_used_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
}

// UpdateMachineClientInput is deliberately narrower than creation. Frozen
// API-client templates keep their purpose, audience, scopes and capabilities;
// only a disabled caller's presentation, TTL and CIDR boundary are editable.
type UpdateMachineClientInput struct {
	DisplayName     string   `json:"display_name"`
	TokenTTLSeconds int      `json:"token_ttl_seconds"`
	AllowedCIDRs    []string `json:"allowed_cidrs"`
}

type ClientCredentialsInput struct {
	ClientID        string
	ClientSecret    string
	Audience        string
	RequestedScopes []string
	SourceIP        netip.Addr
}

type IssuedAccessToken struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
}

type MachineTokenIssuer interface {
	IssueClientCredentialsToken(context.Context, ClientCredentialsInput) (IssuedAccessToken, error)
	AuthenticateBearer(context.Context, string, string, netip.Addr) (domain.MachinePrincipal, error)
}

type MachineManagement interface {
	Create(context.Context, domain.Principal, CreateMachineClientInput) (IssuedMachineClient, error)
	List(context.Context, domain.Principal) ([]MachineClientSummary, error)
	Rotate(context.Context, domain.Principal, string) (IssuedMachineClient, error)
	Update(context.Context, domain.Principal, string, UpdateMachineClientInput) (MachineClientSummary, error)
	Activate(context.Context, domain.Principal, string, string, bool) (MachineClientSummary, error)
	SetEnabled(context.Context, domain.Principal, string, bool) (MachineClientSummary, error)
}

// HistoricalMachineImportInput contains only non-secret source facts. A
// migration never accepts old secret hashes, keys, or access tokens.
type HistoricalMachineImportInput struct {
	ImportRunID       string
	SourceRowID       string
	SourceRowDigest   [32]byte
	ClientID          string
	PrincipalID       string
	PrincipalType     string
	DisplayName       string
	Purpose           string
	Audiences         []string
	Scopes            []string
	Capabilities      []string
	AllowedCIDRs      []string
	CorpID            string
	OwnerScope        domain.OwnerScope
	SourceEnabled     bool
	SourceAuthVersion int64
	TokenTTLSeconds   int
	ExpiresAt         *time.Time
}

type HistoricalMachineImportResult struct {
	Client     MachineClientSummary
	Outcome    string
	ReasonCode string
	Replayed   bool
}

// HistoricalMachineAuditInput carries a redacted historical audit fact. The
// source before/after payloads never leave the protected source snapshot: the
// target stores their digests together with the original action, target and
// occurrence time so import audit is never confused with a legacy action.
type HistoricalMachineAuditInput struct {
	ImportRunID     string
	SourceAuditID   int64
	SourceRowDigest [32]byte
	Operator        string
	Action          string
	TargetType      string
	TargetID        string
	BeforeDigest    [32]byte
	AfterDigest     [32]byte
	OccurredAt      time.Time
}

type HistoricalMachineAuditResult struct {
	Outcome  string
	Replayed bool
}

// HistoricalMachineImportBatch seals one protected source snapshot. A source
// revision may have only one digest, which prevents overlap or source drift
// from being treated as a second historical import.
type HistoricalMachineImportBatch struct {
	ImportRunID    string
	SourceSystem   string
	SourceRevision string
	ManifestDigest [32]byte
	SnapshotAt     time.Time
	ClientCount    int
	AuditCount     int
}

type HistoricalMachineImportBatchResult struct{ Replayed bool }

// MachineHistoricalImporter is for an explicit offline migration command. It
// has no secret-returning method and always records disabled/reissue-required
// credentials.
type MachineHistoricalImporter interface {
	ImportHistorical(context.Context, HistoricalMachineImportInput) (HistoricalMachineImportResult, error)
}

// MachineHistoricalRepository is the Access-owned persistence seam for
// idempotent source-row receipts. The command never writes these tables.
type MachineHistoricalRepository interface {
	ImportHistoricalMachineClient(context.Context, HistoricalMachineImportInput, domain.MachineClient) (domain.MachineClient, bool, error)
	RecordHistoricalMachineExclusion(context.Context, HistoricalMachineImportInput, string) (bool, error)
}

// MachineHistoricalVerificationRepository reads an already-written receipt
// without making an import side effect.
type MachineHistoricalVerificationRepository interface {
	VerifyHistoricalMachineClient(context.Context, HistoricalMachineImportInput) (domain.MachineClient, string, string, error)
}

// MachineHistoricalAuditRepository owns immutable legacy-audit mappings. It
// has no write path for a live credential or a provider effect.
type MachineHistoricalAuditRepository interface {
	ImportHistoricalMachineAudit(context.Context, HistoricalMachineAuditInput) (bool, error)
	VerifyHistoricalMachineAudit(context.Context, HistoricalMachineAuditInput) error
}

type MachineHistoricalAuditImporter interface {
	ImportHistoricalAudit(context.Context, HistoricalMachineAuditInput) (HistoricalMachineAuditResult, error)
	VerifyHistoricalAudit(context.Context, HistoricalMachineAuditInput) error
}

type MachineHistoricalBatchRepository interface {
	BeginHistoricalMachineImport(context.Context, HistoricalMachineImportBatch) (bool, error)
	VerifyHistoricalMachineImport(context.Context, HistoricalMachineImportBatch) error
}

type MachineHistoricalBatcher interface {
	BeginHistoricalImport(context.Context, HistoricalMachineImportBatch) (HistoricalMachineImportBatchResult, error)
	VerifyHistoricalImport(context.Context, HistoricalMachineImportBatch) error
}
