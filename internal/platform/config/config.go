// Package config owns all environment-based runtime configuration.
package config

import (
	"encoding/base64"
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
	ListenAddress              string
	ReleaseSHA                 string
	Role                       Role
	DatabaseURL                string
	PublicOrigin               string
	Bootstrap                  Bootstrap
	WeCom                      WeCom
	GroupOps                   GroupOps
	Effects                    Effects
	TagCatalog                 TagCatalogProvider
	Survey                     Survey
	WeChatPay                  WeChatPay
	WeChatShop                 WeChatShop
	WorkerOwner                string
	WorkerLimit                int
	CustomerSyncTrigger        string
	HXCDashboard               HXCDashboard
	OperationCycleServiceToken string
}

type Bootstrap struct {
	Enabled     bool
	Username    string
	Password    string
	DisplayName string
}

type WeCom struct {
	Enabled             bool
	CallbackEnabled     bool
	CustomerSyncEnabled bool
	CorpID              string
	AgentID             string
	Secret              string
	ContactSecret       string
	CallbackToken       string
	CallbackAESKey      string
	ContextSigningKey   string
	ChannelStateHMACKey string
}

// GroupOps contains only the inbound protocol secret for the local Group Ops
// webhook. It is independent from WeCom/customer credentials and is never
// exposed through a descriptor or structured log.
type GroupOps struct {
	WebhookSecret string
}

type Effects struct{ ProviderEnabled bool }
type HXCDashboard struct {
	Enabled        bool
	SourceDSN      string
	UnionIDScope   string
	SubjectHMACKey string
	SyncTrigger    string
}
type Survey struct {
	DataKey             string
	OAuthEnabled        bool
	OAuthAppID          string
	OAuthSecret         string
	OAuthOpenPlatformID string
}

// TagCatalogProvider is intentionally separate from outbound message/write
// configuration. It permits only the read-only catalog adapter and remains
// off unless both the boolean and a human permission acknowledgement exist.
type TagCatalogProvider struct {
	Enabled    bool
	Permission string
}

type WeChatPay struct {
	Enabled                          bool
	AppID, AppSecret, AppScope       string
	MerchantID, MerchantSerial       string
	PrivateKeyPath, PlatformCertPath string
	APIV3Key                         string
}

type WeChatShop struct {
	Enabled                               bool
	AppID, AppSecret                      string
	CallbackToken, CallbackEncodingAESKey string
}

