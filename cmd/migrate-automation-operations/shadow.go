package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentmigration "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/migration"
)

// ShadowPackage compares the frozen source snapshot with the exact published
// target snapshot at the same reference time. It intentionally emits counts
// and equality flags only; canonical customer IDs never leave the command.
type ShadowPackage struct {
	SourcePackageID      int64  `json:"source_package_id"`
	TargetPackageID      int64  `json:"target_package_id"`
	Lifecycle            string `json:"lifecycle"`
	ConfigurationMatches bool   `json:"configuration_matches"`
	ReferenceTimeMatches bool   `json:"reference_time_matches"`
	SourceMemberRows     int    `json:"source_member_rows"`
	MappedMemberRows     int    `json:"mapped_member_rows"`
	IsolatedMemberRows   int    `json:"isolated_member_rows"`
	CanonicalMemberCount int    `json:"canonical_member_count"`
	TargetMemberCount    int64  `json:"target_member_count"`
	MemberDigestMatches  bool   `json:"member_digest_matches"`
	PausedOrArchived     bool   `json:"paused_or_archived"`
	Ready                bool   `json:"ready"`
}

// ShadowQuarantine proves that a source row intentionally isolated during
// import still has its immutable receipt and original digest. It carries no
// source payload.
type ShadowQuarantine struct {
	SourceTable         string `json:"source_table"`
	SourcePK            string `json:"source_pk"`
	Disposition         string `json:"disposition"`
	ReasonCodeMatches   bool   `json:"reason_code_matches"`
	RecordDigestMatches bool   `json:"record_digest_matches"`
	Ready               bool   `json:"ready"`
}

type ShadowReport struct {
	BatchKey                string             `json:"batch_key"`
	BatchStatus             string             `json:"batch_status"`
	ReferenceTime           time.Time          `json:"reference_time"`
	Packages                []ShadowPackage    `json:"packages"`
	ProviderEffectsCreated  int64              `json:"provider_effects_created"`
	RiverJobsCreated        int64              `json:"river_jobs_created"`
	OutcomeUnknownCount     int64              `json:"outcome_unknown_count"`
	History                 []ShadowHistory    `json:"history"`
	Quarantines             []ShadowQuarantine `json:"quarantines"`
	SnapshotManifestMatches bool               `json:"snapshot_manifest_matches"`
	ReadyForReconcile       bool               `json:"ready_for_reconcile"`
	ReadyForProbe           bool               `json:"ready_for_probe"`
}

// ShadowHistory is a safe, read-only comparison of one frozen legacy runtime
// record. It deliberately carries no source payload or Provider identifier.
type ShadowHistory struct {
	SourceTable               string    `json:"source_table"`
	SourcePK                  string    `json:"source_pk"`
	SourceState               string    `json:"source_state"`
	OccurredAt                time.Time `json:"occurred_at"`
	StateMatches              bool      `json:"state_matches"`
	OccurredAtMatches         bool      `json:"occurred_at_matches"`
	RecordDigestMatches       bool      `json:"record_digest_matches"`
	SourceEffectDigestMatches bool      `json:"source_effect_digest_matches"`
	SourceReceiptMatches      bool      `json:"source_receipt_matches"`
	ReadOnly                  bool      `json:"read_only"`
	Replayable                bool      `json:"replayable"`
	Ready                     bool      `json:"ready"`
}

type frozenSourceRow struct {
	table  string
	pk     string
	digest [32]byte
}

type sourceReceipt struct {
	targetTable string
	targetPK    *int64
	disposition string
}

func receiptKey(table, pk string) string { return table + "\x00" + pk }

