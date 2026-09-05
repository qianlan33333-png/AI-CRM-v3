// Package port is the only cross-domain contract for AI Assistant review plans.
package port

import (
	"context"
	"errors"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

const (
	MaxRecipients        = 5000
	MaxMessagesPerTarget = 20
)

var ErrInvalidCommand = errors.New("invalid AI Assistant command")

type PlanID int64
type RecipientID int64
type ContentVersionID int64

type ActorKind string

const (
	ActorAdmin   ActorKind = "admin"
	ActorService ActorKind = "service"
)

type Actor struct {
	Kind ActorKind
	ID   int64
}

func (a Actor) Valid() bool {
	return a.ID > 0 && (a.Kind == ActorAdmin || a.Kind == ActorService)
}

type ContentKind string

const (
	ContentText        ContentKind = "text"
	ContentImage       ContentKind = "image"
	ContentMiniProgram ContentKind = "mini_program"
	ContentLink        ContentKind = "link"
)

type ContentBlock struct {
	Kind           ContentKind       `json:"kind"`
	Text           string            `json:"text,omitempty"`
	MaterialKind   string            `json:"material_kind,omitempty"`
	MaterialID     int64             `json:"material_id,omitempty"`
	MaterialDigest effectport.Digest `json:"material_digest,omitempty"`
}

func (b ContentBlock) Valid() bool {
	switch b.Kind {
	case ContentText:
		return strings.TrimSpace(b.Text) != "" && len(b.Text) <= 8000 && b.MaterialID == 0 && b.MaterialDigest == ""
	case ContentImage, ContentMiniProgram, ContentLink:
		return b.MaterialID > 0 && b.MaterialKind != "" && effectport.ValidDigest(b.MaterialDigest) && len(b.Text) <= 2000
	default:
		return false
	}
}

// ValidInput accepts a missing material digest at the HTTP edge. The Media
// owner resolves and freezes the authoritative digest inside the plan UoW.
func (b ContentBlock) ValidInput() bool {
	if b.Kind == ContentText {
		return b.Valid()
	}
	provided := b.MaterialDigest
	b.MaterialDigest = effectport.Hash("aiassistant.input-placeholder")
	return (provided == "" || effectport.ValidDigest(provided)) && b.Valid()
}

type RecipientCandidate struct {
	CustomerID customerdomain.CustomerID `json:"customer_id"`
	StaffID    int64                     `json:"staff_id"`
	Content    []ContentBlock            `json:"content"`
}

func (r RecipientCandidate) Valid() bool {
	if r.CustomerID < 1 || r.StaffID < 1 || len(r.Content) == 0 || len(r.Content) > MaxMessagesPerTarget {
		return false
	}
	for _, block := range r.Content {
		if !block.ValidInput() {
			return false
		}
	}
	return true
}

type CreatePlanCommand struct {
	Actor          Actor
	IdempotencyKey string
	Name           string
	SourceKind     string
	SourceDigest   effectport.Digest
	Recipients     []RecipientCandidate
	OccurredAt     time.Time
}

func (c CreatePlanCommand) Valid() bool {
	if !c.Actor.Valid() || len(c.IdempotencyKey) < 8 || len(c.IdempotencyKey) > 200 || strings.TrimSpace(c.Name) == "" || len(c.Name) > 200 || strings.TrimSpace(c.SourceKind) == "" || !effectport.ValidDigest(c.SourceDigest) || len(c.Recipients) == 0 || len(c.Recipients) > MaxRecipients {
		return false
	}
	for _, recipient := range c.Recipients {
		if !recipient.Valid() {
			return false
		}
	}
	return !c.OccurredAt.IsZero()
}

type CreatePlanResult struct {
	Plan     Plan
	Replayed bool
}

type Intake interface {
	CreatePlan(context.Context, CreatePlanCommand) (CreatePlanResult, error)
}

// TransactionalIntake creates a review plan in the caller's already-bound
// PostgreSQL transaction. It exists for the small set of Owners that must
// commit their own durable fact and an AI review plan atomically.
//
// Calling it without a transaction is an explicit failure; it never opens a
// nested Unit of Work.
type TransactionalIntake interface {
	CreatePlanWithin(context.Context, CreatePlanCommand) (CreatePlanResult, error)
}
