package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
)

// RuntimeField is the public, non-secret schema used by the Config host.  A
// secret is represented only as the closed protected deployment reference
// named by SecretReference; Config never accepts or returns secret material.
type RuntimeField struct {
	Key             configport.RuntimeSettingKey `json:"key"`
	Label           string                       `json:"label"`
	Group           string                       `json:"group"`
	Input           string                       `json:"input"`
	Application     string                       `json:"application"`
	RequiredRoles   []string                     `json:"required_roles,omitempty"`
	SecretReference string                       `json:"secret_reference,omitempty"`
	Configured      *bool                        `json:"configured,omitempty"`
	Unsupported     string                       `json:"unsupported,omitempty"`
}

type RuntimeCategory struct {
	Key        string         `json:"key"`
	Label      string         `json:"label"`
	Group      string         `json:"group"`
	Fields     []RuntimeField `json:"fields"`
	ManagedURL string         `json:"managed_url,omitempty"`
	Disabled   string         `json:"disabled,omitempty"`
}

type runtimeDefinition struct {
	input    string
	validate func(json.RawMessage) (json.RawMessage, bool)
}

func RuntimeCatalog(statuses ...map[string]bool) []RuntimeCategory {
	presence := map[string]bool{}
	if len(statuses) == 1 {
		for reference, configured := range statuses[0] {
			presence[reference] = configured
		}
	}
	// Keep the category order, labels, blocks and operation vocabulary aligned
	// with dd8d60d's Config Center.  Fields without a safe V3 consumer remain
	// visible with their reason instead of turning into a misleading switch.
	categories := []RuntimeCategory{
		{Key: "wecom_base", Label: "企业微信基础", Group: "核心连接能力", Fields: []RuntimeField{
			field(configport.WeComEnabled, "启用企业微信", "基础信息", "boolean", "restart"),
			scopeBound(configport.RuntimeWeComCorpID, "CorpID（身份作用域）", "基础信息", "restart", "已绑定 CorpID 只能通过企业身份迁移流程变更；未配置时可按部署契约补齐。"),
			field(configport.RuntimeWeComAgentID, "AgentID", "基础信息", "text", "restart"),
			field(configport.WeComCallbackEnabled, "启用回调", "回调", "boolean", "restart"),
			field(configport.WeComCustomerSyncEnabled, "启用客户同步", "接口", "boolean", "restart"),
			field(configport.MessageArchiveEnabled, "启用会话存档", "会话存档", "boolean", "restart"),
			field(configport.MessageArchivePageLimit, "会话存档分页上限", "会话存档", "number", "restart"),
			field(configport.MessageArchivePageBudget, "会话存档分页预算", "会话存档", "number", "restart"),
			secret("WECOM_SECRET", "企业微信 Secret", "密钥", "environment://AICRM_WECOM_SECRET"),
			secret("WECOM_CONTACT_SECRET", "通讯录 Secret", "密钥", "environment://AICRM_WECOM_CONTACT_SECRET"),
			secret("WECOM_CALLBACK_TOKEN", "回调 Token", "回调", "environment://AICRM_WECOM_CALLBACK_TOKEN"),
			secret("WECOM_CALLBACK_AES_KEY", "回调 EncodingAESKey", "回调", "environment://AICRM_WECOM_CALLBACK_AES_KEY"),
			secret("WECOM_ARCHIVE_SECRET", "会话存档 Secret", "会话存档", "environment://AICRM_WECOM_MESSAGE_ARCHIVE_SECRET"),
		}},
		{Key: "admin_access", Label: "后台访问", Group: "后台安全", ManagedURL: "/admin/admin-access", Fields: []RuntimeField{}},
		{Key: "sidebar_identity", Label: "侧边栏与身份", Group: "后台安全", Fields: []RuntimeField{
			field(configport.SidebarContextTokenTTLSeconds, "侧边栏 Context Token 有效期（秒）", "基础信息", "number", "restart"),
			secret("AICRM_SIDEBAR_JSSDK_SECRET", "企微 JSSDK 密钥", "企微 JSSDK", "environment://AICRM_WECOM_SECRET"),
		}},
		{Key: "ai_automation", Label: "AI 与自动化", Group: "自动化能力", Fields: []RuntimeField{
			field(configport.AutomationOperationsProviderMode, "自动化运行模式", "自动化", "select:disabled,probe,limited", "restart"),
			field(configport.AutomationOperationsMaxRecipientsPerRun, "单次运行最大人数", "自动化", "number", "immediate"),
			field(configport.AIAssistantUIEnabled, "启用 AI 助手界面", "AI 助手", "boolean", "restart"),
			protected(configport.AIAssistantIntakeEnabled, "旧 signed intake（部署受控）", "AI 助手", "restart", "V1 caller/OAuth2 创建待审计划不受此字段影响；旧专用签名入口保持 deployment-disabled。"),
			field(configport.AIAssistantDispatchEnabled, "启用 AI 助手发送意图", "AI 助手", "boolean", "restart"),
			secret("AICRM_AUTOMATION_OPS_WEBHOOK_SECRET", "自动化 Webhook 密钥", "自动化", "environment://AICRM_AUTOMATION_OPS_WEBHOOK_SECRET"),
			secret("AICRM_AUTOMATION_OPS_PROVIDER_PERMISSION", "发送授权确认（只读）", "自动化", "environment://AICRM_AUTOMATION_OPS_PROVIDER_PERMISSION"),
		}},
		{Key: "open_api_key", Label: "CRM 开放 API Key", Group: "外部联通能力", ManagedURL: "/admin/api-docs", Fields: []RuntimeField{
			unsupported("Direct API Key", "V3 只支持 V1 caller/OAuth2 管理；Direct API Key 和旧 56 条机器接口已退休。"),
		}},
		{Key: "api_token", Label: "API 接入与 Token", Group: "外部联通能力", ManagedURL: "/admin/api-docs", Fields: []RuntimeField{
			unsupported("API client / token", "调用方、scope 和令牌签发由 Open Platform/Access Owner 管理，Config 不保存凭据。"),
		}},
		{Key: "webhooks_push", Label: "Webhook 与外推", Group: "外部联通能力", Fields: []RuntimeField{
			field(configport.EffectsProviderEnabled, "启用 External Effects Provider", "统一队列", "boolean", "restart"),
			field(configport.SurveyCompletionProviderEnabled, "启用问卷完成外推", "问卷提交", "boolean", "restart"),
			field(configport.CommercePushProviderEnabled, "启用商品/订单外推", "统一队列", "boolean", "restart"),
			field(configport.GroupOpsDirectoryReadEnabled, "允许群目录 Provider 读取", "群运营", "boolean", "restart"),
			field(configport.GroupOpsDispatchEnabled, "允许群运营提交执行意图", "群运营", "boolean", "restart"),
		}},
		{Key: "reliability", Label: "稳定性", Group: "平台治理", Fields: []RuntimeField{
			field(configport.WorkerLimit, "Inbox 单次 claim 处理条数", "基础设施", "number", "restart"),
		}},
		{Key: "wechat_pay", Label: "微信支付", Group: "外部联通能力", Fields: []RuntimeField{
			field(configport.WeChatPayProviderEnabled, "启用微信支付 Provider", "基础信息", "boolean", "restart"),
			field(configport.WeChatPayAppID, "AppID", "基础信息", "text", "restart"),
			protected(configport.WeChatPayAppScope, "App Scope（受控）", "基础信息", "restart", "支付 App scope 绑定身份边界，只能由受控部署变更。"),
			field(configport.WeChatPayH5OAuthEnabled, "启用 H5 OAuth", "公众号授权", "boolean", "restart"),
			field(configport.WeChatPayH5AppID, "H5 AppID", "公众号授权", "text", "restart"),
			protected(configport.WeChatPayH5AppScope, "H5 App Scope（受控）", "公众号授权", "restart", "H5 scope 绑定身份边界，只能由受控部署变更。"),
			field(configport.WeChatPayMerchantID, "商户号", "商户", "text", "restart"),
			field(configport.WeChatPayMerchantSerial, "商户证书序列号", "商户", "text", "restart"),
			secret("WECHAT_PAY_API_V3_KEY", "API v3 Key", "密钥", "environment://AICRM_WECHAT_PAY_API_V3_KEY"),
			secret("WECHAT_PAY_PRIVATE_KEY_PATH", "商户私钥", "密钥", "environment://AICRM_WECHAT_PAY_PRIVATE_KEY_PATH"),
			secret("WECHAT_PAY_PLATFORM_CERT_PATH", "平台证书", "密钥", "environment://AICRM_WECHAT_PAY_PLATFORM_CERT_PATH"),
		}},
		{Key: "alipay", Label: "支付宝支付", Group: "外部联通能力", Disabled: "V3 未注册支付宝 Provider、路由或支付 Owner。", Fields: []RuntimeField{}},
		{Key: "wechat_shop", Label: "微信小店", Group: "外部联通能力", Fields: []RuntimeField{
			field(configport.WeChatShopProviderEnabled, "启用微信小店 Provider", "基础信息", "boolean", "restart"),
			field(configport.WeChatShopAppID, "AppID", "基础信息", "text", "restart"),
			secret("WECHAT_SHOP_APP_SECRET", "AppSecret", "密钥", "environment://AICRM_WECHAT_SHOP_APP_SECRET"),
			secret("WECHAT_SHOP_CALLBACK_TOKEN", "Callback Token", "回调", "environment://AICRM_WECHAT_SHOP_CALLBACK_TOKEN"),
			secret("WECHAT_SHOP_CALLBACK_AES_KEY", "Callback EncodingAESKey", "回调", "environment://AICRM_WECHAT_SHOP_CALLBACK_AES_KEY"),
		}},
		{Key: "wechat_oauth", Label: "公众号授权", Group: "外部联通能力", Fields: []RuntimeField{
			field(configport.SurveyOAuthEnabled, "启用公众号 OAuth", "授权", "boolean", "restart"),
			field(configport.SurveyOAuthAppID, "AppID", "授权", "text", "restart"),
			scopeBound(configport.SurveyOAuthOpenPlatformID, "开放平台 AppID（身份作用域）", "授权", "restart", "已绑定开放平台作用域只能通过身份迁移流程变更；未配置时可按部署契约补齐。"),
			protected(configport.SurveyOAuthScope, "OAuth Scope（受控）", "授权", "restart", "OAuth scope 绑定 Open Platform 身份边界，只能由受控部署变更。"),
			secret("WECHAT_MP_APP_SECRET", "AppSecret", "密钥", "environment://AICRM_SURVEY_OAUTH_SECRET"),
		}},
	}
	for categoryIndex := range categories {
		categories[categoryIndex].Fields = append(categories[categoryIndex].Fields, legacyCatalogFields(categories[categoryIndex].Key)...)
		for fieldIndex := range categories[categoryIndex].Fields {
			field := &categories[categoryIndex].Fields[fieldIndex]
			if field.SecretReference != "" {
				configured := presence[field.SecretReference]
				field.Configured = &configured
			}
		}
	}
	return categories
}

