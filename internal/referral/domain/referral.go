// Package domain contains Referral's closed business facts.  It intentionally
// does not know about HTTP, trusted-session construction, customer tables,
// payment, or the persistence implementation.
package domain

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalid    = errors.New("invalid referral fact")
	ErrTransition = errors.New("invalid referral transition")
	ErrVersion    = errors.New("referral version conflict")
)

type CampaignState string

const (
	CampaignDraft     CampaignState = "draft"
	CampaignScheduled CampaignState = "scheduled"
	CampaignActive    CampaignState = "active"
	CampaignEnded     CampaignState = "ended"
	CampaignDisabled  CampaignState = "disabled"
)

func (s CampaignState) Valid() bool {
	switch s {
	case CampaignDraft, CampaignScheduled, CampaignActive, CampaignEnded, CampaignDisabled:
		return true
	default:
		return false
	}
}

// Campaign is Referral-owned configuration.  Its scoring rule is deliberately
// fixed to one point for a direct, accepted activity participation in v1.
type Campaign struct {
	ID                          int64
	Name, CoverURL, Description string
	RewardRules                 string
	State                       CampaignState
	StartsAt, EndsAt            time.Time
	Version                     int64
	CreatedBy                   int64
	CreatedAt, UpdatedAt        time.Time
}

func (c Campaign) Valid() bool {
	return c.ID > 0 && validText(c.Name, 200, true) && validText(c.CoverURL, 2000, false) &&
		validText(c.Description, 5000, false) && validText(c.RewardRules, 5000, false) && c.State.Valid() &&
		!c.StartsAt.IsZero() && !c.EndsAt.IsZero() && c.EndsAt.After(c.StartsAt) && c.Version > 0 &&
		c.CreatedBy >= 0 && !c.CreatedAt.IsZero() && !c.UpdatedAt.Before(c.CreatedAt)
}

func (c Campaign) ValidForInsert() bool {
	if c.ID != 0 || c.Version != 1 {
		return false
	}
	probe := c
	probe.ID = 1
	return probe.Valid()
}

func (c Campaign) AcceptingAt(at time.Time) bool {
	if !c.Valid() || at.IsZero() {
		return false
	}
	return (c.State == CampaignScheduled || c.State == CampaignActive) && !at.Before(c.StartsAt) && at.Before(c.EndsAt)
}

// EffectiveState lets public reads report a consistent lifecycle even when a
// durable close job has not yet been scheduled after a process restart.
func (c Campaign) EffectiveState(at time.Time) CampaignState {
	if !c.Valid() || at.IsZero() || c.State == CampaignDraft || c.State == CampaignDisabled || c.State == CampaignEnded {
		return c.State
	}
	if !at.Before(c.EndsAt) {
		return CampaignEnded
	}
	if at.Before(c.StartsAt) {
		return CampaignScheduled
	}
	return CampaignActive
}

func (c Campaign) Update(expectedVersion int64, name, coverURL, description, rewardRules string, startsAt, endsAt time.Time, at time.Time) (Campaign, error) {
	if expectedVersion != c.Version {
		return Campaign{}, ErrVersion
	}
	if (c.State != CampaignDraft && c.State != CampaignScheduled) || c.EffectiveState(at) == CampaignActive || c.EffectiveState(at) == CampaignEnded {
		return Campaign{}, ErrTransition
	}
	next := c
	next.Name, next.CoverURL, next.Description, next.RewardRules = name, coverURL, description, rewardRules
	next.StartsAt, next.EndsAt, next.Version, next.UpdatedAt = startsAt.UTC(), endsAt.UTC(), c.Version+1, at.UTC()
	if at.IsZero() || at.Before(c.UpdatedAt) || !next.Valid() {
		return Campaign{}, ErrInvalid
	}
	return next, nil
}

func (c Campaign) Transition(expectedVersion int64, target CampaignState, at time.Time) (Campaign, error) {
	if expectedVersion != c.Version {
		return Campaign{}, ErrVersion
	}
	if at.IsZero() || at.Before(c.UpdatedAt) || !target.Valid() {
		return Campaign{}, ErrInvalid
	}
	allowed := false
	switch c.State {
	case CampaignDraft:
		allowed = target == CampaignScheduled || target == CampaignActive || target == CampaignDisabled
	case CampaignScheduled:
		allowed = target == CampaignActive || target == CampaignDisabled || target == CampaignEnded
	case CampaignActive:
		allowed = target == CampaignEnded || target == CampaignDisabled
	}
	if !allowed {
		return Campaign{}, ErrTransition
	}
	next := c
	next.State, next.Version, next.UpdatedAt = target, c.Version+1, at.UTC()
	return next, nil
}

type Team struct {
	ID, CampaignID, CaptainCustomerID int64
	Name, LogoURL                     string
	Version                           int64
	CreatedAt, UpdatedAt              time.Time
}

func (t Team) Valid() bool {
	return t.ID > 0 && t.CampaignID > 0 && t.CaptainCustomerID > 0 && validText(t.Name, 100, true) &&
		validText(t.LogoURL, 2000, false) && t.Version > 0 && !t.CreatedAt.IsZero() && !t.UpdatedAt.Before(t.CreatedAt)
}

func (t Team) ValidForInsert() bool {
	if t.ID != 0 || t.Version != 1 {
		return false
	}
	probe := t
	probe.ID = 1
	return probe.Valid()
}

type ParticipationState string

