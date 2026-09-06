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
	ClientID        string
	DisplayName     string
	Purpose         string
	Audiences       []string
	Scopes          []string
	Capabilities    []string
	AllowedCIDRs    []string
	TokenTTLSeconds int
	ExpiresAt       *time.Time
}

type IssuedMachineClient struct {
	Client MachineClientSummary `json:"client"`
	Secret string               `json:"secret"`
}

type MachineClientSummary struct {
	ClientID        string     `json:"client_id"`
	DisplayName     string     `json:"display_name"`
	Purpose         string     `json:"purpose"`
	CredentialHint  string     `json:"credential_hint"`
	Audiences       []string   `json:"audiences"`
	Scopes          []string   `json:"scopes"`
	Capabilities    []string   `json:"capabilities"`
	AllowedCIDRs    []string   `json:"allowed_cidrs"`
	TokenTTLSeconds int        `json:"token_ttl_seconds"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	Enabled         bool       `json:"enabled"`
	ReissueRequired bool       `json:"reissue_required"`
	AuthVersion     int64      `json:"auth_version"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
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
	SetEnabled(context.Context, domain.Principal, string, bool) (MachineClientSummary, error)
}
