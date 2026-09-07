package main

import (
	"encoding/json"
	"testing"
	"time"

	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func runtimeApplySetting(t *testing.T, key configport.RuntimeSettingKey, value any) configport.RuntimeSetting {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return configport.RuntimeSetting{Key: key, Value: raw}
}

// OneID decision: the test covers configuration guards around already-bound
// identity scopes; it neither resolves nor writes identities. Persistence and
// external-effects decisions: neither applies; this is pure startup projection.
func TestApplyRuntimeConfigAllowsValidatedAutomationAndAIDispatchSwitches(t *testing.T) {
	cfg := platformconfig.Runtime{
		AutomationOperations: platformconfig.AutomationOperations{ProviderMode: platformconfig.AutomationProviderDisabled, MaxRecipientsPerRun: 1},
		AIAssistant:          platformconfig.AIAssistant{DispatchEnabled: false},
		WorkerLimit:          1,
		WeCom:                platformconfig.WeCom{ContextTokenTTL: time.Minute, MessageArchivePageLimit: 1, MessageArchivePageBudget: 1},
	}
	applied, err := applyRuntimeConfig(cfg, configport.EffectiveSnapshot{Settings: []configport.RuntimeSetting{
		runtimeApplySetting(t, configport.AutomationOperationsProviderMode, "limited"),
		runtimeApplySetting(t, configport.AIAssistantDispatchEnabled, true),
	}})
	if err != nil {
		t.Fatalf("apply editable switches: %v", err)
	}
	if applied.AutomationOperations.ProviderMode != platformconfig.AutomationProviderLimited || !applied.AIAssistant.DispatchEnabled {
		t.Fatalf("editable switches were not applied: %#v", applied)
	}
}

func TestApplyRuntimeConfigRejectsBoundPaymentAppIDSwitch(t *testing.T) {
	cfg := platformconfig.Runtime{
		AutomationOperations: platformconfig.AutomationOperations{ProviderMode: platformconfig.AutomationProviderDisabled, MaxRecipientsPerRun: 1},
		WeChatPay:            platformconfig.WeChatPay{AppID: "wx-pay-bound", AppScope: "wechat-app:bound", H5AppID: "wx-h5-bound", H5AppScope: "wechat-h5:bound", MerchantID: "mch-bound", MerchantSerial: "serial-old"},
		WeChatShop:           platformconfig.WeChatShop{AppID: "shop-bound"},
		WorkerLimit:          1,
		WeCom:                platformconfig.WeCom{ContextTokenTTL: time.Minute, MessageArchivePageLimit: 1, MessageArchivePageBudget: 1},
	}
	for _, setting := range []configport.RuntimeSetting{
		runtimeApplySetting(t, configport.WeChatPayAppID, "wx-pay-other"),
		runtimeApplySetting(t, configport.WeChatPayH5AppID, "wx-h5-other"),
		runtimeApplySetting(t, configport.WeChatPayMerchantID, "mch-other"),
		runtimeApplySetting(t, configport.WeChatShopAppID, "shop-other"),
	} {
		if _, err := applyRuntimeConfig(cfg, configport.EffectiveSnapshot{Settings: []configport.RuntimeSetting{setting}}); err == nil {
			t.Fatalf("expected bound %s switch rejection", setting.Key)
		}
	}
}

func TestValidateAppliedRuntimeConfigRequiresExistingAutomationAuthorization(t *testing.T) {
	cfg := platformconfig.Runtime{
		AutomationOperations: platformconfig.AutomationOperations{ProviderMode: platformconfig.AutomationProviderLimited, ProviderPermission: "fixed-script-send-authorized", MaxRecipientsPerRun: 1},
		Effects:              platformconfig.Effects{ProviderEnabled: true},
		WeCom:                platformconfig.WeCom{Enabled: true, Secret: "configured", ContextSigningKey: "configured", ContactSecret: "configured", ContextTokenTTL: time.Minute, MessageArchivePageLimit: 1, MessageArchivePageBudget: 1},
		WorkerLimit:          1,
	}
	if err := validateAppliedRuntimeConfig(cfg); err != nil {
		t.Fatalf("validate configured automation: %v", err)
	}
	cfg.AutomationOperations.ProviderPermission = ""
	if err := validateAppliedRuntimeConfig(cfg); err == nil {
		t.Fatal("expected missing automation authorization rejection")
	}
}

// Payment merchant identity and the shop application are bound to existing
// protected credentials. A blank initial deployment may be completed through
// the same release path, while merchant certificate serials remain rotatable.
func TestApplyRuntimeConfigAllowsInitialBindingAndMerchantCertificateRotation(t *testing.T) {
	cfg := platformconfig.Runtime{
		AutomationOperations: platformconfig.AutomationOperations{ProviderMode: platformconfig.AutomationProviderDisabled, MaxRecipientsPerRun: 1},
		WeChatPay:            platformconfig.WeChatPay{MerchantID: "", MerchantSerial: "serial-old"},
		WeChatShop:           platformconfig.WeChatShop{AppID: ""},
		WorkerLimit:          1,
		WeCom:                platformconfig.WeCom{ContextTokenTTL: time.Minute, MessageArchivePageLimit: 1, MessageArchivePageBudget: 1},
	}
	applied, err := applyRuntimeConfig(cfg, configport.EffectiveSnapshot{Settings: []configport.RuntimeSetting{
		runtimeApplySetting(t, configport.WeChatPayMerchantID, "mch-first"),
		runtimeApplySetting(t, configport.WeChatPayMerchantSerial, "serial-rotated"),
		runtimeApplySetting(t, configport.WeChatShopAppID, "shop-first"),
	}})
	if err != nil {
		t.Fatalf("apply initial integration bindings: %v", err)
	}
	if applied.WeChatPay.MerchantID != "mch-first" || applied.WeChatPay.MerchantSerial != "serial-rotated" || applied.WeChatShop.AppID != "shop-first" {
		t.Fatalf("initial binding or serial rotation was not applied: %#v", applied)
	}
}
