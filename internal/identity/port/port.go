// Package port freezes the public Identity boundary.
package port

import (
	"context"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

type ResolveStatus string

const (
	ResolveFound    ResolveStatus = "found"
	ResolveNotFound ResolveStatus = "not_found"
	ResolveConflict ResolveStatus = "conflict"
)

type ResolveResult struct {
	Status     ResolveStatus
	CustomerID customerdomain.CustomerID
	IdentityID int64
}

type Resolver interface {
	Resolve(context.Context, identitydomain.Reference) (ResolveResult, error)
}

// OutboundWeComIdentityReader is consumed only by the composition-owned
// private-message target resolver. It may reveal a verified channel identity
// to Outbound in memory, never to an HTTP response or structured log.
type OutboundWeComIdentityReader interface {
	VerifiedWeComIdentityForCustomer(context.Context, customerdomain.CustomerID, string) (string, bool, error)
}

type ProvisionCommand struct {
	Fact           identitydomain.VerifiedFact
	IdempotencyKey string
}

type ProvisionResult struct {
	CustomerID customerdomain.CustomerID
	IdentityID int64
	Created    bool
}

type VerifiedProvisioner interface {
	ProvisionVerifiedIdentity(context.Context, ProvisionCommand) (ProvisionResult, error)
}

// DeclaredAttachCommand deliberately contains an existing CustomerID and no
// provisioning or merge instruction. SourceRowDigest is the non-PII replay
// fingerprint retained by the one-time import ledger.
type DeclaredAttachCommand struct {
	CustomerID      customerdomain.CustomerID
	Reference       identitydomain.Reference
	ImportRunID     int64
	SourceRowID     string
	SourceRowDigest [32]byte
	IdempotencyKey  string
}

type DeclaredAttachStatus string

const (
	DeclaredAttached      DeclaredAttachStatus = "attached"
	DeclaredAlreadyLinked DeclaredAttachStatus = "already_linked"
	DeclaredConflict      DeclaredAttachStatus = "conflict"
	DeclaredInvalid       DeclaredAttachStatus = "invalid"
	DeclaredReplayed      DeclaredAttachStatus = "replayed"
)

type DeclaredAttachResult struct {
	Status     DeclaredAttachStatus
	ReplayOf   DeclaredAttachStatus
	CustomerID customerdomain.CustomerID
	IdentityID int64
}

// DeclaredIdentityAttacher can only attach a declared identity to a known
// customer. It intentionally exposes neither Provision nor Merge.
type DeclaredIdentityAttacher interface {
	AttachDeclaredIdentity(context.Context, DeclaredAttachCommand) (DeclaredAttachResult, error)
}

type DirectoryIdentitySummary struct {
	Kind      identitydomain.Kind      `json:"kind"`
	Scope     string                   `json:"scope"`
	Assurance identitydomain.Assurance `json:"assurance"`
	Status    string                   `json:"status"`
	Source    string                   `json:"source"`
	CreatedAt time.Time                `json:"created_at"`
}

type MaskedPhone struct {
	Masked    string                   `json:"masked"`
	Assurance identitydomain.Assurance `json:"assurance"`
}

// DirectoryIdentityReader is the read-only Identity boundary used by the
// customer directory. Only RevealPhone may return a raw phone, and callers
// must enforce RBAC, CSRF, no-store and audit before returning it.
type DirectoryIdentityReader interface {
	VerifiedWeComCustomer(context.Context, string, string) (customerdomain.CustomerID, bool, error)
	CustomerForPhone(context.Context, string) (customerdomain.CustomerID, bool, error)
	DirectoryIdentities(context.Context, customerdomain.CustomerID) ([]DirectoryIdentitySummary, []MaskedPhone, error)
	RevealPhone(context.Context, customerdomain.CustomerID) (string, bool, error)
}

// ExternalIdentityValueReader is a narrowly scoped, transaction-bound read
// used by composition to resolve a current Provider relationship. Callers may
// use the returned value only transiently for an authorized Provider call and
// must never persist or log it outside Identity/WeCom ownership.
type ExternalIdentityValueReader interface {
	VerifiedExternalIdentityValue(context.Context, customerdomain.CustomerID, identitydomain.Kind, string) (string, bool, error)
}
