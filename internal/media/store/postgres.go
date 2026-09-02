// Package store owns PostgreSQL persistence for Media. It never reads or
// writes another domain's tables; references are opaque Media ledger facts.
package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var (
	ErrNotFound   = errors.New("media resource not found")
	ErrConflict   = errors.New("media command conflict")
	ErrReferences = errors.New("media resource has references")
	ErrInvalid    = errors.New("invalid media command")
)

type Repository struct {
	pool *pgxpool.Pool
	uow  platformport.UnitOfWork
}

func NewPostgreSQL(pool *pgxpool.Pool, uow platformport.UnitOfWork) (*Repository, error) {
	if pool == nil || uow == nil {
		return nil, ErrInvalid
	}
	return &Repository{pool: pool, uow: uow}, nil
}

func (r *Repository) Within(ctx context.Context, callback func(context.Context) error) error {
	if r == nil || r.uow == nil || callback == nil {
		return ErrInvalid
	}
	return r.uow.Within(ctx, callback)
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}
func bytesDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (r *Repository) reserve(ctx context.Context, operation, kind string, actor int64, key, command string) (json.RawMessage, bool, error) {
	if actor < 1 || len(key) < 16 || len(key) > 128 || strings.TrimSpace(key) != key || operation == "" || kind == "" {
		return nil, false, ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, false, err
	}
	keyDigest, commandDigest := digest(key), digest(command)
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO media_operation_receipts(operation,actor_admin_user_id,resource_kind,idempotency_key_digest,command_digest)
VALUES($1,$2,$3,$4,$5) ON CONFLICT(operation,actor_admin_user_id,idempotency_key_digest) DO NOTHING RETURNING id`, operation, actor, kind, keyDigest, commandDigest).Scan(&id)
	if err == nil {
		return nil, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	var existingDigest string
	var result []byte
	if err = tx.QueryRow(ctx, `SELECT command_digest,result FROM media_operation_receipts WHERE operation=$1 AND actor_admin_user_id=$2 AND idempotency_key_digest=$3`, operation, actor, keyDigest).Scan(&existingDigest, &result); err != nil {
		return nil, false, err
	}
	if existingDigest != commandDigest {
		return nil, false, ErrConflict
	}
	return json.RawMessage(result), false, nil
}

func (r *Repository) complete(ctx context.Context, operation, kind string, actor int64, key string, resourceID int64, result any, event string) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	keyDigest := digest(key)
	command, err := tx.Exec(ctx, `UPDATE media_operation_receipts SET resource_id=$1,result=$2::jsonb,completed_at=clock_timestamp()
WHERE operation=$3 AND actor_admin_user_id=$4 AND idempotency_key_digest=$5`, resourceID, encoded, operation, actor, keyDigest)
	if err != nil || command.RowsAffected() != 1 {
		if err != nil {
			return err
		}
		return ErrConflict
	}
	source := "client"
	if strings.HasPrefix(key, "server_compat_") {
		source = "server_compat"
	}
	payload, _ := json.Marshal(map[string]any{"resource_id": resourceID, "operation": operation, "idempotency_source": source})
	if _, err = tx.Exec(ctx, `INSERT INTO media_audit_events(event_type,resource_kind,resource_id,actor_admin_user_id,payload) VALUES($1,$2,$3,$4,$5::jsonb)`, event, kind, resourceID, actor, payload); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO media_outbox(event_type,aggregate_kind,aggregate_id,payload) VALUES($1,$2,$3,$4::jsonb)`, event, kind, resourceID, payload)
	return err
}

type ImageInput struct {
	FileName, MIME, Name, Description, Tags, Category string
	Content                                           []byte
	Width, Height                                     int32
	Enabled                                           bool
}

func (r *Repository) CreateImage(ctx context.Context, actor int64, key string, input ImageInput) (map[string]any, error) {
	command, err := json.Marshal(struct {
		F, M, N, D, T, C, H string
		S                   int
		W, X                int32
		E                   bool
	}{input.FileName, input.MIME, input.Name, input.Description, input.Tags, input.Category, bytesDigest(input.Content), len(input.Content), input.Width, input.Height, input.Enabled})
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = r.Within(ctx, func(txctx context.Context) error {
		replay, owned, err := r.reserve(txctx, "image.create", "image", actor, key, string(command))
		if err != nil {
			return err
		}
		if !owned {
			return json.Unmarshal(replay, &out)
		}
		tx, _ := platformpostgres.RequireTransaction(txctx)
		blob := bytesDigest(input.Content)
		if _, err = tx.Exec(txctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,$2,$3,$4) ON CONFLICT(digest) DO NOTHING`, blob, input.MIME, len(input.Content), input.Content); err != nil {
			return err
		}
		var id int64
		var created, updated time.Time
		err = tx.QueryRow(txctx, `INSERT INTO media_images(blob_digest,file_name,name,description,tags,category,mime_type,byte_size,width,height,enabled,created_by,updated_by)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12) RETURNING id,created_at,updated_at`, blob, input.FileName, input.Name, input.Description, input.Tags, input.Category, input.MIME, len(input.Content), input.Width, input.Height, input.Enabled, actor).Scan(&id, &created, &updated)
		if err != nil {
			return err
		}
		out = imageMap(id, input.FileName, input.Name, input.Description, input.Tags, input.Category, input.MIME, int64(len(input.Content)), input.Width, input.Height, input.Enabled, created, updated)
		return r.complete(txctx, "image.create", "image", actor, key, id, out, "media.image_created")
	})
	return out, err
}

func imageMap(id int64, file, name, description, tags, category, mime string, size int64, width, height int32, enabled bool, created, updated time.Time) map[string]any {
	base := fmt.Sprintf("/api/admin/image-library/%d/variants/", id)
	return map[string]any{"id": id, "resource_id": id, "file_name": file, "name": name, "description": description, "tags": splitTags(tags), "category": category, "mime_type": mime, "content_type": mime, "file_size": size, "width": width, "height": height, "enabled": enabled, "source": "local_repository", "source_url": "", "thumb_media_id": "", "thumb_media_id_expires_at": nil, "ai_metadata": map[string]any{}, "created_at": created.UTC().Format(time.RFC3339Nano), "updated_at": updated.UTC().Format(time.RFC3339Nano), "thumb_160_url": base + "thumb_160", "thumb_320_url": base + "thumb_320", "thumb_url": base + "thumb_320", "preview_url": base + "mobile_1080", "mobile_1080_url": base + "mobile_1080", "large_1440_url": base + "large_1440", "original_url": base + "original"}
}
func splitTags(value string) []string {
	var out []string
	for _, v := range strings.Split(value, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

type ImageQuery struct {
	Limit, Offset   int
	EnabledOnly     bool
	Query, Category string
	Tags            []string
	TagGroups       [][]string
	OnlyUnlabeled   bool
}

func (r *Repository) ListImages(ctx context.Context, limit, offset int, enabledOnly bool, q, category string) ([]map[string]any, int, error) {
	return r.ListImagesFiltered(ctx, ImageQuery{Limit: limit, Offset: offset, EnabledOnly: enabledOnly, Query: q, Category: category})
}
func (r *Repository) ListImagesFiltered(ctx context.Context, query ImageQuery) ([]map[string]any, int, error) {
	if query.Limit < 1 || query.Limit > 500 || query.Offset < 0 {
		return nil, 0, ErrInvalid
	}
	var rowsOut []map[string]any
	var total int
	err := r.Within(ctx, func(txctx context.Context) error {
		tx, _ := platformpostgres.RequireTransaction(txctx)
		where := ` WHERE ($1='' OR name ILIKE '%'||$1||'%' OR file_name ILIKE '%'||$1||'%' OR description ILIKE '%'||$1||'%' OR category ILIKE '%'||$1||'%' OR tags ILIKE '%'||$1||'%') AND ($2='' OR category=$2) AND (NOT $3 OR enabled) AND (NOT $4 OR description='' OR category='' OR tags='')`
		args := []any{strings.TrimSpace(query.Query), strings.TrimSpace(query.Category), query.EnabledOnly, query.OnlyUnlabeled}
		if len(query.Tags) > 0 {
			args = append(args, query.Tags)
			where += fmt.Sprintf(` AND string_to_array(tags, ',') && $%d::text[]`, len(args))
		}
		for _, group := range query.TagGroups {
			if len(group) > 0 {
				args = append(args, group)
				where += fmt.Sprintf(` AND string_to_array(tags, ',') && $%d::text[]`, len(args))
			}
		}
		if err := tx.QueryRow(txctx, `SELECT count(*) FROM media_images`+where, args...).Scan(&total); err != nil {
			return err
		}
		args = append(args, query.Limit, query.Offset)
		rows, err := tx.Query(txctx, `SELECT id,file_name,name,description,tags,category,mime_type,byte_size,width,height,enabled,created_at,updated_at FROM media_images`+where+fmt.Sprintf(` ORDER BY updated_at DESC,id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, size int64
			var file, name, description, tags, cat, mime string
			var width, height int32
			var enabled bool
			var created, updated time.Time
			if err = rows.Scan(&id, &file, &name, &description, &tags, &cat, &mime, &size, &width, &height, &enabled, &created, &updated); err != nil {
				return err
			}
			rowsOut = append(rowsOut, imageMap(id, file, name, description, tags, cat, mime, size, width, height, enabled, created, updated))
		}
		return rows.Err()
	})
	return rowsOut, total, err
}

