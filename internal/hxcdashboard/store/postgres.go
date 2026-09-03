package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/domain"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrNotFound = errors.New("hxc dashboard not found")
var ErrActiveRefresh = errors.New("hxc dashboard refresh already active")

type PostgreSQL struct{ pool *pgxpool.Pool }

func NewPostgreSQL(pool *pgxpool.Pool) *PostgreSQL { return &PostgreSQL{pool: pool} }

func (store *PostgreSQL) CreateRun(ctx context.Context, runKey string, requestDigest [32]byte, trigger string, requestedBy int64) (domain.RefreshRun, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return domain.RefreshRun{}, false, err
	}
	var run domain.RefreshRun
	err = tx.QueryRow(ctx, `INSERT INTO hxc_dashboard_refresh_runs(run_key,request_digest,trigger,status,requested_by) VALUES($1,$2,$3,'queued',NULLIF($4,0)) ON CONFLICT(run_key) DO NOTHING RETURNING id,trigger,status,projection_id,source_count,processed_count,COALESCE(error_code,''),version,COALESCE(requested_by,0),started_at,completed_at,created_at,updated_at`, runKey, requestDigest[:], trigger, requestedBy).Scan(&run.ID, &run.Trigger, &run.Status, &run.ProjectionID, &run.SourceCount, &run.ProcessedCount, &run.ErrorCode, &run.Version, &run.RequestedBy, &run.StartedAt, &run.CompletedAt, &run.CreatedAt, &run.UpdatedAt)
	if err == nil {
		return run, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		if isUniqueViolation(err) {
			return domain.RefreshRun{}, false, ErrActiveRefresh
		}
		return domain.RefreshRun{}, false, fmt.Errorf("create HXC refresh: %w", err)
	}
	run, err = store.GetRun(ctxByTx(ctx), 0, runKey)
	return run, true, err
}

func ctxByTx(ctx context.Context) context.Context { return ctx }

func (store *PostgreSQL) GetRun(ctx context.Context, id int64, runKey string) (domain.RefreshRun, error) {
	queryer := queryer(store.pool)
	if tx, err := platformpostgres.RequireTransaction(ctx); err == nil {
		queryer = tx
	}
	query := `SELECT id,trigger,status,projection_id,source_count,processed_count,COALESCE(error_code,''),version,COALESCE(requested_by,0),started_at,completed_at,created_at,updated_at FROM hxc_dashboard_refresh_runs WHERE `
	var arg any
	if id > 0 {
		query += "id=$1"
		arg = id
	} else {
		query += "run_key=$1"
		arg = runKey
	}
	var run domain.RefreshRun
	err := queryer.QueryRow(ctx, query, arg).Scan(&run.ID, &run.Trigger, &run.Status, &run.ProjectionID, &run.SourceCount, &run.ProcessedCount, &run.ErrorCode, &run.Version, &run.RequestedBy, &run.StartedAt, &run.CompletedAt, &run.CreatedAt, &run.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RefreshRun{}, ErrNotFound
	}
	if err != nil {
		return domain.RefreshRun{}, fmt.Errorf("get HXC refresh: %w", err)
	}
	return run, nil
}

type rowQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func queryer(pool *pgxpool.Pool) rowQueryer { return pool }

