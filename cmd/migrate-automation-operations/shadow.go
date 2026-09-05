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
	CanonicalMemberCount int    `json:"canonical_member_count"`
	TargetMemberCount    int64  `json:"target_member_count"`
	MemberDigestMatches  bool   `json:"member_digest_matches"`
	PausedOrArchived     bool   `json:"paused_or_archived"`
	Ready                bool   `json:"ready"`
}

type ShadowReport struct {
	BatchKey               string          `json:"batch_key"`
	BatchStatus            string          `json:"batch_status"`
	ReferenceTime          time.Time       `json:"reference_time"`
	Packages               []ShadowPackage `json:"packages"`
	ProviderEffectsCreated int64           `json:"provider_effects_created"`
	RiverJobsCreated       int64           `json:"river_jobs_created"`
	OutcomeUnknownCount    int64           `json:"outcome_unknown_count"`
	History                []ShadowHistory `json:"history"`
	ReadyForProbe          bool            `json:"ready_for_probe"`
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
	ReadOnly                  bool      `json:"read_only"`
	Replayable                bool      `json:"replayable"`
	Ready                     bool      `json:"ready"`
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

	report := ShadowReport{BatchKey: batchKey, ReferenceTime: snapshot.Manifest.SnapshotAt.UTC()}
	var batchID, effectsBefore, effectsAfter, jobsBefore, jobsAfter int64
	var donorCommit, sourceWatermark string
	if err = tx.QueryRow(ctx, `SELECT id,status,donor_commit,encode(source_watermark_digest,'hex'),provider_effect_count_before,provider_effect_count_after,river_job_count_before,river_job_count_after FROM automation_operations_migration_batches WHERE batch_key=$1`, batchKey).Scan(&batchID, &report.BatchStatus, &donorCommit, &sourceWatermark, &effectsBefore, &effectsAfter, &jobsBefore, &jobsAfter); err != nil {
		return report, err
	}
	if donorCommit != snapshot.Manifest.DonorCommit || sourceWatermark != snapshot.Manifest.SourceWatermarkDigest {
		return report, errors.New("stored batch does not belong to supplied frozen snapshot")
	}
	report.ProviderEffectsCreated = effectsAfter - effectsBefore
	report.RiverJobsCreated = jobsAfter - jobsBefore

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

	allReady := report.BatchStatus == "reconciled" && report.ProviderEffectsCreated == 0 && report.RiverJobsCreated == 0
	for _, source := range packages {
		item := ShadowPackage{SourcePackageID: source.ID, SourceMemberRows: len(membersByPackage[source.ID])}
		if err = tx.QueryRow(ctx, `SELECT target_pk FROM automation_operations_migration_source_map WHERE batch_id=$1 AND source_table='audience_packages' AND source_pk=$2 AND disposition IN ('imported','mapped')`, batchID, sourcePK(snapshot, strconv.FormatInt(source.ID, 10))).Scan(&item.TargetPackageID); err != nil {
			return report, fmt.Errorf("package %d source map: %w", source.ID, err)
		}
		var targetDefinition []byte
		var targetCron *string
		var targetReference time.Time
		var targetDigest []byte
		if err = tx.QueryRow(ctx, `SELECT p.lifecycle,c.definition::text,c.refresh_cron_utc,s.reference_time,s.member_count,s.member_digest FROM segment_audience_packages p JOIN segment_audience_configuration_versions c ON c.id=p.current_configuration_version_id AND c.package_id=p.id JOIN segment_audience_snapshots s ON s.id=p.published_snapshot_id AND s.package_id=p.id WHERE p.id=$1`, item.TargetPackageID).Scan(&item.Lifecycle, &targetDefinition, &targetCron, &targetReference, &item.TargetMemberCount, &targetDigest); err != nil {
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
			var targetCustomer *int64
			var disposition, targetTable string
			mapErr := tx.QueryRow(ctx, `SELECT target_pk,disposition,target_table FROM automation_operations_migration_source_map WHERE batch_id=$1 AND source_table='audience_members' AND source_pk=$2`, batchID, sourcePK(snapshot, fmt.Sprintf("%d:%d", sourceMember.SegmentID, sourceMember.CustomerID))).Scan(&targetCustomer, &disposition, &targetTable)
			if mapErr != nil && !errors.Is(mapErr, pgx.ErrNoRows) {
				return report, mapErr
			}
			if mapErr == nil && disposition == "mapped" && targetCustomer != nil && *targetCustomer > 0 && targetTable == "segment_audience_snapshot_members.customer_id" {
				item.MappedMemberRows++
				canonical = append(canonical, *targetCustomer)
			}
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
		item.MemberDigestMatches = item.MappedMemberRows == item.SourceMemberRows && int64(len(unique)) == item.TargetMemberCount && bytes.Equal(expectedDigest[:], targetDigest)
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
			var storedDigest, storedEffect []byte
			var storedState string
			var storedOccurred time.Time
			if err = tx.QueryRow(ctx, `SELECT source_state,occurred_at,record_digest,source_effect_digest,read_only,replayable FROM automation_operations_legacy_history WHERE batch_id=$1 AND source_table=$2 AND source_pk=$3`, batchID, name, sourcePK(snapshot, pk)).Scan(&storedState, &storedOccurred, &storedDigest, &storedEffect, &item.ReadOnly, &item.Replayable); err != nil {
				return report, fmt.Errorf("history %s/%s: %w", name, pk, err)
			}
			item.StateMatches = storedState == item.SourceState
			item.OccurredAtMatches = storedOccurred.UTC().Equal(item.OccurredAt.UTC())
			expected := recordDigest(raw)
			item.RecordDigestMatches = bytes.Equal(storedDigest, expected[:])
			expectedEffect := []byte(nil)
			if effectID := historyString(object, "external_effect_id", ""); effectID != "" {
				sum := sha256.Sum256([]byte(effectID))
				expectedEffect = sum[:]
			}
			item.SourceEffectDigestMatches = bytes.Equal(storedEffect, expectedEffect)
			item.Ready = item.StateMatches && item.OccurredAtMatches && item.RecordDigestMatches && item.SourceEffectDigestMatches && item.ReadOnly && !item.Replayable
			allReady = allReady && item.Ready
			report.History = append(report.History, item)
		}
	}
	report.ReadyForProbe = allReady && report.OutcomeUnknownCount == 0 && len(report.Packages) > 0
	if err = tx.Commit(ctx); err != nil {
		return report, err
	}
	return report, nil
}
