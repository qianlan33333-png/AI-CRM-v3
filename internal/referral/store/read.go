package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type CampaignCounts struct {
	ParticipantCount, InvitationCount, TeamCount int64
}

func (r *Repository) ListCampaignsWithin(ctx context.Context, offset, limit int32, publicOnly bool) ([]referraldomain.Campaign, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if offset < 0 || limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	query := `SELECT ` + campaignColumns + ` FROM referral_campaigns`
	if publicOnly {
		query += ` WHERE state <> 'draft'`
	}
	query += ` ORDER BY starts_at DESC,id DESC OFFSET $1 LIMIT $2`
	rows, err := tx.Query(ctx, query, offset, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referraldomain.Campaign, 0, limit)
	for rows.Next() {
		value, scanErr := scanCampaign(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

func (r *Repository) CampaignCountsWithin(ctx context.Context, campaignID int64) (CampaignCounts, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return CampaignCounts{}, err
	}
	if campaignID < 1 {
		return CampaignCounts{}, ErrInvalid
	}
	var result CampaignCounts
	err = tx.QueryRow(ctx, `SELECT
        (SELECT count(*) FROM referral_participations WHERE campaign_id=$1 AND state='active'),
		(SELECT count(*) FROM referral_score_events c WHERE c.campaign_id=$1 AND c.kind='credit'
		 AND NOT EXISTS (SELECT 1 FROM referral_score_events r WHERE r.kind='reversal' AND r.reverses_score_event_id=c.id)),
        (SELECT count(*) FROM referral_teams WHERE campaign_id=$1)`, campaignID).Scan(&result.ParticipantCount, &result.InvitationCount, &result.TeamCount)
	if err != nil {
		return CampaignCounts{}, mapError(err)
	}
	return result, nil
}

func (r *Repository) CountDirectInvitationsWithin(ctx context.Context, campaignID, inviterCustomerID int64) (int64, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return 0, err
	}
	if campaignID < 1 || inviterCustomerID < 1 {
		return 0, ErrInvalid
	}
	var count int64
	err = tx.QueryRow(ctx, `SELECT count(*) FROM referral_participations WHERE campaign_id=$1 AND inviter_customer_id=$2 AND state='active'`, campaignID, inviterCustomerID).Scan(&count)
	if err != nil {
		return 0, mapError(err)
	}
	return count, nil
}

func (r *Repository) ListTeamsWithin(ctx context.Context, campaignID int64) ([]referraldomain.Team, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if campaignID < 1 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT `+teamColumns+` FROM referral_teams WHERE campaign_id=$1 ORDER BY id`, campaignID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referraldomain.Team, 0)
	for rows.Next() {
		value, scanErr := scanTeam(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

func (r *Repository) CampaignDailyMetricsWithin(ctx context.Context, campaignID int64) ([]referralport.CampaignDailyMetric, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if campaignID < 1 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `WITH participants AS (
        SELECT (joined_at AT TIME ZONE 'Asia/Shanghai')::date AS day, count(*)::bigint AS participants
        FROM referral_participations WHERE campaign_id=$1 AND state='active' GROUP BY 1
    ), invitations AS (
        SELECT (occurred_at AT TIME ZONE 'Asia/Shanghai')::date AS day, count(*)::bigint AS invites
        FROM referral_score_events c WHERE campaign_id=$1 AND kind='credit'
          AND NOT EXISTS (SELECT 1 FROM referral_score_events r WHERE r.kind='reversal' AND r.reverses_score_event_id=c.id)
        GROUP BY 1
    ) SELECT COALESCE(p.day,i.day),COALESCE(p.participants,0),COALESCE(i.invites,0)
      FROM participants p FULL OUTER JOIN invitations i USING(day) ORDER BY 1 DESC`, campaignID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referralport.CampaignDailyMetric, 0)
	for rows.Next() {
		var value referralport.CampaignDailyMetric
		if err = rows.Scan(&value.Date, &value.ParticipantCount, &value.InviteCount); err != nil {
			return nil, mapError(err)
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

func (r *Repository) MyParticipationWithin(ctx context.Context, campaignID, customerID int64) (referraldomain.Participation, error) {
	return r.ReadParticipationWithin(ctx, campaignID, customerID, false)
}

func (r *Repository) CurrentRelationshipAtWithin(ctx context.Context, customerID int64, at time.Time) (referraldomain.Relationship, bool, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return referraldomain.Relationship{}, false, err
	}
	if customerID < 1 || at.IsZero() {
		return referraldomain.Relationship{}, false, ErrInvalid
	}
	var result referraldomain.Relationship
	err = tx.QueryRow(ctx, `SELECT h.relationship_id,h.customer_id,h.referrer_customer_id,h.source_campaign_id,h.invitation_id,h.version,h.accepted_at
        FROM referral_relationship_history h
        WHERE h.customer_id=$1 AND h.accepted_at <= $2
        ORDER BY h.accepted_at DESC,h.id DESC LIMIT 1`, customerID, at.UTC()).Scan(&result.ID, &result.CustomerID, &result.ReferrerCustomerID, &result.SourceCampaignID, &result.InvitationID, &result.Version, &result.EffectiveAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return referraldomain.Relationship{}, false, nil
	}
	if err != nil {
		return referraldomain.Relationship{}, false, mapError(err)
	}
	if !result.Valid() {
		return referraldomain.Relationship{}, false, referralport.ErrUnavailable
	}
	return result, true, nil
}

func (r *Repository) ListInviteItemsWithin(ctx context.Context, campaignID, inviterCustomerID int64, offset, limit int32) ([]referralport.InviteItem, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if campaignID < 1 || inviterCustomerID < 1 || offset < 0 || limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT `+participationColumns+`,COALESCE((SELECT sum(delta) FROM referral_score_events e WHERE e.participation_id=p.id),0)
        FROM referral_participations p
        WHERE p.campaign_id=$1 AND p.inviter_customer_id=$2
        ORDER BY p.joined_at DESC,p.id DESC OFFSET $3 LIMIT $4`, campaignID, inviterCustomerID, offset, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referralport.InviteItem, 0, limit)
	for rows.Next() {
		var value referraldomain.Participation
		var state string
		var delta int64
		if err = rows.Scan(&value.ID, &value.CampaignID, &value.CustomerID, &value.TeamID, &value.InvitationID, &value.InviterCustomerID, &value.InviterTeamID, &state, &value.JoinedAt, &delta); err != nil {
			return nil, mapError(err)
		}
		value.State = referraldomain.ParticipationState(state)
		if !value.Valid() {
			return nil, referralport.ErrUnavailable
		}
		scoreState := "valid"
		if delta < 1 {
			scoreState = "reversed"
		}
		values = append(values, referralport.InviteItem{Participation: value, ScoreState: scoreState, ScoreDelta: delta})
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

func (r *Repository) ListRelationshipHistoryWithin(ctx context.Context, customerID int64, offset, limit int32) ([]referraldomain.RelationshipHistory, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if customerID < 1 || offset < 0 || limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT id,relationship_id,version,customer_id,COALESCE(previous_referrer_customer_id,0),referrer_customer_id,source_campaign_id,invitation_id,accepted_at
        FROM referral_relationship_history WHERE customer_id=$1 ORDER BY accepted_at DESC,id DESC OFFSET $2 LIMIT $3`, customerID, offset, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referraldomain.RelationshipHistory, 0, limit)
	for rows.Next() {
		var value referraldomain.RelationshipHistory
		if err = rows.Scan(&value.ID, &value.RelationshipID, &value.Version, &value.CustomerID, &value.PreviousReferrerCustomerID, &value.ReferrerCustomerID, &value.SourceCampaignID, &value.InvitationID, &value.AcceptedAt); err != nil {
			return nil, mapError(err)
		}
		if !value.Valid() {
			return nil, referralport.ErrUnavailable
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

func (r *Repository) ListRewardsWithin(ctx context.Context, campaignID int64, offset, limit int32) ([]referraldomain.RewardRecord, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if campaignID < 1 || offset < 0 || limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT `+rewardColumns+` FROM referral_reward_records WHERE campaign_id=$1 ORDER BY recorded_at DESC,id DESC OFFSET $2 LIMIT $3`, campaignID, offset, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referraldomain.RewardRecord, 0, limit)
	for rows.Next() {
		value, scanErr := scanReward(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

func (r *Repository) ListAdminReferralsWithin(ctx context.Context, campaignID int64, offset, limit int32) ([]referralport.AdminReferralRecord, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, err
	}
	if campaignID < 1 || offset < 0 || limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	const adminParticipationColumns = `p.id,p.campaign_id,p.customer_id,p.team_id,COALESCE(p.invitation_id,0),COALESCE(p.inviter_customer_id,0),COALESCE(p.inviter_team_id,0),p.state,p.joined_at`
	rows, err := tx.Query(ctx, `SELECT `+adminParticipationColumns+`,c.name,COALESCE((SELECT id FROM referral_score_events e WHERE e.participation_id=p.id AND e.kind='credit'),0),COALESCE((SELECT sum(delta) FROM referral_score_events e WHERE e.participation_id=p.id),0)
        FROM referral_participations p JOIN referral_campaigns c ON c.id=p.campaign_id
        WHERE p.campaign_id=$1 ORDER BY p.joined_at DESC,p.id DESC OFFSET $2 LIMIT $3`, campaignID, offset, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	values := make([]referralport.AdminReferralRecord, 0, limit)
	for rows.Next() {
		var item referralport.AdminReferralRecord
		var state string
		var delta int64
		if err = rows.Scan(&item.Participation.ID, &item.Participation.CampaignID, &item.Participation.CustomerID, &item.Participation.TeamID, &item.Participation.InvitationID, &item.Participation.InviterCustomerID, &item.Participation.InviterTeamID, &state, &item.Participation.JoinedAt, &item.CampaignName, &item.ScoreEventID, &delta); err != nil {
			return nil, mapError(err)
		}
		item.Participation.State = referraldomain.ParticipationState(state)
		if !item.Participation.Valid() {
			return nil, referralport.ErrUnavailable
		}
		item.ScoreState = "none"
		if item.ScoreEventID > 0 {
			item.ScoreState = "valid"
		}
		if item.ScoreEventID > 0 && delta < 1 {
			item.ScoreState = "reversed"
		}
		values = append(values, item)
	}
	if err = rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return values, nil
}

// LeaderboardRows counts non-reversed credits according to their original
// accepted timestamp. A later reversal corrects the relevant historical day
// and week instead of only subtracting from the day it was administered.
func (r *Repository) LeaderboardRowsWithin(ctx context.Context, campaignID int64, kind referralport.LeaderboardKind, teamID int64, start, end time.Time, offset, limit int32, viewerCustomerID, viewerTeamID int64) ([]referralport.LeaderboardEntry, *referralport.LeaderboardEntry, error) {
	tx, err := transaction(ctx)
	if err != nil {
		return nil, nil, err
	}
	if campaignID < 1 || !kind.Valid() || !start.Before(end) || offset < 0 || limit < 1 || limit > 101 || (kind == referralport.LeaderboardInTeam && teamID < 1) {
		return nil, nil, ErrInvalid
	}
	var query, ownQuery string
	var args, ownArgs []any
	switch kind {
	case referralport.LeaderboardPersonal:
		query = `WITH active_credits AS (
            SELECT e.inviter_customer_id,e.team_id,t.name AS team_name,e.occurred_at,e.id
            FROM referral_score_events e JOIN referral_teams t ON t.id=e.team_id
            WHERE e.campaign_id=$1 AND e.kind='credit' AND e.occurred_at >= $2 AND e.occurred_at < $3
              AND NOT EXISTS (SELECT 1 FROM referral_score_events r WHERE r.kind='reversal' AND r.reverses_score_event_id=e.id)
        ), ranked AS (
            SELECT row_number() OVER (ORDER BY count(*) DESC,max(occurred_at) ASC,inviter_customer_id ASC)::bigint AS rank,
                count(*)::bigint AS score,inviter_customer_id AS customer_id,team_id,max(team_name) AS team_name,max(occurred_at) AS first_reached_at
            FROM active_credits GROUP BY inviter_customer_id,team_id
		) SELECT rank,score,customer_id,team_id,team_name,first_reached_at FROM ranked ORDER BY rank OFFSET $4 LIMIT $5`
		ownQuery = strings.Replace(query, `SELECT rank,score,customer_id,team_id,team_name,first_reached_at FROM ranked ORDER BY rank OFFSET $4 LIMIT $5`, `SELECT rank,score,customer_id,team_id,team_name,first_reached_at FROM ranked WHERE customer_id=$4`, 1)
		args = []any{campaignID, start.UTC(), end.UTC(), offset, limit}
		ownArgs = []any{campaignID, start.UTC(), end.UTC(), viewerCustomerID}
	case referralport.LeaderboardInTeam:
		query = `WITH active_credits AS (
            SELECT e.inviter_customer_id,e.team_id,t.name AS team_name,e.occurred_at,e.id
            FROM referral_score_events e JOIN referral_teams t ON t.id=e.team_id
            WHERE e.campaign_id=$1 AND e.kind='credit' AND e.occurred_at >= $2 AND e.occurred_at < $3 AND e.team_id=$4
              AND NOT EXISTS (SELECT 1 FROM referral_score_events r WHERE r.kind='reversal' AND r.reverses_score_event_id=e.id)
        ), ranked AS (
            SELECT row_number() OVER (ORDER BY count(*) DESC,max(occurred_at) ASC,inviter_customer_id ASC)::bigint AS rank,
                count(*)::bigint AS score,inviter_customer_id AS customer_id,team_id,max(team_name) AS team_name,max(occurred_at) AS first_reached_at
            FROM active_credits GROUP BY inviter_customer_id,team_id
        ) SELECT rank,score,customer_id,team_id,team_name,first_reached_at FROM ranked ORDER BY rank OFFSET $5 LIMIT $6`
		ownQuery = strings.Replace(query, `SELECT rank,score,customer_id,team_id,team_name,first_reached_at FROM ranked ORDER BY rank OFFSET $5 LIMIT $6`, `SELECT rank,score,customer_id,team_id,team_name,first_reached_at FROM ranked WHERE customer_id=$5`, 1)
		args = []any{campaignID, start.UTC(), end.UTC(), teamID, offset, limit}
		ownArgs = []any{campaignID, start.UTC(), end.UTC(), teamID, viewerCustomerID}
	case referralport.LeaderboardTeam:
		query = `WITH active_credits AS (
            SELECT e.team_id,t.name AS team_name,e.occurred_at,e.id
            FROM referral_score_events e JOIN referral_teams t ON t.id=e.team_id
            WHERE e.campaign_id=$1 AND e.kind='credit' AND e.occurred_at >= $2 AND e.occurred_at < $3
              AND NOT EXISTS (SELECT 1 FROM referral_score_events r WHERE r.kind='reversal' AND r.reverses_score_event_id=e.id)
        ), ranked AS (
            SELECT row_number() OVER (ORDER BY count(*) DESC,max(occurred_at) ASC,team_id ASC)::bigint AS rank,
                count(*)::bigint AS score,0::bigint AS customer_id,team_id,max(team_name) AS team_name,max(occurred_at) AS first_reached_at
            FROM active_credits GROUP BY team_id
		) SELECT rank,score,customer_id,team_id,team_name,first_reached_at FROM ranked ORDER BY rank OFFSET $4 LIMIT $5`
		ownQuery = strings.Replace(query, `SELECT rank,score,customer_id,team_id,team_name,first_reached_at FROM ranked ORDER BY rank OFFSET $4 LIMIT $5`, `SELECT rank,score,customer_id,team_id,team_name,first_reached_at FROM ranked WHERE team_id=$4`, 1)
		args = []any{campaignID, start.UTC(), end.UTC(), offset, limit}
		ownArgs = []any{campaignID, start.UTC(), end.UTC(), viewerTeamID}
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, nil, mapError(err)
	}
	defer rows.Close()
	items := make([]referralport.LeaderboardEntry, 0, limit)
	for rows.Next() {
		var item referralport.LeaderboardEntry
		if err = rows.Scan(&item.Rank, &item.Score, &item.CustomerID, &item.TeamID, &item.TeamName, &item.FirstReachedAt); err != nil {
			return nil, nil, mapError(err)
		}
		item.Mine = kind != referralport.LeaderboardTeam && item.CustomerID == viewerCustomerID
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, mapError(err)
	}
	viewerKey := viewerCustomerID
	if kind == referralport.LeaderboardTeam {
		viewerKey = viewerTeamID
	}
	if viewerKey < 1 {
		return items, nil, nil
	}
	var own referralport.LeaderboardEntry
	err = tx.QueryRow(ctx, ownQuery, ownArgs...).Scan(&own.Rank, &own.Score, &own.CustomerID, &own.TeamID, &own.TeamName, &own.FirstReachedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return items, nil, nil
	}
	if err != nil {
		return nil, nil, mapError(err)
	}
	own.Mine = true
	return items, &own, nil
}

func OffsetCursor(value string) (int32, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || parsed < 0 || value != strconv.FormatInt(parsed, 10) {
		return 0, referralport.ErrConflict
	}
	return int32(parsed), nil
}

func NextOffsetCursor(offset int32, count int, limit int32) string {
	if int32(count) < limit {
		return ""
	}
	return strconv.FormatInt(int64(offset)+int64(count), 10)
}