func (r *Repository) ImageFacets(ctx context.Context) ([]string, []string, error) {
	var categories, tags []string
	err := r.Within(ctx, func(txctx context.Context) error {
		tx, _ := platformpostgres.RequireTransaction(txctx)
		rows, err := tx.Query(txctx, `SELECT DISTINCT category FROM media_images WHERE category<>'' ORDER BY category`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var value string
			if err = rows.Scan(&value); err != nil {
				return err
			}
			categories = append(categories, value)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(txctx, `SELECT DISTINCT trim(value) FROM media_images CROSS JOIN LATERAL unnest(string_to_array(tags, ',')) AS value WHERE trim(value)<>'' ORDER BY trim(value)`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var value string
			if err = rows.Scan(&value); err != nil {
				return err
			}
			tags = append(tags, value)
		}
		return rows.Err()
	})
	return categories, tags, err
}

func (r *Repository) Image(ctx context.Context, id int64) (map[string]any, []byte, string, error) {
	if id < 1 {
		return nil, nil, "", ErrNotFound
	}
	var result map[string]any
	var content []byte
	var digestValue string
	err := r.Within(ctx, func(txctx context.Context) error {
		tx, _ := platformpostgres.RequireTransaction(txctx)
		var file, name, description, tags, cat, mime string
		var size int64
		var width, height int32
		var enabled bool
		var created, updated time.Time
		err := tx.QueryRow(txctx, `SELECT i.file_name,i.name,i.description,i.tags,i.category,i.mime_type,i.byte_size,i.width,i.height,i.enabled,i.created_at,i.updated_at,b.content,b.digest FROM media_images i JOIN media_blobs b ON b.digest=i.blob_digest WHERE i.id=$1`, id).Scan(&file, &name, &description, &tags, &cat, &mime, &size, &width, &height, &enabled, &created, &updated, &content, &digestValue)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if bytesDigest(content) != digestValue {
			return ErrConflict
		}
		result = imageMap(id, file, name, description, tags, cat, mime, size, width, height, enabled, created, updated)
		return nil
	})
	return result, content, digestValue, err
}

func (r *Repository) UpdateImage(ctx context.Context, id, actor int64, key string, patch map[string]any) (map[string]any, error) {
	if id < 1 {
		return nil, ErrNotFound
	}
	encoded, _ := json.Marshal(patch)
	var out map[string]any
	err := r.Within(ctx, func(txctx context.Context) error {
		replay, owned, err := r.reserve(txctx, "image.update", "image", actor, key, string(encoded))
		if err != nil {
			return err
		}
		if !owned {
			return json.Unmarshal(replay, &out)
		}
		tx, _ := platformpostgres.RequireTransaction(txctx)
		var file, name, description, tags, category, mime string
		var size int64
		var width, height int32
		var enabled bool
		var created, updated time.Time
		err = tx.QueryRow(txctx, `SELECT file_name,name,description,tags,category,mime_type,byte_size,width,height,enabled,created_at,updated_at FROM media_images WHERE id=$1 FOR UPDATE`, id).Scan(&file, &name, &description, &tags, &category, &mime, &size, &width, &height, &enabled, &created, &updated)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if v, ok := patch["name"].(string); ok {
			name = strings.TrimSpace(v)
		}
		if v, ok := patch["description"].(string); ok {
			description = strings.TrimSpace(v)
		}
		if v, ok := patch["tags"].(string); ok {
			tags = strings.TrimSpace(v)
		} else if values, ok := patch["tags"].([]any); ok {
			normalized := make([]string, 0, len(values))
			seen := map[string]bool{}
			for _, value := range values {
				text, ok := value.(string)
				if !ok {
					return ErrInvalid
				}
				text = strings.TrimSpace(text)
				if text != "" && !seen[text] {
					normalized = append(normalized, text)
					seen[text] = true
				}
			}
			tags = strings.Join(normalized, ",")
		}
		if v, ok := patch["category"].(string); ok {
			category = strings.TrimSpace(v)
		}
		if v, ok := patch["enabled"].(bool); ok {
			enabled = v
		}
		if name == "" || len(name) > 200 || len(description) > 10000 || len(tags) > 10000 || len(category) > 200 {
			return ErrInvalid
		}
		if err = tx.QueryRow(txctx, `UPDATE media_images SET name=$2,description=$3,tags=$4,category=$5,enabled=$6,updated_by=$7,version=version+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING updated_at`, id, name, description, tags, category, enabled, actor).Scan(&updated); err != nil {
			return err
		}
		out = imageMap(id, file, name, description, tags, category, mime, size, width, height, enabled, created, updated)
		return r.complete(txctx, "image.update", "image", actor, key, id, out, "media.image_metadata_updated")
	})
	return out, err
}

func (r *Repository) Delete(ctx context.Context, kind string, id, actor int64, key string) (map[string]any, error) {
	if id < 1 {
		return nil, ErrNotFound
	}
	if !mediaKind(kind) {
		return nil, ErrInvalid
	}
	command := fmt.Sprintf("%s:%d", kind, id)
	var out map[string]any
	err := r.Within(ctx, func(txctx context.Context) error {
		replay, owned, err := r.reserve(txctx, kind+".delete", kind, actor, key, command)
		if err != nil {
			return err
		}
		if !owned {
			return json.Unmarshal(replay, &out)
		}
		tx, _ := platformpostgres.RequireTransaction(txctx)
		references, err := r.ListMediaReferences(txctx, kind, id)
		if err != nil {
			return err
		}
		if len(references) != 0 {
			return ErrReferences
		}
		if kind == "miniprogram" {
			if err = removeLocalImageReference(txctx, "media.miniprogram.thumbnail", id); err != nil {
				return err
			}
		}
		if kind == "group_invite" {
			if err = removeLocalImageReference(txctx, "media.group_invite.cover", id); err != nil {
				return err
			}
		}
		table := map[string]string{"image": "media_images", "attachment": "media_attachments", "miniprogram": "media_miniprograms", "group_invite": "media_group_invites"}[kind]
		tag, err := tx.Exec(txctx, "DELETE FROM "+table+" WHERE id=$1", id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		out = map[string]any{"id": id, "deleted": true, "hard_deleted": true}
		return r.complete(txctx, kind+".delete", kind, actor, key, id, out, "media."+kind+"_deleted")
	})
	return out, err
}

type AttachmentInput struct {
	FileName, Name, Description string
	Tags                        []string
	Content                     []byte
	Enabled                     bool
}

func attachmentMap(id int64, file, name, description, mime string, tags []string, size int64, enabled bool, version int64, created, updated time.Time) map[string]any {
	return map[string]any{"id": id, "resource_id": id, "file_name": file, "name": name, "description": description, "tags": tags, "mime_type": mime, "file_size": size, "enabled": enabled, "version": version, "created_at": created.UTC().Format(time.RFC3339Nano), "updated_at": updated.UTC().Format(time.RFC3339Nano), "download_url": fmt.Sprintf("/api/admin/attachment-library/%d/download", id)}
}
func normalizedTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, raw := range tags {
		v := strings.TrimSpace(raw)
		if v != "" && !seen[v] {
			if len(v) > 64 {
				return nil
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	if len(out) > 50 {
		return nil
	}
	return out
}

func (r *Repository) CreateAttachment(ctx context.Context, actor int64, key string, input AttachmentInput) (map[string]any, error) {
	tags := normalizedTags(input.Tags)
	if tags == nil && len(input.Tags) > 0 {
		return nil, ErrInvalid
	}
	command, _ := json.Marshal(struct {
		F, N, D, H string
		T          []string
		E          bool
	}{input.FileName, input.Name, input.Description, bytesDigest(input.Content), tags, input.Enabled})
	var out map[string]any
	err := r.Within(ctx, func(txctx context.Context) error {
		replay, owned, err := r.reserve(txctx, "attachment.create", "attachment", actor, key, string(command))
		if err != nil {
			return err
		}
		if !owned {
			return json.Unmarshal(replay, &out)
		}
		tx, _ := platformpostgres.RequireTransaction(txctx)
		blob := bytesDigest(input.Content)
		if _, err = tx.Exec(txctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,'application/pdf',$2,$3) ON CONFLICT(digest) DO NOTHING`, blob, len(input.Content), input.Content); err != nil {
			return err
		}
		tagBytes, _ := json.Marshal(tags)
		var id, version int64
		var created, updated time.Time
		err = tx.QueryRow(txctx, `INSERT INTO media_attachments(blob_digest,file_name,name,description,tags,mime_type,byte_size,enabled,created_by,updated_by) VALUES($1,$2,$3,$4,$5::jsonb,'application/pdf',$6,$7,$8,$8) RETURNING id,version,created_at,updated_at`, blob, input.FileName, input.Name, input.Description, tagBytes, len(input.Content), input.Enabled, actor).Scan(&id, &version, &created, &updated)
		if err != nil {
			return err
		}
		out = attachmentMap(id, input.FileName, input.Name, input.Description, "application/pdf", tags, int64(len(input.Content)), input.Enabled, version, created, updated)
		out["created_by"], out["updated_by"] = actor, actor
		return r.complete(txctx, "attachment.create", "attachment", actor, key, id, out, "media.attachment_created")
	})
	return out, err
}
func (r *Repository) ListAttachments(ctx context.Context, limit, offset int, enabledOnly bool, q string) ([]map[string]any, int, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, 0, ErrInvalid
	}
	var out []map[string]any
	var total int
	err := r.Within(ctx, func(txctx context.Context) error {
		tx, _ := platformpostgres.RequireTransaction(txctx)
		where := ` WHERE ($1='' OR lower(name) LIKE '%'||lower($1)||'%') AND (NOT $2 OR enabled)`
		if err := tx.QueryRow(txctx, `SELECT count(*) FROM media_attachments`+where, q, enabledOnly).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(txctx, `SELECT id,file_name,name,description,tags,mime_type,byte_size,enabled,version,created_by,updated_by,created_at,updated_at FROM media_attachments`+where+` ORDER BY updated_at DESC,id DESC LIMIT $3 OFFSET $4`, q, enabledOnly, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, size, version, createdBy, updatedBy int64
			var f, n, d, m string
			var tagsBytes []byte
			var enabled bool
			var c, u time.Time
			if err = rows.Scan(&id, &f, &n, &d, &tagsBytes, &m, &size, &enabled, &version, &createdBy, &updatedBy, &c, &u); err != nil {
				return err
			}
			var tags []string
			if json.Unmarshal(tagsBytes, &tags) != nil {
				return ErrInvalid
			}
			item := attachmentMap(id, f, n, d, m, tags, size, enabled, version, c, u)
			item["created_by"], item["updated_by"] = createdBy, updatedBy
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, total, err
}
func (r *Repository) Attachment(ctx context.Context, id int64) (map[string]any, []byte, error) {
	if id < 1 {
		return nil, nil, ErrNotFound
	}
	var out map[string]any
	var content []byte
	err := r.Within(ctx, func(txctx context.Context) error {
		tx, _ := platformpostgres.RequireTransaction(txctx)
		var f, n, d, m, digestValue string
		var tagsBytes []byte
		var size, version, createdBy, updatedBy int64
		var enabled bool
		var c, u time.Time
		err := tx.QueryRow(txctx, `SELECT a.file_name,a.name,a.description,a.tags,a.mime_type,a.byte_size,a.enabled,a.version,a.created_by,a.updated_by,a.created_at,a.updated_at,b.content,b.digest FROM media_attachments a JOIN media_blobs b ON b.digest=a.blob_digest WHERE a.id=$1`, id).Scan(&f, &n, &d, &tagsBytes, &m, &size, &enabled, &version, &createdBy, &updatedBy, &c, &u, &content, &digestValue)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if bytesDigest(content) != digestValue {
			return ErrConflict
		}
		var tags []string
		if json.Unmarshal(tagsBytes, &tags) != nil {
			return ErrInvalid
		}
		out = attachmentMap(id, f, n, d, m, tags, size, enabled, version, c, u)
		out["created_by"], out["updated_by"] = createdBy, updatedBy
		return nil
	})
	return out, content, err
}
func (r *Repository) UpdateAttachment(ctx context.Context, id, actor int64, key string, patch map[string]any) (map[string]any, error) {
	if id < 1 {
		return nil, ErrNotFound
	}
	raw, _ := json.Marshal(patch)
	var out map[string]any
	err := r.Within(ctx, func(txctx context.Context) error {
		replay, owned, err := r.reserve(txctx, "attachment.update", "attachment", actor, key, string(raw))
		if err != nil {
			return err
		}
		if !owned {
			return json.Unmarshal(replay, &out)
		}
		tx, _ := platformpostgres.RequireTransaction(txctx)
		var f, n, d, m string
		var tagsBytes []byte
		var size, version, createdBy, updatedBy int64
		var enabled bool
		var c, u time.Time
		err = tx.QueryRow(txctx, `SELECT file_name,name,description,tags,mime_type,byte_size,enabled,version,created_by,updated_by,created_at,updated_at FROM media_attachments WHERE id=$1 FOR UPDATE`, id).Scan(&f, &n, &d, &tagsBytes, &m, &size, &enabled, &version, &createdBy, &updatedBy, &c, &u)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var tags []string
		_ = json.Unmarshal(tagsBytes, &tags)
		expected, ok := patch["expected_version"].(float64)
		if !ok || int64(expected) != version {
			return ErrConflict
		}
		if v, ok := patch["name"].(string); ok {
			n = strings.TrimSpace(v)
		}
		if v, ok := patch["description"].(string); ok {
			d = strings.TrimSpace(v)
		}
		if v, ok := patch["enabled"].(bool); ok {
			enabled = v
		}
		if values, ok := patch["tags"].([]any); ok {
			tags = []string{}
			for _, v := range values {
				s, ok := v.(string)
				if !ok {
					return ErrInvalid
				}
				tags = append(tags, s)
			}
			tags = normalizedTags(tags)
			if tags == nil {
				return ErrInvalid
			}
		}
		if n == "" || len(n) > 200 || len(d) > 10000 {
			return ErrInvalid
		}
		tagBytes, _ := json.Marshal(tags)
		err = tx.QueryRow(txctx, `UPDATE media_attachments SET name=$2,description=$3,tags=$4::jsonb,enabled=$5,updated_by=$6,version=version+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING version,updated_at`, id, n, d, tagBytes, enabled, actor).Scan(&version, &u)
		if err != nil {
			return err
		}
		out = attachmentMap(id, f, n, d, m, tags, size, enabled, version, c, u)
		out["created_by"], out["updated_by"] = createdBy, actor
		return r.complete(txctx, "attachment.update", "attachment", actor, key, id, out, "media.attachment_updated")
	})
	return out, err
}

func miniMap(id int64, name, appID, page, title string, thumb *int64, enabled bool, version, createdBy, updatedBy int64, created, updated time.Time) map[string]any {
	out := map[string]any{"id": id, "resource_id": id, "name": name, "appid": appID, "app_id": appID, "pagepath": page, "page_path": page, "title": title, "enabled": enabled, "version": version, "created_by": createdBy, "updated_by": updatedBy, "created_at": created.UTC().Format(time.RFC3339Nano), "updated_at": updated.UTC().Format(time.RFC3339Nano), "thumb_media_id": "", "thumb_image_url": "", "thumb_image_base64": "", "thumbnail_status": "not_available"}
	if thumb != nil {
		out["thumb_image_id"] = *thumb
		out["thumb_image_url"] = fmt.Sprintf("/api/admin/image-library/%d/variants/thumb_320", *thumb)
	}
	return out
}
func (r *Repository) ListMiniPrograms(ctx context.Context, limit, offset int, enabledOnly bool, q string) ([]map[string]any, int, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, 0, ErrInvalid
	}
	var out []map[string]any
	var total int
	err := r.Within(ctx, func(txctx context.Context) error {
		tx, _ := platformpostgres.RequireTransaction(txctx)
		where := ` WHERE ($1='' OR lower(name) LIKE '%'||lower($1)||'%') AND (NOT $2 OR enabled)`
		if err := tx.QueryRow(txctx, `SELECT count(*) FROM media_miniprograms`+where, q, enabledOnly).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(txctx, `SELECT id,name,app_id,page_path,title,thumb_image_id,enabled,version,created_by,updated_by,created_at,updated_at FROM media_miniprograms`+where+` ORDER BY updated_at DESC,id DESC LIMIT $3 OFFSET $4`, q, enabledOnly, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, v, createdBy, updatedBy int64
			var n, a, p, t string
			var thumb *int64
			var enabled bool
			var c, u time.Time
			if err = rows.Scan(&id, &n, &a, &p, &t, &thumb, &enabled, &v, &createdBy, &updatedBy, &c, &u); err != nil {
				return err
			}
			out = append(out, miniMap(id, n, a, p, t, thumb, enabled, v, createdBy, updatedBy, c, u))
		}
		return rows.Err()
	})
	return out, total, err
}
func (r *Repository) CreateMiniProgram(ctx context.Context, actor int64, key string, input map[string]any) (map[string]any, error) {
	raw, _ := json.Marshal(input)
	var out map[string]any
	err := r.Within(ctx, func(txctx context.Context) error {
		replay, owned, err := r.reserve(txctx, "miniprogram.create", "miniprogram", actor, key, string(raw))
		if err != nil {
			return err
		}
		if !owned {
			return json.Unmarshal(replay, &out)
		}
		tx, _ := platformpostgres.RequireTransaction(txctx)
		n, _ := input["name"].(string)
		a, _ := input["appid"].(string)
		if a == "" {
			a, _ = input["app_id"].(string)
		}
		p, _ := input["pagepath"].(string)
		if p == "" {
			p, _ = input["page_path"].(string)
		}
		t, _ := input["title"].(string)
		enabled := true
		if e, ok := input["enabled"].(bool); ok {
			enabled = e
		}
		if n = strings.TrimSpace(n); n == "" || len(n) > 200 || a == "" || len(a) > 120 || p == "" || len(p) > 500 || t == "" || len(t) > 200 {
			return ErrInvalid
		}
		var thumb *int64
		if fv, ok := input["thumb_image_id"].(float64); ok {
			v := int64(fv)
			if v < 1 {
				return ErrInvalid
			}
			var exists bool
			if err = tx.QueryRow(txctx, `SELECT EXISTS(SELECT 1 FROM media_images WHERE id=$1)`, v).Scan(&exists); err != nil || !exists {
				return ErrInvalid
			}
			thumb = &v
		}
		var id, version int64
		var c, u time.Time
		err = tx.QueryRow(txctx, `INSERT INTO media_miniprograms(name,app_id,page_path,title,thumb_image_id,enabled,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$7) RETURNING id,version,created_at,updated_at`, n, a, p, t, thumb, enabled, actor).Scan(&id, &version, &c, &u)
		if err != nil {
			return err
		}
		if err = replaceLocalImageReference(txctx, "media.miniprogram.thumbnail", id, nil, thumb); err != nil {
			return err
		}
		out = miniMap(id, n, a, p, t, thumb, enabled, version, actor, actor, c, u)
		return r.complete(txctx, "miniprogram.create", "miniprogram", actor, key, id, out, "media.miniprogram.created")
	})
	return out, err
}

func (r *Repository) MiniProgram(ctx context.Context, id int64) (map[string]any, error) {
	if id < 1 {
		return nil, ErrNotFound
	}
	var out map[string]any
	err := r.Within(ctx, func(txctx context.Context) error {
		tx, _ := platformpostgres.RequireTransaction(txctx)
		var n, a, p, t string
		var thumb *int64
		var enabled bool
		var version, createdBy, updatedBy int64
		var c, u time.Time
		err := tx.QueryRow(txctx, `SELECT id,name,app_id,page_path,title,thumb_image_id,enabled,version,created_by,updated_by,created_at,updated_at FROM media_miniprograms WHERE id=$1`, id).Scan(&id, &n, &a, &p, &t, &thumb, &enabled, &version, &createdBy, &updatedBy, &c, &u)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out = miniMap(id, n, a, p, t, thumb, enabled, version, createdBy, updatedBy, c, u)
		return nil
	})
	return out, err
}
func (r *Repository) UpdateMiniProgram(ctx context.Context, id, actor int64, key string, input map[string]any) (map[string]any, error) {
	if id < 1 {
		return nil, ErrNotFound
	}
	raw, _ := json.Marshal(input)
	var out map[string]any
	err := r.Within(ctx, func(txctx context.Context) error {
		replay, owned, err := r.reserve(txctx, "miniprogram.update", "miniprogram", actor, key, string(raw))
		if err != nil {
			return err
		}
		if !owned {
			return json.Unmarshal(replay, &out)
		}
		tx, _ := platformpostgres.RequireTransaction(txctx)
		var n, a, p, t string
		var thumb *int64
		var enabled bool
		var version, createdBy, updatedBy int64
		var c, u time.Time
		err = tx.QueryRow(txctx, `SELECT name,app_id,page_path,title,thumb_image_id,enabled,version,created_by,updated_by,created_at,updated_at FROM media_miniprograms WHERE id=$1 FOR UPDATE`, id).Scan(&n, &a, &p, &t, &thumb, &enabled, &version, &createdBy, &updatedBy, &c, &u)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		oldThumb := thumb
		if v, ok := input["name"].(string); ok {
			n = strings.TrimSpace(v)
		}
		if v, ok := input["appid"].(string); ok {
			a = strings.TrimSpace(v)
		}
		if v, ok := input["app_id"].(string); ok {
			a = strings.TrimSpace(v)
		}
		if v, ok := input["pagepath"].(string); ok {
			p = strings.TrimSpace(v)
		}
		if v, ok := input["page_path"].(string); ok {
			p = strings.TrimSpace(v)
		}
		if v, ok := input["title"].(string); ok {
			t = strings.TrimSpace(v)
		}
		if v, ok := input["enabled"].(bool); ok {
			enabled = v
		}
		if fv, exists := input["thumb_image_id"]; exists {
			if fv == nil {
				thumb = nil
			} else {
				number, ok := fv.(float64)
				if !ok || number < 1 {
					return ErrInvalid
				}
				value := int64(number)
				var image bool
				if err = tx.QueryRow(txctx, `SELECT EXISTS(SELECT 1 FROM media_images WHERE id=$1)`, value).Scan(&image); err != nil || !image {
					return ErrInvalid
				}
				thumb = &value
			}
		}
		if n == "" || a == "" || p == "" || t == "" || len(n) > 200 || len(a) > 120 || len(p) > 500 || len(t) > 200 {
			return ErrInvalid
		}
		err = tx.QueryRow(txctx, `UPDATE media_miniprograms SET name=$2,app_id=$3,page_path=$4,title=$5,thumb_image_id=$6,enabled=$7,updated_by=$8,version=version+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING version,updated_at`, id, n, a, p, t, thumb, enabled, actor).Scan(&version, &u)
		if err != nil {
			return err
		}
		if err = replaceLocalImageReference(txctx, "media.miniprogram.thumbnail", id, oldThumb, thumb); err != nil {
			return err
		}
		out = miniMap(id, n, a, p, t, thumb, enabled, version, createdBy, actor, c, u)
		return r.complete(txctx, "miniprogram.update", "miniprogram", actor, key, id, out, "media.miniprogram.updated")
	})
	return out, err
}

// ResolveMiniProgramThumbnail records a truthful local cache outcome. Media
// owns no remote thumbnail fetcher in PR02, so it must never manufacture a
// "resolved" result merely because a card exists.
func (r *Repository) ResolveMiniProgramThumbnail(ctx context.Context, id, actor int64, key string) (map[string]any, error) {
	if id < 1 {
		return nil, ErrNotFound
	}
	var resolution map[string]any
	err := r.Within(ctx, func(txctx context.Context) error {
		replay, owned, err := r.reserve(txctx, "miniprogram.thumbnail.resolve", "miniprogram", actor, key, fmt.Sprintf("miniprogram:%d", id))
		if err != nil {
			return err
		}
		if !owned {
			var snapshot struct {
				Resolution map[string]any `json:"resolution"`
			}
			if json.Unmarshal(replay, &snapshot) != nil || snapshot.Resolution == nil {
				return ErrConflict
			}
			resolution = snapshot.Resolution
			return nil
		}
		tx, _ := platformpostgres.RequireTransaction(txctx)
		var exists bool
		if err = tx.QueryRow(txctx, `SELECT EXISTS(SELECT 1 FROM media_miniprograms WHERE id=$1 FOR KEY SHARE)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		resolution = map[string]any{"status": "not_available", "cache_owner": "media.thumbnail_cache", "cache_receipt": digest(key), "side_effect_executed": false, "real_external_call_executed": false}
		return r.complete(txctx, "miniprogram.thumbnail.resolve", "miniprogram", actor, key, id, map[string]any{"resolution": resolution}, "media.miniprogram_thumbnail_resolution_recorded")
	})
	return resolution, err
}

func groupMap(id int64, name, title, description, joinURL string, cover *int64, enabled bool, version, createdBy, updatedBy int64, created, updated time.Time, archived *time.Time) map[string]any {
	out := map[string]any{"id": id, "name": name, "title": title, "description": description, "join_url": joinURL, "enabled": enabled, "version": version, "created_by": createdBy, "updated_by": updatedBy, "created_at": created.UTC().Format(time.RFC3339Nano), "updated_at": updated.UTC().Format(time.RFC3339Nano)}
	if cover != nil {
		out["cover_image_id"] = *cover
	}
	if archived != nil {
		out["archived_at"] = archived.UTC().Format(time.RFC3339Nano)
	}
	return out
}
func (r *Repository) ListGroupInvites(ctx context.Context, limit, offset int, enabledOnly bool, q string) ([]map[string]any, int, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, 0, ErrInvalid
	}
	var out []map[string]any
	var total int
	err := r.Within(ctx, func(txctx context.Context) error {
		tx, _ := platformpostgres.RequireTransaction(txctx)
		where := ` WHERE archived_at IS NULL AND ($1='' OR lower(name) LIKE '%'||lower($1)||'%') AND (NOT $2 OR enabled)`
		if err := tx.QueryRow(txctx, `SELECT count(*) FROM media_group_invites`+where, q, enabledOnly).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(txctx, `SELECT id,name,title,description,join_url,cover_image_id,enabled,version,created_by,updated_by,created_at,updated_at,archived_at FROM media_group_invites`+where+` ORDER BY updated_at DESC,id DESC LIMIT $3 OFFSET $4`, q, enabledOnly, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, v, createdBy, updatedBy int64
			var n, t, d, j string
			var cover *int64
			var enabled bool
			var c, u time.Time
			var archived *time.Time
			if err = rows.Scan(&id, &n, &t, &d, &j, &cover, &enabled, &v, &createdBy, &updatedBy, &c, &u, &archived); err != nil {
				return err
			}
			out = append(out, groupMap(id, n, t, d, j, cover, enabled, v, createdBy, updatedBy, c, u, archived))
		}
		return rows.Err()
	})
	return out, total, err
}
func (r *Repository) CreateGroupInvite(ctx context.Context, actor int64, key string, input map[string]any) (map[string]any, error) {
	raw, _ := json.Marshal(input)
	var out map[string]any
	err := r.Within(ctx, func(txctx context.Context) error {
		replay, owned, err := r.reserve(txctx, "group_invite.create", "group_invite", actor, key, string(raw))
		if err != nil {
			return err
		}
		if !owned {
			return json.Unmarshal(replay, &out)
		}
		tx, _ := platformpostgres.RequireTransaction(txctx)
		n, _ := input["name"].(string)
		t, _ := input["title"].(string)
		d, _ := input["description"].(string)
		j, _ := input["join_url"].(string)
		n = strings.TrimSpace(n)
		t = strings.TrimSpace(t)
		j = strings.TrimSpace(j)
		if n == "" {
			n = t
		}
		enabled := true
		if value, ok := input["enabled"].(bool); ok {
			enabled = value
		}
		if n == "" || t == "" || j == "" || len(n) > 200 || len(t) > 128 || len(d) > 512 || len(j) > 2048 || !strings.HasPrefix(j, "https://work.weixin.qq.com/gm/") || strings.ContainsAny(j, "?#") {
			return ErrInvalid
		}
		var cover *int64
		if value, ok := input["cover_image_id"].(float64); ok {
			v := int64(value)
			var exists bool
			if v < 1 || tx.QueryRow(txctx, `SELECT EXISTS(SELECT 1 FROM media_images WHERE id=$1)`, v).Scan(&exists) != nil || !exists {
				return ErrInvalid
			}
			cover = &v
		}
		var id, v int64
		var c, u time.Time
		err = tx.QueryRow(txctx, `INSERT INTO media_group_invites(name,title,description,join_url,cover_image_id,enabled,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$7) RETURNING id,version,created_at,updated_at`, n, t, d, j, cover, enabled, actor).Scan(&id, &v, &c, &u)
		if err != nil {
			return err
		}
		if err = replaceLocalImageReference(txctx, "media.group_invite.cover", id, nil, cover); err != nil {
			return err
		}
		out = groupMap(id, n, t, d, j, cover, enabled, v, actor, actor, c, u, nil)
		return r.complete(txctx, "group_invite.create", "group_invite", actor, key, id, out, "media.group_invite_created")
	})
	return out, err
}

func (r *Repository) GroupInvite(ctx context.Context, id int64) (map[string]any, error) {
	if id < 1 {
		return nil, ErrNotFound
	}
	var out map[string]any
	err := r.Within(ctx, func(txctx context.Context) error {
		tx, _ := platformpostgres.RequireTransaction(txctx)
		var name, title, description, joinURL string
		var cover *int64
		var enabled bool
		var version, createdBy, updatedBy int64
		var created, updated time.Time
		var archived *time.Time
		err := tx.QueryRow(txctx, `SELECT name,title,description,join_url,cover_image_id,enabled,version,created_by,updated_by,created_at,updated_at,archived_at FROM media_group_invites WHERE id=$1 AND archived_at IS NULL`, id).Scan(&name, &title, &description, &joinURL, &cover, &enabled, &version, &createdBy, &updatedBy, &created, &updated, &archived)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out = groupMap(id, name, title, description, joinURL, cover, enabled, version, createdBy, updatedBy, created, updated, archived)
		return nil
	})
	return out, err
}

func (r *Repository) UpdateGroupInvite(ctx context.Context, id, actor int64, key string, input map[string]any) (map[string]any, error) {
	if id < 1 {
		return nil, ErrNotFound
	}
	raw, _ := json.Marshal(input)
	var out map[string]any
	err := r.Within(ctx, func(txctx context.Context) error {
		replay, owned, err := r.reserve(txctx, "group_invite.update", "group_invite", actor, key, string(raw))
		if err != nil {
			return err
		}
		if !owned {
			return json.Unmarshal(replay, &out)
		}
		tx, _ := platformpostgres.RequireTransaction(txctx)
		var name, title, description, joinURL string
		var cover *int64
		var enabled bool
		var version int64
		var created, updated time.Time
		err = tx.QueryRow(txctx, `SELECT name,title,description,join_url,cover_image_id,enabled,version,created_at,updated_at FROM media_group_invites WHERE id=$1 AND archived_at IS NULL FOR UPDATE`, id).Scan(&name, &title, &description, &joinURL, &cover, &enabled, &version, &created, &updated)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		oldCover := cover
		if value, ok := input["name"].(string); ok {
			name = strings.TrimSpace(value)
		}
		if value, ok := input["title"].(string); ok {
			title = strings.TrimSpace(value)
		}
		if value, ok := input["description"].(string); ok {
			description = strings.TrimSpace(value)
		}
		if value, ok := input["join_url"].(string); ok {
			joinURL = strings.TrimSpace(value)
		}
		if value, ok := input["enabled"].(bool); ok {
			enabled = value
		}
		if rawCover, exists := input["cover_image_id"]; exists {
			if rawCover == nil {
				cover = nil
			} else {
				number, ok := rawCover.(float64)
				if !ok || number < 1 || number != float64(int64(number)) {
					return ErrInvalid
				}
				candidate := int64(number)
				var exists bool
				if err = tx.QueryRow(txctx, `SELECT EXISTS(SELECT 1 FROM media_images WHERE id=$1)`, candidate).Scan(&exists); err != nil || !exists {
					return ErrInvalid
				}
				cover = &candidate
			}
		}
		if name == "" || title == "" || joinURL == "" || len(name) > 200 || len(title) > 128 || len(description) > 512 || len(joinURL) > 2048 || !strings.HasPrefix(joinURL, "https://work.weixin.qq.com/gm/") || strings.ContainsAny(joinURL, "?#") {
			return ErrInvalid
		}
		err = tx.QueryRow(txctx, `UPDATE media_group_invites SET name=$2,title=$3,description=$4,join_url=$5,cover_image_id=$6,enabled=$7,updated_by=$8,version=version+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING version,updated_at`, id, name, title, description, joinURL, cover, enabled, actor).Scan(&version, &updated)
		if err != nil {
			return err
		}
		if err = replaceLocalImageReference(txctx, "media.group_invite.cover", id, oldCover, cover); err != nil {
			return err
		}
		out = groupMap(id, name, title, description, joinURL, cover, enabled, version, actor, actor, created, updated, nil)
		return r.complete(txctx, "group_invite.update", "group_invite", actor, key, id, out, "media.group_invite_updated")
	})
	return out, err
}

func (r *Repository) ArchiveGroupInvite(ctx context.Context, id, actor int64, key string) (map[string]any, error) {
	if id < 1 {
		return nil, ErrNotFound
	}
	var out map[string]any
	err := r.Within(ctx, func(txctx context.Context) error {
		replay, owned, err := r.reserve(txctx, "group_invite.archive", "group_invite", actor, key, fmt.Sprintf("group_invite:%d", id))
		if err != nil {
			return err
		}
		if !owned {
			return json.Unmarshal(replay, &out)
		}
		tx, _ := platformpostgres.RequireTransaction(txctx)
		references, err := r.ListMediaReferences(txctx, "group_invite", id)
		if err != nil {
			return err
		}
		if len(references) != 0 {
			return ErrReferences
		}
		var name, title, description, joinURL string
		var cover *int64
		var version int64
		var created, updated, archived time.Time
		err = tx.QueryRow(txctx, `UPDATE media_group_invites SET enabled=false,archived_at=clock_timestamp(),updated_by=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1 AND archived_at IS NULL RETURNING name,title,description,join_url,cover_image_id,version,created_at,updated_at,archived_at`, id, actor).Scan(&name, &title, &description, &joinURL, &cover, &version, &created, &updated, &archived)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out = groupMap(id, name, title, description, joinURL, cover, false, version, actor, actor, created, updated, &archived)
		return r.complete(txctx, "group_invite.archive", "group_invite", actor, key, id, out, "media.group_invite_archived")
	})
	return out, err
}

type AttachmentUploadInput struct {
	FileName, Name, Description, Digest string
	Size                                int64
	Enabled                             bool
}

func (r *Repository) InitiateAttachmentUpload(ctx context.Context, actor int64, key string, input AttachmentUploadInput) (int64, error) {
	if actor < 1 || input.Size < 1 || input.Size > domain.MaxAttachmentBytes || !validDigest(input.Digest) || strings.TrimSpace(input.FileName) != input.FileName || input.FileName == "" || input.Name == "" || len(input.FileName) > 255 || len(input.Name) > 200 || len(input.Description) > 10000 {
		return 0, ErrInvalid
	}
	command, _ := json.Marshal(input)
	var uploadID int64
	err := r.Within(ctx, func(txctx context.Context) error {
		replay, owned, err := r.reserve(txctx, "attachment.upload.initiate", "upload", actor, key, string(command))
		if err != nil {
			return err
		}
		if !owned {
			var item struct {
				UploadID int64 `json:"upload_id"`
			}
			if json.Unmarshal(replay, &item) != nil || item.UploadID < 1 {
				return ErrConflict
			}
			uploadID = item.UploadID
			return nil
		}
		tx, _ := platformpostgres.RequireTransaction(txctx)
		err = tx.QueryRow(txctx, `INSERT INTO media_attachment_uploads(actor_admin_user_id,idempotency_key_digest,file_name,name,description,expected_size,expected_digest,enabled,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,clock_timestamp()+interval '1 hour') RETURNING id`, actor, digest(key), input.FileName, input.Name, input.Description, input.Size, input.Digest, input.Enabled).Scan(&uploadID)
		if err != nil {
			return err
		}
		return r.complete(txctx, "attachment.upload.initiate", "upload", actor, key, uploadID, map[string]any{"upload_id": uploadID}, "media.attachment_upload_initiated")
	})
	return uploadID, err
}

func (r *Repository) PutAttachmentUploadPart(ctx context.Context, uploadID int64, partNumber int, actor int64, key, expectedDigest string, content []byte) error {
	if uploadID < 1 || partNumber < 1 || len(content) == 0 || len(content) > 1<<20 || !validDigest(expectedDigest) || bytesDigest(content) != expectedDigest {
		return ErrInvalid
	}
	command := fmt.Sprintf("%d:%d:%s", uploadID, partNumber, expectedDigest)
	return r.Within(ctx, func(txctx context.Context) error {
		replay, owned, err := r.reserve(txctx, "attachment.upload.part", "upload", actor, key, command)
		if err != nil {
			return err
		}
		if !owned {
			_ = replay
			return nil
		}
		tx, _ := platformpostgres.RequireTransaction(txctx)
		var expectedSize int64
		var completed *int64
		var expires time.Time
		if err = tx.QueryRow(txctx, `SELECT expected_size,completed_attachment_id,expires_at FROM media_attachment_uploads WHERE id=$1 AND actor_admin_user_id=$2 FOR UPDATE`, uploadID, actor).Scan(&expectedSize, &completed, &expires); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if completed != nil || !expires.After(time.Now().UTC()) {
			return ErrConflict
		}
		var count int
		var stored int64
		if err = tx.QueryRow(txctx, `SELECT count(*),COALESCE(sum(octet_length(content)),0) FROM media_attachment_upload_parts WHERE upload_id=$1`, uploadID).Scan(&count, &stored); err != nil {
			return err
		}
		if count >= 1024 || stored+int64(len(content)) > expectedSize {
			return ErrInvalid
		}
		commandTag, err := tx.Exec(txctx, `INSERT INTO media_attachment_upload_parts(upload_id,part_number,digest,content) VALUES($1,$2,$3,$4) ON CONFLICT(upload_id,part_number) DO NOTHING`, uploadID, partNumber, expectedDigest, content)
		if err != nil {
			return err
		}
		if commandTag.RowsAffected() == 0 {
			var digestValue string
			if err = tx.QueryRow(txctx, `SELECT digest FROM media_attachment_upload_parts WHERE upload_id=$1 AND part_number=$2`, uploadID, partNumber).Scan(&digestValue); err != nil {
				return err
			}
			if digestValue != expectedDigest {
				return ErrConflict
			}
		}
		return r.complete(txctx, "attachment.upload.part", "upload", actor, key, uploadID, map[string]any{"upload_id": uploadID, "part_number": partNumber}, "media.attachment_upload_part_stored")
	})
}

func (r *Repository) CompleteAttachmentUpload(ctx context.Context, uploadID, actor int64, key string) (int64, error) {
	if uploadID < 1 {
		return 0, ErrNotFound
	}
	var attachmentID int64
	err := r.Within(ctx, func(txctx context.Context) error {
		replay, owned, err := r.reserve(txctx, "attachment.upload.complete", "attachment", actor, key, fmt.Sprintf("upload:%d", uploadID))
		if err != nil {
			return err
		}
		if !owned {
			var item struct {
				AttachmentID int64 `json:"attachment_id"`
			}
			if json.Unmarshal(replay, &item) != nil || item.AttachmentID < 1 {
				return ErrConflict
			}
			attachmentID = item.AttachmentID
			return nil
		}
		tx, _ := platformpostgres.RequireTransaction(txctx)
		var fileName, name, description, expectedDigest string
		var expectedSize int64
		var enabled bool
		var existing *int64
		err = tx.QueryRow(txctx, `SELECT file_name,name,description,expected_size,expected_digest,enabled,completed_attachment_id FROM media_attachment_uploads WHERE id=$1 AND actor_admin_user_id=$2 AND expires_at>clock_timestamp() FOR UPDATE`, uploadID, actor).Scan(&fileName, &name, &description, &expectedSize, &expectedDigest, &enabled, &existing)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if existing != nil {
			return ErrConflict
		}
		rows, err := tx.Query(txctx, `SELECT part_number,content FROM media_attachment_upload_parts WHERE upload_id=$1 ORDER BY part_number`, uploadID)
		if err != nil {
			return err
		}
		defer rows.Close()
		var joined bytes.Buffer
		expectedPart := 1
		for rows.Next() {
			var part int
			var content []byte
			if err = rows.Scan(&part, &content); err != nil {
				return err
			}
			if part != expectedPart {
				return ErrConflict
			}
			expectedPart++
			_, _ = joined.Write(content)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		content := joined.Bytes()
		if int64(len(content)) != expectedSize || bytesDigest(content) != expectedDigest {
			return ErrConflict
		}
		if _, err = domain.InspectAttachment(fileName, "application/pdf", content); err != nil {
			return ErrInvalid
		}
		if _, err = tx.Exec(txctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,'application/pdf',$2,$3) ON CONFLICT(digest) DO NOTHING`, expectedDigest, len(content), content); err != nil {
			return err
		}
		var version int64
		var created, updated time.Time
		var tagBytes = []byte("[]")
		err = tx.QueryRow(txctx, `INSERT INTO media_attachments(blob_digest,file_name,name,description,tags,mime_type,byte_size,enabled,created_by,updated_by) VALUES($1,$2,$3,$4,$5::jsonb,'application/pdf',$6,$7,$8,$8) RETURNING id,version,created_at,updated_at`, expectedDigest, fileName, name, description, tagBytes, len(content), enabled, actor).Scan(&attachmentID, &version, &created, &updated)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(txctx, `UPDATE media_attachment_uploads SET completed_attachment_id=$2 WHERE id=$1`, uploadID, attachmentID); err != nil {
			return err
		}
		out := attachmentMap(attachmentID, fileName, name, description, "application/pdf", []string{}, int64(len(content)), enabled, version, created, updated)
		out["created_by"], out["updated_by"] = actor, actor
		return r.complete(txctx, "attachment.upload.complete", "attachment", actor, key, attachmentID, map[string]any{"attachment_id": attachmentID, "item": out}, "media.attachment_upload_completed")
	})
	return attachmentID, err
}

func validDigest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
