package provider

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
)

const BatchSize = 1000
const sourceTimeout = 20 * time.Minute

var ErrSourceNotReady = errors.New("hxc source is not ready")

type MySQL struct{ db *sql.DB }

func Open(dsn string) (*MySQL, error) {
	if strings.TrimSpace(dsn) != dsn || dsn == "" {
		return nil, ErrSourceNotReady
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, ErrSourceNotReady
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 2 * time.Minute
	cfg.WriteTimeout = 5 * time.Second
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, ErrSourceNotReady
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(10 * time.Minute)
	return &MySQL{db: db}, nil
}

func (source *MySQL) Close() error {
	if source == nil || source.db == nil {
		return nil
	}
	return source.db.Close()
}
func (source *MySQL) Ready() bool { return source != nil && source.db != nil }

var requiredColumns = map[string][]string{
	"new_version_users":               {"id", "unionid", "phone", "member_level", "member_expires_at", "updated_at", "is_deleted"},
	"new_version_user_subscriptions":  {"user_id", "tier", "expires_at", "monthly_chat_quota", "current_period_used", "updated_at"},
	"new_version_memberships":         {"id", "user_id", "phone", "status", "consultation_limit", "consultation_used", "start_date", "end_date", "created_at", "updated_at"},
	"new_version_conversations":       {"user_id", "lesson_id", "content_type", "chat_mode", "created_at", "updated_at", "is_deleted"},
	"new_version_messages":            {"user_id", "role", "created_at", "is_deleted"},
	"new_version_consultation_states": {"user_id", "session_id", "is_deep_consult", "session_type", "started_at", "ended_at", "created_at", "updated_at"},
	"new_version_assessments":         {"user_id", "status", "completed_at", "created_at", "updated_at"},
	"new_version_growth_reviews":      {"user_id", "surfaced_at", "created_at"},
	"new_version_user_backgrounds":    {"user_id", "business_stage", "main_line_type", "focus_topics", "pain_tag", "updated_at"},
	"new_version_user_diagnoses":      {"user_id", "stage", "main_line_type", "user_segment", "updated_at"},
	"new_version_user_interests":      {"user_id", "interest_keys", "updated_at"},
}

func (source *MySQL) Preflight(ctx context.Context) error {
	if !source.Ready() {
		return ErrSourceNotReady
	}
	for table, columns := range requiredColumns {
		for _, column := range columns {
			var exists int
			if err := source.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name=?`, table, column).Scan(&exists); err != nil || exists != 1 {
				return fmt.Errorf("hxc_schema_missing:%s.%s", table, column)
			}
		}
	}
	rows, err := source.db.QueryContext(ctx, "EXPLAIN "+currentBatchSQL, batchArgs("", time.Now().UTC())...)
	if err != nil {
		return fmt.Errorf("hxc_explain_failed: %w", err)
	}
	return rows.Close()
}

func (source *MySQL) ReadSnapshot(ctx context.Context, asOf time.Time) (port.Snapshot, error) {
	if !source.Ready() {
		return port.Snapshot{}, ErrSourceNotReady
	}
	ctx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	tx, err := source.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return port.Snapshot{}, fmt.Errorf("begin HXC snapshot: %w", err)
	}
	defer tx.Rollback()
	result := port.Snapshot{AsOf: asOf.UTC(), Rows: make([]domain.SourceRow, 0, BatchSize)}
	after := ""
	hasher := sha256.New()
	for {
		batch, err := readBatch(ctx, tx, after, asOf.UTC())
		if err != nil {
			return port.Snapshot{}, err
		}
		if len(batch) == 0 {
			break
		}
		for i := range batch {
			row := batch[i]
			encoded, _ := json.Marshal(row)
			var size [8]byte
			binary.BigEndian.PutUint64(size[:], uint64(len(encoded)))
			hasher.Write(size[:])
			hasher.Write(encoded)
			result.Rows = append(result.Rows, row)
			if result.Watermark == nil || row.SourceUpdatedAt.After(*result.Watermark) {
				value := row.SourceUpdatedAt
				result.Watermark = &value
			}
		}
		after = batch[len(batch)-1].HXCUserID
		if len(batch) < BatchSize {
			break
		}
	}
	copy(result.Digest[:], hasher.Sum(nil))
	if err = tx.Commit(); err != nil {
		return port.Snapshot{}, fmt.Errorf("commit HXC read-only snapshot: %w", err)
	}
	return result, nil
}

func readBatch(ctx context.Context, tx *sql.Tx, after string, asOf time.Time) ([]domain.SourceRow, error) {
	rows, err := tx.QueryContext(ctx, currentBatchSQL, batchArgs(after, asOf)...)
	if err != nil {
		return nil, fmt.Errorf("query HXC batch: %w", err)
	}
	defer rows.Close()
	result := make([]domain.SourceRow, 0, BatchSize)
	for rows.Next() {
		var row domain.SourceRow
		var union, lastCapability, business, mainline, segment, pain sql.NullString
		var expiry sql.NullTime
		// MySQL reports the GREATEST/NULLIF expression as []byte even with
		// parseTime enabled. mysql.NullTime accepts both expression bytes and
		// native time.Time values; the source connection is normalized to UTC.
		var lastUsed mysql.NullTime
		var capJSON, topicsJSON []byte
		if err = rows.Scan(&row.HXCUserID, &union, &row.SubscriptionTier, &expiry, &row.MonthlyChatQuota, &row.CurrentPeriodUsed, &row.ConsultationLimit, &row.ConsultationUsed, &row.MembershipAttribution, &row.Sessions7D, &row.Sessions30D, &row.SessionsTotal, &row.UserMessages7D, &row.UserMessages30D, &row.UserMessagesTotal, &capJSON, &lastUsed, &lastCapability, &business, &mainline, &segment, &topicsJSON, &pain, &row.SourceUpdatedAt); err != nil {
			return nil, fmt.Errorf("scan HXC batch: %w", err)
		}
		row.UnionID = union.String
		if expiry.Valid {
			row.SubscriptionExpiresAt = &expiry.Time
		}
		if lastUsed.Valid {
			row.LastUsedAt = &lastUsed.Time
		}
		row.CapabilityUsage = append([]byte(nil), capJSON...)
		row.FocusTopics = append([]byte(nil), topicsJSON...)
		row.LastCapability = lastCapability.String
		row.BusinessStage = business.String
		row.MainLineType = mainline.String
		row.UserSegment = segment.String
		row.PainTag = pain.String
		result = append(result, row)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate HXC batch: %w", err)
	}
	return result, nil
}

func batchArgs(after string, asOf time.Time) []any {
	args := []any{after, BatchSize}
	for range 15 {
		args = append(args, asOf)
	}
	return args
}

const currentBatchSQL = `WITH active_users AS (
 SELECT id,unionid,phone,member_level,member_expires_at,updated_at FROM new_version_users WHERE is_deleted=0 AND id>? ORDER BY id LIMIT ?
), phone_counts AS (SELECT phone,COUNT(*) n FROM new_version_users WHERE is_deleted=0 AND phone IS NOT NULL AND TRIM(phone)<>'' GROUP BY phone),
membership_ranked AS (
 SELECT u.id user_id,m.consultation_limit,m.consultation_used,IF(m.user_id=u.id,'user_id','unique_phone') attribution,COALESCE(m.updated_at,m.created_at,m.end_date,m.start_date) source_updated_at,
 ROW_NUMBER() OVER(PARTITION BY u.id ORDER BY (m.user_id=u.id) DESC,(m.status='active' AND m.end_date>=?) DESC,(m.status='active') DESC,m.end_date DESC,COALESCE(m.updated_at,m.created_at,m.end_date,m.start_date) DESC,m.id DESC) row_num
 FROM active_users u LEFT JOIN phone_counts pc ON pc.phone=u.phone AND pc.n=1 JOIN new_version_memberships m ON m.user_id=u.id OR ((m.user_id IS NULL OR m.user_id='') AND pc.phone IS NOT NULL AND m.phone=pc.phone)
), membership_current AS (SELECT user_id,consultation_limit,consultation_used,attribution,source_updated_at FROM membership_ranked WHERE row_num=1),
conversation_usage AS (SELECT c.user_id,COUNT(*) sessions_total,SUM(c.created_at>=? - INTERVAL 30 DAY) sessions_30d,SUM(c.created_at>=? - INTERVAL 7 DAY) sessions_7d,
 SUM(NOT((c.lesson_id IS NOT NULL AND c.lesson_id<>'') OR c.content_type='lesson') AND c.chat_mode='peer') peer_total,SUM(NOT((c.lesson_id IS NOT NULL AND c.lesson_id<>'') OR c.content_type='lesson') AND c.chat_mode='peer' AND c.created_at>=? - INTERVAL 30 DAY) peer_30d,SUM(NOT((c.lesson_id IS NOT NULL AND c.lesson_id<>'') OR c.content_type='lesson') AND c.chat_mode='peer' AND c.created_at>=? - INTERVAL 7 DAY) peer_7d,MAX(CASE WHEN NOT((c.lesson_id IS NOT NULL AND c.lesson_id<>'') OR c.content_type='lesson') AND c.chat_mode='peer' THEN c.updated_at END) peer_last,
 SUM((c.lesson_id IS NOT NULL AND c.lesson_id<>'') OR c.content_type='lesson') lesson_total,SUM(((c.lesson_id IS NOT NULL AND c.lesson_id<>'') OR c.content_type='lesson') AND c.created_at>=? - INTERVAL 30 DAY) lesson_30d,SUM(((c.lesson_id IS NOT NULL AND c.lesson_id<>'') OR c.content_type='lesson') AND c.created_at>=? - INTERVAL 7 DAY) lesson_7d,MAX(CASE WHEN (c.lesson_id IS NOT NULL AND c.lesson_id<>'') OR c.content_type='lesson' THEN c.updated_at END) lesson_last,MAX(c.updated_at) source_updated_at FROM new_version_conversations c JOIN active_users u ON u.id=c.user_id WHERE c.is_deleted=0 GROUP BY c.user_id),
message_usage AS (SELECT m.user_id,COUNT(*) messages_total,SUM(m.created_at>=? - INTERVAL 30 DAY) messages_30d,SUM(m.created_at>=? - INTERVAL 7 DAY) messages_7d,MAX(m.created_at) last_used,MAX(m.created_at) source_updated_at FROM new_version_messages m JOIN active_users u ON u.id=m.user_id WHERE m.is_deleted=0 AND m.role='user' GROUP BY m.user_id),
coach_usage AS (SELECT c.user_id,COUNT(DISTINCT c.session_id) total,SUM(COALESCE(c.started_at,c.created_at,c.updated_at)>=? - INTERVAL 30 DAY) count_30d,SUM(COALESCE(c.started_at,c.created_at,c.updated_at)>=? - INTERVAL 7 DAY) count_7d,MAX(COALESCE(c.ended_at,c.updated_at,c.started_at,c.created_at)) last_used,MAX(COALESCE(c.updated_at,c.ended_at,c.started_at,c.created_at)) source_updated_at FROM new_version_consultation_states c JOIN active_users u ON u.id=c.user_id WHERE c.is_deep_consult=1 OR c.session_type='topic_consult' GROUP BY c.user_id),
assessment_usage AS (SELECT a.user_id,COUNT(*) total,SUM(COALESCE(a.completed_at,a.updated_at,a.created_at)>=? - INTERVAL 30 DAY) count_30d,SUM(COALESCE(a.completed_at,a.updated_at,a.created_at)>=? - INTERVAL 7 DAY) count_7d,MAX(COALESCE(a.completed_at,a.updated_at,a.created_at)) last_used,MAX(COALESCE(a.updated_at,a.completed_at,a.created_at)) source_updated_at FROM new_version_assessments a JOIN active_users u ON a.user_id COLLATE utf8mb4_general_ci=u.id WHERE a.status='completed' GROUP BY a.user_id),
review_usage AS (SELECT r.user_id,COUNT(*) total,SUM(COALESCE(r.surfaced_at,r.created_at)>=? - INTERVAL 30 DAY) count_30d,SUM(COALESCE(r.surfaced_at,r.created_at)>=? - INTERVAL 7 DAY) count_7d,MAX(COALESCE(r.surfaced_at,r.created_at)) last_used,MAX(COALESCE(r.surfaced_at,r.created_at)) source_updated_at FROM new_version_growth_reviews r JOIN active_users u ON r.user_id COLLATE utf8mb4_general_ci=u.id GROUP BY r.user_id)
SELECT u.id,NULLIF(TRIM(u.unionid),''),COALESCE(NULLIF(TRIM(s.tier),''),NULLIF(TRIM(u.member_level),''),'free'),COALESCE(s.expires_at,u.member_expires_at),GREATEST(COALESCE(s.monthly_chat_quota,0),0),GREATEST(COALESCE(s.current_period_used,0),0),GREATEST(COALESCE(mc.consultation_limit,0),0),GREATEST(COALESCE(mc.consultation_used,0),0),COALESCE(mc.attribution,'none'),COALESCE(c.sessions_7d,0),COALESCE(c.sessions_30d,0),COALESCE(c.sessions_total,0),COALESCE(msg.messages_7d,0),COALESCE(msg.messages_30d,0),COALESCE(msg.messages_total,0),
JSON_OBJECT('peer_chat',JSON_OBJECT('count_7d',COALESCE(c.peer_7d,0),'count_30d',COALESCE(c.peer_30d,0),'count_total',COALESCE(c.peer_total,0),'last_used_at',c.peer_last),'coach_consult',JSON_OBJECT('count_7d',COALESCE(coach.count_7d,0),'count_30d',COALESCE(coach.count_30d,0),'count_total',COALESCE(coach.total,0),'last_used_at',coach.last_used),'lesson',JSON_OBJECT('count_7d',COALESCE(c.lesson_7d,0),'count_30d',COALESCE(c.lesson_30d,0),'count_total',COALESCE(c.lesson_total,0),'last_used_at',c.lesson_last),'assessment',JSON_OBJECT('count_7d',COALESCE(a.count_7d,0),'count_30d',COALESCE(a.count_30d,0),'count_total',COALESCE(a.total,0),'last_used_at',a.last_used),'weekly_review',JSON_OBJECT('count_7d',COALESCE(r.count_7d,0),'count_30d',COALESCE(r.count_30d,0),'count_total',COALESCE(r.total,0),'last_used_at',r.last_used)),
NULLIF(GREATEST(COALESCE(c.peer_last,TIMESTAMP('1000-01-01 00:00:00')),COALESCE(coach.last_used,TIMESTAMP('1000-01-01 00:00:00')),COALESCE(c.lesson_last,TIMESTAMP('1000-01-01 00:00:00')),COALESCE(a.last_used,TIMESTAMP('1000-01-01 00:00:00')),COALESCE(r.last_used,TIMESTAMP('1000-01-01 00:00:00')),COALESCE(msg.last_used,TIMESTAMP('1000-01-01 00:00:00'))),TIMESTAMP('1000-01-01 00:00:00')),
CASE WHEN GREATEST(COALESCE(r.last_used,'1000-01-01'),COALESCE(a.last_used,'1000-01-01'),COALESCE(c.lesson_last,'1000-01-01'),COALESCE(coach.last_used,'1000-01-01'),COALESCE(c.peer_last,'1000-01-01'),COALESCE(msg.last_used,'1000-01-01'))='1000-01-01' THEN NULL WHEN COALESCE(msg.last_used,'1000-01-01')>=GREATEST(COALESCE(r.last_used,'1000-01-01'),COALESCE(a.last_used,'1000-01-01'),COALESCE(c.lesson_last,'1000-01-01'),COALESCE(coach.last_used,'1000-01-01'),COALESCE(c.peer_last,'1000-01-01')) THEN 'user_message' WHEN COALESCE(r.last_used,'1000-01-01')>=GREATEST(COALESCE(a.last_used,'1000-01-01'),COALESCE(c.lesson_last,'1000-01-01'),COALESCE(coach.last_used,'1000-01-01'),COALESCE(c.peer_last,'1000-01-01')) THEN 'weekly_review' WHEN COALESCE(a.last_used,'1000-01-01')>=GREATEST(COALESCE(c.lesson_last,'1000-01-01'),COALESCE(coach.last_used,'1000-01-01'),COALESCE(c.peer_last,'1000-01-01')) THEN 'assessment' WHEN COALESCE(c.lesson_last,'1000-01-01')>=GREATEST(COALESCE(coach.last_used,'1000-01-01'),COALESCE(c.peer_last,'1000-01-01')) THEN 'lesson' WHEN COALESCE(coach.last_used,'1000-01-01')>=COALESCE(c.peer_last,'1000-01-01') THEN 'coach_consult' ELSE 'peer_chat' END,
COALESCE(NULLIF(TRIM(bg.business_stage),''),NULLIF(TRIM(d.stage),'')),COALESCE(NULLIF(TRIM(bg.main_line_type),''),NULLIF(TRIM(d.main_line_type),'')),NULLIF(TRIM(d.user_segment),''),CASE WHEN JSON_TYPE(bg.focus_topics)='ARRAY' AND JSON_LENGTH(bg.focus_topics)>0 THEN bg.focus_topics WHEN JSON_TYPE(i.interest_keys)='ARRAY' THEN i.interest_keys ELSE JSON_ARRAY() END,NULLIF(TRIM(bg.pain_tag),''),
GREATEST(u.updated_at,COALESCE(s.updated_at,u.updated_at),COALESCE(mc.source_updated_at,u.updated_at),COALESCE(c.source_updated_at,u.updated_at),COALESCE(msg.source_updated_at,u.updated_at),COALESCE(coach.source_updated_at,u.updated_at),COALESCE(a.source_updated_at,u.updated_at),COALESCE(r.source_updated_at,u.updated_at),COALESCE(bg.updated_at,u.updated_at),COALESCE(d.updated_at,u.updated_at),COALESCE(i.updated_at,u.updated_at))
FROM active_users u LEFT JOIN new_version_user_subscriptions s ON s.user_id COLLATE utf8mb4_general_ci=u.id LEFT JOIN membership_current mc ON mc.user_id=u.id LEFT JOIN conversation_usage c ON c.user_id=u.id LEFT JOIN message_usage msg ON msg.user_id=u.id LEFT JOIN coach_usage coach ON coach.user_id=u.id LEFT JOIN assessment_usage a ON a.user_id COLLATE utf8mb4_general_ci=u.id LEFT JOIN review_usage r ON r.user_id COLLATE utf8mb4_general_ci=u.id LEFT JOIN new_version_user_backgrounds bg ON bg.user_id COLLATE utf8mb4_general_ci=u.id LEFT JOIN new_version_user_diagnoses d ON d.user_id COLLATE utf8mb4_general_ci=u.id LEFT JOIN new_version_user_interests i ON i.user_id COLLATE utf8mb4_general_ci=u.id ORDER BY u.id`
