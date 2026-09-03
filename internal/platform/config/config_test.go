package config

import (
	"strings"
	"testing"
)

func TestLoadDefaultsAndRejectsInvalidRole(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_LISTEN_ADDR", "")
	t.Setenv("AICRM_RELEASE_SHA", "")
	t.Setenv("AICRM_ROLE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddress != "127.0.0.1:8080" || cfg.ReleaseSHA != "development" || cfg.Role != RoleAPI ||
		cfg.DatabaseURL == "" || cfg.PublicOrigin != "https://id-dev.youcangogogo.com" || cfg.WorkerLimit != 25 {
		t.Fatalf("defaults=%+v", cfg)
	}

	t.Setenv("AICRM_ROLE", "unknown")
	if _, err = Load(); err == nil {
		t.Fatal("expected invalid role error")
	}
}

func TestLoadValidatesBootstrapAndWeComAsClosedConfigurationGroups(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_BOOTSTRAP_USERNAME", "admin")
	if _, err := Load(); err == nil {
		t.Fatal("expected partial bootstrap configuration error")
	}

	t.Setenv("AICRM_BOOTSTRAP_PASSWORD", "this-is-a-strong-password")
	t.Setenv("AICRM_BOOTSTRAP_DISPLAY_NAME", "CRM Admin")
	t.Setenv("AICRM_WECOM_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("expected partial WeCom configuration error")
	}

	t.Setenv("AICRM_WECOM_CORP_ID", "ww-corp")
	t.Setenv("AICRM_WECOM_AGENT_ID", "1000002")
	t.Setenv("AICRM_WECOM_SECRET", "provider-secret")
	t.Setenv("AICRM_WECOM_CONTEXT_SIGNING_KEY", "0123456789abcdef0123456789abcdef")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Bootstrap.Enabled || !cfg.WeCom.Enabled {
		t.Fatalf("config=%+v", cfg)
	}
}

func TestLoadValidatesCallbackConfigurationIndependently(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_WECOM_CALLBACK_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("expected partial callback configuration error")
	}

	t.Setenv("AICRM_WECOM_CORP_ID", "ww-corp")
	t.Setenv("AICRM_WECOM_CALLBACK_TOKEN", "callback-token")
	t.Setenv("AICRM_WECOM_CALLBACK_AES_KEY", strings.Repeat("a", 43))
	t.Setenv("AICRM_CHANNEL_STATE_HMAC_KEY", strings.Repeat("b", 32))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.WeCom.CallbackEnabled || cfg.WeCom.Enabled {
		t.Fatalf("config=%+v", cfg.WeCom)
	}
}

func TestLoadRejectsInsecureOriginAndLooseBoolean(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_PUBLIC_ORIGIN", "http://id-dev.youcangogogo.com")
	if _, err := Load(); err == nil {
		t.Fatal("expected insecure origin error")
	}
	t.Setenv("AICRM_PUBLIC_ORIGIN", "https://id-dev.youcangogogo.com")
	t.Setenv("AICRM_WECOM_ENABLED", "1")
	if _, err := Load(); err == nil {
		t.Fatal("expected strict boolean error")
	}
	t.Setenv("AICRM_WECOM_ENABLED", "false")
	t.Setenv("AICRM_WECOM_CALLBACK_ENABLED", "1")
	if _, err := Load(); err == nil {
		t.Fatal("expected callback strict boolean error")
	}
	t.Setenv("AICRM_WECOM_CALLBACK_ENABLED", "false")
	t.Setenv("AICRM_OUTBOUND_PROVIDER_ENABLED", "1")
	if _, err := Load(); err == nil {
		t.Fatal("expected outbound provider loose boolean error")
	}
}

func TestLoadAcceptsEffectsWorkerRole(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_ROLE", "effects-worker")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Role != RoleEffectsWorker || cfg.Effects.ProviderEnabled {
		t.Fatalf("config=%+v", cfg)
	}
}

