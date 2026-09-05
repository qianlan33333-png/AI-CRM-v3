package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	segmentmigration "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/migration"
)

type resolverStub struct {
	results map[string]identityport.ResolveResult
	err     error
}

func (stub resolverStub) Resolve(_ context.Context, reference identitydomain.Reference) (identityport.ResolveResult, error) {
	if stub.err != nil {
		return identityport.ResolveResult{}, stub.err
	}
	result, ok := stub.results[string(reference.Kind)+"|"+reference.Scope+"|"+reference.Value]
	if !ok {
		return identityport.ResolveResult{Status: identityport.ResolveNotFound}, nil
	}
	return result, nil
}

func TestResolveOneIDNeverUsesSourceCustomerID(t *testing.T) {
	resolver := resolverStub{results: map[string]identityport.ResolveResult{
		"unionid|wechat-open-platform:wx-open|union-1": {Status: identityport.ResolveFound, CustomerID: customerdomain.CustomerID(91)},
	}}
	id, disposition, err := resolveOneID(context.Background(), resolver, []identityRow{{Kind: "unionid", Scope: "wechat-open-platform:wx-open", Value: "union-1", Assurance: "verified", Source: "provider-history:v2"}})
	if err != nil || id != 91 || disposition != "mapped" {
		t.Fatalf("id=%d disposition=%s err=%v", id, disposition, err)
	}
	id, disposition, err = resolveOneID(context.Background(), resolver, nil)
	if err != nil || id != 0 || disposition != "unresolved" {
		t.Fatalf("id=%d disposition=%s err=%v", id, disposition, err)
	}
}

func TestResolveOneIDConflictsAcrossCanonicalRoots(t *testing.T) {
	resolver := resolverStub{results: map[string]identityport.ResolveResult{
		"unionid|wechat-open-platform:wx-open|union-1":  {Status: identityport.ResolveFound, CustomerID: 91},
		"wecom_external_userid|wecom-corp:corp-1|ext-1": {Status: identityport.ResolveFound, CustomerID: 92},
	}}
	rows := []identityRow{
		{Kind: "unionid", Scope: "wechat-open-platform:wx-open", Value: "union-1", Assurance: "verified", Source: "provider-history:v2"},
		{Kind: "wecom_external_userid", Scope: "wecom-corp:corp-1", Value: "ext-1", Assurance: "verified", Source: "provider-history:v2"},
	}
	if id, disposition, err := resolveOneID(context.Background(), resolver, rows); err != nil || id != 0 || disposition != "conflict" {
		t.Fatalf("id=%d disposition=%s err=%v", id, disposition, err)
	}
}

func TestResolveOneIDRejectsUnverifiedInvalidAndPropagatesStoreFailure(t *testing.T) {
	if _, disposition, err := resolveOneID(context.Background(), resolverStub{}, []identityRow{{Kind: "unionid", Scope: "", Value: "x", Assurance: "verified", Source: "provider-history:v2"}}); err != nil || disposition != "invalid" {
		t.Fatalf("disposition=%s err=%v", disposition, err)
	}
	if _, disposition, err := resolveOneID(context.Background(), resolverStub{}, []identityRow{{Kind: "unionid", Scope: "wechat-open-platform:wx-open", Value: "x", Assurance: "declared", Source: "provider-history:v2"}}); err != nil || disposition != "unresolved" {
		t.Fatalf("disposition=%s err=%v", disposition, err)
	}
	want := errors.New("store unavailable")
	if _, _, err := resolveOneID(context.Background(), resolverStub{err: want}, []identityRow{{Kind: "unionid", Scope: "wechat-open-platform:wx-open", Value: "x", Assurance: "verified", Source: "provider-history:v2"}}); !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}

