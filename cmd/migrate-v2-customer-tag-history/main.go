// migrate-v2-customer-tag-history imports immutable, already-executed v2
// external_effect_job facts. It never emits a current customer tag command.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	customerstore "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	tagstore "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/store"
)

const (
	sourceSystem = "v2_external_effect_job"
	streamMarker = "__AICRM_V2_EXTERNAL_EFFECT_JOB__|"
	timeMarker   = "__AICRM_V2_EXTERNAL_EFFECT_SNAPSHOT__|"
)

type options struct {
	mode, snapshot, sourceStream, digest, corpID string
	confirm                                      bool
}
type manifest struct {
	SchemaVersion int         `json:"schema_version"`
	SourceSystem  string      `json:"source_system"`
	CapturedAt    time.Time   `json:"captured_at"`
	CorpID        string      `json:"wecom_corp_id"`
	Jobs          []sourceJob `json:"jobs"`
}

// sourceJob mirrors the narrow v2 external_effect_job projection. target_id,
// actor_id and payload_json are PII-bearing source facts and remain only in
// the 0600 snapshot; reporting deliberately never serializes them.
type sourceJob struct {
	ID          int64           `json:"id"`
	EffectType  string          `json:"effect_type"`
	Operation   string          `json:"operation"`
	TargetID    string          `json:"target_id"`
	ActorID     string          `json:"actor_id"`
	Payload     json.RawMessage `json:"payload_json"`
	Status      string          `json:"status"`
	CreatedAt   time.Time       `json:"created_at"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
}
type counters struct{ Input, Imported, Replayed, Pending, Conflict, Excluded, Failed int }

func (c *counters) add(result customerport.HistoricalTagImportResult) {
	c.Imported += result.Imported
	c.Replayed += result.Replayed
	c.Pending += result.Pending
	c.Conflict += result.Conflict
	c.Excluded += result.Excluded
	c.Failed += result.Failed
}
func (c counters) conserved() bool {
	return c.Input == c.Imported+c.Replayed+c.Pending+c.Conflict+c.Excluded+c.Failed
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "customer tag history migration failed:", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("migrate-v2-customer-tag-history", flag.ContinueOnError)
	var o options
	flags.StringVar(&o.mode, "mode", "inspect", "extract|inspect|dry-run|apply|verify")
	flags.StringVar(&o.snapshot, "snapshot", "", "protected snapshot path")
	flags.StringVar(&o.sourceStream, "source-stream", "", "read-only v2 external_effect_job stream")
	flags.StringVar(&o.digest, "manifest-sha256", "", "exact protected snapshot SHA-256")
	flags.StringVar(&o.corpID, "wecom-corp-id", "", "WeCom corp ID scope for target_id")
	flags.BoolVar(&o.confirm, "confirm-apply", false, "confirm immutable receipt apply")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if o.mode == "extract" {
		if o.snapshot == "" || o.sourceStream == "" || o.corpID == "" {
			return errors.New("extract requires --snapshot, --source-stream and --wecom-corp-id")
		}
		m, err := extract(o.sourceStream, o.corpID)
		if err != nil {
			return err
		}
		if err = save(o.snapshot, m); err != nil {
			return err
		}
		return printResult("extract", m, counters{Input: len(m.Jobs)})
	}
	if o.snapshot == "" {
		return errors.New("--snapshot is required")
	}
	m, digest, err := load(o.snapshot)
	if err != nil {
		return err
	}
	if o.mode == "inspect" {
		return printResult("inspect", m, counters{Input: len(m.Jobs)})
	}
	if o.mode != "dry-run" && o.mode != "apply" && o.mode != "verify" {
		return errors.New("unsupported mode")
	}
	if o.mode == "apply" && (!o.confirm || !strings.EqualFold(o.digest, digest)) {
		return errors.New("apply requires --confirm-apply and exact --manifest-sha256")
	}
	dsn, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: dsn, MaxConnections: 8, MinConnections: 1})
	if err != nil {
		return err
	}
	defer pool.Close()
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	tags, err := tagstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return err
	}
	r := resolver{uow: uow, identities: identityapp.OneIDService{Store: identitystore.NewPostgresStore()}, staff: accessstore.NewPostgreSQL(), tags: tags, corpID: m.CorpID}
	records, out, err := r.records(ctx, m)
	if err != nil {
		return err
	}
	out.Input = len(m.Jobs)
	if o.mode == "dry-run" {
		return printResultWithCounters("dry-run", m, out)
	}
	service := customerapp.HistoricalTagImportService{UOW: uow, Store: customerstore.TagHistoryPostgreSQL{}}
	batch := customerport.HistoricalTagBatch{SourceSystem: sourceSystem, SnapshotDigest: "sha256:" + digest, SnapshotAt: m.CapturedAt}
	if o.mode == "verify" {
		got, verifyErr := service.VerifyHistoricalTagRecords(ctx, batch, records)
		if verifyErr != nil {
			return verifyErr
		}
		if got.Imported != out.Imported || got.Pending != out.Pending || got.Conflict != out.Conflict || got.Excluded != out.Excluded || got.Failed != out.Failed {
			return errors.New("verify result mismatch")
		}
		return printResultWithCounters("verify", m, out)
	}
	lease, err := acquire(ctx, pool.Native(), digest)
	if err != nil {
		return err
	}
	defer lease.Release()
	got, err := service.ApplyHistoricalTagRecords(ctx, batch, records)
	if err != nil {
		return err
	}
	out.Imported = got.Imported
	out.Replayed = got.Replayed
	out.Pending = got.Pending
	out.Conflict = got.Conflict
	out.Excluded = got.Excluded
	out.Failed = got.Failed
	if !out.conserved() {
		return errors.New("historical receipt conservation mismatch")
	}
	return printResultWithCounters("apply", m, out)
}

func extract(path, corpID string) (manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return manifest{}, err
	}
	defer f.Close()
	m := manifest{SchemaVersion: 1, SourceSystem: sourceSystem, CorpID: corpID, Jobs: []sourceJob{}}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	seen := map[int64]bool{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, timeMarker) {
			if !m.CapturedAt.IsZero() {
				return manifest{}, errors.New("duplicate source snapshot timestamp")
			}
			value := strings.TrimPrefix(line, timeMarker)
			m.CapturedAt, err = time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return manifest{}, errors.New("invalid source snapshot timestamp")
			}
			m.CapturedAt = m.CapturedAt.UTC()
			continue
		}
		if !strings.HasPrefix(line, streamMarker) {
			continue
		}
		raw, decodeErr := hex.DecodeString(strings.TrimPrefix(line, streamMarker))
		if decodeErr != nil {
			return manifest{}, errors.New("invalid external_effect_job source row")
		}
		var job sourceJob
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&job) != nil {
			return manifest{}, errors.New("source field drift or invalid external_effect_job row")
		}
		if err = validJob(job); err != nil {
			return manifest{}, err
		}
		if seen[job.ID] {
			return manifest{}, errors.New("duplicate external_effect_job source id")
		}
		seen[job.ID] = true
		m.Jobs = append(m.Jobs, job)
	}
	if err = scanner.Err(); err != nil {
		return manifest{}, err
	}
	if m.CapturedAt.IsZero() {
		return manifest{}, errors.New("source snapshot timestamp unavailable")
	}
	if err = validManifest(m); err != nil {
		return manifest{}, err
	}
	return m, nil
}
func save(path string, m manifest) error {
	if filepath.Clean(path) != path {
		return errors.New("snapshot path must be clean")
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func load(path string) (manifest, string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return manifest{}, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0600 {
		return manifest{}, "", errors.New("snapshot must be a non-symlink 0600 file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, "", err
	}
	if len(raw) == 0 || len(raw) > 512<<20 {
		return manifest{}, "", errors.New("invalid snapshot size")
	}
	var m manifest
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil {
		return manifest{}, "", errors.New("invalid snapshot JSON")
	}
	if err = validManifest(m); err != nil {
		return manifest{}, "", err
	}
	sum := sha256.Sum256(raw)
	return m, hex.EncodeToString(sum[:]), nil
}
func validManifest(m manifest) error {
	if m.SchemaVersion != 1 || m.SourceSystem != sourceSystem || strings.TrimSpace(m.CorpID) != m.CorpID || m.CorpID == "" || m.CapturedAt.IsZero() || len(m.Jobs) > 2_000_000 {
		return errors.New("invalid snapshot manifest")
	}
	seen := map[int64]bool{}
	for _, j := range m.Jobs {
		if err := validJob(j); err != nil {
			return err
		}
		if seen[j.ID] {
			return errors.New("duplicate external_effect_job source id")
		}
		seen[j.ID] = true
	}
	return nil
}
func validJob(j sourceJob) error {
	if j.ID < 1 || j.EffectType != "wecom.contact.tag.mark" && j.EffectType != "wecom.contact.tag.unmark" || j.Operation != "tag_mark" && j.Operation != "tag_unmark" || strings.TrimSpace(j.TargetID) != j.TargetID || j.TargetID == "" || strings.TrimSpace(j.ActorID) != j.ActorID || j.ActorID == "" || strings.TrimSpace(j.Status) != j.Status || j.Status == "" || j.CreatedAt.IsZero() || !json.Valid(j.Payload) {
		return errors.New("source field drift or invalid external_effect_job row")
	}
	return nil
}
func printResult(mode string, m manifest, c counters) error {
	return printResultWithCounters(mode, m, c)
}
func printResultWithCounters(mode string, m manifest, c counters) error {
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": mode, "source_system": sourceSystem, "snapshot_at": m.CapturedAt.UTC(), "input": c.Input, "imported": c.Imported, "replayed": c.Replayed, "pending": c.Pending, "conflict": c.Conflict, "excluded": c.Excluded, "failed": c.Failed, "provider_calls": 0, "provider_effects": 0, "tag_commands": 0, "river_jobs": 0})
}

type resolver struct {
	uow        *platformpostgres.UnitOfWork
	identities identityport.Resolver
	staff      accessport.Repository
	tags       tagport.ProviderTagLocalIDReader
	corpID     string
}

func (r resolver) records(ctx context.Context, m manifest) ([]customerport.HistoricalTagRecord, counters, error) {
	records := make([]customerport.HistoricalTagRecord, 0, len(m.Jobs))
	out := counters{}
	for _, job := range m.Jobs {
		record, err := r.record(ctx, job)
		if err != nil {
			return nil, out, err
		}
		records = append(records, record)
		switch record.Resolution {
		case "imported":
			out.Imported++
		case "pending":
			out.Pending++
		case "conflict":
			out.Conflict++
		case "excluded":
			out.Excluded++
		default:
			out.Failed++
		}
	}
	return records, out, nil
}
func (r resolver) record(ctx context.Context, j sourceJob) (customerport.HistoricalTagRecord, error) {
	canonical, err := canonicalJob(j)
	if err != nil {
		return customerport.HistoricalTagRecord{}, err
	}
	rec := customerport.HistoricalTagRecord{SourceJobID: j.ID, SourceDigest: hash(canonical), EffectType: j.EffectType, Operation: j.Operation, SourceState: j.Status, Resolution: "failed", Reason: "source_invalid", OccurredAt: j.CreatedAt.UTC(), CompletedAt: j.CompletedAt}
	if (j.EffectType == "wecom.contact.tag.mark") != (j.Operation == "tag_mark") {
		rec.Resolution = "excluded"
		rec.Reason = "source_operation_mismatch"
		return rec, nil
	}
	providerTags, ok := payloadTags(j.Payload)
	if !ok {
		rec.Reason = "payload_tags_invalid"
		return rec, nil
	}
	if len(providerTags) == 0 {
		rec.Resolution = "excluded"
		rec.Reason = "payload_tags_missing"
		return rec, nil
	}
	err = r.uow.Within(ctx, func(tx context.Context) error {
		result, e := r.identities.Resolve(tx, identitydomain.Reference{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:" + r.corpID, Value: j.TargetID, Assurance: identitydomain.AssuranceVerified, Source: "v2_tag_history"})
		if e != nil {
			return e
		}
		if result.Status == identityport.ResolveConflict {
			rec.Resolution = "conflict"
			rec.Reason = "identity_conflict"
			return nil
		}
		if result.Status != identityport.ResolveFound {
			rec.Resolution = "pending"
			rec.Reason = "identity_unresolved"
			return nil
		}
		rec.CustomerID = customerdomain.CustomerID(result.CustomerID)
		staff, e := r.staff.UserByWeComUserID(tx, j.ActorID, false)
		if errors.Is(e, pgx.ErrNoRows) {
			rec.Resolution = "pending"
			rec.Reason = "staff_unresolved"
			return nil
		}
		if e != nil {
			return e
		}
		if !staff.Active {
			rec.Resolution = "pending"
			rec.Reason = "staff_inactive"
			return nil
		}
		rec.StaffID = staff.ID
		mapped := make([]int64, 0, len(providerTags))
		for _, id := range providerTags {
			local, found, e := r.tags.LocalTagID(tx, id)
			if e != nil {
				return e
			}
			if !found {
				rec.Resolution = "pending"
				rec.Reason = "tag_unmapped"
				return nil
			}
			mapped = append(mapped, local)
		}
		sort.Slice(mapped, func(i, j int) bool { return mapped[i] < mapped[j] })
		for i := 1; i < len(mapped); i++ {
			if mapped[i] == mapped[i-1] {
				rec.Resolution = "conflict"
				rec.Reason = "tag_mapping_conflict"
				return nil
			}
		}
		rec.Resolution = "imported"
		rec.Reason = ""
		if j.Operation == "tag_mark" {
			rec.AddTagIDs = mapped
		} else {
			rec.RemoveTagIDs = mapped
		}
		return nil
	})
	if err != nil {
		return customerport.HistoricalTagRecord{}, err
	}
	return rec, nil
}
func payloadTags(raw json.RawMessage) ([]string, bool) {
	var p struct {
		TagIDs []string `json:"tag_ids"`
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	if d.Decode(&p) != nil {
		return nil, false
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(p.TagIDs))
	for _, id := range p.TagIDs {
		if strings.TrimSpace(id) != id || id == "" || seen[id] {
			return nil, false
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, true
}
func canonicalJob(j sourceJob) (string, error) {
	payload, err := compact(j.Payload)
	if err != nil {
		return "", err
	}
	completed := ""
	if j.CompletedAt != nil {
		completed = j.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	return strings.Join([]string{fmt.Sprint(j.ID), j.EffectType, j.Operation, j.TargetID, j.ActorID, string(payload), j.Status, j.CreatedAt.UTC().Format(time.RFC3339Nano), completed}, "\x00"), nil
}
func compact(raw []byte) ([]byte, error) {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil, errors.New("invalid payload_json")
	}
	return json.Marshal(v)
}
func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

type applyLease struct {
	conn          *pgxpool.Conn
	first, second int32
}

func acquire(ctx context.Context, pool *pgxpool.Pool, digest string) (*applyLease, error) {
	if pool == nil {
		return nil, errors.New("migration pool unavailable")
	}
	sum := sha256.Sum256([]byte("aicrm:v2-customer-tag-history:" + digest))
	first, second := int32(binary.BigEndian.Uint32(sum[:4])), int32(binary.BigEndian.Uint32(sum[4:8]))
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	var locked bool
	if err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1::integer,$2::integer)", first, second).Scan(&locked); err != nil || !locked {
		conn.Release()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("customer tag history apply is already in progress")
	}
	return &applyLease{conn: conn, first: first, second: second}, nil
}
func (l *applyLease) Release() {
	if l == nil || l.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = l.conn.Exec(ctx, "SELECT pg_advisory_unlock($1::integer,$2::integer)", l.first, l.second)
	l.conn.Release()
	l.conn = nil
}
