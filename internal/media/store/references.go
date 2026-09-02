package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// MediaReference is an opaque Media-owned protection fact. Its owner and
// digest are sufficient to decide whether a material can be changed; Media
// never reads another domain's rows to expand a reference.
type MediaReference struct {
	Owner           string
	ReferenceDigest string
}

// ReferenceReader is the narrow read adapter destructive Media operations
// use. Other owners protect a material by recording an opaque ledger fact.
type ReferenceReader interface {
	ListMediaReferences(context.Context, string, int64) ([]MediaReference, error)
}

func referenceDigest(owner string, id int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", owner, id)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (r *Repository) ListMediaReferences(ctx context.Context, kind string, id int64) ([]MediaReference, error) {
	if r == nil || id < 1 || !mediaKind(kind) {
		return nil, ErrInvalid
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT owner,reference_digest FROM media_references WHERE material_kind=$1 AND material_id=$2 ORDER BY owner,reference_digest`, kind, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	references := make([]MediaReference, 0)
	for rows.Next() {
		var reference MediaReference
		if err = rows.Scan(&reference.Owner, &reference.ReferenceDigest); err != nil {
			return nil, err
		}
		references = append(references, reference)
	}
	return references, rows.Err()
}

func mediaKind(kind string) bool {
	return kind == "image" || kind == "attachment" || kind == "miniprogram" || kind == "group_invite"
}

func replaceLocalImageReference(ctx context.Context, owner string, localID int64, oldImage, newImage *int64) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	digest := referenceDigest(owner, localID)
	if oldImage != nil {
		if _, err = tx.Exec(ctx, `DELETE FROM media_references WHERE material_kind='image' AND material_id=$1 AND owner=$2 AND reference_digest=$3`, *oldImage, owner, digest); err != nil {
			return err
		}
	}
	if newImage != nil {
		_, err = tx.Exec(ctx, `INSERT INTO media_references(material_kind,material_id,owner,reference_digest) VALUES('image',$1,$2,$3) ON CONFLICT DO NOTHING`, *newImage, owner, digest)
	}
	return err
}

func removeLocalImageReference(ctx context.Context, owner string, localID int64) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM media_references WHERE material_kind='image' AND owner=$1 AND reference_digest=$2`, owner, referenceDigest(owner, localID))
	return err
}