func TestImportDryRunApplyReplayAndReconcilePostgreSQL(t *testing.T) {
	url, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("database URL not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	random := make([]byte, 6)
	if _, err = rand.Read(random); err != nil {
		t.Fatal(err)
	}
	schema := "automationops_migration_" + hex.EncodeToString(random)
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	applyPlatformSQL(t, ctx, pool)
	if _, err = pool.Exec(ctx, `CREATE TABLE river_job(id BIGSERIAL PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	var actorID, customerID int64
	if err = pool.QueryRow(ctx, `INSERT INTO admin_users(username,password_hash,display_name,wecom_userid,is_active,created_at,updated_at) VALUES('migration-admin','$argon2id$fixture','Migration Admin','staff-provider-1',true,clock_timestamp(),clock_timestamp()) RETURNING id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO customers(status,created_at,updated_at) VALUES('active',clock_timestamp(),clock_timestamp()) RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,status,verified_at,created_at,updated_at) VALUES($1,'unionid','wechat-open-platform:fixture','union-fixture','verified','fixture',1,'active',clock_timestamp(),clock_timestamp(),clock_timestamp())`, customerID); err != nil {
		t.Fatal(err)
	}
	snapshot := migrationFixture(t)
	dependencies := Dependencies{ActorID: actorID, Identity: identityapp.OneIDService{Store: identitystore.NewPostgresStore()}, Access: accessstore.NewPostgreSQL()}
	report, err := Import(ctx, pool, snapshot, dependencies, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Tables["audience_members"].Mapped != 1 {
		t.Fatalf("dry-run report=%+v", report)
	}
	var batches int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM automation_operations_migration_batches`).Scan(&batches); err != nil || batches != 0 {
		t.Fatalf("dry-run batches=%d err=%v", batches, err)
	}
	report, err = Import(ctx, pool, snapshot, dependencies, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.ProviderEffectsCreated != 0 || report.RiverJobsCreated != 0 {
		t.Fatalf("side effects=%+v", report)
	}
	// Re-applying the same frozen snapshot is the operator's recovery path.
	// It must only load the prior source receipts: no history row may be
	// rewritten and it must not create River work or a Provider effect.
	replay, err := Import(ctx, pool, snapshot, dependencies, false)
	if err != nil || replay.Tables["audience_members"].Mapped != 1 {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	if replay.ProviderEffectsCreated != 0 || replay.RiverJobsCreated != 0 {
		t.Fatalf("replay side effects=%+v", replay)
	}
	var lifecycle string
	var member int64
	if err = pool.QueryRow(ctx, `SELECT p.lifecycle,m.customer_id FROM segment_audience_packages p JOIN segment_audience_snapshot_members m ON m.snapshot_id=p.published_snapshot_id WHERE p.code='v2-audience-10'`).Scan(&lifecycle, &member); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "paused" || member != customerID {
		t.Fatalf("lifecycle=%s member=%d want=%d", lifecycle, member, customerID)
	}
	var historyRows, readOnlyRows, replayableRows, effectDigests, effects, riverJobs int
	if err = pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE read_only),count(*) FILTER (WHERE replayable),count(*) FILTER (WHERE source_effect_digest IS NOT NULL) FROM automation_operations_legacy_history`).Scan(&historyRows, &readOnlyRows, &replayableRows, &effectDigests); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM external_effects`).Scan(&effects); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM river_job`).Scan(&riverJobs); err != nil {
		t.Fatal(err)
	}
	if historyRows != 4 || readOnlyRows != 4 || replayableRows != 0 || effectDigests != 1 || effects != 0 || riverJobs != 0 {
		t.Fatalf("legacy history rows=%d readonly=%d replayable=%d effects=%d external=%d river=%d", historyRows, readOnlyRows, replayableRows, effectDigests, effects, riverJobs)
	}
	if _, err = Reconcile(ctx, pool, report.BatchKey); err != nil {
		t.Fatal(err)
	}
	shadow, err := Shadow(ctx, pool, report.BatchKey, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !shadow.ReadyForProbe || len(shadow.Packages) != 1 || !shadow.Packages[0].MemberDigestMatches || len(shadow.History) != 4 {
		t.Fatalf("shadow=%+v", shadow)
	}
	for _, item := range shadow.History {
		if !item.Ready || !item.StateMatches || !item.OccurredAtMatches || !item.ReadOnly || item.Replayable {
			t.Fatalf("history shadow=%+v", item)
		}
	}
}

func applyPlatformSQL(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	directory := filepath.Join(filepath.Dir(file), "..", "..", "migrations")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		raw, readErr := os.ReadFile(filepath.Join(directory, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = pool.Exec(ctx, string(raw)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
}

func migrationFixture(t *testing.T) segmentmigration.Snapshot {
	t.Helper()
	at := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	rows := map[string]any{
		"audience_groups":                 []any{map[string]any{"id": 1, "name": "Migrated", "sort_order": 1, "version": 1, "created_at": at, "updated_at": at}},
		"audience_packages":               []any{map[string]any{"id": 10, "group_id": 1, "lifecycle": "active", "version": 2, "name": "Legacy audience", "definition": map[string]any{"schema_version": 1, "template_key": "active_contacts", "parameters": map[string]any{"within_days": "30"}}, "refresh_mode": "manual", "member_count": 1, "created_at": at, "updated_at": at}},
		"audience_configuration_versions": []any{map[string]any{"package_id": 10, "version": 1, "definition": map[string]any{"schema_version": 1, "template_key": "active_contacts", "parameters": map[string]any{"within_days": "30"}}, "refresh_mode": "manual", "created_at": at}},
		"automation_agents":               []any{map[string]any{"id": 20, "agent_name": "Fixed", "agent_code": "migrated-fixed", "automation_type": "fixed_script", "status": "paused", "draft_role_prompt": "", "draft_task_prompt": "", "published_role_prompt": "", "published_task_prompt": "", "draft_version": 1, "published_version": 1, "fixed_content_package_json": map[string]any{"content_text": "hello", "image_library_ids": []int64{}, "miniprogram_library_ids": []int64{}, "attachment_library_ids": []int64{}, "group_invite_library_ids": []int64{}}, "created_at": at, "updated_at": at}},
		"audience_bindings":               []any{map[string]any{"package_id": 10, "automation_agent_id": 20, "version": 1, "created_at": at, "updated_at": at}},
		"audience_senders":                []any{map[string]any{"package_id": 10, "sender_userid": "staff-provider-1", "sort_order": 1, "is_enabled": true, "created_at": at, "updated_at": at}},
		"audience_members":                []any{map[string]any{"segment_id": 10, "customer_id": 999999, "computed_at": at, "identities": []any{map[string]any{"kind": "unionid", "scope": "wechat-open-platform:fixture", "value": "union-fixture", "assurance": "verified", "source": "fixture"}}}},
		"automation_policies":             []any{map[string]any{"id": 30, "status": "active", "created_at": at}},
		"automation_policy_versions":      []any{map[string]any{"automation_id": 30, "version": 2, "status": "published", "published_at": at.Add(time.Minute)}},
		"automation_enrollments":          []any{map[string]any{"id": 40, "state": "sent", "external_effect_id": "legacy-effect-40", "enrolled_at": at.Add(2 * time.Minute)}},
		"automation_actions":              []any{map[string]any{"id": 50, "status": "failed", "completed_at": at.Add(3 * time.Minute)}},
	}
	snapshot := segmentmigration.Snapshot{Manifest: segmentmigration.Manifest{SourceSystem: "fixture", DonorCommit: segmentmigration.DonorCommit, SnapshotAt: at, SchemaDigest: strings.Repeat("1", 64), Counts: map[string]int{}, Digests: map[string]string{}}, Tables: map[string]json.RawMessage{}}
	for _, name := range segmentmigration.LogicalTables {
		raw, err := json.Marshal(rows[name])
		if err != nil {
			t.Fatal(err)
		}
		snapshot.Tables[name] = raw
		var decoded []json.RawMessage
		if err = json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		snapshot.Manifest.Counts[name] = len(decoded)
		digest := sha256.Sum256(raw)
		snapshot.Manifest.Digests[name] = hex.EncodeToString(digest[:])
	}
	watermark := sha256.Sum256([]byte("fixture"))
	snapshot.Manifest.SourceWatermarkDigest = hex.EncodeToString(watermark[:])
	return snapshot
}