func TestWeChatShopProviderIsIndependentAndClosedConfiguration(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_WECHAT_SHOP_PROVIDER_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("expected partial WeChat Shop configuration error")
	}
	t.Setenv("AICRM_WECHAT_SHOP_APP_ID", "shop-app")
	t.Setenv("AICRM_WECHAT_SHOP_APP_SECRET", "shop-secret")
	t.Setenv("AICRM_WECHAT_SHOP_CALLBACK_TOKEN", "callback-token")
	t.Setenv("AICRM_WECHAT_SHOP_CALLBACK_AES_KEY", strings.Repeat("a", 43))
	cfg, err := Load()
	if err != nil || !cfg.WeChatShop.Enabled || cfg.WeChatPay.Enabled {
		t.Fatalf("config=%+v err=%v", cfg.WeChatShop, err)
	}
}

func TestWeChatPayProviderRequiresMiniProgramIdentityCredential(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_WECHAT_PAY_PROVIDER_ENABLED", "true")
	t.Setenv("AICRM_WECHAT_PAY_APP_ID", "wx-app")
	t.Setenv("AICRM_WECHAT_PAY_APP_SCOPE", "wechat-app:wx-app")
	t.Setenv("AICRM_WECHAT_PAY_MERCHANT_ID", "merchant")
	t.Setenv("AICRM_WECHAT_PAY_MERCHANT_SERIAL", "serial")
	t.Setenv("AICRM_WECHAT_PAY_PRIVATE_KEY_PATH", "/keys/merchant.pem")
	t.Setenv("AICRM_WECHAT_PAY_PLATFORM_CERT_PATH", "/keys/platform.pem")
	t.Setenv("AICRM_WECHAT_PAY_API_V3_KEY", strings.Repeat("k", 32))
	if _, err := Load(); err == nil {
		t.Fatal("expected missing mini program AppSecret to fail closed")
	}
	t.Setenv("AICRM_WECHAT_PAY_APP_SECRET", "app-secret")
	cfg, err := Load()
	if err != nil || !cfg.WeChatPay.Enabled || cfg.WeChatPay.AppSecret != "app-secret" {
		t.Fatalf("config=%+v err=%v", cfg.WeChatPay, err)
	}
}

func TestTagCatalogProviderRequiresNarrowExplicitPermission(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://aicrm:test@localhost/aicrm")
	t.Setenv("AICRM_WECOM_TAG_CATALOG_PROVIDER_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("expected disabled WeCom rejection")
	}
	t.Setenv("AICRM_WECOM_ENABLED", "true")
	t.Setenv("AICRM_WECOM_CORP_ID", "ww-corp")
	t.Setenv("AICRM_WECOM_AGENT_ID", "1000002")
	t.Setenv("AICRM_WECOM_SECRET", "provider-secret")
	t.Setenv("AICRM_WECOM_CONTEXT_SIGNING_KEY", "0123456789abcdef0123456789abcdef")
	if _, err := Load(); err == nil {
		t.Fatal("expected permission rejection")
	}
	t.Setenv("AICRM_WECOM_TAG_CATALOG_PROVIDER_PERMISSION", "catalog-read-authorized")
	cfg, err := Load()
	if err != nil || !cfg.TagCatalog.Enabled || cfg.Effects.ProviderEnabled {
		t.Fatalf("config=%+v err=%v", cfg.TagCatalog, err)
	}
}

func TestDatabaseURLPrecedenceAndValidation(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://fallback")
	t.Setenv("AICRM_DATABASE_URL", "postgres://canonical")

	url, err := DatabaseURL()
	if err != nil {
		t.Fatal(err)
	}
	if url != "postgres://canonical" {
		t.Fatalf("DatabaseURL()=%q", url)
	}

	t.Setenv("AICRM_DATABASE_URL", " postgres://invalid")
	if _, err = DatabaseURL(); err == nil {
		t.Fatal("expected surrounding whitespace to be rejected")
	}
}

func TestDatabaseURLRequiresConfiguration(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "")
	t.Setenv("DATABASE_URL", "")
	if _, err := DatabaseURL(); err == nil {
		t.Fatal("expected missing database URL error")
	}
}
