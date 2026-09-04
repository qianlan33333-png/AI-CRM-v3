package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	couponstore "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/store"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const (
	productionSourceSystem = "ai-crm-production:150.158.82.186/openclaw_wecom"
	snapshotMarker         = "__AICRM_SIDEBAR_SNAPSHOT__|"
	entitlementMarker      = "__AICRM_SIDEBAR_ENTITLEMENT__|"
	couponMarker           = "__AICRM_SIDEBAR_COUPON__|"
)

type options struct {
	mode, snapshot, sourceStream, unionIDScope, digest string
	confirm                                            bool
}
type manifest struct {
	SchemaVersion int                 `json:"schema_version"`
	RunKey        string              `json:"run_key"`
	SourceSystem  string              `json:"source_system"`
	UnionIDScope  string              `json:"unionid_scope"`
	CapturedAt    time.Time           `json:"captured_at"`
	Entitlements  []sourceEntitlement `json:"entitlements"`
	Coupons       []sourceCoupon      `json:"coupons"`
	rawDigest     [32]byte
}
type sourceEntitlement struct {
	SourceID         int64     `json:"source_id"`
	UnionID          string    `json:"unionid"`
	ServiceProductID int64     `json:"service_product_id"`
	ProductName      string    `json:"product_name"`
	Status           string    `json:"status"`
	StartAt          time.Time `json:"start_at"`
	EndAt            time.Time `json:"end_at"`
	Remark           string    `json:"remark"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
type sourceCoupon struct {
	SourceID   int64      `json:"source_id"`
	UnionID    string     `json:"unionid"`
	CouponID   int64      `json:"coupon_id"`
	Status     string     `json:"status"`
	ClaimedAt  time.Time  `json:"claimed_at"`
	ValidFrom  *time.Time `json:"valid_from"`
	ValidUntil *time.Time `json:"valid_until"`
	RedeemedAt *time.Time `json:"redeemed_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}
type result struct {
	Input       int64 `json:"input"`
	Imported    int64 `json:"imported"`
	Replayed    int64 `json:"replayed"`
	Quarantined int64 `json:"quarantined"`
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sidebar history migration failed:", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("migrate-sidebar-history", flag.ContinueOnError)
	var cfg options
	flags.StringVar(&cfg.mode, "mode", "inspect", "inspect-stream|inspect|dry-run|preflight|apply|reconcile")
	flags.StringVar(&cfg.snapshot, "snapshot", "", "protected normalized JSON snapshot")
	flags.StringVar(&cfg.sourceStream, "source-stream", "", "read-only psql source stream")
	flags.StringVar(&cfg.unionIDScope, "unionid-scope", "", "verified WeChat Open Platform scope")
	flags.StringVar(&cfg.digest, "manifest-sha256", "", "exact snapshot SHA-256")
	flags.BoolVar(&cfg.confirm, "confirm-apply", false, "confirm exact apply")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if cfg.mode == "inspect-stream" {
		if cfg.snapshot == "" || cfg.sourceStream == "" || !strings.HasPrefix(cfg.unionIDScope, "wechat-open-platform:") {
			return errors.New("inspect-stream requires snapshot, source-stream and unionid-scope")
		}
		m, err := extractStream(cfg.sourceStream, cfg.unionIDScope)
		if err != nil {
			return err
		}
		if err = save(cfg.snapshot, m); err != nil {
			return err
		}
		m, err = load(cfg.snapshot)
		if err != nil {
			return err
		}
		return printSummary("inspect-stream", m, true)
	}
	if cfg.snapshot == "" {
		return errors.New("snapshot is required")
	}
	m, err := load(cfg.snapshot)
	if err != nil {
		return err
	}
	if cfg.mode == "inspect" || cfg.mode == "dry-run" {
		return printSummary(cfg.mode, m, cfg.mode == "dry-run")
	}
	want, err := hex.DecodeString(cfg.digest)
	if err != nil || len(want) != 32 || string(want) != string(m.rawDigest[:]) {
		return errors.New("manifest digest confirmation mismatch")
	}
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return err
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL, MaxConnections: 8, MinConnections: 1})
	if err != nil {
		return err
	}
	defer pool.Close()
	if cfg.mode == "preflight" {
		return preflight(ctx, pool, m)
	}
	if cfg.mode == "reconcile" {
		return reconcile(ctx, pool, m)
	}
	if cfg.mode != "apply" || !cfg.confirm {
		return errors.New("apply requires --confirm-apply")
	}
	return apply(ctx, pool, m)
}

