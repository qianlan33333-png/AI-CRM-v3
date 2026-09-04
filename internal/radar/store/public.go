package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

var _ radarport.PublicRepository = (*Postgres)(nil)

func (store *Postgres) CreateOAuthState(ctx context.Context, digest [32]byte, state radarport.OAuthState, now time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO radar_oauth_states(state_digest,radar_id,radar_version,redirect_path,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6)`, digest[:], state.RadarID, state.Version, state.Path, state.Expires.UTC(), now.UTC())
	return mapError(err)
}

func (store *Postgres) ConsumeOAuthState(ctx context.Context, digest [32]byte, now time.Time) (radarport.OAuthState, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radarport.OAuthState{}, err
	}
	var state radarport.OAuthState
	err = tx.QueryRow(ctx, `UPDATE radar_oauth_states SET consumed_at=$2 WHERE state_digest=$1 AND consumed_at IS NULL AND expires_at>$2 RETURNING radar_id,radar_version,redirect_path,expires_at`, digest[:], now.UTC()).Scan(&state.RadarID, &state.Version, &state.Path, &state.Expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return radarport.OAuthState{}, radarport.ErrNotFound
	}
	return state, mapError(err)
}

func (store *Postgres) CreateSession(ctx context.Context, digest [32]byte, session radarport.ViewSession, evidence [32]byte, now time.Time) (radarport.ViewSession, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radarport.ViewSession{}, err
	}
	var identityID, customerID any
	var evidenceValue any
	if session.Attribution == radarport.AttributionResolved {
		identityID, customerID, evidenceValue = session.IdentityID, session.CustomerID, evidence[:]
	}
	err = tx.QueryRow(ctx, `INSERT INTO radar_view_sessions(session_digest,radar_id,radar_version,identity_id,customer_id,attribution_status,evidence_digest,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, digest[:], session.RadarID, session.Version, identityID, customerID, session.Attribution, evidenceValue, session.ExpiresAt.UTC(), now.UTC()).Scan(&session.ID)
	return session, mapError(err)
}

func (store *Postgres) ReadSession(ctx context.Context, digest [32]byte, radarID radar.RadarID, version radar.LinkVersion, now time.Time) (radarport.ViewSession, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radarport.ViewSession{}, err
	}
	var session radarport.ViewSession
	var identityID, customerID *int64
	err = tx.QueryRow(ctx, `SELECT id,radar_id,radar_version,identity_id,customer_id,attribution_status,expires_at FROM radar_view_sessions WHERE session_digest=$1 AND radar_id=$2 AND radar_version=$3 AND revoked_at IS NULL AND expires_at>$4`, digest[:], radarID, version, now.UTC()).Scan(&session.ID, &session.RadarID, &session.Version, &identityID, &customerID, &session.Attribution, &session.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return radarport.ViewSession{}, radarport.ErrNotFound
	}
	if err != nil {
		return radarport.ViewSession{}, mapError(err)
	}
	if identityID != nil {
		session.IdentityID = *identityID
	}
	if customerID != nil {
		session.CustomerID = customerdomain.CustomerID(*customerID)
	}
	return session, nil
}

func (store *Postgres) AppendEvent(ctx context.Context, record radarport.EventRecord, now time.Time) (radarport.EventProjection, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radarport.EventProjection{}, false, err
	}
	var identityID, customerID any
	if record.Attribution == radarport.AttributionResolved {
		identityID, customerID = record.IdentityID, record.CustomerID
	}
	command, err := tx.Exec(ctx, `INSERT INTO radar_events(receipt_id,radar_id,radar_version,session_id,stage,attribution_status,identity_id,customer_id,key_digest,payload_digest,failure_code,occurred_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),$12,$13) ON CONFLICT(session_id,radar_version,stage) DO NOTHING`, record.ReceiptID, record.RadarID, record.Version, record.SessionID, record.Stage, record.Attribution, identityID, customerID, record.KeyDigest[:], record.PayloadDigest[:], record.FailureCode, record.OccurredAt.UTC(), now.UTC())
	if err != nil {
		return radarport.EventProjection{}, false, mapError(err)
	}
	var projection radarport.EventProjection
	var storedPayload []byte
	err = tx.QueryRow(ctx, `SELECT id,receipt_id,radar_id,radar_version,stage,attribution_status,COALESCE('cus_'||customer_id::text,''),occurred_at,payload_digest FROM radar_events WHERE session_id=$1 AND radar_version=$2 AND stage=$3`, record.SessionID, record.Version, record.Stage).Scan(&projection.EventID, &projection.ReceiptID, &projection.RadarID, &projection.Version, &projection.Stage, &projection.Attribution, &projection.CustomerRef, &projection.OccurredAt, &storedPayload)
	if err != nil {
		return radarport.EventProjection{}, false, mapError(err)
	}
	if len(storedPayload) != len(record.PayloadDigest) {
		return radarport.EventProjection{}, false, radarport.ErrConflict
	}
	for i := range storedPayload {
		if storedPayload[i] != record.PayloadDigest[i] {
			return radarport.EventProjection{}, false, radarport.ErrConflict
		}
	}
	return projection, command.RowsAffected() == 0, nil
}

