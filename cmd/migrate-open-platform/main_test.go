package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadHistoricalMachineManifestRejectsCredentialMaterial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	payload := `{"schema_version":"aicrm-open-platform-machine-history-v1","source_system":"ai-crm","source_revision":"dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f","import_run_id":"open-platform-history-1","clients":[{"source_row_id":"42","client_id":"historic.identity","display_name":"Historic identity","purpose":"identity","token_ttl_seconds":1800,"secret_hash":"must-not-be-accepted"}]}`
	if err := os.WriteFile(path, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadManifest(path); err == nil {
		t.Fatal("historical secret field was accepted")
	}
}

func TestCanonicalHistoricalMachineManifestIsStableAndInert(t *testing.T) {
	expires := time.Date(2025, 1, 1, 0, 0, 0, 0, time.FixedZone("historic", 8*60*60))
	manifest := historicalManifest{
		SchemaVersion: historySchemaVersion, SourceSystem: "ai-crm", SourceRevision: "dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f", ImportRunID: "open-platform-history-2",
		Clients: []historicalClientRow{{SourceRowID: "b", ClientID: "historic.identity", DisplayName: "Historic identity", Purpose: "identity", TokenTTLSeconds: 1800, ExpiresAt: &expires}, {SourceRowID: "a", ClientID: "historic.mcp", DisplayName: "Historic MCP", Purpose: "mcp"}},
	}
	canonical, digest, err := canonicalManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if canonical.Clients[0].SourceRowID != "a" || canonical.Clients[0].TokenTTLSeconds != 1800 || canonical.Clients[1].ExpiresAt == nil || canonical.Clients[1].ExpiresAt.Location() != time.UTC || digest == [32]byte{} {
		t.Fatalf("canonical manifest=%+v digest=%x", canonical, digest)
	}
	rowDigest, err := sourceRowDigest(canonical.Clients[1])
	if err != nil || rowDigest == [32]byte{} {
		t.Fatalf("row digest=%x err=%v", rowDigest, err)
	}
}

func TestRunApplyRequiresDigestAndExplicitConfirmation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	payload := `{"schema_version":"aicrm-open-platform-machine-history-v1","source_system":"ai-crm","source_revision":"dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f","import_run_id":"open-platform-history-3","clients":[{"source_row_id":"42","client_id":"historic.identity","display_name":"Historic identity","purpose":"identity","token_ttl_seconds":1800}]}`
	if err := os.WriteFile(path, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(t.Context(), []string{"-mode", "apply", "-manifest", path}); err == nil {
		t.Fatal("apply without manifest digest reached the database path")
	}
	_, digest, err := loadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = run(t.Context(), []string{"-mode", "apply", "-manifest", path, "-manifest-sha256", fmt.Sprintf("%x", digest)}); err == nil {
		t.Fatal("apply without confirmation reached the database path")
	}
}
