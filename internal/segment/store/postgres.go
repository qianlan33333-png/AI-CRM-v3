// Package store owns Segment/Audience PostgreSQL records. It never reads or
// writes Customer, Identity, Access, Outbound or External Effects tables.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

var (
	ErrInvalid  = errors.New("invalid segment audience persistence command")
	ErrNotFound = errors.New("segment audience record not found")
	ErrConflict = errors.New("segment audience persistence conflict")
)

type Repository struct {
	pool *pgxpool.Pool
	uow  platformport.UnitOfWork
}

type Reservation struct {
	Operation     string
	ActorScope    string
	KeyDigest     [32]byte
	PayloadDigest [32]byte
	CreatedAt     time.Time
}

type Receipt struct {
	ID             int64
	Operation      string
	ActorScope     string
	KeyDigest      [32]byte
	PayloadDigest  [32]byte
	State          string
	ResultSnapshot json.RawMessage
	CreatedAt      time.Time
	CompletedAt    *time.Time
}

type MutationFact struct {
	ResourceKind   string
	ResourceID     int64
	Operation      string
	EventType      string
	ActorID        int64
	Payload        json.RawMessage
	IdempotencyKey string
	OccurredAt     time.Time
}

func NewPostgreSQL(pool *pgxpool.Pool, uow platformport.UnitOfWork) (*Repository, error) {
	if pool == nil || uow == nil {
		return nil, ErrInvalid
	}
	return &Repository{pool: pool, uow: uow}, nil
}

func (r *Repository) UnitOfWork() platformport.UnitOfWork { return r.uow }
func tx(ctx context.Context) (pgx.Tx, error)              { return platformpostgres.RequireTransaction(ctx) }

const groupColumns = `id,name,sort_order,version,created_by,updated_by,created_at,updated_at`

func scanGroup(row pgx.Row) (segmentdomain.Group, error) {
	var group segmentdomain.Group
	err := row.Scan(&group.ID, &group.Name, &group.SortOrder, &group.Version, &group.CreatedBy, &group.UpdatedBy, &group.CreatedAt, &group.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return segmentdomain.Group{}, ErrNotFound
	}
	return group, err
}

func (r *Repository) CreateGroup(ctx context.Context, group segmentdomain.Group) (segmentdomain.Group, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.Group{}, err
	}
	query := `INSERT INTO segment_audience_groups(name,sort_order,version,created_by,updated_by,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING ` + groupColumns
	created, err := scanGroup(t.QueryRow(ctx, query, group.Name, group.SortOrder, group.Version, group.CreatedBy, group.UpdatedBy, group.CreatedAt, group.UpdatedAt))
	if unique(err) {
		return segmentdomain.Group{}, ErrConflict
	}
	return created, err
}

func (r *Repository) LockGroup(ctx context.Context, id int64) (segmentdomain.Group, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.Group{}, err
	}
	return scanGroup(t.QueryRow(ctx, `SELECT `+groupColumns+` FROM segment_audience_groups WHERE id=$1 FOR UPDATE`, id))
}

func (r *Repository) UpdateGroup(ctx context.Context, group segmentdomain.Group, expectedVersion int64) (segmentdomain.Group, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.Group{}, err
	}
	query := `UPDATE segment_audience_groups SET name=$2,sort_order=$3,version=$4,updated_by=$5,updated_at=$6
		WHERE id=$1 AND version=$7 RETURNING ` + groupColumns
	updated, err := scanGroup(t.QueryRow(ctx, query, group.ID, group.Name, group.SortOrder, group.Version, group.UpdatedBy, group.UpdatedAt, expectedVersion))
	if unique(err) || errors.Is(err, ErrNotFound) {
		return segmentdomain.Group{}, ErrConflict
	}
	return updated, err
}

func (r *Repository) DeleteEmptyGroup(ctx context.Context, id, expectedVersion int64) error {
	t, err := tx(ctx)
	if err != nil {
		return err
	}
	command, err := t.Exec(ctx, `DELETE FROM segment_audience_groups g WHERE g.id=$1 AND g.version=$2 AND NOT EXISTS (SELECT 1 FROM segment_audience_packages p WHERE p.group_id=g.id)`, id, expectedVersion)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

const packageColumns = `id,group_id,code,name,lifecycle,version,current_configuration_version_id,published_snapshot_id,created_by,updated_by,created_at,updated_at,archived_at`

func scanPackage(row pgx.Row) (segmentdomain.Package, error) {
	var item segmentdomain.Package
	var lifecycle string
	err := row.Scan(&item.ID, &item.GroupID, &item.Code, &item.Name, &lifecycle, &item.Version, &item.CurrentConfigurationVersionID, &item.PublishedSnapshotID, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt, &item.ArchivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return segmentdomain.Package{}, ErrNotFound
	}
	item.Lifecycle = segmentdomain.Lifecycle(lifecycle)
	return item, err
}

func (r *Repository) CreatePackage(ctx context.Context, item segmentdomain.Package) (segmentdomain.Package, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.Package{}, err
	}
	query := `INSERT INTO segment_audience_packages(group_id,code,name,lifecycle,version,current_configuration_version_id,created_by,updated_by,created_at,updated_at,archived_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING ` + packageColumns
	created, err := scanPackage(t.QueryRow(ctx, query, item.GroupID, item.Code, item.Name, string(item.Lifecycle), item.Version, item.CurrentConfigurationVersionID, item.CreatedBy, item.UpdatedBy, item.CreatedAt, item.UpdatedAt, item.ArchivedAt))
	if unique(err) {
		return segmentdomain.Package{}, ErrConflict
	}
	return created, err
}

func (r *Repository) GetPackage(ctx context.Context, id int64) (segmentdomain.Package, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.Package{}, err
	}
	return scanPackage(t.QueryRow(ctx, `SELECT `+packageColumns+` FROM segment_audience_packages WHERE id=$1`, id))
}

func (r *Repository) LockPackage(ctx context.Context, id int64) (segmentdomain.Package, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.Package{}, err
	}
	return scanPackage(t.QueryRow(ctx, `SELECT `+packageColumns+` FROM segment_audience_packages WHERE id=$1 FOR UPDATE`, id))
}

func (r *Repository) UpdatePackage(ctx context.Context, item segmentdomain.Package, expectedVersion int64) (segmentdomain.Package, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.Package{}, err
	}
	query := `UPDATE segment_audience_packages SET group_id=$2,name=$3,lifecycle=$4,version=$5,current_configuration_version_id=$6,updated_by=$7,updated_at=$8,archived_at=$9
		WHERE id=$1 AND version=$10 RETURNING ` + packageColumns
	updated, err := scanPackage(t.QueryRow(ctx, query, item.ID, item.GroupID, item.Name, string(item.Lifecycle), item.Version, item.CurrentConfigurationVersionID, item.UpdatedBy, item.UpdatedAt, item.ArchivedAt, expectedVersion))
	if errors.Is(err, ErrNotFound) {
		return segmentdomain.Package{}, ErrConflict
	}
	return updated, err
}