// frozenSourceRows derives every source receipt key from the protected,
// canonical snapshot. This is deliberately shared by target and quarantine
// checks so a count-only reconciliation cannot hide a missing or changed row.
func frozenSourceRows(snapshot segmentmigration.Snapshot) ([]frozenSourceRow, error) {
	rows := make([]frozenSourceRow, 0)
	for _, table := range segmentmigration.LogicalTables {
		var rawRows []json.RawMessage
		if err := json.Unmarshal(snapshot.Tables[table], &rawRows); err != nil {
			return nil, fmt.Errorf("decode source table %s", table)
		}
		for index, raw := range rawRows {
			var object map[string]json.RawMessage
			if json.Unmarshal(raw, &object) != nil {
				return nil, fmt.Errorf("decode source row %s", table)
			}
			var id, packageID, version, segmentID, customerID, sortOrder int64
			_ = json.Unmarshal(object["id"], &id)
			_ = json.Unmarshal(object["package_id"], &packageID)
			_ = json.Unmarshal(object["automation_id"], &packageID)
			_ = json.Unmarshal(object["version"], &version)
			_ = json.Unmarshal(object["segment_id"], &segmentID)
			_ = json.Unmarshal(object["customer_id"], &customerID)
			_ = json.Unmarshal(object["sort_order"], &sortOrder)
			var pk string
			switch table {
			case "audience_groups", "audience_packages", "automation_agents":
				pk = strconv.FormatInt(id, 10)
			case "audience_configuration_versions":
				pk = fmt.Sprintf("%d:%d", packageID, version)
			case "audience_bindings":
				pk = strconv.FormatInt(packageID, 10)
			case "audience_senders":
				pk = fmt.Sprintf("%d:%d", packageID, sortOrder)
			case "audience_members":
				pk = fmt.Sprintf("%d:%d", segmentID, customerID)
			default:
				pk = historyPK(table, object, index)
			}
			if pk == "" || pk == "0" || pk == "0:0" {
				return nil, fmt.Errorf("invalid source key %s", table)
			}
			rows = append(rows, frozenSourceRow{table: table, pk: pk, digest: recordDigest(raw)})
		}
	}
	return rows, nil
}