func (store *Postgres) Stats(ctx context.Context, id radar.RadarID) (radarport.Stats, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radarport.Stats{}, err
	}
	var stats radarport.Stats
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM radar_links WHERE id=$1)`, id).Scan(&exists); err != nil {
		return radarport.Stats{}, mapError(err)
	}
	if !exists {
		return radarport.Stats{}, radarport.ErrNotFound
	}
	err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE stage='landing'),count(DISTINCT customer_id) FILTER(WHERE attribution_status='resolved'),count(*) FILTER(WHERE stage IN ('content_opened','redirected','image_loaded','pdf_opened')),count(*) FILTER(WHERE stage IN ('content_opened','redirected','image_loaded','pdf_opened') AND attribution_status='resolved'),count(*) FILTER(WHERE stage='redirected'),count(*) FILTER(WHERE stage='image_loaded'),count(*) FILTER(WHERE stage='pdf_opened'),count(*) FILTER(WHERE stage='landing' AND occurred_at >= date_trunc('day',clock_timestamp())),count(*) FILTER(WHERE stage IN ('content_opened','redirected','image_loaded','pdf_opened') AND occurred_at >= date_trunc('day',clock_timestamp())),max(occurred_at) FILTER(WHERE stage IN ('content_opened','redirected','image_loaded','pdf_opened')) FROM radar_events WHERE radar_id=$1`, id).Scan(&stats.TotalEvents, &stats.TotalLandings, &stats.AuthorizedUsers, &stats.ViewCount, &stats.AuthorizedViews, &stats.Redirects, &stats.ImageLoaded, &stats.PDFOpened, &stats.TodayLandings, &stats.TodayViews, &stats.LastViewedAt)
	if err != nil {
		return radarport.Stats{}, mapError(err)
	}
	if stats.TotalLandings > 0 {
		stats.ConversionRate = float64(stats.AuthorizedUsers) / float64(stats.TotalLandings)
	}
	return stats, nil
}

func (store *Postgres) Events(ctx context.Context, query radarport.EventQuery) (radarport.EventPage, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return radarport.EventPage{}, err
	}
	where := "radar_id=$1"
	args := []any{query.RadarID}
	add := func(sql string, value any) { args = append(args, value); where += fmt.Sprintf(" AND "+sql, len(args)) }
	if query.Stage != "" {
		add("stage=$%d", query.Stage)
	}
	if query.Attribution != "" {
		add("attribution_status=$%d", query.Attribution)
	}
	if query.Start != nil {
		add("occurred_at>=$%d", query.Start.UTC())
	}
	if query.End != nil {
		add("occurred_at<$%d", query.End.UTC())
	}
	var total int64
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM radar_links WHERE id=$1)`, query.RadarID).Scan(&exists); err != nil {
		return radarport.EventPage{}, mapError(err)
	}
	if !exists {
		return radarport.EventPage{}, radarport.ErrNotFound
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM radar_events WHERE `+where, args...).Scan(&total); err != nil {
		return radarport.EventPage{}, mapError(err)
	}
	args = append(args, query.Limit, query.Offset)
	rows, err := tx.Query(ctx, `SELECT id,receipt_id,radar_id,radar_version,stage,attribution_status,COALESCE('cus_'||customer_id::text,''),occurred_at FROM radar_events WHERE `+where+fmt.Sprintf(" ORDER BY occurred_at DESC,id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return radarport.EventPage{}, mapError(err)
	}
	defer rows.Close()
	items := make([]radarport.EventProjection, 0)
	for rows.Next() {
		var item radarport.EventProjection
		if err = rows.Scan(&item.EventID, &item.ReceiptID, &item.RadarID, &item.Version, &item.Stage, &item.Attribution, &item.CustomerRef, &item.OccurredAt); err != nil {
			return radarport.EventPage{}, mapError(err)
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return radarport.EventPage{}, mapError(err)
	}
	return radarport.EventPage{Items: items, Total: total, Limit: query.Limit, Offset: query.Offset, HasMore: int64(query.Offset)+int64(len(items)) < total}, nil
}
