package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

const configurationColumns = `id,package_id,version,schema_version,definition,refresh_cron_utc,digest,created_by,created_at`

func scanConfiguration(row pgx.Row) (segmentdomain.ConfigurationVersion, error) {
	var item segmentdomain.ConfigurationVersion
	var digest []byte
	err := row.Scan(&item.ID, &item.PackageID, &item.Version, &item.SchemaVersion, &item.Definition, &item.RefreshCronUTC, &digest, &item.CreatedBy, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return segmentdomain.ConfigurationVersion{}, ErrNotFound
	}
	if err != nil {
		return segmentdomain.ConfigurationVersion{}, err
	}
	if len(digest) != sha256.Size {
		return segmentdomain.ConfigurationVersion{}, ErrConflict
	}
	copy(item.Digest[:], digest)
	return item, nil
}

func (r *Repository) CreateConfigurationVersion(ctx context.Context, item segmentdomain.ConfigurationVersion) (segmentdomain.ConfigurationVersion, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.ConfigurationVersion{}, err
	}
	query := `INSERT INTO segment_audience_configuration_versions(package_id,version,schema_version,definition,refresh_cron_utc,digest,created_by,created_at)
		VALUES($1,$2,$3,$4::jsonb,NULLIF($5,''),$6,$7,$8) RETURNING ` + configurationColumns
	created, err := scanConfiguration(t.QueryRow(ctx, query, item.PackageID, item.Version, item.SchemaVersion, item.Definition, item.RefreshCronUTC, item.Digest[:], item.CreatedBy, item.CreatedAt))
	if unique(err) {
		return segmentdomain.ConfigurationVersion{}, ErrConflict
	}
	return created, err
}

func (r *Repository) SetCurrentConfiguration(ctx context.Context, packageID, configurationID, expectedPackageVersion, actor int64, now time.Time) (segmentdomain.Package, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.Package{}, err
	}
	query := `UPDATE segment_audience_packages SET current_configuration_version_id=$2,version=version+1,updated_by=$4,updated_at=$5
		WHERE id=$1 AND version=$3 AND lifecycle='paused' RETURNING ` + packageColumns
	updated, err := scanPackage(t.QueryRow(ctx, query, packageID, configurationID, expectedPackageVersion, actor, now))
	if errors.Is(err, ErrNotFound) {
		return segmentdomain.Package{}, ErrConflict
	}
	return updated, err
}
