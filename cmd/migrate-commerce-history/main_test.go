package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectAllowsIncompleteButDryRunFailsClosed(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "snapshot.json")
	raw := `{"schema_version":"aicrm-commerce-history-v2","run_key":"test-run","coverage":{"identities":true},"identities":[],"orders":[],"refunds":[]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--mode=inspect", "--snapshot=" + path}); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--mode=dry-run", "--snapshot=" + path}); err == nil {
		t.Fatal("dry-run accepted incomplete source coverage")
	}
}
