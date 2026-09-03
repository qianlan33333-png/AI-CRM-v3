package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
)

const bindingColumns = `id,package_id,version,agent_id,automation_type,agent_published_version,content_digest,materials_digest,created_by,created_at`

func scanBinding(row pgx.Row) (segmentdomain.AutomationBinding, error) {
	var out segmentdomain.AutomationBinding
	var kind string
	var content, materials []byte
	err := row.Scan(&out.ID, &out.PackageID, &out.Version, &out.AgentID, &kind, &out.AgentPublishedVersion, &content, &materials, &out.CreatedBy, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if len(content) != sha256.Size || len(materials) != sha256.Size {
		return out, ErrConflict
	}
	copy(out.ContentDigest[:], content)
	copy(out.MaterialsDigest[:], materials)
	out.AutomationType = automationport.AutomationType(kind)
	return out, nil
}
func (r *Repository) CurrentBinding(ctx context.Context, packageID int64) (segmentdomain.AutomationBinding, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.AutomationBinding{}, err
	}
	return scanBinding(t.QueryRow(ctx, `SELECT `+bindingColumns+` FROM segment_audience_packages p JOIN segment_audience_automation_binding_versions b ON b.id=p.current_automation_binding_id AND b.package_id=p.id WHERE p.id=$1`, packageID))
}
func (r *Repository) CreateBinding(ctx context.Context, item segmentdomain.AutomationBinding) (segmentdomain.AutomationBinding, error) {
	t, err := tx(ctx)
	if err != nil {
		return item, err
	}
	var version int64
	if err = t.QueryRow(ctx, `SELECT COALESCE(max(version),0)+1 FROM segment_audience_automation_binding_versions WHERE package_id=$1`, item.PackageID).Scan(&version); err != nil {
		return item, err
	}
	item.Version = version
	return scanBinding(t.QueryRow(ctx, `INSERT INTO segment_audience_automation_binding_versions(package_id,version,agent_id,automation_type,agent_published_version,content_digest,materials_digest,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+bindingColumns, item.PackageID, item.Version, item.AgentID, item.AutomationType, item.AgentPublishedVersion, item.ContentDigest[:], item.MaterialsDigest[:], item.CreatedBy, item.CreatedAt))
}
func (r *Repository) SetCurrentBinding(ctx context.Context, packageID, bindingID, expectedVersion, actor int64, now time.Time) (segmentdomain.Package, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.Package{}, err
	}
	item, err := scanPackage(t.QueryRow(ctx, `UPDATE segment_audience_packages SET current_automation_binding_id=$2,version=version+1,updated_by=$4,updated_at=$5 WHERE id=$1 AND version=$3 AND lifecycle='paused' RETURNING `+packageColumns, packageID, bindingID, expectedVersion, actor, now))
	if errors.Is(err, ErrNotFound) {
		return item, ErrConflict
	}
	return item, err
}

func (r *Repository) CurrentSenderSet(ctx context.Context, packageID int64) (segmentdomain.SenderSet, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.SenderSet{}, err
	}
	var out segmentdomain.SenderSet
	err = t.QueryRow(ctx, `SELECT s.id,s.package_id,s.version,s.created_by,s.created_at FROM segment_audience_packages p JOIN segment_audience_sender_sets s ON s.id=p.current_sender_set_id AND s.package_id=p.id WHERE p.id=$1`, packageID).Scan(&out.ID, &out.PackageID, &out.Version, &out.CreatedBy, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	rows, err := t.Query(ctx, `SELECT sort_order,staff_id,eligibility_version,eligibility_refreshed_at FROM segment_audience_sender_set_members WHERE sender_set_id=$1 ORDER BY sort_order`, out.ID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item segmentdomain.Sender
		if err = rows.Scan(&item.SortOrder, &item.StaffID, &item.EligibilityVersion, &item.EligibilityRefreshedAt); err != nil {
			return out, err
		}
		out.Members = append(out.Members, item)
	}
	return out, rows.Err()
}
func (r *Repository) CreateSenderSet(ctx context.Context, item segmentdomain.SenderSet) (segmentdomain.SenderSet, error) {
	t, err := tx(ctx)
	if err != nil {
		return item, err
	}
	if len(item.Members) < 1 || len(item.Members) > 5 {
		return item, ErrInvalid
	}
	if err = t.QueryRow(ctx, `SELECT COALESCE(max(version),0)+1 FROM segment_audience_sender_sets WHERE package_id=$1`, item.PackageID).Scan(&item.Version); err != nil {
		return item, err
	}
	if err = t.QueryRow(ctx, `INSERT INTO segment_audience_sender_sets(package_id,version,created_by,created_at) VALUES($1,$2,$3,$4) RETURNING id`, item.PackageID, item.Version, item.CreatedBy, item.CreatedAt).Scan(&item.ID); err != nil {
		return item, err
	}
	for index, member := range item.Members {
		if member.StaffID < 1 || member.EligibilityVersion < 1 || member.EligibilityRefreshedAt.IsZero() {
			return item, ErrInvalid
		}
		member.SortOrder = index + 1
		item.Members[index] = member
		if _, err = t.Exec(ctx, `INSERT INTO segment_audience_sender_set_members(sender_set_id,sort_order,staff_id,eligibility_version,eligibility_refreshed_at) VALUES($1,$2,$3,$4,$5)`, item.ID, member.SortOrder, member.StaffID, member.EligibilityVersion, member.EligibilityRefreshedAt); err != nil {
			if unique(err) {
				return item, ErrConflict
			}
			return item, err
		}
	}
	return item, nil
}
func (r *Repository) SetCurrentSenderSet(ctx context.Context, packageID, senderSetID, expectedVersion, actor int64, now time.Time) (segmentdomain.Package, error) {
	t, err := tx(ctx)
	if err != nil {
		return segmentdomain.Package{}, err
	}
	item, err := scanPackage(t.QueryRow(ctx, `UPDATE segment_audience_packages SET current_sender_set_id=$2,version=version+1,updated_by=$4,updated_at=$5 WHERE id=$1 AND version=$3 AND lifecycle='paused' RETURNING `+packageColumns, packageID, senderSetID, expectedVersion, actor, now))
	if errors.Is(err, ErrNotFound) {
		return item, ErrConflict
	}
	return item, err
}
