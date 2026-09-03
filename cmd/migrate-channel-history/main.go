package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

type options struct {
	mode, snapshot, report, manifestDigest, unionIDScope string
	actorID                                              int64
	confirm                                              bool
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "channel history migration failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("migrate-channel-history", flag.ContinueOnError)
	var cfg options
	flags.StringVar(&cfg.mode, "mode", "inspect", "inspect|validate|dry-run|import|reconcile|replay-check|rollback")
	flags.StringVar(&cfg.snapshot, "snapshot", "", "encrypted snapshot path")
	flags.StringVar(&cfg.report, "report", "", "optional schema discovery report path")
	flags.StringVar(&cfg.manifestDigest, "manifest-sha256", "", "required manifest digest for mutating modes")
	flags.StringVar(&cfg.unionIDScope, "unionid-scope", "", "verified OneID scope, for example wechat-open-platform:main")
	flags.Int64Var(&cfg.actorID, "actor-id", 0, "active migration administrator id; zero selects the first active superadmin")
	flags.BoolVar(&cfg.confirm, "confirm", false, "confirm import or rollback")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if cfg.mode == "inspect" {
		return inspectSource(ctx, cfg)
	}
	if cfg.snapshot == "" {
		return errors.New("--snapshot is required")
	}
	key, err := snapshotKeyFromEnvironment()
	if err != nil {
		return err
	}
	manifest, err := loadEncryptedSnapshot(cfg.snapshot, key)
	if err != nil {
		return err
	}
	if err = manifest.Validate(); err != nil {
		return err
	}
	if cfg.mode == "validate" {
		return printJSON(map[string]any{"mode": cfg.mode, "valid": true, "snapshot_id": manifest.SnapshotID, "manifest_sha256": manifest.DigestHex(), "summary": manifest.Summary()})
	}
	if cfg.mode == "dry-run" {
		return printJSON(map[string]any{"mode": cfg.mode, "eligible": true, "provider_calls": 0, "provider_effects": 0, "snapshot_id": manifest.SnapshotID, "manifest_sha256": manifest.DigestHex(), "summary": manifest.Summary()})
	}
	if !strings.EqualFold(cfg.manifestDigest, manifest.DigestHex()) {
		return errors.New("manifest digest confirmation mismatch")
	}
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL, MaxConnections: 10, MinConnections: 1})
	if err != nil {
		return err
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	runner := importRunner{Pool: pool, UOW: uow, Resolver: identityapp.OneIDService{Store: identitystore.NewPostgresStore()}, UnionIDScope: cfg.unionIDScope, ActorID: cfg.actorID}
	switch cfg.mode {
	case "import":
		if !cfg.confirm {
			return errors.New("import requires --confirm")
		}
		result, applyErr := runner.Import(ctx, manifest)
		if applyErr != nil {
			return applyErr
		}
		return printJSON(map[string]any{"mode": cfg.mode, "result": result})
	case "reconcile":
		result, reconcileErr := runner.Reconcile(ctx, manifest)
		if reconcileErr != nil {
			return reconcileErr
		}
		return printJSON(map[string]any{"mode": cfg.mode, "result": result})
	case "replay-check":
		result, replayErr := runner.ReplayCheck(ctx, manifest)
		if replayErr != nil {
			return replayErr
		}
		return printJSON(map[string]any{"mode": cfg.mode, "result": result})
	case "rollback":
		if !cfg.confirm {
			return errors.New("rollback requires --confirm")
		}
		result, rollbackErr := runner.Rollback(ctx, manifest)
		if rollbackErr != nil {
			return rollbackErr
		}
		return printJSON(map[string]any{"mode": cfg.mode, "result": result})
	default:
		return errors.New("unsupported mode")
	}
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
