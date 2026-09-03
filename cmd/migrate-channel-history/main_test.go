package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEncryptedSnapshotRoundTripAndTamperDetection(t *testing.T) {
	payload := json.RawMessage(`{"id":1,"channel_id":7}`)
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

func hashText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}
