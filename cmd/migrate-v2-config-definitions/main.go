package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	automationstore "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/configmigration/source"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/configmigration/target"
	couponstore "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/store"
	groupopsstore "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate-v2-config-definitions", flag.ContinueOnError)
	mode := fs.String("mode", "inspect", "inspect|extract|dry-run|apply|verify")
	snapshot := fs.String("snapshot", "", "encrypted snapshot")
	key := fs.String("snapshot-key-file", "", "0600 AES key")
	revision := fs.String("source-revision", "", "40-char source revision")
	actor := fs.Int64("actor-admin-user-id", 0, "explicit target administrator")
	want := fs.String("manifest-sha256", "", "snapshot digest confirmation")
	confirm := fs.Bool("confirm-apply", false, "confirm target write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *mode == "extract" {
		if *snapshot == "" || *key == "" || *revision == "" {
			return errors.New("extract requires AICRM_SOURCE_DATABASE_URL, snapshot, snapshot-key-file, source-revision")
		}
		sourceDatabaseURL, e := platformconfig.SourceDatabaseURL()
		if e != nil {
			return e
		}
		p, e := pgxpool.New(ctx, sourceDatabaseURL)
		if e != nil {
			return e
		}
		defer p.Close()
		s, e := source.Extract(ctx, p, *revision)
		if e != nil {
			return e
		}
		if e = source.ValidateExpectedBaseline(s); e != nil {
			return e
		}
		d, e := source.SealToFile(s, *snapshot, *key)
		if e != nil {
			return e
		}
		return print(summary("extract", s, d))
	}
	if *snapshot == "" || *key == "" {
		return errors.New("snapshot and snapshot-key-file are required")
	}
	s, d, e := source.LoadFile(*snapshot, *key)
	if e != nil {
		return e
	}
	if e = s.Validate(); e != nil {
		return e
	}
	if e = source.ValidateExpectedBaseline(s); e != nil {
		return e
	}
	if *mode == "inspect" {
		return print(summary("inspect", s, d))
	}
	if *actor < 1 {
		return errors.New("explicit actor-admin-user-id is required")
	}
	if *want != target.DigestHex(d) {
		return errors.New("manifest-sha256 confirmation mismatch")
	}
	url, e := platformconfig.DatabaseURL()
	if e != nil {
		return e
	}
	pool, e := platformpostgres.Open(ctx, platformpostgres.Config{URL: url})
	if e != nil {
		return e
	}
	defer pool.Close()
	if *mode != "apply" && *mode != "dry-run" && *mode != "verify" {
		return errors.New("unknown mode")
	}
	if *mode == "apply" && !*confirm {
		return errors.New("apply requires --confirm-apply")
	}
	uow, e := platformpostgres.NewUnitOfWork(pool)
	if e != nil {
		return e
	}
	p, e := productstore.NewPostgreSQL(pool.Native(), uow)
	if e != nil {
		return e
	}
	c, e := couponstore.NewPostgreSQL(pool.Native(), uow)
	if e != nil {
		return e
	}
	g, e := groupopsstore.NewPostgreSQL(pool.Native(), uow)
	if e != nil {
		return e
	}
	a, e := automationstore.NewPostgreSQL(pool.Native(), uow)
	if e != nil {
		return e
	}
	runner := target.Runner{UOW: uow, Products: p, Coupons: c, GroupOps: g, Automation: a}
	if *mode == "dry-run" {
		if e = runner.Preflight(ctx, s, d, *actor); e != nil {
			return e
		}
		return print(map[string]any{"mode": "dry-run", "eligible": true, "manifest_sha256": target.DigestHex(d), "counts": s.Summary()})
	}
	if *mode == "verify" {
		out, e := runner.Verify(ctx, s, d)
		if e != nil {
			return e
		}
		return print(map[string]any{"mode": "verify", "manifest_sha256": target.DigestHex(d), "result": out})
	}
	out, e := runner.Apply(ctx, s, d, *actor)
	if e != nil {
		return e
	}
	return print(map[string]any{"mode": "apply", "manifest_sha256": target.DigestHex(d), "result": out})
}
func print(v any) error { return json.NewEncoder(os.Stdout).Encode(v) }

func summary(mode string, snapshot source.Snapshot, digest [32]byte) map[string]any {
	return map[string]any{
		"mode":            mode,
		"manifest_sha256": target.DigestHex(digest),
		"source_system":   snapshot.Manifest.SourceSystem,
		"source_revision": snapshot.Manifest.SourceRevision,
		"snapshot_at":     snapshot.Manifest.SnapshotAt,
		"counts":          snapshot.Summary(),
	}
}
