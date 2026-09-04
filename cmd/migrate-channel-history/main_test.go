package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEncryptedSnapshotRoundTripAndTamperDetection(t *testing.T) {
	payload, err := compactJSONObject([]byte(`{ "id": 1, "channel_id": 7, "url": "https://example.test/?a=1&b=<x>" }`))
	if err != nil {
		t.Fatal(err)
	}
	rowDigest := sha256.Sum256(payload)
	row := snapshotRow{SourcePK: "1", Digest: "sha256:" + hex.EncodeToString(rowDigest[:]), Payload: payload}
	manifest := snapshotManifest{SchemaVersion: 1, SnapshotTimestamp: time.Unix(1_788_336_000, 0).UTC(), SourceHostDigest: hashText("source"), Tables: []snapshotTable{{Name: "automation_channel_contact", Columns: []string{"id", "channel_id"}, Rows: []snapshotRow{row}}}}
	manifest.Tables[0].Digest = digestRows(manifest.Tables[0].Rows)
	manifest.ManifestDigest = manifest.computeDigest()
	manifest.SnapshotID = "channel-" + manifest.DigestHex()[:24]
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x42}, 32)
	path := filepath.Join(t.TempDir(), "snapshot.enc")
	if err := writeEncryptedSnapshot(path, key, manifest); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode=%o", info.Mode().Perm())
	}
	loaded, err := loadEncryptedSnapshot(path, key)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SnapshotID != manifest.SnapshotID || loaded.ManifestDigest != manifest.ManifestDigest {
		t.Fatalf("loaded=%+v", loaded)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = loadEncryptedSnapshot(path, key); err == nil {
		t.Fatal("tampered snapshot was accepted")
	}
}

func TestManifestRejectsRowAndTableDrift(t *testing.T) {
	payload := json.RawMessage(`{"id":1}`)
	digest := sha256.Sum256(payload)
	row := snapshotRow{SourcePK: "1", Digest: "sha256:" + hex.EncodeToString(digest[:]), Payload: payload}
	manifest := snapshotManifest{SchemaVersion: 1, SnapshotID: "channel-test", SnapshotTimestamp: time.Now().UTC(), SourceHostDigest: hashText("host"), Tables: []snapshotTable{{Name: "automation_channel", Digest: digestRows([]snapshotRow{row}), Rows: []snapshotRow{row}}}}
	manifest.ManifestDigest = manifest.computeDigest()
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	manifest.Tables[0].Rows[0].Payload = json.RawMessage(`{"id":2}`)
	if err := manifest.Validate(); err == nil {
		t.Fatal("row drift was accepted")
	}
}

func TestDuplicateSourceIDsAreDetectedWithoutConflatingOccurrences(t *testing.T) {
	manifest := snapshotManifest{Tables: []snapshotTable{
		{Name: "automation_channel", Rows: []snapshotRow{{SourcePK: "id=7#1", Payload: json.RawMessage(`{"id":7,"code":"one"}`)}, {SourcePK: "id=7#2", Payload: json.RawMessage(`{"id":7,"code":"two"}`)}, {SourcePK: "id=8#1", Payload: json.RawMessage(`{"id":8}`)}}},
		{Name: "automation_channel_contact", Rows: []snapshotRow{{SourcePK: "id=9#1", Payload: json.RawMessage(`{"id":9,"channel_id":7}`)}}},
	}}
	duplicates := duplicateSourceIDs(manifest)
	if !duplicates["automation_channel"][7] || duplicates["automation_channel"][8] || !duplicateRowID(manifest.Tables[0].Rows[1], duplicates["automation_channel"]) {
		t.Fatalf("duplicates=%v", duplicates)
	}
	if duplicateRowID(manifest.Tables[1].Rows[0], duplicates["automation_channel_contact"]) {
		t.Fatalf("unique child row was marked duplicate: %v", duplicates)
	}
}

func TestParseSourceStreamBuildsDeterministicManifest(t *testing.T) {
	columns, err := json.Marshal([]string{"id", "channel_code"})
	if err != nil {
		t.Fatal(err)
	}
	row := []byte(`{ "channel_code": "legacy", "id": 7 }`)
	canonicalRow := `{"channel_code":"legacy","id":7}`
	stream := fmt.Sprintf("ignored psql header\n %s2026-09-04T01:02:03.123456Z \n %sautomation_channel|%s \n %s%s \n(1 row)\n",
		streamSnapshotPrefix, streamTablePrefix, hex.EncodeToString(columns), streamRowPrefix, hex.EncodeToString(row))
	manifest, err := parseSourceStream(bytes.NewBufferString(stream), "source.example")
	if err != nil {
		t.Fatal(err)
	}
	if got := manifest.SnapshotTimestamp.Format(time.RFC3339Nano); got != "2026-09-04T01:02:03.123456Z" {
		t.Fatalf("snapshot timestamp=%s", got)
	}
	if len(manifest.Tables) != 1 || manifest.Tables[0].Name != "automation_channel" || len(manifest.Tables[0].Rows) != 1 {
		t.Fatalf("manifest=%+v", manifest)
	}
	if manifest.Tables[0].Rows[0].SourcePK != "id=7#1" || string(manifest.Tables[0].Rows[0].Payload) != canonicalRow {
		t.Fatalf("row=%+v", manifest.Tables[0].Rows[0])
	}
}

