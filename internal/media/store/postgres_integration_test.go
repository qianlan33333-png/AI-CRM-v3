package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

func TestPostgreSQLReceiptAuditOutboxAndPayloadDrift(t *testing.T) {
	url, urlErr := platformconfig.DatabaseURL()
	if urlErr != nil {
		t.Skip("database URL not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "media_it_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	_, file, _, _ := runtime.Caller(0)
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0006_media.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	key := "integration-key-0001"
	first, err := repo.CreateMiniProgram(ctx, 7, key, map[string]any{"name": "素材", "appid": "wx123", "pagepath": "pages/a", "title": "卡片", "enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := repo.CreateMiniProgram(ctx, 7, key, map[string]any{"name": "素材", "appid": "wx123", "pagepath": "pages/a", "title": "卡片", "enabled": true})
	if err != nil || fmt.Sprint(replay["id"]) != fmt.Sprint(first["id"]) {
		t.Fatalf("replay=%v err=%v", replay, err)
	}
	if _, err = repo.CreateMiniProgram(ctx, 7, key, map[string]any{"name": "漂移", "appid": "wx123", "pagepath": "pages/a", "title": "卡片"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("payload drift=%v", err)
	}
	var receipts, audits, outbox int
	for _, q := range []string{"SELECT count(*) FROM media_operation_receipts", "SELECT count(*) FROM media_audit_events", "SELECT count(*) FROM media_outbox"} {
		var n int
		if err = native.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if receipts == 0 {
			receipts = n
		} else if audits == 0 {
			audits = n
		} else {
			outbox = n
		}
	}
	if receipts != 1 || audits != 1 || outbox != 1 {
		t.Fatalf("atomic facts receipts=%d audit=%d outbox=%d", receipts, audits, outbox)
	}
}

func TestPostgreSQLReferenceLedgerProtectsDeleteAndArchive(t *testing.T) {
	url, urlErr := platformconfig.DatabaseURL()
	if urlErr != nil {
		t.Skip("database URL not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "media_refs_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	_, file, _, _ := runtime.Caller(0)
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0006_media.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	imageBytes := testPNG(t)
	created, err := repo.CreateImage(ctx, 7, "image-reference-key-0001", ImageInput{FileName: "cover.png", MIME: "image/png", Name: "cover", Content: imageBytes, Width: 2, Height: 2, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	imageID := created["id"].(int64)
	mini, err := repo.CreateMiniProgram(ctx, 7, "mini-reference-key-0001", map[string]any{"name": "mini", "appid": "wx123", "pagepath": "pages/a", "title": "card", "thumb_image_id": float64(imageID)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Delete(ctx, "image", imageID, 7, "delete-image-reference-0001"); !errors.Is(err, ErrReferences) {
		t.Fatalf("local mini reference must block image delete: %v", err)
	}
	references, err := repoReferenceSnapshot(ctx, repo, "image", imageID)
	if err != nil || len(references) != 1 || references[0].Owner != "media.miniprogram.thumbnail" {
		t.Fatalf("references=%+v err=%v", references, err)
	}
	if _, err = repo.Delete(ctx, "miniprogram", mini["id"].(int64), 7, "delete-mini-reference-0001"); err != nil {
		t.Fatal(err)
	}
	deleted, err := repo.Delete(ctx, "image", imageID, 7, "delete-image-reference-0002")
	if err != nil || deleted["references"] != nil {
		t.Fatalf("empty ledger must allow delete without fabricated references: result=%v err=%v", deleted, err)
	}
	cover, err := repo.CreateImage(ctx, 7, "group-cover-image-key-0001", ImageInput{FileName: "group.png", MIME: "image/png", Name: "group", Content: imageBytes, Width: 2, Height: 2, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	coverID := cover["id"].(int64)
	group, err := repo.CreateGroupInvite(ctx, 7, "group-reference-key-0001", map[string]any{"name": "group", "title": "group", "join_url": "https://work.weixin.qq.com/gm/a", "cover_image_id": float64(coverID)})
	if err != nil {
		t.Fatal(err)
	}
	groupID := group["id"].(int64)
	if _, err = repo.Delete(ctx, "image", coverID, 7, "delete-group-cover-key-0001"); !errors.Is(err, ErrReferences) {
		t.Fatalf("local group cover reference must block image delete: %v", err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO media_references(material_kind,material_id,owner,reference_digest) VALUES('group_invite',$1,'automation.group','sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`, groupID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ArchiveGroupInvite(ctx, groupID, 7, "archive-group-reference-0001"); !errors.Is(err, ErrReferences) {
		t.Fatalf("ledger reference must block archive: %v", err)
	}
	if _, err = native.Exec(ctx, `DELETE FROM media_references WHERE material_kind='group_invite' AND material_id=$1`, groupID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ArchiveGroupInvite(ctx, groupID, 7, "archive-group-reference-0002"); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Delete(ctx, "image", coverID, 7, "delete-group-cover-key-0002"); !errors.Is(err, ErrReferences) {
		t.Fatalf("archived group must retain the local cover protection fact: %v", err)
	}
}

func repoReferenceSnapshot(ctx context.Context, repo *Repository, kind string, id int64) ([]MediaReference, error) {
	var references []MediaReference
	err := repo.Within(ctx, func(tx context.Context) error {
		var err error
		references, err = repo.ListMediaReferences(tx, kind, id)
		return err
	})
	return references, err
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, 2, 2))
	value.Set(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, value); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestPostgreSQLMultipartPartVsCompleteAndBlobChecksum(t *testing.T) {
	url, urlErr := platformconfig.DatabaseURL()
	if urlErr != nil {
		t.Skip("database URL not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 6)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "media_concurrent_" + hex.EncodeToString(raw)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	native, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	_, file, _, _ := runtime.Caller(0)
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0006_media.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n")
	uploadID, err := repo.InitiateAttachmentUpload(ctx, 7, "concurrency-init-key-0001", AttachmentUploadInput{FileName: "guide.pdf", Name: "guide", Size: int64(len(content)), Digest: bytesDigest(content), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wait sync.WaitGroup
	var partErr, completeErr error
	var attachmentID int64
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		partErr = repo.PutAttachmentUploadPart(ctx, uploadID, 1, 7, "concurrency-part-key-0001", bytesDigest(content), content)
	}()
	go func() {
		defer wait.Done()
		<-start
		attachmentID, completeErr = repo.CompleteAttachmentUpload(ctx, uploadID, 7, "concurrency-complete-key-0001")
	}()
	close(start)
	wait.Wait()
	if partErr != nil {
		t.Fatalf("concurrent part failed: %v", partErr)
	}
	if completeErr != nil && !errors.Is(completeErr, ErrConflict) {
		t.Fatalf("concurrent complete err=%v", completeErr)
	}
	if attachmentID == 0 {
		attachmentID, err = repo.CompleteAttachmentUpload(ctx, uploadID, 7, "concurrency-complete-key-0001")
		if err != nil {
			t.Fatalf("complete after part: %v", err)
		}
	}
	var attachmentCount int
	if err = native.QueryRow(ctx, `SELECT count(*) FROM media_attachments`).Scan(&attachmentCount); err != nil || attachmentCount != 1 {
		t.Fatalf("attachment count=%d err=%v", attachmentCount, err)
	}
	if _, _, err = repo.Attachment(ctx, attachmentID); err != nil {
		t.Fatalf("stored attachment=%d err=%v", attachmentID, err)
	}
	if _, err = native.Exec(ctx, `UPDATE media_blobs SET content=convert_to(repeat('x', byte_size::integer),'UTF8') WHERE digest=$1`, bytesDigest(content)); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repo.Attachment(ctx, attachmentID); !errors.Is(err, ErrConflict) {
		t.Fatalf("blob checksum corruption must fail closed: %v", err)
	}
}
