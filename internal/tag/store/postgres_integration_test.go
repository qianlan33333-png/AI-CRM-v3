package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	tagapp "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
)

func TestPostgreSQLCatalogReorderArchiveReplayAndReferenceProtection(t *testing.T) {
	native, cleanup := tagIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	for _, table := range []string{"tag_groups", "tag_catalog_tags", "tag_references", "tag_operation_receipts", "tag_audit_events", "tag_outbox", "tag_sync_receipts", "tag_provider_observations"} {
		var owned bool
		if err := native.QueryRow(ctx, `SELECT tableowner=current_user FROM pg_tables WHERE schemaname=current_schema() AND tablename=$1`, table).Scan(&owned); err != nil || !owned {
			t.Fatalf("table %s ownership = %t, %v", table, owned, err)
		}
	}
	for _, index := range []string{"tag_groups_active_sort_unique", "tag_catalog_tags_active_sort_unique"} {
		var exists bool
		if err := native.QueryRow(ctx, `SELECT to_regclass(current_schema() || '.' || $1) IS NOT NULL`, index).Scan(&exists); err != nil || !exists {
			t.Fatalf("required index %s exists=%t err=%v", index, exists, err)
		}
	}
	wrapped, err := platformpostgres.Wrap(native, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewPostgreSQL(native, uow)
	if err != nil {
		t.Fatal(err)
	}
	service := tagapp.NewService(uow, repository, repository, repository, repository)
	groupOne, tagOne, err := service.CreateGroup(ctx, domain.Command{Actor: 7, IdempotencyKey: "pg-group-one-key-0001", GroupName: "Lifecycle", FirstTagName: "Warm"})
	if err != nil {
		t.Fatal(err)
	}
	groupTwo, groupTwoTag, err := service.CreateGroup(ctx, domain.Command{Actor: 7, IdempotencyKey: "pg-group-two-key-0001", GroupName: "Source", FirstTagName: "Organic"})
	if err != nil {
		t.Fatal(err)
	}
	tagTwo, err := service.CreateTag(ctx, domain.Command{Actor: 7, IdempotencyKey: "pg-tag-two-key-00001", GroupID: groupOne.ID, GroupName: groupOne.Name, TagName: "Hot"})
	if err != nil {
		t.Fatal(err)
	}
	groups, err := service.ReorderGroups(ctx, domain.Command{Actor: 7, IdempotencyKey: "pg-group-swap-key-01", IDs: []int64{groupTwo.ID, groupOne.ID}})
	if err != nil || groups[0].ID != groupTwo.ID || groups[1].ID != groupOne.ID {
		t.Fatalf("group swap = %#v, %v", groups, err)
	}
	tags, err := service.ReorderTags(ctx, domain.Command{Actor: 7, IdempotencyKey: "pg-tag-swap-key-0001", IDs: []int64{groupTwoTag.ID, tagTwo.ID}})
	if err == nil { // full catalog includes groupTwo's first tag; stale subset must fail closed.
		t.Fatalf("partial tag reorder unexpectedly succeeded: %#v", tags)
	}
	if _, err = service.ReorderTags(ctx, domain.Command{Actor: 7, IdempotencyKey: "pg-tag-cross-group-key", IDs: []int64{tagTwo.ID, groupTwoTag.ID, tagOne.ID}}); err == nil {
		t.Fatal("cross-group tag reorder unexpectedly succeeded")
	}
	tags, err = service.ReorderTags(ctx, domain.Command{Actor: 7, IdempotencyKey: "pg-tag-swap-all-key", IDs: []int64{groupTwoTag.ID, tagTwo.ID, tagOne.ID}})
	if err != nil || len(tags) != 3 || tags[0].ID != groupTwoTag.ID || tags[1].ID != tagTwo.ID || tags[2].ID != tagOne.ID {
		t.Fatalf("tag group-preserving swap = %#v, %v", tags, err)
	}
	archive := domain.Command{Actor: 7, TagID: tagOne.ID, IdempotencyKey: "pg-tag-archive-replay"}
	first, err := service.ArchiveTag(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ArchiveTag(ctx, archive)
	if err != nil || second != first {
		t.Fatalf("archive replay = %#v, %v; want %#v", second, err, first)
	}
	if _, err = service.GetTag(ctx, tagOne.ID); !errors.Is(err, tagapp.ErrNotFound) {
		t.Fatalf("archived public GetTag = %v", err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO tag_references(resource_kind,resource_id,reference_digest,owner) VALUES('tag',$1,'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','test')`, tagTwo.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ArchiveGroup(ctx, domain.Command{Actor: 7, GroupID: groupOne.ID, IdempotencyKey: "pg-group-reference-key"}); !errors.Is(err, tagapp.ErrReferenced) {
		t.Fatalf("group archive with child tag ref = %v", err)
	}
	var effectID int64
	if err = native.QueryRow(ctx, `INSERT INTO external_effects(owner,kind,source_ref_digest,target_ref_digest,payload_digest,policy_version_hash,envelope_fingerprint,state) VALUES('outbound','wecom_tag_catalog',$1,$2,$3,$4,$5,'executed') RETURNING id`, Digest("source"), Digest("target"), Digest("payload"), Digest("policy"), Digest("fingerprint")).Scan(&effectID); err != nil {
		t.Fatal(err)
	}
	if _, err = native.Exec(ctx, `INSERT INTO external_effect_generations(effect_id,generation) VALUES($1,1)`, effectID); err != nil {
		t.Fatal(err)
	}
	observation := tagport.ProviderObservation{EffectID: effectID, Generation: 1, Snapshot: []byte(`{"groups":[]}`)}
	observation.ArtifactDigest = providerArtifactDigest(observation.Snapshot)
	if err = uow.Within(ctx, func(txCtx context.Context) error { return repository.StoreProviderObservation(txCtx, observation) }); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(txCtx context.Context) error { return repository.StoreProviderObservation(txCtx, observation) }); err != nil {
		t.Fatalf("same observation replay=%v", err)
	}
	driftObservation := observation
	driftObservation.ArtifactDigest = providerArtifactDigest([]byte(`{"groups":[{"id":"g","name":"g","order":0,"tags":[]}]}`))
	driftObservation.Snapshot = []byte(`{"groups":[{"id":"g","name":"g","order":0,"tags":[]}]}`)
	if err = uow.Within(ctx, func(txCtx context.Context) error { return repository.StoreProviderObservation(txCtx, driftObservation) }); !errors.Is(err, ErrConflict) {
		t.Fatalf("observation drift=%v", err)
	}
	if _, err = native.Exec(ctx, `UPDATE tag_provider_observations SET artifact_digest=artifact_digest`); err == nil {
		t.Fatal("observation update unexpectedly succeeded")
	}
	if _, err = native.Exec(ctx, `DELETE FROM tag_provider_observations`); err == nil {
		t.Fatal("observation delete unexpectedly succeeded")
	}
}

func tagIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured; skipping PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	var bytes [8]byte
	if _, err = rand.Read(bytes[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_tags_test_" + hex.EncodeToString(bytes[:])
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	config = config.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration test")
	}
	base := filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations")
	effectsSQL, err := os.ReadFile(filepath.Join(base, "0005_external_effects.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(effectsSQL)); err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile(filepath.Join(base, "0008_tag_catalog.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}