func printSummary(mode string, m manifest, eligible bool) error {
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"mode": mode, "manifest_sha256": hex.EncodeToString(m.rawDigest[:]), "run_key": m.RunKey,
		"input": len(m.Entitlements) + len(m.Coupons), "entitlements": len(m.Entitlements),
		"coupons": len(m.Coupons), "eligible": eligible, "provider_calls": 0, "oneid_links_created": 0,
	})
}

func preflight(ctx context.Context, pool *platformpostgres.Pool, m manifest) error {
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	blockers := map[string]int64{"identity_not_found": 0, "identity_conflict": 0, "definition_not_mapped": 0}
	var ready int64
	check := func(kind, sourceKind, unionID string, sourceID int64) error {
		_, reason, resolveErr := resolve(ctx, uow, oneID, m.UnionIDScope, unionID)
		if resolveErr != nil {
			return resolveErr
		}
		if reason != "" {
			blockers[reason]++
			return nil
		}
		_, found, mapErr := definitionMap(ctx, pool, m.SourceSystem, kind, sourceKind, sourceID)
		if mapErr != nil {
			return mapErr
		}
		if !found {
			blockers["definition_not_mapped"]++
			return nil
		}
		ready++
		return nil
	}
	for _, row := range m.Entitlements {
		if err = check("product", "service_period_products", row.UnionID, row.ServiceProductID); err != nil {
			return err
		}
	}
	for _, row := range m.Coupons {
		if err = check("coupon", "commerce_coupons", row.UnionID, row.CouponID); err != nil {
			return err
		}
	}
	input := int64(len(m.Entitlements) + len(m.Coupons))
	blocked := input - ready
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"mode": "preflight", "run_key": m.RunKey, "input": input, "ready": ready,
		"blocked": blocked, "blockers": blockers, "provider_calls": 0, "oneid_links_created": 0,
	})
}

func extractStream(path, scope string) (manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return manifest{}, err
	}
	defer file.Close()
	m := manifest{SchemaVersion: 1, SourceSystem: productionSourceSystem, UnionIDScope: scope, Entitlements: []sourceEntitlement{}, Coupons: []sourceCoupon{}}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	markers := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, snapshotMarker):
			if markers != 0 {
				return manifest{}, errors.New("duplicate sidebar snapshot marker")
			}
			captured, parseErr := time.Parse(time.RFC3339Nano, strings.TrimPrefix(line, snapshotMarker))
			if parseErr != nil {
				return manifest{}, errors.New("invalid sidebar snapshot marker")
			}
			m.CapturedAt = captured.UTC()
			markers++
		case strings.HasPrefix(line, entitlementMarker):
			var row sourceEntitlement
			if err = decodeHexJSON(strings.TrimPrefix(line, entitlementMarker), &row); err != nil {
				return manifest{}, err
			}
			m.Entitlements = append(m.Entitlements, row)
		case strings.HasPrefix(line, couponMarker):
			var row sourceCoupon
			if err = decodeHexJSON(strings.TrimPrefix(line, couponMarker), &row); err != nil {
				return manifest{}, err
			}
			m.Coupons = append(m.Coupons, row)
		}
	}
	if err = scanner.Err(); err != nil {
		return manifest{}, err
	}
	if markers != 1 {
		return manifest{}, errors.New("sidebar snapshot marker unavailable")
	}
	digest := sha256.Sum256([]byte(m.CapturedAt.Format(time.RFC3339Nano) + "\x00" + scope))
	m.RunKey = "sidebar-history-" + hex.EncodeToString(digest[:12])
	if err = validate(m); err != nil {
		return manifest{}, err
	}
	return m, nil
}