func field(key configport.RuntimeSettingKey, label, group, input, application string) RuntimeField {
	roles := []string{"api", "worker", "effects-worker"}
	if application == "immediate" {
		roles = []string{"api", "worker"}
	}
	return RuntimeField{Key: key, Label: label, Group: group, Input: input, Application: application, RequiredRoles: roles}
}

func protected(key configport.RuntimeSettingKey, label, group, application, reason string) RuntimeField {
	roles := []string{"api", "worker", "effects-worker"}
	return RuntimeField{Key: key, Label: label, Group: group, Input: "protected", Application: application, RequiredRoles: roles, Unsupported: reason}
}

func scopeBound(key configport.RuntimeSettingKey, label, group, application, reason string) RuntimeField {
	roles := []string{"api", "worker", "effects-worker"}
	return RuntimeField{Key: key, Label: label, Group: group, Input: "scope-bound", Application: application, RequiredRoles: roles, Unsupported: reason}
}

func secret(key, label, group, reference string) RuntimeField {
	return RuntimeField{Key: configport.RuntimeSettingKey(key), Label: label, Group: group, Input: "secret-reference", Application: "restart", SecretReference: reference}
}

func unsupported(key, reason string) RuntimeField {
	return RuntimeField{Key: configport.RuntimeSettingKey(key), Label: key, Group: "旧版字段", Input: "unsupported", Unsupported: reason}
}