func (store *PostgreSQL) MarkRunning(ctx context.Context, id int64) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE hxc_dashboard_refresh_runs SET status='running',started_at=COALESCE(started_at,now()),updated_at=now(),version=version+1 WHERE id=$1 AND status IN ('queued','running','publishing')`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}
func (store *PostgreSQL) MarkPublishing(ctx context.Context, id int64, count int64) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE hxc_dashboard_refresh_runs SET status='publishing',source_count=$2,processed_count=$2,updated_at=now(),version=version+1 WHERE id=$1 AND status='running'`, id, count)
	return err
}
func (store *PostgreSQL) Fail(ctx context.Context, id int64, code string) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE hxc_dashboard_refresh_runs SET status='failed',error_code=$2,completed_at=now(),updated_at=now(),version=version+1 WHERE id=$1 AND status IN ('queued','running','publishing')`, id, code)
	return err
}

func (store *PostgreSQL) Publish(ctx context.Context, runID int64, projection domain.Projection) (int64, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE hxc_dashboard_versions SET status='superseded' WHERE status='published'`); err != nil {
		return 0, err
	}
	var id int64
	c := projection.Counts
	err = tx.QueryRow(ctx, `INSERT INTO hxc_dashboard_versions(rule_version,status,projection_as_of,source_watermark,source_digest,projection_digest,total_count,active_used_count,active_unused_count,registered_no_active_membership_count,matched_count,unmatched_count,conflict_count) VALUES($1,'published',$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`, domain.RuleVersion, projection.AsOf, projection.Watermark, projection.SourceDigest[:], projection.ProjectionDigest[:], c.Total, c.ActiveUsed, c.ActiveUnused, c.RegisteredNoActiveMembership, c.Matched, c.Unmatched, c.Conflict).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert projection: %w", err)
	}
	rows := make([][]any, 0, len(projection.Rows))
	for _, r := range projection.Rows {
		var customer any
		if r.CustomerID > 0 {
			customer = int64(r.CustomerID)
		}
		rows = append(rows, []any{id, r.SubjectDigest[:], r.UserRef, r.Stage, r.SubscriptionTier, r.SubscriptionExpiresAt, r.MonthlyChatQuota, r.CurrentPeriodUsed, r.ConsultationLimit, r.ConsultationUsed, r.MembershipAttribution, r.Sessions7D, r.Sessions30D, r.SessionsTotal, r.UserMessages7D, r.UserMessages30D, r.UserMessagesTotal, r.CapabilityUsage, r.LastUsedAt, nullString(r.LastCapability), nullString(r.BusinessStage), nullString(r.MainLineType), nullString(r.UserSegment), r.FocusTopics, nullString(r.PainTag), customer, r.IdentityState, r.SourceUpdatedAt})
	}
	columns := []string{"projection_id", "subject_digest", "user_ref", "stage", "subscription_tier", "subscription_expires_at", "monthly_chat_quota", "current_period_used", "consultation_limit", "consultation_used", "membership_attribution", "sessions_7d", "sessions_30d", "sessions_total", "user_messages_7d", "user_messages_30d", "user_messages_total", "capability_usage", "last_used_at", "last_capability", "business_stage", "main_line_type", "user_segment", "focus_topics", "pain_tag", "customer_id", "identity_state", "source_updated_at"}
	if len(rows) > 0 {
		if n, copyErr := tx.CopyFrom(ctx, pgx.Identifier{"hxc_dashboard_rows"}, columns, pgx.CopyFromRows(rows)); copyErr != nil || n != int64(len(rows)) {
			return 0, fmt.Errorf("copy projection rows: %w", copyErr)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE hxc_dashboard_refresh_runs SET status='succeeded',projection_id=$2,source_count=$3,processed_count=$3,error_code=NULL,completed_at=now(),updated_at=now(),version=version+1 WHERE id=$1 AND status='publishing'`, runID, id, len(rows)); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM hxc_dashboard_versions WHERE id IN (SELECT id FROM hxc_dashboard_versions WHERE status='superseded' ORDER BY published_at DESC OFFSET 7)`); err != nil {
		return 0, err
	}
	return id, nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

type Summary struct {
	ID               int64
	AsOf             time.Time
	Watermark        *time.Time
	PublishedAt      time.Time
	SourceDigest     [32]byte
	ProjectionDigest [32]byte
	Counts           domain.Counts
}

func (store *PostgreSQL) Summary(ctx context.Context) (Summary, error) {
	var s Summary
	var sourceDigest, projectionDigest []byte
	err := store.pool.QueryRow(ctx, `SELECT id,projection_as_of,source_watermark,published_at,source_digest,projection_digest,total_count,active_used_count,active_unused_count,registered_no_active_membership_count,matched_count,unmatched_count,conflict_count FROM hxc_dashboard_versions WHERE status='published'`).Scan(&s.ID, &s.AsOf, &s.Watermark, &s.PublishedAt, &sourceDigest, &projectionDigest, &s.Counts.Total, &s.Counts.ActiveUsed, &s.Counts.ActiveUnused, &s.Counts.RegisteredNoActiveMembership, &s.Counts.Matched, &s.Counts.Unmatched, &s.Counts.Conflict)
	if errors.Is(err, pgx.ErrNoRows) {
		return Summary{}, ErrNotFound
	}
	copy(s.SourceDigest[:], sourceDigest)
	copy(s.ProjectionDigest[:], projectionDigest)
	return s, err
}

type Query struct {
	ProjectionID                                                                              int64
	Stages, SubscriptionTiers, LastCapabilities, BusinessStages, UserSegments, IdentityStates []string
	SubjectDigest                                                                             []byte
	Sort                                                                                      string
	GroupBy                                                                                   string
	Limit, Offset                                                                             int
}
type Row struct {
	UserRef               string          `json:"user_ref"`
	Stage                 string          `json:"stage"`
	SubscriptionTier      string          `json:"subscription_tier"`
	SubscriptionExpiresAt *time.Time      `json:"subscription_expires_at,omitempty"`
	MonthlyChatQuota      int64           `json:"monthly_chat_quota"`
	CurrentPeriodUsed     int64           `json:"current_period_used"`
	ConsultationLimit     int64           `json:"consultation_limit"`
	ConsultationUsed      int64           `json:"consultation_used"`
	MembershipAttribution string          `json:"membership_attribution"`
	Sessions7D            int64           `json:"sessions_7d"`
	Sessions30D           int64           `json:"sessions_30d"`
	SessionsTotal         int64           `json:"sessions_total"`
	UserMessages7D        int64           `json:"user_messages_7d"`
	UserMessages30D       int64           `json:"user_messages_30d"`
	UserMessagesTotal     int64           `json:"user_messages_total"`
	CapabilityUsage       json.RawMessage `json:"capability_usage"`
	LastUsedAt            *time.Time      `json:"last_used_at,omitempty"`
	LastCapability        string          `json:"last_capability,omitempty"`
	BusinessStage         string          `json:"business_stage,omitempty"`
	MainLineType          string          `json:"main_line_type,omitempty"`
	UserSegment           string          `json:"user_segment,omitempty"`
	FocusTopics           json.RawMessage `json:"focus_topics"`
	PainTag               string          `json:"pain_tag,omitempty"`
	IdentityState         string          `json:"identity_state"`
	SourceUpdatedAt       time.Time       `json:"source_updated_at"`
}
type Group struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

func (store *PostgreSQL) QueryRows(ctx context.Context, q Query) ([]Row, []Group, bool, error) {
	where := []string{"projection_id=$1"}
	args := []any{q.ProjectionID}
	addArray := func(column string, values []string) {
		if len(values) > 0 {
			args = append(args, values)
			where = append(where, fmt.Sprintf("%s=ANY($%d)", column, len(args)))
		}
	}
	addArray("stage", q.Stages)
	addArray("subscription_tier", q.SubscriptionTiers)
	addArray("last_capability", q.LastCapabilities)
	addArray("business_stage", q.BusinessStages)
	addArray("user_segment", q.UserSegments)
	addArray("identity_state", q.IdentityStates)
	if len(q.SubjectDigest) > 0 {
		args = append(args, q.SubjectDigest)
		where = append(where, fmt.Sprintf("subject_digest=$%d", len(args)))
	}
	order := map[string]string{"last_used_at_desc": "last_used_at DESC NULLS LAST,subject_digest", "source_updated_at_desc": "source_updated_at DESC,subject_digest", "subscription_expires_at_asc": "subscription_expires_at ASC NULLS LAST,subject_digest", "subscription_expires_at_desc": "subscription_expires_at DESC NULLS LAST,subject_digest", "messages_7d_desc": "user_messages_7d DESC,subject_digest"}[q.Sort]
	if order == "" {
		order = "last_used_at DESC NULLS LAST,subject_digest"
	}
	args = append(args, q.Limit+1, q.Offset)
	sqlText := `SELECT user_ref,stage,subscription_tier,subscription_expires_at,monthly_chat_quota,current_period_used,consultation_limit,consultation_used,membership_attribution,sessions_7d,sessions_30d,sessions_total,user_messages_7d,user_messages_30d,user_messages_total,capability_usage,last_used_at,COALESCE(last_capability,''),COALESCE(business_stage,''),COALESCE(main_line_type,''),COALESCE(user_segment,''),focus_topics,COALESCE(pain_tag,''),identity_state,source_updated_at FROM hxc_dashboard_rows WHERE ` + strings.Join(where, " AND ") + " ORDER BY " + order + fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := store.pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, nil, false, err
	}
	defer rows.Close()
	items := make([]Row, 0, q.Limit+1)
	for rows.Next() {
		var row Row
		if err = rows.Scan(&row.UserRef, &row.Stage, &row.SubscriptionTier, &row.SubscriptionExpiresAt, &row.MonthlyChatQuota, &row.CurrentPeriodUsed, &row.ConsultationLimit, &row.ConsultationUsed, &row.MembershipAttribution, &row.Sessions7D, &row.Sessions30D, &row.SessionsTotal, &row.UserMessages7D, &row.UserMessages30D, &row.UserMessagesTotal, &row.CapabilityUsage, &row.LastUsedAt, &row.LastCapability, &row.BusinessStage, &row.MainLineType, &row.UserSegment, &row.FocusTopics, &row.PainTag, &row.IdentityState, &row.SourceUpdatedAt); err != nil {
			return nil, nil, false, err
		}
		items = append(items, row)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, false, err
	}
	more := len(items) > q.Limit
	if more {
		items = items[:q.Limit]
	}
	groups := []Group{}
	groupColumn := map[string]string{"stage": "stage", "subscription_tier": "subscription_tier", "last_capability": "last_capability", "business_stage": "business_stage", "user_segment": "user_segment", "identity_state": "identity_state"}[q.GroupBy]
	if groupColumn != "" {
		groupArgs := args[:len(args)-2]
		groupRows, groupErr := store.pool.Query(ctx, `SELECT COALESCE(`+groupColumn+`,'(empty)'),COUNT(*) FROM hxc_dashboard_rows WHERE `+strings.Join(where, " AND ")+` GROUP BY `+groupColumn+` ORDER BY COUNT(*) DESC,1`, groupArgs...)
		if groupErr != nil {
			return nil, nil, false, groupErr
		}
		defer groupRows.Close()
		for groupRows.Next() {
			var group Group
			if err = groupRows.Scan(&group.Key, &group.Count); err != nil {
				return nil, nil, false, err
			}
			groups = append(groups, group)
		}
	}
	return items, groups, more, nil
}