func TestParseSourceStreamFailsClosed(t *testing.T) {
	for name, stream := range map[string]string{
		"missing timestamp": streamTablePrefix + "automation_channel|5b226964225d\n",
		"row first":         streamSnapshotPrefix + "2026-09-04T01:02:03Z\n" + streamRowPrefix + "7b7d\n",
		"bad row":           streamSnapshotPrefix + "2026-09-04T01:02:03Z\n" + streamTablePrefix + "automation_channel|5b226964225d\n" + streamRowPrefix + "00\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSourceStream(bytes.NewBufferString(stream), "source.example"); err == nil {
				t.Fatal("invalid source stream was accepted")
			}
		})
	}
}

func TestSemanticValidationRequiresReferencedDependenciesAndOwnerFallback(t *testing.T) {
	manifest := snapshotManifest{}
	for _, name := range expectedSourceTables {
		manifest.Tables = append(manifest.Tables, snapshotTable{Name: name})
	}
	for index := range manifest.Tables {
		switch manifest.Tables[index].Name {
		case "automation_channel":
			manifest.Tables[index].Rows = []snapshotRow{{Payload: json.RawMessage(`{"id":7,"owner_staff_id":"owner-a","welcome_image_library_ids":[3],"entry_tag_id":"tag-a"}`)}}
		case "image_library":
			manifest.Tables[index].Rows = []snapshotRow{{Payload: json.RawMessage(`{"id":3}`)}}
		case "wecom_corp_tags":
			manifest.Tables[index].Rows = []snapshotRow{{Payload: json.RawMessage(`{"tag_id":"tag-a"}`)}}
		}
	}
	result, err := validateSemantics(manifest)
	if err != nil || result.Channels != 1 || result.ReferencedMaterials != 1 || result.ReferencedTags != 1 {
		t.Fatalf("semantic validation=%+v err=%v", result, err)
	}
	assignees := semanticExpectedAssignees(manifest)
	if len(assignees[7]) != 1 || firstString(assignees[7][0].Payload, "staff_id") != "owner-a" {
		t.Fatalf("owner fallback=%v", assignees)
	}
	for index := range manifest.Tables {
		if manifest.Tables[index].Name == "image_library" {
			manifest.Tables[index].Rows = nil
		}
	}
	if result, err = validateSemantics(manifest); err == nil || result.MissingMaterialRows != 1 {
		t.Fatalf("missing material validation=%+v err=%v", result, err)
	}
}

func TestProjectMappedEntryTagRequiresCompleteOwnerProjection(t *testing.T) {
	id := int64(41)
	projected, name, group, ok := projectMappedEntryTag(&id, "mapped", " 新客 ", " 来源 ")
	if !ok || projected != id || name != "新客" || group != "来源" {
		t.Fatalf("mapped projection=(%v,%q,%q,%v)", projected, name, group, ok)
	}
	for testName, value := range map[string]struct {
		id          *int64
		state       string
		name, group string
	}{
		"unresolved":    {&id, "unresolved", "新客", "来源"},
		"missing id":    {nil, "mapped", "新客", "来源"},
		"missing name":  {&id, "mapped", "", "来源"},
		"missing group": {&id, "mapped", "新客", ""},
	} {
		t.Run(testName, func(t *testing.T) {
			projected, name, group, ok := projectMappedEntryTag(value.id, value.state, value.name, value.group)
			if ok || projected != nil || name != "" || group != "" {
				t.Fatalf("unsafe projection=(%v,%q,%q,%v)", projected, name, group, ok)
			}
		})
	}
}

func TestSemanticSourceAssignmentDefectsRemainBlockedWithoutInventingData(t *testing.T) {
	if got := semanticAssignmentBlockers("ratio", nil); len(got) != 1 || got[0] != "assignees_missing" {
		t.Fatalf("empty assignment blockers=%v", got)
	}
	assigned := []semanticAssignment{{id: 1, priority: 1, ratio: 100}, {id: 2, priority: 2, ratio: 100}}
	if got := semanticAssignmentBlockers("ratio", assigned); len(got) != 1 || got[0] != "assignment_ratio_invalid" {
		t.Fatalf("invalid ratio blockers=%v", got)
	}
	if assigned[0].ratio != 100 || assigned[1].ratio != 100 {
		t.Fatalf("source ratios were mutated: %+v", assigned)
	}
	assigned[1].priority = 1
	if !normalizeSemanticPriorities(assigned) || assigned[0].priority != 1 || assigned[1].priority != 2 {
		t.Fatalf("duplicate priorities were not deterministically projected: %+v", assigned)
	}
	if got := semanticAssignmentBlockers("ratio", []semanticAssignment{{ratio: 40}, {ratio: 60}}); len(got) != 0 {
		t.Fatalf("valid ratio blockers=%v", got)
	}
}

func TestSemanticConfigMismatchCountDoesNotDoubleCountConflicts(t *testing.T) {
	if got := semanticConfigMismatchCount(51, 46); got != 5 {
		t.Fatalf("mismatch count=%d", got)
	}
	if got := semanticConfigMismatchCount(51, 51); got != 0 {
		t.Fatalf("complete mismatch count=%d", got)
	}
	if got := semanticConfigMismatchCount(51, 52); got != 0 {
		t.Fatalf("overcomplete mismatch count=%d", got)
	}
}

func hashText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}