const (
	ParticipationActive   ParticipationState = "active"
	ParticipationReversed ParticipationState = "reversed"
)

func (s ParticipationState) Valid() bool {
	return s == ParticipationActive || s == ParticipationReversed
}

// Participation freezes team and invitation source for one campaign.  Later
// current-referrer changes never rewrite this record.
type Participation struct {
	ID, CampaignID, CustomerID, TeamID             int64
	InvitationID, InviterCustomerID, InviterTeamID int64
	State                                          ParticipationState
	JoinedAt                                       time.Time
}

func (p Participation) Valid() bool {
	return p.ID > 0 && p.CampaignID > 0 && p.CustomerID > 0 && p.TeamID > 0 && p.InvitationID >= 0 &&
		p.InviterCustomerID >= 0 && p.InviterTeamID >= 0 && p.State.Valid() && !p.JoinedAt.IsZero() &&
		((p.InvitationID == 0 && p.InviterCustomerID == 0 && p.InviterTeamID == 0) ||
			(p.InvitationID > 0 && p.InviterCustomerID > 0 && p.InviterTeamID > 0))
}

type InvitationState string

const (
	InvitationActive  InvitationState = "active"
	InvitationRevoked InvitationState = "revoked"
	InvitationExpired InvitationState = "expired"
)

func (s InvitationState) Valid() bool {
	return s == InvitationActive || s == InvitationRevoked || s == InvitationExpired
}

type Invitation struct {
	ID, CampaignID, InviterCustomerID int64
	TokenDigest                       [32]byte
	State                             InvitationState
	CreatedAt, ExpiresAt              time.Time
	RevokedAt                         *time.Time
}

func (i Invitation) Valid() bool {
	return i.ID > 0 && i.CampaignID > 0 && i.InviterCustomerID > 0 && i.State.Valid() && !i.CreatedAt.IsZero() &&
		i.ExpiresAt.After(i.CreatedAt) && ((i.State == InvitationRevoked) == (i.RevokedAt != nil))
}

type Relationship struct {
	ID, CustomerID, ReferrerCustomerID, SourceCampaignID, InvitationID int64
	Version                                                            int64
	EffectiveAt                                                        time.Time
}

func (r Relationship) Valid() bool {
	return r.ID > 0 && r.CustomerID > 0 && r.ReferrerCustomerID > 0 && r.CustomerID != r.ReferrerCustomerID &&
		r.SourceCampaignID > 0 && r.InvitationID > 0 && r.Version > 0 && !r.EffectiveAt.IsZero()
}

// RelationshipHistory is append-only evidence of a successful, explicit
// invitation acceptance.  Zero old values mean the customer had no relation.
type RelationshipHistory struct {
	ID, RelationshipID, Version, CustomerID, PreviousReferrerCustomerID, ReferrerCustomerID, SourceCampaignID, InvitationID int64
	AcceptedAt                                                                                                              time.Time
}

func (h RelationshipHistory) Valid() bool {
	return h.ID > 0 && h.RelationshipID > 0 && h.Version > 0 && h.CustomerID > 0 && h.ReferrerCustomerID > 0 &&
		h.CustomerID != h.ReferrerCustomerID && h.SourceCampaignID > 0 && h.InvitationID > 0 && !h.AcceptedAt.IsZero()
}

type ScoreEventKind string

const (
	ScoreCredit  ScoreEventKind = "credit"
	ScoreReverse ScoreEventKind = "reversal"
)

func (k ScoreEventKind) Valid() bool { return k == ScoreCredit || k == ScoreReverse }

// ScoreEvent is immutable.  Reversing an invitation appends a negative event
// rather than editing the original credit, preserving rank explainability.
type ScoreEvent struct {
	ID, CampaignID, ParticipationID, InviterCustomerID, TeamID, Delta, ReversesScoreEventID int64
	Kind                                                                                    ScoreEventKind
	Reason                                                                                  string
	OccurredAt                                                                              time.Time
}

func (e ScoreEvent) Valid() bool {
	if e.ID < 1 || e.CampaignID < 1 || e.ParticipationID < 1 || e.InviterCustomerID < 1 || e.TeamID < 1 || !e.Kind.Valid() || e.OccurredAt.IsZero() {
		return false
	}
	if e.Kind == ScoreCredit {
		return e.Delta == 1 && e.ReversesScoreEventID == 0 && e.Reason == ""
	}
	return e.Delta == -1 && e.ReversesScoreEventID > 0 && validText(e.Reason, 500, true)
}

type RewardState string

const (
	RewardRecorded    RewardState = "recorded"
	RewardNeedsReview RewardState = "needs_review"
)

func (s RewardState) Valid() bool { return s == RewardRecorded || s == RewardNeedsReview }

type RewardRecord struct {
	ID, CampaignID, CustomerID, ScoreEventID int64
	Period, Reward, EvidenceReference        string
	State                                    RewardState
	RecordedBy                               int64
	RecordedAt                               time.Time
}

func (r RewardRecord) Valid() bool {
	return r.ID > 0 && r.CampaignID > 0 && r.CustomerID > 0 && r.ScoreEventID >= 0 &&
		validText(r.Period, 32, true) && validText(r.Reward, 500, true) && validText(r.EvidenceReference, 500, false) &&
		r.State.Valid() && r.RecordedBy > 0 && !r.RecordedAt.IsZero()
}

func validText(value string, maximum int, required bool) bool {
	return value == strings.TrimSpace(value) && len(value) <= maximum && (!required || value != "")
}