func decodeHexJSON(encoded string, target any) error {
	raw, err := hex.DecodeString(encoded)
	if err != nil || len(raw) == 0 || !json.Valid(raw) {
		return errors.New("invalid sidebar source row")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return errors.New("invalid sidebar source row")
	}
	return nil
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
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func load(path string) (manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, err
	}
	if len(data) == 0 || len(data) > 512<<20 {
		return manifest{}, errors.New("invalid snapshot size")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var m manifest
	if decoder.Decode(&m) != nil {
		return m, errors.New("invalid snapshot JSON")
	}
	m.rawDigest = sha256.Sum256(data)
	if err = validate(m); err != nil {
		return m, err
	}
	return m, nil
}

func validate(m manifest) error {
	if m.SchemaVersion != 1 || !regexp.MustCompile(`^[A-Za-z0-9._:-]{8,200}$`).MatchString(m.RunKey) || m.SourceSystem != productionSourceSystem || !strings.HasPrefix(m.UnionIDScope, "wechat-open-platform:") || m.CapturedAt.IsZero() || len(m.Entitlements)+len(m.Coupons) > 2_000_000 {
		return errors.New("invalid snapshot manifest")
	}
	seen := map[string]bool{}
	for _, row := range m.Entitlements {
		key := "e:" + strconv.FormatInt(row.SourceID, 10)
		if seen[key] || row.SourceID < 1 || row.UnionID == "" || row.ServiceProductID < 1 || row.ProductName == "" || (row.Status != "active" && row.Status != "expired" && row.Status != "refunded") || row.StartAt.IsZero() || row.EndAt.Before(row.StartAt) {
			return errors.New("invalid entitlement row")
		}
		seen[key] = true
	}
	for _, row := range m.Coupons {
		key := "c:" + strconv.FormatInt(row.SourceID, 10)
		if seen[key] || row.SourceID < 1 || row.UnionID == "" || row.CouponID < 1 || row.ClaimedAt.IsZero() || (row.Status != "available" && row.Status != "reserved" && row.Status != "consumed" && row.Status != "expired") {
			return errors.New("invalid coupon row")
		}
		seen[key] = true
	}
	return nil
}

func apply(ctx context.Context, pool *platformpostgres.Pool, m manifest) error {
	uow, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		return err
	}
	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	orders, err := orderstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return err
	}
	entitlementApp, _ := orderapp.NewEntitlementApplication(uow, orders)
	coupons, err := couponstore.NewPostgreSQL(pool.Native(), uow)
	if err != nil {
		return err
	}
	couponApp, _ := couponapp.NewCustomerCouponApplication(uow, coupons)
	batchID, err := beginBatch(ctx, pool, m)
	if err != nil {
		return err
	}
	out := result{Input: int64(len(m.Entitlements) + len(m.Coupons))}
	for _, row := range m.Entitlements {
		raw, _ := json.Marshal(row)
		digest := sha256.Sum256(raw)
		customer, reason, err := resolve(ctx, uow, oneID, m.UnionIDScope, row.UnionID)
		if err != nil {
			return err
		}
		sourceKey := strconv.FormatInt(row.SourceID, 10)
		if reason != "" {
			if err = quarantine(ctx, pool, batchID, "service_period_entitlement", sourceKey, digest, row.UnionID, reason); err != nil {
				return err
			}
			out.Quarantined++
			continue
		}
		productID, found, err := definitionMap(ctx, pool, m.SourceSystem, "product", "service_period_products", row.ServiceProductID)
		if err != nil {
			return err
		}
		if !found {
			if err = quarantine(ctx, pool, batchID, "service_period_entitlement", sourceKey, digest, row.UnionID, "definition_not_mapped"); err != nil {
				return err
			}
			out.Quarantined++
			continue
		}
		item, created, err := entitlementApp.ImportHistoricalEntitlement(ctx, orderport.HistoricalEntitlement{SourceSystem: m.SourceSystem, SourceKey: sourceKey, CustomerID: customer, ServiceProductID: productID, ProductName: row.ProductName, Status: row.Status, StartAt: row.StartAt, EndAt: row.EndAt, Remark: row.Remark, SourceDigest: digest, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
		if err != nil {
			return err
		}
		if err = mapSource(ctx, pool, batchID, "service_period_entitlement", sourceKey, digest, customer, "order_service_entitlements", item.ID, created); err != nil {
			return err
		}
		if created {
			out.Imported++
		} else {
			out.Replayed++
		}
	}
	for _, row := range m.Coupons {
		raw, _ := json.Marshal(row)
		digest := sha256.Sum256(raw)
		customer, reason, err := resolve(ctx, uow, oneID, m.UnionIDScope, row.UnionID)
		if err != nil {
			return err
		}
		sourceKey := strconv.FormatInt(row.SourceID, 10)
		if reason != "" {
			if err = quarantine(ctx, pool, batchID, "coupon_claim", sourceKey, digest, row.UnionID, reason); err != nil {
				return err
			}
			out.Quarantined++
			continue
		}
		couponID, found, err := definitionMap(ctx, pool, m.SourceSystem, "coupon", "commerce_coupons", row.CouponID)
		if err != nil {
			return err
		}
		if !found {
			if err = quarantine(ctx, pool, batchID, "coupon_claim", sourceKey, digest, row.UnionID, "definition_not_mapped"); err != nil {
				return err
			}
			out.Quarantined++
			continue
		}
		status := mapCouponStatus(row.Status)
		item, created, err := couponApp.ImportHistoricalCustomerCoupon(ctx, couponport.HistoricalCustomerCoupon{SourceSystem: m.SourceSystem, SourceKey: sourceKey, CustomerID: customer, CouponID: couponID, Status: status, ClaimedAt: row.ClaimedAt, ValidFrom: row.ValidFrom, ValidUntil: row.ValidUntil, RedeemedAt: row.RedeemedAt, SourceDigest: digest, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
		if err != nil {
			return err
		}
		if err = mapSource(ctx, pool, batchID, "coupon_claim", sourceKey, digest, customer, "coupon_customer_claims", item.ClaimID, created); err != nil {
			return err
		}
		if created {
			out.Imported++
		} else {
			out.Replayed++
		}
	}
	if out.Input != out.Imported+out.Replayed+out.Quarantined {
		return errors.New("migration conservation mismatch")
	}
	if _, err = pool.Native().Exec(ctx, `UPDATE sidebar_history_migration_batches SET imported_count=$2,replayed_count=$3,quarantined_count=$4,status='applied',completed_at=clock_timestamp() WHERE id=$1 AND status IN ('applying','applied')`, batchID, out.Imported, out.Replayed, out.Quarantined); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "apply", "run_key": m.RunKey, "result": out})
}

func resolve(ctx context.Context, uow platformport.UnitOfWork, oneID identityapp.OneIDService, scope, value string) (int64, string, error) {
	var out identityport.ResolveResult
	err := uow.Within(ctx, func(tx context.Context) error {
		var e error
		out, e = oneID.Resolve(tx, identitydomain.Reference{Kind: identitydomain.KindUnionID, Scope: scope, Value: value, Assurance: identitydomain.AssuranceVerified, Source: "sidebar_history"})
		return e
	})
	if err != nil {
		return 0, "", err
	}
	if out.Status == identityport.ResolveFound {
		return int64(out.CustomerID), "", nil
	}
	if out.Status == identityport.ResolveConflict {
		return 0, "identity_conflict", nil
	}
	return 0, "identity_not_found", nil
}
func beginBatch(ctx context.Context, pool *platformpostgres.Pool, m manifest) (int64, error) {
	var id int64
	var digest []byte
	var status string
	err := pool.Native().QueryRow(ctx, `INSERT INTO sidebar_history_migration_batches(run_key,manifest_digest,source_system,input_count,status) VALUES($1,$2,$3,$4,'applying') ON CONFLICT(run_key) DO NOTHING RETURNING id`, m.RunKey, m.rawDigest[:], m.SourceSystem, len(m.Entitlements)+len(m.Coupons)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = pool.Native().QueryRow(ctx, `SELECT id,manifest_digest,status FROM sidebar_history_migration_batches WHERE run_key=$1`, m.RunKey).Scan(&id, &digest, &status)
		if err == nil && string(digest) != string(m.rawDigest[:]) {
			return 0, errors.New("batch replay mismatch")
		}
	}
	return id, err
}
func definitionMap(ctx context.Context, pool *platformpostgres.Pool, source, domain, kind string, id int64) (int64, bool, error) {
	var target int64
	err := pool.Native().QueryRow(ctx, `SELECT target_id FROM config_definition_import_source_maps WHERE source_system=$1 AND domain=$2 AND source_kind=$3 AND source_key=$4`, source, domain, kind, strconv.FormatInt(id, 10)).Scan(&target)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return target, err == nil, err
}
func quarantine(ctx context.Context, pool *platformpostgres.Pool, batch int64, kind, key string, digest [32]byte, subject, reason string) error {
	subjectDigest := sha256.Sum256([]byte(subject))
	_, err := pool.Native().Exec(ctx, `INSERT INTO sidebar_history_migration_quarantine(batch_id,source_kind,source_key,source_digest,subject_digest,reason) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(batch_id,source_kind,source_key) DO NOTHING`, batch, kind, key, digest[:], subjectDigest[:], reason)
	return err
}
func mapSource(ctx context.Context, pool *platformpostgres.Pool, batch int64, kind, key string, digest [32]byte, customer int64, table string, target int64, created bool) error {
	disposition := "replayed"
	if created {
		disposition = "imported"
	}
	_, err := pool.Native().Exec(ctx, `INSERT INTO sidebar_history_migration_source_map(batch_id,source_kind,source_key,source_digest,customer_id,target_table,target_id,disposition) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(batch_id,source_kind,source_key) DO NOTHING`, batch, kind, key, digest[:], customer, table, target, disposition)
	return err
}
func mapCouponStatus(value string) string {
	switch value {
	case "available":
		return "claimed"
	case "consumed":
		return "redeemed"
	default:
		return value
	}
}
func reconcile(ctx context.Context, pool *platformpostgres.Pool, m manifest) error {
	var id, input, mapped, quarantined, missing int64
	var digest []byte
	err := pool.Native().QueryRow(ctx, `SELECT id,input_count,manifest_digest FROM sidebar_history_migration_batches WHERE run_key=$1 AND status IN ('applied','reconciled')`, m.RunKey).Scan(&id, &input, &digest)
	if err != nil {
		return err
	}
	if input != int64(len(m.Entitlements)+len(m.Coupons)) || string(digest) != string(m.rawDigest[:]) {
		return errors.New("reconciliation manifest mismatch")
	}
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM sidebar_history_migration_source_map WHERE batch_id=$1`, id).Scan(&mapped); err != nil {
		return err
	}
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM sidebar_history_migration_quarantine WHERE batch_id=$1`, id).Scan(&quarantined); err != nil {
		return err
	}
	if err = pool.Native().QueryRow(ctx, `SELECT count(*) FROM sidebar_history_migration_source_map m WHERE m.batch_id=$1 AND ((m.target_table='order_service_entitlements' AND NOT EXISTS(SELECT 1 FROM order_service_entitlements e WHERE e.id=m.target_id AND e.customer_id=m.customer_id AND e.source_digest=m.source_digest)) OR (m.target_table='coupon_customer_claims' AND NOT EXISTS(SELECT 1 FROM coupon_customer_claims c WHERE c.id=m.target_id AND c.customer_id=m.customer_id AND c.source_digest=m.source_digest)))`, id).Scan(&missing); err != nil {
		return err
	}
	if mapped+quarantined != input || missing != 0 {
		return errors.New("reconciliation conservation mismatch")
	}
	_, err = pool.Native().Exec(ctx, `UPDATE sidebar_history_migration_batches SET status='reconciled',completed_at=clock_timestamp() WHERE id=$1`, id)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "reconcile", "run_key": m.RunKey, "matched": true, "input": input, "mapped": mapped, "quarantined": quarantined})
}
