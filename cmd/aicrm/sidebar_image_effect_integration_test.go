package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"github.com/riverqueue/river"
)

type sidebarImageEffectUploader struct {
	calls     int
	uncertain bool
	t         *testing.T
}

func (u *sidebarImageEffectUploader) UploadSidebarImage(ctx context.Context, _ outboundport.SidebarImagePreparationSource) (outboundport.SidebarImageUploadReceipt, bool, error) {
	if _, err := platformpostgres.RequireTransaction(ctx); err == nil {
		u.t.Fatal("network inside UoW")
	}
	u.calls++
	if u.uncertain {
		return outboundport.SidebarImageUploadReceipt{}, true, errors.New("lost upload response")
	}
	return outboundport.SidebarImageUploadReceipt{MediaID: "official-temporary-media", ReadyUntil: time.Now().Add(70 * time.Hour)}, true, nil
}

// Runs the real EER acceptance, attempt and completion kernel. The transport
// alone is substituted; there is no message-sending provider in this test.
func TestPostgreSQLSidebarImageEffectCompletesAndRecoversUnknown(t *testing.T) {
	ctx := context.Background()
	dsn, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	native, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := platformpostgres.Wrap(native, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	workers := river.NewWorkers()
	if err = river.AddWorkerSafely[externaleffects.EffectJobArgs](workers, externaleffects.NewWorker(nil, nil)); err != nil {
		t.Fatal(err)
	}
	insert, err := platformjobqueue.NewInsertClient(native, workers)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := externaleffects.NewRepository(native, insert)
	if err != nil {
		t.Fatal(err)
	}
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := outbound.NewSidebarMediaPreparationService(uow, effects, native)
	if err != nil {
		t.Fatal(err)
	}
	if err = effects.SetCompletionSink(service); err != nil {
		t.Fatal(err)
	}
	upload := &sidebarImageEffectUploader{t: t}
	provider, err := outbound.NewSidebarMediaPreparationProvider(service, upload)
	if err != nil {
		t.Fatal(err)
	}
	source := outboundport.SidebarImagePreparationSource{ImageID: 1, Content: []byte("trusted-image-fixture"), FileName: "image.png", MediaType: "image/png", Scope: "corp:agent"}
	source.SourceDigest = sha256.Sum256(source.Content)
	through := time.Now().Add(6 * time.Minute)
	run := func(prepared outboundport.SidebarImagePreparation, stale bool) {
		t.Helper()
		id, e := strconv.ParseInt(strings.TrimPrefix(prepared.EffectID, "eer_"), 10, 64)
		if e != nil {
			t.Fatal(e)
		}
		var jobID int64
		if e = native.QueryRow(ctx, `SELECT river_job_id FROM external_effect_jobs WHERE effect_id=$1 AND generation=1`, id).Scan(&jobID); e != nil {
			t.Fatal(e)
		}
		if stale {
			if _, e = native.Exec(ctx, `UPDATE external_effects SET state='attempted',attempt_count=1,lease_fence=1,lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, id); e != nil {
				t.Fatal(e)
			}
			if _, e = native.Exec(ctx, `INSERT INTO external_effect_attempts(effect_id,number,generation,fence,state) VALUES($1,1,1,1,'attempted')`, id); e != nil {
				t.Fatal(e)
			}
		}
		if e = effects.RunAttempt(ctx, id, 1, jobID, provider); e != nil {
			t.Fatal(e)
		}
	}
	first, err := service.PrepareSidebarImage(ctx, source, through)
	if err != nil || first.State != "queued" {
		t.Fatalf("prepare=%+v %v", first, err)
	}
	run(first, false)
	ready, err := service.PrepareSidebarImage(ctx, source, through)
	if err != nil || ready.State != "ready" || ready.MediaID != "official-temporary-media" {
		t.Fatalf("real EER completion did not expose ready: %+v %v", ready, err)
	}
	projection, err := effects.Get(ctx, first.EffectID)
	if err != nil || projection.State != externaleffects.StateExecuted {
		t.Fatalf("effect=%+v %v", projection, err)
	}
	var events, outbox, sends int
	if err = native.QueryRow(ctx, `SELECT (SELECT count(*) FROM outbound_sidebar_image_events),(SELECT count(*) FROM outbound_sidebar_image_outbox),(SELECT count(*) FROM outbound_sidebar_send_intents)`).Scan(&events, &outbox, &sends); err != nil {
		t.Fatal(err)
	}
	if events != 2 || outbox != 2 || sends != 0 {
		t.Fatalf("events=%d outbox=%d sends=%d", events, outbox, sends)
	}
	// Lost transport response is projected as unknown and cannot create a new
	// upload even if the caller changes content or retries its browser request.
	source.ImageID = 2
	upload.uncertain = true
	unknown, err := service.PrepareSidebarImage(ctx, source, through)
	if err != nil {
		t.Fatal(err)
	}
	run(unknown, false)
	locked, err := service.PrepareSidebarImage(ctx, source, through)
	if err != nil || locked.State != "outcome_unknown" || locked.EffectID != unknown.EffectID {
		t.Fatalf("unknown=%+v %v", locked, err)
	}
	// Simulate process loss after EER committed attempted: stale recovery must
	// project the owning upload fact too and must not call the transport again.
	source.ImageID = 3
	stale, err := service.PrepareSidebarImage(ctx, source, through)
	if err != nil {
		t.Fatal(err)
	}
	run(stale, true)
	locked, err = service.PrepareSidebarImage(ctx, source, through)
	if err != nil || locked.State != "outcome_unknown" || locked.EffectID != stale.EffectID {
		t.Fatalf("stale recovery=%+v %v", locked, err)
	}
	if upload.calls != 2 {
		t.Fatalf("upload calls=%d", upload.calls)
	}
	if err = native.QueryRow(ctx, `SELECT count(*) FROM outbound_sidebar_image_preparations`).Scan(&events); err != nil || events != 3 {
		t.Fatalf("replacement intents=%d %v", events, err)
	}
}
