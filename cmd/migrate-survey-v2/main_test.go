package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptedSnapshotAuthenticatesAndRejectsTampering(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	plain := []byte(`{"manifest":{"source_system":"test"}}`)
	sealed, err := encrypt(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := decrypt(key, sealed)
	if err != nil || !bytes.Equal(opened, plain) {
		t.Fatalf("roundtrip err=%v", err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err = decrypt(key, sealed); err == nil {
		t.Fatal("tampered snapshot accepted")
	}
}

func TestReadKeyRequiresOwnerOnlyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	value := make([]byte, 32)
	if err := os.WriteFile(path, []byte(base64.RawStdEncoding.EncodeToString(value)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readKey(path); err == nil {
		t.Fatal("group-readable key accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if key, err := readKey(path); err != nil || len(key) != 32 {
		t.Fatalf("key err=%v", err)
	}
}
