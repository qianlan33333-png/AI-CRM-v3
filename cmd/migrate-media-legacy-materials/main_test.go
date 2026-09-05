package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFrozenSnapshotRejectsDuplicateAndUnverifiedRecords(t *testing.T) {
	s := validSnapshot()
	if err := s.validate(); err != nil {
		t.Fatal(err)
	}
	s.Materials = append(s.Materials, s.Materials[0])
	if err := s.validate(); err == nil {
		t.Fatal("duplicate immutable source record was accepted")
	}
	s = validSnapshot()
	s.Materials[0].SourceRecordDigest = "sha256:not-a-digest"
	if err := s.validate(); err == nil {
		t.Fatal("unverified source record digest was accepted")
	}
}

func TestInspectNeedsOnlyFrozenSnapshot(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "materials.json")
	raw := []byte(`{"manifest":{"source_system":"ai-crm-v2","source_revision":"0123456789012345678901234567890123456789","snapshot_at":"2026-09-05T01:02:03Z"},"materials":[{"kind":"image","legacy_id":"1","source_record_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","material_id":7,"source_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--mode=inspect", "--snapshot=" + path}); err != nil {
		t.Fatal(err)
	}
}

func validSnapshot() snapshot {
	var s snapshot
	s.Manifest.SourceSystem = expectedSourceSystem
	s.Manifest.SourceRevision = "0123456789012345678901234567890123456789"
	s.Manifest.SnapshotAt = time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC)
	s.Materials = []material{{Kind: "image", LegacyID: "1", SourceRecordDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", MaterialID: 7, SourceDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
	return s
}
