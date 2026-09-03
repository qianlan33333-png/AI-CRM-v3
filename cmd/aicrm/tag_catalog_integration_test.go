package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	tagstore "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/store"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

type integrationCatalogReader func(context.Context) (outbound.CatalogSnapshot, error)

func (f integrationCatalogReader) ListCatalog(ctx context.Context) (outbound.CatalogSnapshot, error) {
	return f(ctx)
}

type integrationAdapter func(context.Context, effectport.Envelope, effectport.Attempt) (effectport.AdapterResult, error)

func (f integrationAdapter) Execute(ctx context.Context, e effectport.Envelope, a effectport.Attempt) (effectport.AdapterResult, error) {
	return f(ctx, e, a)
}

func TestPostgreSQLCatalogCompletionEndToEnd(t *testing.T) {
	pool, cleanup := catalogIntegrationPool(t)
	defer cleanup()
	ctx := context.Background()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	tags, err := tagstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	sink, err := outbound.NewTagCatalogCompletionSink(tags)
	if err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[effects.EffectJobArgs](workers, effects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	client, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := effects.NewRepository(pool, client)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.SetCompletionSink(sink); err != nil {
		t.Fatal(err)
	}
	provider, _ := outbound.NewTagCatalogProvider(integrationCatalogReader(func(context.Context) (outbound.CatalogSnapshot, error) {
		return outbound.CatalogSnapshot{Groups: []outbound.CatalogGroup{{ID: "g", Name: "group", Tags: []outbound.CatalogTag{{ID: "t", Name: "tag"}}}}}, nil
	}))
	projection, id, job := acceptCatalogEffect(t, ctx, pool, repository, "executed")
	if err = repository.RunAttempt(ctx, id, 1, job, provider); err != nil {
		t.Fatal(err)
	}
	current, err := repository.Get(ctx, projection.ID)
	if err != nil || current.State != effects.StateExecuted {
		t.Fatalf("executed=%+v err=%v", current, err)
	}
	var snapshot string
	if err = pool.QueryRow(ctx, `SELECT snapshot::text FROM tag_provider_observations WHERE effect_id=$1 AND generation=1`, id).Scan(&snapshot); err != nil || snapshot == "" {
		t.Fatalf("observation=%q err=%v", snapshot, err)
	}

	// Schema-invalid but digest-valid artifacts fail inside the real tag sink;
	// the EER completion CAS rolls back and no observation appears.
	_, badID, badJob := acceptCatalogEffect(t, ctx, pool, repository, "sink-fail")
	badPayload := []byte(`{"groups":null}`)
	badArtifact := effectport.ResultArtifact{Kind: "wecom.tag_catalog.snapshot.v1", Payload: badPayload, Digest: effectport.Hash("external-effect.artifact.v1", "wecom.tag_catalog.snapshot.v1", string(badPayload))}
	badAdapter := integrationAdapter(func(context.Context, effectport.Envelope, effectport.Attempt) (effectport.AdapterResult, error) {
		return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("receipt", "bad"), CallAttempted: true, RealExternalCallExecuted: true, Artifact: badArtifact}, nil
	})
	if err = repository.RunAttempt(ctx, badID, 1, badJob, badAdapter); err == nil {
		t.Fatal("expected sink validation failure")
	}
	current, err = repository.Get(ctx, "eer_"+itoa(badID))
	if err != nil || current.State != effects.StateAttempted {
		t.Fatalf("sink rollback=%+v err=%v", current, err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM tag_provider_observations WHERE effect_id=$1`, badID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("sink rollback snapshot count=%d err=%v", count, err)
	}

	_, unknownID, unknownJob := acceptCatalogEffect(t, ctx, pool, repository, "unknown")
	unknown := integrationAdapter(func(context.Context, effectport.Envelope, effectport.Attempt) (effectport.AdapterResult, error) {
		return effectport.AdapterResult{CallAttempted: true}, errors.New("post-call")
	})
	if err = repository.RunAttempt(ctx, unknownID, 1, unknownJob, unknown); err != nil {
		t.Fatal(err)
	}
	current, err = repository.Get(ctx, "eer_"+itoa(unknownID))
	if err != nil || current.State != effects.StateUnknown {
		t.Fatalf("unknown=%+v err=%v", current, err)
	}
	if _, _, err = repository.Reconcile(ctx, effects.ControlCommand{EffectID: "eer_" + itoa(unknownID), ReceiptKey: effectport.Hash("reconcile", "unknown"), EvidenceDigest: effectport.Hash("evidence", "not-applied"), ActorAdminUserID: 7}); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM tag_provider_observations WHERE effect_id=$1`, unknownID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("reconciled wrote snapshot count=%d err=%v", count, err)
	}

	_, finalID, finalJob := acceptCatalogEffect(t, ctx, pool, repository, "final-failed")
	final := integrationAdapter(func(context.Context, effectport.Envelope, effectport.Attempt) (effectport.AdapterResult, error) {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: effectport.Hash("receipt", "final")}, nil
	})
	if err = repository.RunAttempt(ctx, finalID, 1, finalJob, final); err != nil {
		t.Fatal(err)
	}
	current, err = repository.Get(ctx, "eer_"+itoa(finalID))
	if err != nil || current.State != effects.StateFinalFailed {
		t.Fatalf("final=%+v err=%v", current, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM tag_provider_observations WHERE effect_id=$1`, finalID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("final wrote snapshot count=%d err=%v", count, err)
	}

	// A stale River generation returns before adapter/sink work.
	stale, staleID, staleJob := acceptCatalogEffect(t, ctx, pool, repository, "stale")
	if _, err = pool.Exec(ctx, `UPDATE external_effects SET state='retryable_failed' WHERE id=$1`, staleID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repository.Retry(ctx, effects.ControlCommand{EffectID: stale.ID, ReceiptKey: effectport.Hash("retry", "stale"), ActorAdminUserID: 7}); err != nil {
		t.Fatal(err)
	}
	if err = repository.RunAttempt(ctx, staleID, 1, staleJob, provider); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM tag_provider_observations WHERE effect_id=$1`, staleID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale wrote snapshot count=%d err=%v", count, err)
	}
}

func acceptCatalogEffect(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repository *effects.Repository, unique string) (effectport.Projection, int64, int64) {
	t.Helper()
	e := effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindWeComTagCatalog, SourceRefDigest: effectport.Hash("src", unique), TargetRefDigest: effectport.Hash("target"), PayloadDigest: effectport.Hash("payload", unique), PolicyVersionHash: effectport.Hash("policy")}
	p, _, err := repository.AcceptAndQueue(ctx, effectport.AcceptCommand{ReceiptKey: effectport.Hash("accept", unique), Envelope: e})
	if err != nil {
		t.Fatal(err)
	}
	var id, job int64
	if _, err = fmt.Sscanf(p.ID, "eer_%d", &id); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT river_job_id FROM external_effect_jobs WHERE effect_id=$1`, id).Scan(&job); err != nil {
		t.Fatal(err)
	}
	return p, id, job
}

func catalogIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var b [8]byte
	if _, err = rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "aicrm_catalog_effect_" + hex.EncodeToString(b[:])
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	cfg = cfg.Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate")
	}
	base := filepath.Join(filepath.Dir(file), "..", "..", "migrations")
	for _, name := range []string{"0005_external_effects.sql", "0008_tag_catalog.sql"} {
		sql, readErr := os.ReadFile(filepath.Join(base, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	return pool, func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}
}

func itoa(value int64) string { return strconv.FormatInt(value, 10) }

var _ tagport.SnapshotWriter = (*tagstore.Repository)(nil)
