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
	Kind           ContentKind
	Text           string
	MaterialKind   string
	MaterialID     int64
	MaterialDigest effectport.Digest
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

type RecipientCandidate struct {
	CustomerID customerdomain.CustomerID
	StaffID    int64
	Content    []ContentBlock
}

func (r RecipientCandidate) Valid() bool {
	if r.CustomerID < 1 || r.StaffID < 1 || len(r.Content) == 0 || len(r.Content) > MaxMessagesPerTarget {
		return false
	}
	for _, block := range r.Content {
		if !block.Valid() {
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
