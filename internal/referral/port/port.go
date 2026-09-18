// Package port exposes Referral's stable cross-domain and delivery contracts.
// Referral accepts only a verified Distribution browser-session actor; it does
// not resolve, create, or merge customer identities itself.
package port

import (
	"context"
	"errors"
	"time"

	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
)

var (
	ErrNotFound            = errors.New("referral record not found")
	ErrConflict            = errors.New("referral command conflict")
	ErrUnavailable         = errors.New("referral unavailable")
	ErrUnauthorized        = errors.New("referral unauthorized")
	ErrInvitationInvalid   = errors.New("referral invitation invalid")
	ErrCampaignUnavailable = errors.New("referral campaign unavailable")
)

// TrustedSessionActor is an alias, not a second session or identity model.
// HTTP derives it only through the existing verified WeChat/Payment bridge.
type TrustedSessionActor = distributionport.TrustedSessionActor

type LeaderboardKind string

const (
	LeaderboardPersonal LeaderboardKind = "personal"
	LeaderboardTeam     LeaderboardKind = "team"
	LeaderboardInTeam   LeaderboardKind = "in_team"
)

func (k LeaderboardKind) Valid() bool {
	return k == LeaderboardPersonal || k == LeaderboardTeam || k == LeaderboardInTeam
}

type LeaderboardPeriod string

const (
	LeaderboardTotal LeaderboardPeriod = "total"
	LeaderboardWeek  LeaderboardPeriod = "week"
	LeaderboardDay   LeaderboardPeriod = "day"
)

func (p LeaderboardPeriod) Valid() bool {
	return p == LeaderboardTotal || p == LeaderboardWeek || p == LeaderboardDay
}

type CampaignSummary struct {
	Campaign         domain.Campaign
	EffectiveState   domain.CampaignState
	ParticipantCount int64
	InvitationCount  int64
	TeamCount        int64
}

type CampaignView struct {
	CampaignSummary
	Teams        []domain.Team
	DailyMetrics []CampaignDailyMetric
}

// CampaignDailyMetric is a server-time (Asia/Shanghai) activity projection.
// It contains aggregate counts only and never exposes customer identifiers.
type CampaignDailyMetric struct {
	Date                          time.Time
	ParticipantCount, InviteCount int64
}

type InvitationPreview struct {
	Campaign          CampaignSummary
	InviterCustomerID int64
	InviterTeam       domain.Team
	ExpiresAt         time.Time
}

type InvitationLink struct {
	URL       string
	ExpiresAt time.Time
}

type MyCampaign struct {
	Campaign              CampaignSummary
	Participation         *domain.Participation
	Team                  *domain.Team
	CurrentRelationship   *domain.Relationship
	DirectInvitationCount int64
	PersonalTotalScore    int64
	TeamTotalScore        int64
	PersonalRank          int64
	TeamRank              int64
	InvitationAvailable   bool
}

type InviteItem struct {
	Participation domain.Participation
	ScoreState    string
	ScoreDelta    int64
}

type InvitePage struct {
	Items      []InviteItem
	NextCursor string
}

type LeaderboardQuery struct {
	CampaignID int64
	Kind       LeaderboardKind
	Period     LeaderboardPeriod
	// Anchor is interpreted in Asia/Shanghai.  For total it is ignored; for
	// day it selects that calendar day, and for week it selects its Monday.
	Anchor           time.Time
	ViewerCustomerID int64
	// ViewerTeamID is server-derived for the pinned own-team row. Public HTTP
	// must not let a browser choose another customer's team as its own.
	ViewerTeamID int64
	TeamID       int64 // required only for in_team
	Limit        int32
	Cursor       string
}

type LeaderboardEntry struct {
	Rank, Score        int64
	CustomerID, TeamID int64
	TeamName           string
	FirstReachedAt     time.Time
	Mine               bool
}

type LeaderboardPage struct {
	Kind        LeaderboardKind
	Period      LeaderboardPeriod
	WindowStart time.Time
	WindowEnd   time.Time
	Items       []LeaderboardEntry
	MyEntry     *LeaderboardEntry
	NextCursor  string
}

type JoinCampaignCommand struct {
	Actor           TrustedSessionActor
	CampaignID      int64
	InvitationToken string
	// TeamID can only be selected by a participant who has no invitation.
	TeamID         int64
	IdempotencyKey string
}

type IssueInvitationCommand struct {
	Actor          TrustedSessionActor
	CampaignID     int64
	IdempotencyKey string
}