func runtimeDefinitions() map[configport.RuntimeSettingKey]runtimeDefinition {
	boolValue := runtimeBoolean
	text := runtimeText
	return map[configport.RuntimeSettingKey]runtimeDefinition{
		configport.AutomationOperationsMaxRecipientsPerRun: {input: "number", validate: runtimeInteger(1, 5000)},
		configport.AutomationOperationsProviderMode:        {input: "select", validate: runtimeChoice("disabled", "probe", "limited")},
		configport.AIAssistantUIEnabled:                    {input: "boolean", validate: boolValue},
		configport.AIAssistantIntakeEnabled:                {input: "boolean", validate: boolValue},
		configport.AIAssistantDispatchEnabled:              {input: "boolean", validate: boolValue},
		configport.WeComEnabled:                            {input: "boolean", validate: boolValue},
		configport.RuntimeWeComCorpID:                      {input: "text", validate: text},
		configport.RuntimeWeComAgentID:                     {input: "text", validate: text},
		configport.WeComCallbackEnabled:                    {input: "boolean", validate: boolValue},
		configport.WeComCustomerSyncEnabled:                {input: "boolean", validate: boolValue},
		configport.MessageArchiveEnabled:                   {input: "boolean", validate: boolValue},
		configport.MessageArchivePageLimit:                 {input: "number", validate: runtimeInteger(1, 1000)},
		configport.MessageArchivePageBudget:                {input: "number", validate: runtimeInteger(1, 1000)},
		configport.SidebarContextTokenTTLSeconds:           {input: "number", validate: runtimeInteger(60, 86400)},
		configport.GroupOpsDirectoryReadEnabled:            {input: "boolean", validate: boolValue},
		configport.GroupOpsDispatchEnabled:                 {input: "boolean", validate: boolValue},
		configport.EffectsProviderEnabled:                  {input: "boolean", validate: boolValue},
		configport.SurveyCompletionProviderEnabled:         {input: "boolean", validate: boolValue},
		configport.CommercePushProviderEnabled:             {input: "boolean", validate: boolValue},
		configport.WorkerLimit:                             {input: "number", validate: runtimeInteger(1, 100)},
		configport.WeChatPayProviderEnabled:                {input: "boolean", validate: boolValue},
		configport.WeChatPayAppID:                          {input: "text", validate: text},
		configport.WeChatPayAppScope:                       {input: "text", validate: text},
		configport.WeChatPayH5OAuthEnabled:                 {input: "boolean", validate: boolValue},
		configport.WeChatPayH5AppID:                        {input: "text", validate: text},
		configport.WeChatPayH5AppScope:                     {input: "text", validate: text},
		configport.WeChatPayMerchantID:                     {input: "text", validate: text},
		configport.WeChatPayMerchantSerial:                 {input: "text", validate: text},
		configport.WeChatShopProviderEnabled:               {input: "boolean", validate: boolValue},
		configport.WeChatShopAppID:                         {input: "text", validate: text},
		configport.SurveyOAuthEnabled:                      {input: "boolean", validate: boolValue},
		configport.SurveyOAuthAppID:                        {input: "text", validate: text},
		configport.SurveyOAuthOpenPlatformID:               {input: "text", validate: text},
		configport.SurveyOAuthScope:                        {input: "text", validate: text},
	}
}

