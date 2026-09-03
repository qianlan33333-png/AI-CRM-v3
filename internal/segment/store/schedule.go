package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

func (r *Repository) ScheduledConfigurations(ctx context.Context, limit int) ([]segmentdomain.ScheduledConfiguration, error) {
	if limit < 1 || limit > 10000 {
		return nil, ErrInvalid
	}
	database, err := tx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := database.Query(ctx, `SELECT p.id,c.id,c.refresh_cron_utc,c.created_by,c.created_at,s.next_due_at,COALESCE(s.version,0)
		FROM segment_audience_packages p
		JOIN segment_audience_configuration_versions c ON c.id=p.current_configuration_version_id AND c.package_id=p.id
		LEFT JOIN segment_audience_schedule_states s ON s.configuration_version_id=c.id
		WHERE p.lifecycle='active' AND c.refresh_cron_utc IS NOT NULL
		ORDER BY p.id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []segmentdomain.ScheduledConfiguration{}
	for rows.Next() {
		var item segmentdomain.ScheduledConfiguration
		if err = rows.Scan(&item.PackageID, &item.ConfigurationVersionID, &item.CronUTC, &item.Actor, &item.ConfigurationCreatedAt, &item.NextDueAt, &item.ScheduleVersion); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ClaimScheduledOccurrence advances one configuration's durable cursor. The
// caller accepts the refresh in the same UoW; any acceptance failure rolls the
// cursor back with it.
func (r *Repository) ClaimScheduledOccurrence(ctx context.Context, item segmentdomain.ScheduledConfiguration, occurrence, next, now time.Time) (bool, error) {
	if item.PackageID < 1 || item.ConfigurationVersionID < 1 || item.Actor < 1 || item.CronUTC == "" || occurrence.IsZero() || next.IsZero() || !next.After(occurrence) || now.IsZero() {
		return false, ErrInvalid
	}
	database, err := tx(ctx)
	if err != nil {
		return false, err
	}
	var currentCron string
	var lifecycle string
	if err = database.QueryRow(ctx, `SELECT c.refresh_cron_utc,p.lifecycle FROM segment_audience_packages p JOIN segment_audience_configuration_versions c ON c.id=p.current_configuration_version_id AND c.package_id=p.id WHERE p.id=$1 AND c.id=$2 FOR UPDATE OF p`, item.PackageID, item.ConfigurationVersionID).Scan(&currentCron, &lifecycle); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if lifecycle != "active" || currentCron != item.CronUTC {
		return false, nil
	}
	if item.NextDueAt == nil {
		command, err := database.Exec(ctx, `INSERT INTO segment_audience_schedule_states(configuration_version_id,package_id,next_due_at,last_dispatched_at,version,updated_at) VALUES($1,$2,$3,$4,1,$5) ON CONFLICT(configuration_version_id) DO NOTHING`, item.ConfigurationVersionID, item.PackageID, next, occurrence, now)
		return err == nil && command.RowsAffected() == 1, err
	}
	command, err := database.Exec(ctx, `UPDATE segment_audience_schedule_states SET next_due_at=$3,last_dispatched_at=$4,version=version+1,updated_at=$5 WHERE configuration_version_id=$1 AND package_id=$2 AND next_due_at=$6 AND version=$7`, item.ConfigurationVersionID, item.PackageID, next, occurrence, now, item.NextDueAt.UTC(), item.ScheduleVersion)
	return err == nil && command.RowsAffected() == 1, err
}