func Load() (Runtime, error) {
	databaseURL, err := DatabaseURL()
	if err != nil {
		return Runtime{}, err
	}
	cfg := Runtime{
		ListenAddress:              valueOrDefault("AICRM_LISTEN_ADDR", "127.0.0.1:8080"),
		ReleaseSHA:                 valueOrDefault("AICRM_RELEASE_SHA", "development"),
		Role:                       Role(valueOrDefault("AICRM_ROLE", string(RoleAPI))),
		DatabaseURL:                databaseURL,
		PublicOrigin:               valueOrDefault("AICRM_PUBLIC_ORIGIN", "https://id-dev.youcangogogo.com"),
		WorkerOwner:                valueOrDefault("AICRM_WORKER_OWNER", "aicrm-wecom-worker"),
		WorkerLimit:                25,
		CustomerSyncTrigger:        os.Getenv("AICRM_CUSTOMER_SYNC_TRIGGER"),
		HXCDashboard:               HXCDashboard{SourceDSN: os.Getenv("AICRM_HXC_SOURCE_DSN"), UnionIDScope: os.Getenv("AICRM_HXC_UNIONID_SCOPE"), SubjectHMACKey: os.Getenv("AICRM_HXC_SUBJECT_HMAC_KEY"), SyncTrigger: os.Getenv("AICRM_HXC_SYNC_TRIGGER")},
		OperationCycleServiceToken: os.Getenv("AICRM_OPERATION_CYCLE_SERVICE_TOKEN"),
		Bootstrap: Bootstrap{
			Username: os.Getenv("AICRM_BOOTSTRAP_USERNAME"), Password: os.Getenv("AICRM_BOOTSTRAP_PASSWORD"),
			DisplayName: os.Getenv("AICRM_BOOTSTRAP_DISPLAY_NAME"),
		},
		WeCom: WeCom{
			CorpID: os.Getenv("AICRM_WECOM_CORP_ID"), AgentID: os.Getenv("AICRM_WECOM_AGENT_ID"),
			Secret: os.Getenv("AICRM_WECOM_SECRET"), ContactSecret: os.Getenv("AICRM_WECOM_CONTACT_SECRET"), CallbackToken: os.Getenv("AICRM_WECOM_CALLBACK_TOKEN"),
			CallbackAESKey: os.Getenv("AICRM_WECOM_CALLBACK_AES_KEY"), ContextSigningKey: os.Getenv("AICRM_WECOM_CONTEXT_SIGNING_KEY"),
			ChannelStateHMACKey: os.Getenv("AICRM_CHANNEL_STATE_HMAC_KEY"),
		},
		GroupOps: GroupOps{WebhookSecret: os.Getenv("AICRM_GROUP_OPS_WEBHOOK_SECRET")},
		Survey:   Survey{DataKey: os.Getenv("AICRM_SURVEY_DATA_KEY"), OAuthAppID: os.Getenv("AICRM_SURVEY_OAUTH_APP_ID"), OAuthSecret: os.Getenv("AICRM_SURVEY_OAUTH_SECRET"), OAuthOpenPlatformID: os.Getenv("AICRM_SURVEY_OAUTH_OPEN_PLATFORM_ID")},
	}
	if cfg.Survey.OAuthEnabled, err = strictBool("AICRM_SURVEY_OAUTH_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.Effects.ProviderEnabled, err = strictBool("AICRM_OUTBOUND_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.HXCDashboard.Enabled, err = strictBool("AICRM_HXC_SYNC_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.TagCatalog.Enabled, err = strictBool("AICRM_WECOM_TAG_CATALOG_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.WeChatPay.Enabled, err = strictBool("AICRM_WECHAT_PAY_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	cfg.WeChatPay.AppID = os.Getenv("AICRM_WECHAT_PAY_APP_ID")
	cfg.WeChatPay.AppSecret = os.Getenv("AICRM_WECHAT_PAY_APP_SECRET")
	cfg.WeChatPay.AppScope = os.Getenv("AICRM_WECHAT_PAY_APP_SCOPE")
	cfg.WeChatPay.MerchantID = os.Getenv("AICRM_WECHAT_PAY_MERCHANT_ID")
	cfg.WeChatPay.MerchantSerial = os.Getenv("AICRM_WECHAT_PAY_MERCHANT_SERIAL")
	cfg.WeChatPay.PrivateKeyPath = os.Getenv("AICRM_WECHAT_PAY_PRIVATE_KEY_PATH")
	cfg.WeChatPay.PlatformCertPath = os.Getenv("AICRM_WECHAT_PAY_PLATFORM_CERT_PATH")
	cfg.WeChatPay.APIV3Key = os.Getenv("AICRM_WECHAT_PAY_API_V3_KEY")
	if cfg.WeChatShop.Enabled, err = strictBool("AICRM_WECHAT_SHOP_PROVIDER_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	cfg.WeChatShop.AppID = os.Getenv("AICRM_WECHAT_SHOP_APP_ID")
	cfg.WeChatShop.AppSecret = os.Getenv("AICRM_WECHAT_SHOP_APP_SECRET")
	cfg.WeChatShop.CallbackToken = os.Getenv("AICRM_WECHAT_SHOP_CALLBACK_TOKEN")
	cfg.WeChatShop.CallbackEncodingAESKey = os.Getenv("AICRM_WECHAT_SHOP_CALLBACK_AES_KEY")
	cfg.TagCatalog.Permission = os.Getenv("AICRM_WECOM_TAG_CATALOG_PROVIDER_PERMISSION")
	if cfg.WeCom.Enabled, err = strictBool("AICRM_WECOM_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.WeCom.CallbackEnabled, err = strictBool("AICRM_WECOM_CALLBACK_ENABLED", false); err != nil {
		return Runtime{}, err
	}
	if cfg.WeCom.CustomerSyncEnabled, err = strictBool("AICRM_WECOM_CUSTOMER_SYNC_ENABLED", false); err != nil {
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
	if cfg.CustomerSyncTrigger != "" && cfg.CustomerSyncTrigger != "daily" && cfg.CustomerSyncTrigger != "initial" {
		return Runtime{}, errors.New("invalid AICRM_CUSTOMER_SYNC_TRIGGER")
	}
	if cfg.HXCDashboard.SyncTrigger != "" && cfg.HXCDashboard.SyncTrigger != "scheduled" && cfg.HXCDashboard.SyncTrigger != "initial" {
		return Runtime{}, errors.New("invalid AICRM_HXC_SYNC_TRIGGER")
	}
	if cfg.HXCDashboard.Enabled {
		if strings.TrimSpace(cfg.HXCDashboard.SourceDSN) != cfg.HXCDashboard.SourceDSN || cfg.HXCDashboard.SourceDSN == "" || !strings.HasPrefix(cfg.HXCDashboard.UnionIDScope, "wechat-open-platform:") || len(cfg.HXCDashboard.UnionIDScope) <= len("wechat-open-platform:") || strings.TrimSpace(cfg.HXCDashboard.UnionIDScope) != cfg.HXCDashboard.UnionIDScope || len(cfg.HXCDashboard.SubjectHMACKey) < 32 {
			return Runtime{}, errors.New("enabled HXC dashboard configuration is incomplete")
		}
	}
	if cfg.OperationCycleServiceToken != "" && (strings.TrimSpace(cfg.OperationCycleServiceToken) != cfg.OperationCycleServiceToken || len(cfg.OperationCycleServiceToken) < 32) {
		return Runtime{}, errors.New("invalid AICRM_OPERATION_CYCLE_SERVICE_TOKEN")
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
		values := []string{cfg.WeCom.CorpID, cfg.WeCom.AgentID, cfg.WeCom.Secret, cfg.WeCom.ContextSigningKey}
		if nonEmptyCount(values) != len(values) || len(cfg.WeCom.ContextSigningKey) < 32 {
			return Runtime{}, errors.New("enabled WeCom configuration is incomplete")
		}
		for _, value := range values {
			if strings.TrimSpace(value) != value {
				return Runtime{}, errors.New("invalid enabled WeCom configuration")
			}
		}
	}
	if cfg.WeCom.CallbackEnabled {
		values := []string{cfg.WeCom.CorpID, cfg.WeCom.CallbackToken, cfg.WeCom.CallbackAESKey, cfg.WeCom.ChannelStateHMACKey}
		if nonEmptyCount(values) != len(values) || len(cfg.WeCom.ChannelStateHMACKey) < 32 {
			return Runtime{}, errors.New("enabled WeCom callback configuration is incomplete")
		}
		for _, value := range values {
			if strings.TrimSpace(value) != value {
				return Runtime{}, errors.New("invalid enabled WeCom callback configuration")
			}
		}
	}
	if cfg.WeCom.CustomerSyncEnabled && (!cfg.WeCom.Enabled || strings.TrimSpace(cfg.WeCom.ContactSecret) != cfg.WeCom.ContactSecret || cfg.WeCom.ContactSecret == "") {
		return Runtime{}, errors.New("enabled WeCom customer sync configuration is incomplete")
	}
	if cfg.TagCatalog.Enabled {
		if !cfg.WeCom.Enabled || cfg.TagCatalog.Permission != "catalog-read-authorized" {
			return Runtime{}, errors.New("enabled tag catalog provider requires WeCom and explicit permission")
		}
	}
	if cfg.WeChatPay.Enabled {
		values := []string{cfg.WeChatPay.AppID, cfg.WeChatPay.AppSecret, cfg.WeChatPay.AppScope, cfg.WeChatPay.MerchantID, cfg.WeChatPay.MerchantSerial, cfg.WeChatPay.PrivateKeyPath, cfg.WeChatPay.PlatformCertPath, cfg.WeChatPay.APIV3Key}
		if nonEmptyCount(values) != len(values) || len(cfg.WeChatPay.APIV3Key) != 32 || !strings.HasPrefix(cfg.WeChatPay.AppScope, "wechat-app:") {
			return Runtime{}, errors.New("enabled WeChat Pay configuration is incomplete")
		}
		for _, value := range values {
			if strings.TrimSpace(value) != value {
				return Runtime{}, errors.New("invalid enabled WeChat Pay configuration")
			}
		}
	}
	if cfg.WeChatShop.Enabled {
		values := []string{cfg.WeChatShop.AppID, cfg.WeChatShop.AppSecret, cfg.WeChatShop.CallbackToken, cfg.WeChatShop.CallbackEncodingAESKey}
		if nonEmptyCount(values) != len(values) || len(cfg.WeChatShop.CallbackEncodingAESKey) != 43 {
			return Runtime{}, errors.New("enabled WeChat Shop configuration is incomplete")
		}
		for _, value := range values {
			if strings.TrimSpace(value) != value {
				return Runtime{}, errors.New("invalid enabled WeChat Shop configuration")
			}
		}
	}
	if cfg.GroupOps.WebhookSecret != "" && (strings.TrimSpace(cfg.GroupOps.WebhookSecret) != cfg.GroupOps.WebhookSecret || len(cfg.GroupOps.WebhookSecret) < 32 || len(cfg.GroupOps.WebhookSecret) > 4096) {
		return Runtime{}, errors.New("invalid Group Ops webhook secret")
	}
	if cfg.Survey.DataKey != "" {
		if decoded, decodeErr := base64.RawStdEncoding.DecodeString(cfg.Survey.DataKey); decodeErr != nil || len(decoded) != 32 {
			return Runtime{}, errors.New("invalid AICRM_SURVEY_DATA_KEY")
		}
	}
	if cfg.Survey.OAuthEnabled {
		values := []string{cfg.Survey.OAuthAppID, cfg.Survey.OAuthSecret, cfg.Survey.OAuthOpenPlatformID}
		if nonEmptyCount(values) != len(values) {
			return Runtime{}, errors.New("enabled survey OAuth configuration is incomplete")
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