func sourceReceipts(ctx context.Context, tx pgx.Tx, batchID int64, snapshot segmentmigration.Snapshot) (map[string]sourceReceipt, []ShadowQuarantine, error) {
	rows, err := frozenSourceRows(snapshot)
	if err != nil {
		return nil, nil, err
	}
	receipts := make(map[string]sourceReceipt, len(rows))
	quarantines := make([]ShadowQuarantine, 0)
	for _, source := range rows {
		receiptSourcePK := sourcePK(snapshot, source.pk)
		var receipt sourceReceipt
		var digest []byte
		if err = tx.QueryRow(ctx, `SELECT target_table,target_pk,disposition,record_digest FROM automation_operations_migration_source_map WHERE batch_id=$1 AND source_table=$2 AND source_pk=$3`, batchID, source.table, receiptSourcePK).Scan(&receipt.targetTable, &receipt.targetPK, &receipt.disposition, &digest); err != nil {
			return nil, nil, fmt.Errorf("source receipt %s/%s: %w", source.table, source.pk, err)
		}
		if !bytes.Equal(digest, source.digest[:]) {
			return nil, nil, fmt.Errorf("source receipt digest %s/%s", source.table, source.pk)
		}
		if receipt.disposition == "unresolved" || receipt.disposition == "conflict" || receipt.disposition == "invalid" || receipt.disposition == "quarantine" {
			var reason string
			var quarantinedDigest []byte
			if err = tx.QueryRow(ctx, `SELECT reason_code,record_digest FROM automation_operations_migration_quarantine WHERE batch_id=$1 AND source_table=$2 AND source_pk=$3`, batchID, source.table, receiptSourcePK).Scan(&reason, &quarantinedDigest); err != nil {
				return nil, nil, fmt.Errorf("quarantine receipt %s/%s: %w", source.table, source.pk, err)
			}
			item := ShadowQuarantine{SourceTable: source.table, SourcePK: receiptSourcePK, Disposition: receipt.disposition, ReasonCodeMatches: true, RecordDigestMatches: bytes.Equal(quarantinedDigest, source.digest[:])}
			if source.table == "audience_members" {
				item.ReasonCodeMatches = reason == "oneid_"+receipt.disposition
			}
			item.Ready = item.ReasonCodeMatches && item.RecordDigestMatches
			if !item.Ready {
				return nil, nil, fmt.Errorf("quarantine receipt drift %s/%s", source.table, source.pk)
			}
			quarantines = append(quarantines, item)
		}
		receipts[receiptKey(source.table, source.pk)] = receipt
	}
	var persisted int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM automation_operations_migration_quarantine WHERE batch_id=$1`, batchID).Scan(&persisted); err != nil {
		return nil, nil, err
	}
	if persisted != len(quarantines) {
		return nil, nil, errors.New("migration quarantine receipt count drift")
	}
	return receipts, quarantines, nil
}

func Shadow(ctx context.Context, pool *pgxpool.Pool, batchKey string, snapshot segmentmigration.Snapshot) (ShadowReport, error) {
	if pool == nil || batchKey == "" {
		return ShadowReport{}, errors.New("target and batch-key are required")
	}
	if err := segmentmigration.ValidateSnapshot(snapshot); err != nil {
		return ShadowReport{}, err
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ShadowReport{}, err
	}
	defer tx.Rollback(ctx)

	report, err := shadowInTx(ctx, tx, batchKey, snapshot)
	if err != nil {
		return report, err
	}
	if err = tx.Commit(ctx); err != nil {
		return report, err
	}
	return report, nil
}

// shadowInTx is shared by the read-only operator report and Reconcile. The
// latter uses it inside its SERIALIZABLE transaction so a batch is never
// marked reconciled after a separately observed, stale comparison.
func shadowInTx(ctx context.Context, tx pgx.Tx, batchKey string, snapshot segmentmigration.Snapshot) (ShadowReport, error) {
	report := ShadowReport{BatchKey: batchKey, ReferenceTime: snapshot.Manifest.SnapshotAt.UTC()}
	var batchID, effectsBefore, effectsAfter, jobsBefore, jobsAfter int64
	var donorCommit, sourceWatermark string
	var manifestDigest []byte
	if err := tx.QueryRow(ctx, `SELECT id,status,donor_commit,encode(source_watermark_digest,'hex'),manifest_digest,provider_effect_count_before,provider_effect_count_after,river_job_count_before,river_job_count_after FROM automation_operations_migration_batches WHERE batch_key=$1`, batchKey).Scan(&batchID, &report.BatchStatus, &donorCommit, &sourceWatermark, &manifestDigest, &effectsBefore, &effectsAfter, &jobsBefore, &jobsAfter); err != nil {
		return report, err
	}
	if donorCommit != snapshot.Manifest.DonorCommit || sourceWatermark != snapshot.Manifest.SourceWatermarkDigest {
		return report, errors.New("stored batch does not belong to supplied frozen snapshot")
	}
	manifestRaw, err := json.Marshal(snapshot.Manifest)
	if err != nil {
		return report, errors.New("encode supplied frozen manifest")
	}
	expectedManifestDigest := sha256.Sum256(manifestRaw)
	report.SnapshotManifestMatches = bytes.Equal(manifestDigest, expectedManifestDigest[:])
	if !report.SnapshotManifestMatches {
		return report, errors.New("stored batch manifest does not match supplied frozen snapshot")
	}
	report.ProviderEffectsCreated = effectsAfter - effectsBefore
	report.RiverJobsCreated = jobsAfter - jobsBefore
	receipts, quarantines, err := sourceReceipts(ctx, tx, batchID, snapshot)
	if err != nil {
		return report, err
	}
	report.Quarantines = quarantines

	packages, _, err := decodeRows[packageRow](snapshot, "audience_packages")
	if err != nil {
		return report, err
	}
	configs, _, err := decodeRows[configRow](snapshot, "audience_configuration_versions")
	if err != nil {
		return report, err
	}
	members, _, err := decodeRows[memberRow](snapshot, "audience_members")
	if err != nil {
		return report, err
	}
	latestConfig := map[int64]configRow{}
	for _, item := range configs {
		if current, ok := latestConfig[item.PackageID]; !ok || item.Version > current.Version {
			latestConfig[item.PackageID] = item
		}
	}
	membersByPackage := map[int64][]memberRow{}
	for _, item := range members {
		membersByPackage[item.SegmentID] = append(membersByPackage[item.SegmentID], item)
	}
	sort.Slice(packages, func(a, b int) bool { return packages[a].ID < packages[b].ID })

	allReady := report.ProviderEffectsCreated == 0 && report.RiverJobsCreated == 0
	for _, source := range packages {
		item := ShadowPackage{SourcePackageID: source.ID, SourceMemberRows: len(membersByPackage[source.ID])}
		packageReceipt, ok := receipts[receiptKey("audience_packages", strconv.FormatInt(source.ID, 10))]
		if !ok || packageReceipt.targetPK == nil || (packageReceipt.disposition != "imported" && packageReceipt.disposition != "mapped") || packageReceipt.targetTable != "segment_audience_packages" {
			return report, fmt.Errorf("package %d source receipt", source.ID)
		}
		item.TargetPackageID = *packageReceipt.targetPK
		var targetDefinition []byte
		var targetCron *string
		var targetSnapshotID int64
		var targetSnapshotState string
		var targetReference time.Time
		var targetDigest []byte
		if err = tx.QueryRow(ctx, `SELECT p.lifecycle,c.definition::text,c.refresh_cron_utc,s.id,s.state,s.reference_time,s.member_count,s.member_digest FROM segment_audience_packages p JOIN segment_audience_configuration_versions c ON c.id=p.current_configuration_version_id AND c.package_id=p.id JOIN segment_audience_snapshots s ON s.id=p.published_snapshot_id AND s.package_id=p.id WHERE p.id=$1`, item.TargetPackageID).Scan(&item.Lifecycle, &targetDefinition, &targetCron, &targetSnapshotID, &targetSnapshotState, &targetReference, &item.TargetMemberCount, &targetDigest); err != nil {
			return report, fmt.Errorf("package %d target projection: %w", source.ID, err)
		}
		sourceConfiguration, ok := latestConfig[source.ID]
		if !ok {
			return report, fmt.Errorf("package %d has no source configuration", source.ID)
		}
		expectedCron := ""
		if sourceConfiguration.RefreshMode == "scheduled" && sourceConfiguration.RefreshCron != nil {
			expectedCron = *sourceConfiguration.RefreshCron
		}
		actualCron := ""
		if targetCron != nil {
			actualCron = *targetCron
		}
		item.ConfigurationMatches = sameJSON(sourceConfiguration.Definition, targetDefinition) && expectedCron == actualCron
		item.ReferenceTimeMatches = targetReference.UTC().Equal(snapshot.Manifest.SnapshotAt.UTC())
		item.PausedOrArchived = item.Lifecycle == "paused" || (source.Lifecycle == "archived" && item.Lifecycle == "archived")

		canonical := make([]int64, 0, item.SourceMemberRows)
		for _, sourceMember := range membersByPackage[source.ID] {
			memberKey := fmt.Sprintf("%d:%d", sourceMember.SegmentID, sourceMember.CustomerID)
			memberReceipt, ok := receipts[receiptKey("audience_members", memberKey)]
			if !ok {
				return report, fmt.Errorf("member source receipt %s", memberKey)
			}
			if memberReceipt.disposition == "mapped" && memberReceipt.targetPK != nil && *memberReceipt.targetPK > 0 && memberReceipt.targetTable == "segment_audience_snapshot_members.customer_id" {
				item.MappedMemberRows++
				canonical = append(canonical, *memberReceipt.targetPK)
				continue
			}
			if memberReceipt.disposition != "unresolved" && memberReceipt.disposition != "conflict" && memberReceipt.disposition != "invalid" && memberReceipt.disposition != "quarantine" {
				return report, fmt.Errorf("member source receipt disposition %q", memberReceipt.disposition)
			}
			item.IsolatedMemberRows++
		}
		sort.Slice(canonical, func(a, b int) bool { return canonical[a] < canonical[b] })
		unique := canonical[:0]
		for _, id := range canonical {
			if len(unique) == 0 || unique[len(unique)-1] != id {
				unique = append(unique, id)
			}
		}
		item.CanonicalMemberCount = len(unique)
		customerIDs := make([]customerdomain.CustomerID, len(unique))
		for index, id := range unique {
			customerIDs[index] = customerdomain.CustomerID(id)
		}
		expectedDigest := segmentdomain.DigestMembers(customerIDs)
		actualCustomerIDs := make([]customerdomain.CustomerID, 0, item.TargetMemberCount)
		rows, queryErr := tx.Query(ctx, `SELECT customer_id,entered_at,identity_disposition FROM segment_audience_snapshot_members WHERE snapshot_id=$1 ORDER BY customer_id`, targetSnapshotID)
		if queryErr != nil {
			return report, queryErr
		}
		enteredAtMatches := true
		for rows.Next() {
			var customerID int64
			var enteredAt time.Time
			var disposition string
			if queryErr = rows.Scan(&customerID, &enteredAt, &disposition); queryErr != nil {
				rows.Close()
				return report, queryErr
			}
			actualCustomerIDs = append(actualCustomerIDs, customerdomain.CustomerID(customerID))
			enteredAtMatches = enteredAtMatches && enteredAt.UTC().Equal(snapshot.Manifest.SnapshotAt.UTC()) && disposition == "resolved"
		}
		if queryErr = rows.Err(); queryErr != nil {
			rows.Close()
			return report, queryErr
		}
		rows.Close()
		actualDigest := segmentdomain.DigestMembers(actualCustomerIDs)
		item.MemberDigestMatches = targetSnapshotState == "published" && item.MappedMemberRows+item.IsolatedMemberRows == item.SourceMemberRows && int64(len(unique)) == item.TargetMemberCount && int64(len(actualCustomerIDs)) == item.TargetMemberCount && bytes.Equal(expectedDigest[:], targetDigest) && bytes.Equal(expectedDigest[:], actualDigest[:]) && enteredAtMatches
		item.Ready = item.ConfigurationMatches && item.ReferenceTimeMatches && item.PausedOrArchived && item.MemberDigestMatches
		allReady = allReady && item.Ready
		report.Packages = append(report.Packages, item)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM automation_run_recipients r JOIN automation_runs x ON x.id=r.run_id WHERE r.state='outcome_unknown' AND x.package_id IN (SELECT target_pk FROM automation_operations_migration_source_map WHERE batch_id=$1 AND source_table='audience_packages' AND target_pk IS NOT NULL)`, batchID).Scan(&report.OutcomeUnknownCount); err != nil {
		return report, err
	}
	for _, name := range []string{"automation_policies", "automation_policy_versions", "automation_enrollments", "automation_actions"} {
		var rows []json.RawMessage
		if err = json.Unmarshal(snapshot.Tables[name], &rows); err != nil {
			return report, err
		}
		for index, raw := range rows {
			var object map[string]json.RawMessage
			if json.Unmarshal(raw, &object) != nil {
				return report, fmt.Errorf("invalid history row %s", name)
			}
			pk := historyPK(name, object, index)
			item := ShadowHistory{SourceTable: name, SourcePK: pk, SourceState: historyString(object, "state", historyString(object, "status", "unknown")), OccurredAt: historyTime(object)}
			var historyID int64
			var storedDigest, storedEffect, sourceReceiptDigest []byte
			var storedState string
			var storedOccurred time.Time
			if err = tx.QueryRow(ctx, `SELECT id,source_state,occurred_at,record_digest,source_effect_digest,read_only,replayable FROM automation_operations_legacy_history WHERE batch_id=$1 AND source_table=$2 AND source_pk=$3`, batchID, name, sourcePK(snapshot, pk)).Scan(&historyID, &storedState, &storedOccurred, &storedDigest, &storedEffect, &item.ReadOnly, &item.Replayable); err != nil {
				return report, fmt.Errorf("history %s/%s: %w", name, pk, err)
			}
			item.StateMatches = storedState == item.SourceState
			item.OccurredAtMatches = storedOccurred.UTC().Equal(item.OccurredAt.UTC())
			expected := recordDigest(raw)
			item.RecordDigestMatches = bytes.Equal(storedDigest, expected[:])
			var mappedID *int64
			var mappedTarget, mappedDisposition string
			if err = tx.QueryRow(ctx, `SELECT target_pk,target_table,disposition,record_digest FROM automation_operations_migration_source_map WHERE batch_id=$1 AND source_table=$2 AND source_pk=$3`, batchID, name, sourcePK(snapshot, pk)).Scan(&mappedID, &mappedTarget, &mappedDisposition, &sourceReceiptDigest); err != nil {
				return report, fmt.Errorf("history source receipt %s/%s: %w", name, pk, err)
			}
			item.SourceReceiptMatches = mappedID != nil && *mappedID == historyID && mappedTarget == "automation_operations_legacy_history" && mappedDisposition == "imported" && bytes.Equal(sourceReceiptDigest, expected[:])
			expectedEffect := []byte(nil)
			if effectID := historyString(object, "external_effect_id", ""); effectID != "" {
				sum := sha256.Sum256([]byte(effectID))
				expectedEffect = sum[:]
			}
			item.SourceEffectDigestMatches = bytes.Equal(storedEffect, expectedEffect)
			item.Ready = item.StateMatches && item.OccurredAtMatches && item.RecordDigestMatches && item.SourceEffectDigestMatches && item.SourceReceiptMatches && item.ReadOnly && !item.Replayable
			allReady = allReady && item.Ready
			report.History = append(report.History, item)
		}
	}
	report.ReadyForReconcile = (report.BatchStatus == "imported" || report.BatchStatus == "reconciled") && allReady && report.OutcomeUnknownCount == 0 && len(report.Packages) > 0
	report.ReadyForProbe = report.BatchStatus == "reconciled" && report.ReadyForReconcile
	return report, nil
}