func ValidateRuntimeSetting(key configport.RuntimeSettingKey, value json.RawMessage) (json.RawMessage, error) {
	definition, ok := runtimeDefinitions()[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", configport.ErrUnknownSetting, key)
	}
	canonical, valid := definition.validate(value)
	if !valid {
		return nil, fmt.Errorf("%w: %s", configport.ErrInvalidSetting, key)
	}
	return canonical, nil
}

func ValidateRuntimeSettings(settings []configport.RuntimeSetting) ([]configport.RuntimeSetting, []configport.RuntimeValidationIssue) {
	seen := map[configport.RuntimeSettingKey]struct{}{}
	out := make([]configport.RuntimeSetting, 0, len(settings))
	issues := make([]configport.RuntimeValidationIssue, 0)
	for _, setting := range settings {
		if _, exists := seen[setting.Key]; exists {
			issues = append(issues, configport.RuntimeValidationIssue{Key: setting.Key, Error: "duplicate runtime setting"})
			continue
		}
		seen[setting.Key] = struct{}{}
		canonical, err := ValidateRuntimeSetting(setting.Key, setting.Value)
		if err != nil {
			issues = append(issues, configport.RuntimeValidationIssue{Key: setting.Key, Error: "invalid or unmanaged runtime setting"})
			continue
		}
		out = append(out, configport.RuntimeSetting{Key: setting.Key, Value: canonical})
	}
	if len(out) == 0 && len(issues) == 0 {
		issues = append(issues, configport.RuntimeValidationIssue{Error: "at least one managed runtime setting is required"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, issues
}

// ValidateRuntimeDependencies checks relationships between individually valid values.
// It is run against the fully merged effective snapshot before a release can
// become publishable, so toggles cannot claim a usable provider with required
// collaborators still disabled.
func ValidateRuntimeDependencies(settings []configport.RuntimeSetting) []configport.RuntimeValidationIssue {
	values := make(map[configport.RuntimeSettingKey]json.RawMessage, len(settings))
	for _, setting := range settings {
		values[setting.Key] = setting.Value
	}
	boolAt := func(key configport.RuntimeSettingKey) bool {
		var value bool
		return json.Unmarshal(values[key], &value) == nil && value
	}
	textAt := func(key configport.RuntimeSettingKey) string {
		var value string
		_ = json.Unmarshal(values[key], &value)
		return strings.TrimSpace(value)
	}
	issues := make([]configport.RuntimeValidationIssue, 0)
	require := func(enabled bool, key configport.RuntimeSettingKey, ok bool, message string) {
		if enabled && !ok {
			issues = append(issues, configport.RuntimeValidationIssue{Key: key, Error: message})
		}
	}
	providerReady := boolAt(configport.EffectsProviderEnabled) && boolAt(configport.WeComEnabled)
	require(boolAt(configport.GroupOpsDispatchEnabled), configport.GroupOpsDispatchEnabled, providerReady, "requires enabled External Effects and Enterprise WeChat")
	require(boolAt(configport.SurveyCompletionProviderEnabled), configport.SurveyCompletionProviderEnabled, boolAt(configport.EffectsProviderEnabled), "requires enabled External Effects")
	require(boolAt(configport.CommercePushProviderEnabled), configport.CommercePushProviderEnabled, boolAt(configport.EffectsProviderEnabled), "requires enabled External Effects")
	require(boolAt(configport.MessageArchiveEnabled), configport.MessageArchiveEnabled, boolAt(configport.WeComEnabled), "requires enabled Enterprise WeChat")
	var automationMode string
	_ = json.Unmarshal(values[configport.AutomationOperationsProviderMode], &automationMode)
	require(automationMode != "" && automationMode != "disabled", configport.AutomationOperationsProviderMode, providerReady, "requires enabled External Effects and Enterprise WeChat")
	payReady := textAt(configport.WeChatPayAppID) != "" && textAt(configport.WeChatPayMerchantID) != "" && textAt(configport.WeChatPayMerchantSerial) != ""
	require(boolAt(configport.WeChatPayProviderEnabled), configport.WeChatPayProviderEnabled, payReady, "requires AppID, merchant ID, and merchant certificate serial")
	require(boolAt(configport.WeChatPayH5OAuthEnabled), configport.WeChatPayH5OAuthEnabled, textAt(configport.WeChatPayH5AppID) != "" && textAt(configport.WeChatPayH5AppScope) != "", "requires H5 AppID and App Scope")
	require(boolAt(configport.WeChatShopProviderEnabled), configport.WeChatShopProviderEnabled, textAt(configport.WeChatShopAppID) != "", "requires AppID")
	oauthReady := textAt(configport.SurveyOAuthAppID) != "" && textAt(configport.SurveyOAuthOpenPlatformID) != "" && textAt(configport.SurveyOAuthScope) != ""
	require(boolAt(configport.SurveyOAuthEnabled), configport.SurveyOAuthEnabled, oauthReady, "requires AppID, Open Platform AppID, and OAuth Scope")
	return issues
}

func runtimeBoolean(value json.RawMessage) (json.RawMessage, bool) {
	var decoded bool
	if !decodeRuntimeOne(value, &decoded) {
		return nil, false
	}
	canonical, _ := json.Marshal(decoded)
	return canonical, true
}

func runtimeInteger(minimum, maximum int64) func(json.RawMessage) (json.RawMessage, bool) {
	return func(value json.RawMessage) (json.RawMessage, bool) {
		var decoded int64
		if !decodeRuntimeOne(value, &decoded) || decoded < minimum || decoded > maximum {
			return nil, false
		}
		canonical, _ := json.Marshal(decoded)
		return canonical, true
	}
}

func runtimeChoice(values ...string) func(json.RawMessage) (json.RawMessage, bool) {
	return func(value json.RawMessage) (json.RawMessage, bool) {
		var decoded string
		if !decodeRuntimeOne(value, &decoded) {
			return nil, false
		}
		for _, allowed := range values {
			if decoded == allowed {
				canonical, _ := json.Marshal(decoded)
				return canonical, true
			}
		}
		return nil, false
	}
}

func runtimeText(value json.RawMessage) (json.RawMessage, bool) {
	var decoded string
	if !decodeRuntimeOne(value, &decoded) || strings.TrimSpace(decoded) != decoded || len(decoded) > 256 {
		return nil, false
	}
	canonical, _ := json.Marshal(decoded)
	return canonical, true
}

func decodeRuntimeOne(value json.RawMessage, target any) bool {
	decoder := json.NewDecoder(bytes.NewReader(value))
	if err := decoder.Decode(target); err != nil {
		return false
	}
	var trailing any
	return decoder.Decode(&trailing) == io.EOF
}

// legacyCatalogFields intentionally returns one visible field for every donor
// setting that has no Config-owned editable V3 equivalent. Deployment-managed
// and retired rows are separate: the host never turns either into a switch.
func legacyCatalogFields(category string) []RuntimeField {
	deployment := func(group, owner string, keys ...string) []RuntimeField {
		out := make([]RuntimeField, 0, len(keys))
		for _, key := range keys {
			out = append(out, RuntimeField{Key: configport.RuntimeSettingKey(key), Label: key, Group: group, Input: "deployment", Unsupported: owner})
		}
		return out
	}
	retired := func(group, reason string, keys ...string) []RuntimeField {
		out := make([]RuntimeField, 0, len(keys))
		for _, key := range keys {
			out = append(out, RuntimeField{Key: configport.RuntimeSettingKey(key), Label: key, Group: group, Input: "unsupported", Unsupported: reason})
		}
		return out
	}
	combine := func(parts ...[]RuntimeField) []RuntimeField {
		var out []RuntimeField
		for _, part := range parts {
			out = append(out, part...)
		}
		return out
	}
	switch category {
	case "wecom_base":
		return combine(
			deployment("接口", "由 WeCom Provider 的受保护部署清单管理；V3 固定 Provider origin。", "WECOM_API_BASE"),
			retired("接口", "旧默认负责人没有 V3 安全 consumer，已退休。", "WECOM_DEFAULT_OWNER_USERID"),
			deployment("会话存档", "由 Archive Provider 的受保护部署清单管理；只报告引用 presence。", "WECOM_PRIVATE_KEY_PATH", "WECOM_SDK_LIB_PATH", "WECOM_ARCHIVE_TIMEOUT"),
			retired("限制", "旧企业标签上限没有 V3 Config consumer，已退休。", "WECOM_CORP_TAG_LIMIT"))
	case "admin_access":
		return deployment("后台安全", "由 Access Owner 的登录、会话和受信任域部署配置管理。", "ADMIN_AUTH_MODE", "ADMIN_LOGIN_REDIRECT_URI", "ADMIN_WECHAT_TRUSTED_DOMAIN")
	case "sidebar_identity":
		return combine(
			retired("基础信息", "产品 Context Token 旧路径没有 V3 consumer，已退休。", "SIDEBAR_PRODUCT_CONTEXT_TOKEN_TTL_SECONDS"),
			deployment("企微 JSSDK", "由 WeCom Provider 受保护部署配置管理。", "AICRM_SIDEBAR_JSSDK_ADAPTER_MODE", "AICRM_SIDEBAR_JSSDK_REAL_ENABLED", "AICRM_SIDEBAR_JSSDK_TIMEOUT_SECONDS"),
			retired("图片素材", "旧快捷关键词没有 V3 consumer，已退休。", "AICRM_SIDEBAR_IMAGE_QUICK_KEYWORDS"))
	case "ai_automation":
		return combine(
			retired("AI", "DeepSeek 旧 Provider 未注册到 V3，已退休。", "DEEPSEEK_ENABLED", "DEEPSEEK_API_KEY", "DEEPSEEK_BASE_URL", "DEEPSEEK_ROUTER_MODEL", "DEEPSEEK_EXECUTION_MODEL", "DEEPSEEK_REASONER_MODEL", "DEEPSEEK_TIMEOUT_SECONDS"),
			deployment("统一授权平台", "由 Access/Open Platform 受保护部署配置管理。", "AICRM_AUTH_ISSUER", "AICRM_AUTH_SESSION_HASH_PEPPER", "AICRM_AUTH_JWT_SIGNING_KEY", "AICRM_AUTH_TRUSTED_PROXY_ADDRESSES", "AICRM_AUTH_CA_FILE"),
			retired("机器身份", "旧机器 client 配置由 V1 caller/OAuth2 取代，旧入口已退休。", "AICRM_AUTH_AUTOMATION_WORKER_CLIENT_ID", "AICRM_AUTH_AUTOMATION_WORKER_CLIENT_SECRET_REF", "AICRM_AUTH_ARCHIVE_WORKER_CLIENT_ID", "AICRM_AUTH_ARCHIVE_WORKER_CLIENT_SECRET_REF", "AICRM_AUTH_CALLBACK_WORKER_CLIENT_ID", "AICRM_AUTH_CALLBACK_WORKER_CLIENT_SECRET_REF", "AICRM_AUTH_GROUP_BROADCAST_CLIENT_ID", "AICRM_AUTH_GROUP_BROADCAST_CLIENT_SECRET_REF", "AICRM_AUTH_IDENTITY_CLIENT_ID", "AICRM_AUTH_IDENTITY_CLIENT_SECRET_REF", "AICRM_AUTH_MCP_CLIENT_ID", "AICRM_AUTH_MCP_CLIENT_SECRET_REF", "AICRM_AUTH_EXTERNAL_AGENT_CLIENT_ID", "AICRM_AUTH_EXTERNAL_AGENT_CLIENT_SECRET_REF", "AICRM_AUTH_CAMPAIGN_AGENT_CLIENT_ID", "AICRM_AUTH_CAMPAIGN_AGENT_CLIENT_SECRET_REF", "AICRM_AUTH_OPS_REPORTER_CLIENT_ID", "AICRM_AUTH_OPS_REPORTER_CLIENT_SECRET_REF", "AICRM_AUTH_OPERATION_RUNNER_CLIENT_ID", "AICRM_AUTH_OPERATION_RUNNER_CLIENT_SECRET_REF", "AICRM_AUTH_OUTBOUND_WEBHOOK_CLIENT_ID"))
	case "webhooks_push":
		return combine(
			retired("Webhook", "OpenClaw 旧 URL 写入通道未迁入 V3，已退休。", "OPENCLAW_WEBHOOK_URL", "OPENCLAW_FOCUS_MESSAGE_WEBHOOK_TIMEOUT_SECONDS"),
			deployment("问卷提交", "问卷 Completion Target 由 Survey Owner 的受保护部署清单管理。", "QUESTIONNAIRE_SUBMIT_WEBHOOK_URL", "QUESTIONNAIRE_SUBMIT_WEBHOOK_TIMEOUT_SECONDS", "QUESTIONNAIRE_EXTERNAL_PUSH_TIMEOUT_SECONDS"),
			retired("问卷外推", "旧全局推送开关由受控 Survey Completion Provider 取代。", "QUESTIONNAIRE_EXTERNAL_PUSH_GLOBAL_ENABLED"),
			deployment("统一队列", "由 External Effects Owner 的受保护部署策略管理。", "AICRM_EXTERNAL_EFFECT_ALLOWED_BASE_HOSTS", "AICRM_EXTERNAL_EFFECT_TEST_EXECUTION_ONLY", "AICRM_EXTERNAL_EFFECT_ALLOWED_TYPES", "AICRM_EXTERNAL_EFFECT_REALTIME_ENABLED", "AICRM_EXTERNAL_EFFECT_REALTIME_ALLOWED_TYPES", "AICRM_EXTERNAL_EFFECT_REALTIME_MAX_CONCURRENCY", "AICRM_EXTERNAL_EFFECT_WEBHOOK_TIMEOUT_SECONDS"),
			retired("Webhook 执行", "旧 Webhook 任意执行开关没有 V3 Config consumer，已退休。", "AICRM_EXTERNAL_EFFECT_WEBHOOK_EXECUTE"),
			deployment("企微执行", "由 Outbound/WeCom Owner 的受保护部署 allowlist 管理。", "AICRM_EXTERNAL_EFFECT_WECOM_EXECUTE", "AICRM_WECOM_EXECUTION_MODE", "AICRM_WECOM_ENABLED_EFFECT_TYPES", "AICRM_WECOM_DEFAULT_SENDER_USERID", "AICRM_EXTERNAL_EFFECT_ALLOWED_OWNER_USERIDS", "AICRM_EXTERNAL_EFFECT_ALLOWED_TARGET_EXTERNAL_USERIDS", "AICRM_EXTERNAL_EFFECT_ALLOWED_GROUP_OPS_WEBHOOK_KEYS", "AICRM_EXTERNAL_EFFECT_ALLOWED_GROUP_CHAT_IDS", "AICRM_WECOM_PRIVATE_ADAPTER_MODE", "AICRM_ENABLE_REAL_WECOM_PRIVATE_MESSAGE", "AICRM_WECOM_GROUP_ADAPTER_MODE", "AICRM_ENABLE_REAL_WECOM_GROUP_MESSAGE"),
			retired("预留执行开关", "旧预留写开关没有 V3 Config consumer，已退休。", "AICRM_EXTERNAL_EFFECT_PAYMENT_EXECUTE", "AICRM_EXTERNAL_EFFECT_FEISHU_EXECUTE", "AICRM_EXTERNAL_EFFECT_OPENCLAW_EXECUTE", "AICRM_EXTERNAL_EFFECT_MEDIA_UPLOAD_EXECUTE"),
			deployment("重试", "由 External Effects/Outbox 可靠性内核管理，页面不能新建第二套重试。", "OUTBOUND_WEBHOOK_RETRY_ENABLED", "OUTBOUND_WEBHOOK_RETRY_MAX_ATTEMPTS", "OUTBOUND_WEBHOOK_RETRY_INTERVAL_SECONDS"))
	case "reliability":
		return combine(
			deployment("HTTP", "由各 Provider Owner 的受保护部署超时和重试策略管理。", "HTTP_DEFAULT_TIMEOUT", "HTTP_RETRY_MAX", "HTTP_RETRY_BACKOFF_BASE", "CIRCUIT_FAILURE_THRESHOLD", "CIRCUIT_RECOVERY_SECONDS"),
			deployment("任务", "由 River/Outbox 可靠性内核管理。", "RQ_DEFAULT_TIMEOUT", "OUTBOX_MAX_ATTEMPTS", "OUTBOX_BACKOFF_BASE_SECONDS"),
			retired("基础设施", "V3 不使用 Redis，REDIS_URL 已退休。", "REDIS_URL"))
	case "wechat_pay":
		return combine(
			deployment("接口", "Payment Owner 固定回调路由和 Provider origin；由受保护部署管理。", "WECHAT_PAY_NOTIFY_URL", "WECHAT_PAY_API_BASE", "WECHAT_PAY_TIMEOUT_SECONDS"),
			retired("商品", "旧 JSON 商品目录已由 Product Owner 管理，已退休。", "WECHAT_PAY_PRODUCT_CATALOG_JSON"))
	case "alipay":
		return retired("支付宝", "V3 未注册支付宝 Provider、路由或支付 Owner；不能保存、发布或显示已生效。", "ALIPAY_ENABLED", "ALIPAY_APP_ID", "ALIPAY_APP_PRIVATE_KEY_PATH", "ALIPAY_PUBLIC_KEY_PATH", "ALIPAY_SERVER_URL", "ALIPAY_NOTIFY_URL", "ALIPAY_RETURN_URL", "ALIPAY_SIGN_TYPE", "ALIPAY_TIMEOUT_EXPRESS")
	case "wechat_shop":
		return deployment("接口", "WeChat Shop Provider 使用固定协议；API base 和 timeout 由受保护部署管理。", "WECHAT_SHOP_API_BASE", "WECHAT_SHOP_HTTP_TIMEOUT_SECONDS")
	case "wechat_oauth":
		return nil
	default:
		return nil
	}
}

// ProtectedRuntimeSetting identifies values whose change can alter an identity
// scope or an external-write authorization. They are present in snapshots for
// truthful readback, but a Config release may only carry the deployment value.
func ProtectedRuntimeSetting(key configport.RuntimeSettingKey) bool {
	switch key {
	case configport.AIAssistantIntakeEnabled, configport.WeChatPayAppScope,
		configport.WeChatPayH5AppScope,
		configport.SurveyOAuthScope:
		return true
	default:
		return false
	}
}

// ScopeBoundRuntimeSetting is a one-way initial binding: a blank deployment
// value may be completed, but an existing identity scope cannot be changed by
// an ordinary configuration release.
func ScopeBoundRuntimeSetting(key configport.RuntimeSettingKey) bool {
	switch key {
	case configport.RuntimeWeComCorpID, configport.SurveyOAuthOpenPlatformID:
		return true
	default:
		return false
	}
}
