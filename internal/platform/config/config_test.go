package config

import "testing"

func TestLoadDefaultsAndRejectsInvalidRole(t *testing.T) {
	t.Setenv("AICRM_LISTEN_ADDR", "")
	t.Setenv("AICRM_RELEASE_SHA", "")
	t.Setenv("AICRM_ROLE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddress != "127.0.0.1:8080" || cfg.ReleaseSHA != "development" || cfg.Role != RoleAll {
		t.Fatalf("defaults=%+v", cfg)
	}

	t.Setenv("AICRM_ROLE", "unknown")
	if _, err = Load(); err == nil {
		t.Fatal("expected invalid role error")
	}
}
