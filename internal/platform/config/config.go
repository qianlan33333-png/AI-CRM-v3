// Package config owns all environment-based runtime configuration.
package config

import (
	"errors"
	"net"
	"os"
	"strings"
)

type Role string

const (
	RoleAPI    Role = "api"
	RoleWorker Role = "worker"
	RoleAll    Role = "all"
)

type Runtime struct {
	ListenAddress string
	ReleaseSHA    string
	Role          Role
}

func Load() (Runtime, error) {
	cfg := Runtime{
		ListenAddress: valueOrDefault("AICRM_LISTEN_ADDR", "127.0.0.1:8080"),
		ReleaseSHA:    valueOrDefault("AICRM_RELEASE_SHA", "development"),
		Role:          Role(valueOrDefault("AICRM_ROLE", string(RoleAll))),
	}
	if strings.TrimSpace(cfg.ListenAddress) != cfg.ListenAddress || cfg.ListenAddress == "" {
		return Runtime{}, errors.New("invalid AICRM_LISTEN_ADDR")
	}
	if _, _, err := net.SplitHostPort(cfg.ListenAddress); err != nil {
		return Runtime{}, errors.New("invalid AICRM_LISTEN_ADDR")
	}
	if strings.TrimSpace(cfg.ReleaseSHA) != cfg.ReleaseSHA || cfg.ReleaseSHA == "" {
		return Runtime{}, errors.New("invalid AICRM_RELEASE_SHA")
	}
	switch cfg.Role {
	case RoleAPI, RoleWorker, RoleAll:
	default:
		return Runtime{}, errors.New("invalid AICRM_ROLE")
	}
	return cfg, nil
}

func valueOrDefault(key, fallback string) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	return value
}
