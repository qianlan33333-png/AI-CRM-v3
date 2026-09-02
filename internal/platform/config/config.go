// Package config owns all environment-based runtime configuration.
package config

import (
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Role string

const (
	RoleAPI           Role = "api"
	RoleWorker        Role = "worker"
	RoleEffectsWorker Role = "effects-worker"
)

type Runtime struct {
	ListenAddress string
	ReleaseSHA    string
	Role          Role
	DatabaseURL   string
	PublicOrigin  string
	Bootstrap     Bootstrap
	WeCom         WeCom
	Effects       Effects
	WorkerOwner   string
	WorkerLimit   int
}

type Bootstrap struct {
	Enabled     bool
	Username    string
	Password    string
	DisplayName string
}

type WeCom struct {
	Enabled           bool
	CorpID            string
	AgentID           string
	Secret            string
	CallbackToken     string
	CallbackAESKey    string
	ContextSigningKey string
}
type Effects struct{ ProviderEnabled bool }

func Load() (Runtime, error) {
	databaseURL, err := DatabaseURL()
	if err != nil {
		return Runtime{}, err
	}
	cfg := Runtime{
		ListenAddress: valueOrDefault("AICRM_LISTEN_ADDR", "127.0.0.1:8080"),
		ReleaseSHA:    valueOrDefault("AICRM_RELEASE_SHA", "development"),
		Role:          Role(valueOrDefault("AICRM_ROLE", string(RoleAPI))),
		DatabaseURL:   databaseURL,
		PublicOrigin:  valueOrDefault("AICRM_PUBLIC_ORIGIN", "https://id-dev.youcangogogo.com"),
		WorkerOwner:   valueOrDefault("AICRM_WORKER_OWNER", "aicrm-wecom-worker"),
		WorkerLimit:   25,
		Bootstrap: Bootstrap{
			Username: os.Getenv("AICRM_BOOTSTRAP_USERNAME"), Password: os.Getenv("AICRM_BOOTSTRAP_PASSWORD"),
			DisplayName: os.Getenv("AICRM_BOOTSTRAP_DISPLAY_NAME"),
		},
		WeCom: WeCom{
			CorpID: os.Getenv("AICRM_WECOM_CORP_ID"), AgentID: os.Getenv("AICRM_WECOM_AGENT_ID"),
			Secret: os.Getenv("AICRM_WECOM_SECRET"), CallbackToken: os.Getenv("AICRM_WECOM_CALLBACK_TOKEN"),
			CallbackAESKey: os.Getenv("AICRM_WECOM_CALLBACK_AES_KEY"), ContextSigningKey: os.Getenv("AICRM_WECOM_CONTEXT_SIGNING_KEY"),
		},
	}
	if cfg.Effects.ProviderEnabled, err = strictBool("AICRM_OUTBOUND_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.WeCom.Enabled, err = strictBool("AICRM_WECOM_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if raw := os.Getenv("AICRM_WORKER_LIMIT"); raw != "" {
		cfg.WorkerLimit, err = strconv.Atoi(raw)
		if err != nil || cfg.WorkerLimit < 1 || cfg.WorkerLimit > 100 {
			return Runtime{}, errors.New("invalid AICRM_WORKER_LIMIT")
		}
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
	case RoleAPI, RoleWorker, RoleEffectsWorker:
	default:
		return Runtime{}, errors.New("invalid AICRM_ROLE")
	}
	if strings.TrimSpace(cfg.DatabaseURL) != cfg.DatabaseURL || cfg.DatabaseURL == "" {
		return Runtime{}, errors.New("invalid database URL")
	}
	if !validPublicOrigin(cfg.PublicOrigin) {
		return Runtime{}, errors.New("invalid AICRM_PUBLIC_ORIGIN")
	}
	if strings.TrimSpace(cfg.WorkerOwner) != cfg.WorkerOwner || cfg.WorkerOwner == "" || len(cfg.WorkerOwner) > 120 {
		return Runtime{}, errors.New("invalid AICRM_WORKER_OWNER")
	}
	bootstrapValues := []string{cfg.Bootstrap.Username, cfg.Bootstrap.Password, cfg.Bootstrap.DisplayName}
	bootstrapCount := nonEmptyCount(bootstrapValues)
	if bootstrapCount != 0 && bootstrapCount != len(bootstrapValues) {
		return Runtime{}, errors.New("bootstrap administrator configuration must be all-or-none")
	}
	cfg.Bootstrap.Enabled = bootstrapCount == len(bootstrapValues)
	if cfg.Bootstrap.Enabled && (strings.TrimSpace(cfg.Bootstrap.Username) != cfg.Bootstrap.Username ||
		strings.TrimSpace(cfg.Bootstrap.DisplayName) != cfg.Bootstrap.DisplayName || cfg.Bootstrap.Password == "") {
		return Runtime{}, errors.New("invalid bootstrap administrator configuration")
	}
	if cfg.WeCom.Enabled {
		values := []string{cfg.WeCom.CorpID, cfg.WeCom.AgentID, cfg.WeCom.Secret, cfg.WeCom.CallbackToken, cfg.WeCom.CallbackAESKey, cfg.WeCom.ContextSigningKey}
		if nonEmptyCount(values) != len(values) || len(cfg.WeCom.ContextSigningKey) < 32 {
			return Runtime{}, errors.New("enabled WeCom configuration is incomplete")
		}
		for _, value := range values {
			if strings.TrimSpace(value) != value {
				return Runtime{}, errors.New("invalid enabled WeCom configuration")
			}
		}
	}
	return cfg, nil
}

func strictBool(key string, fallback bool) (bool, error) {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback, nil
	}
	if value == "true" {
		return true, nil
	}
	if value == "false" {
		return false, nil
	}
	return false, errors.New("invalid " + key)
}

func validPublicOrigin(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == ""
}

func nonEmptyCount(values []string) int {
	count := 0
	for _, value := range values {
		if value != "" {
			count++
		}
	}
	return count
}

func valueOrDefault(key, fallback string) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	return value
}

// DatabaseURL returns the PostgreSQL connection URL for database-aware
// commands. AICRM_DATABASE_URL is the canonical runtime name; DATABASE_URL is
// accepted for local tools and integration-test environments.
func DatabaseURL() (string, error) {
	for _, key := range []string{"AICRM_DATABASE_URL", "DATABASE_URL"} {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			if strings.TrimSpace(value) != value {
				return "", errors.New("invalid database URL")
			}
			return value, nil
		}
	}
	return "", errors.New("database URL is not configured")
}