// PublicApplication is the complete member-facing Referral surface.  Every
// mutation derives its Customer ID from TrustedSessionActor rather than HTTP.
type PublicApplication interface {
	ListPublicCampaigns(context.Context) ([]CampaignSummary, error)
	ReadPublicCampaign(context.Context, int64) (CampaignView, error)
	PreviewInvitation(context.Context, string) (InvitationPreview, error)
	JoinCampaign(context.Context, JoinCampaignCommand) (MyCampaign, error)
	IssueInvitation(context.Context, IssueInvitationCommand) (InvitationLink, error)
	MyCampaign(context.Context, TrustedSessionActor, int64) (MyCampaign, error)
	ListMyInvites(context.Context, TrustedSessionActor, int64, string, int32) (InvitePage, error)
	Leaderboard(context.Context, LeaderboardQuery) (LeaderboardPage, error)
}

type CreateCampaignCommand struct {
	ActorAdminID                int64
	Name, CoverURL, Description string
	RewardRules                 string
	StartsAt, EndsAt            time.Time
	IdempotencyKey              string
}

type UpdateCampaignCommand struct {
	CampaignID, ExpectedVersion int64
	ActorAdminID                int64
	Name, CoverURL, Description string
	RewardRules                 string
	StartsAt, EndsAt            time.Time
	IdempotencyKey              string
}

type SetCampaignStateCommand struct {
	CampaignID, ExpectedVersion int64
	ActorAdminID                int64
	Target                      domain.CampaignState
	IdempotencyKey              string
}

type CreateTeamCommand struct {
	CampaignID, CaptainCustomerID int64
	ActorAdminID                  int64
	Name, LogoURL                 string
	IdempotencyKey                string
}

type ReverseInvitationCommand struct {
	ParticipationID int64
	ActorAdminID    int64
	Reason          string
	IdempotencyKey  string
}

type RecordRewardCommand struct {
	CampaignID, CustomerID, ScoreEventID int64
	ActorAdminID                         int64
	Period, Reward, EvidenceReference    string
	IdempotencyKey                       string
}

type RevokeInvitationCommand struct {
	InvitationID   int64
	ActorAdminID   int64
	Reason         string
	IdempotencyKey string
}

type AdminCampaignPage struct {
	Items      []CampaignSummary
	NextCursor string
}

type AdminReferralRecord struct {
	Participation domain.Participation
	CampaignName  string
	ScoreEventID  int64
	ScoreState    string
}

type AdminReferralPage struct {
	Items      []AdminReferralRecord
	NextCursor string
}

type RelationshipHistoryPage struct {
	Items      []domain.RelationshipHistory
	NextCursor string
}

type RewardPage struct {
	Items      []domain.RewardRecord
	NextCursor string
}

type AdminApplication interface {
	CreateCampaign(context.Context, CreateCampaignCommand) (domain.Campaign, error)
	UpdateCampaign(context.Context, UpdateCampaignCommand) (domain.Campaign, error)
	SetCampaignState(context.Context, SetCampaignStateCommand) (domain.Campaign, error)
	CreateTeam(context.Context, CreateTeamCommand) (domain.Team, error)
	ReverseInvitation(context.Context, ReverseInvitationCommand) error
	RecordReward(context.Context, RecordRewardCommand) (domain.RewardRecord, error)
	RevokeInvitation(context.Context, RevokeInvitationCommand) error
	ReadAdminCampaign(context.Context, int64) (CampaignView, error)
	ListAdminCampaigns(context.Context, string, int32) (AdminCampaignPage, error)
	ListAdminReferrals(context.Context, int64, string, int32) (AdminReferralPage, error)
	ListRelationshipHistory(context.Context, int64, string, int32) (RelationshipHistoryPage, error)
	ListRewards(context.Context, int64, string, int32) (RewardPage, error)
}

// RelationshipSnapshot is the only planned bridge for a later order-time
// relation attribution decision.  This release writes it but never asks Order
// to use it for commission calculation or overrides an existing promotion URL.
type RelationshipSnapshot struct {
	RelationshipID, Version, CustomerID, ReferrerCustomerID, SourceCampaignID, InvitationID int64
	EffectiveAt                                                                             time.Time
}

type CurrentRelationshipReader interface {
	CurrentRelationship(context.Context, int64) (RelationshipSnapshot, bool, error)
	// RelationshipAt reads the relation version effective at a given instant.
	// It is reserved for a future order-time attribution snapshot and never
	// enables commission in this release.
	RelationshipAt(context.Context, int64, time.Time) (RelationshipSnapshot, bool, error)
}

// CanonicalCustomerVerifier is supplied by a composed Identity/Customer
// adapter.  Referral only needs this narrow verification for staff-selected
// captains; it never reads identity tables or accepts a browser assertion.
type CanonicalCustomerVerifier interface {
	VerifyCanonicalCustomer(context.Context, int64) (bool, error)
}
