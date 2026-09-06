package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
)

type settingDefinition struct {
	secret   bool
	validate func(json.RawMessage) (json.RawMessage, bool)
}

var settingRegistry = map[configport.Key]settingDefinition{
	configport.WeComCorpID:                  {validate: validateNonEmptyString},
	configport.WeComAgentID:                 {validate: validatePositiveInteger},
	configport.OutboundRatePerSecond:        {validate: validateIntegerRange(1, 50)},
	configport.OutboundMaxAttempts:          {validate: validateIntegerRange(1, 10)},
	configport.AdminAppSettingsEnabled:      {validate: validateBoolean},
	configport.AdminPushCapabilitiesEnabled: {validate: validateBoolean},
	configport.AdminReleasesEnabled:         {validate: validateBoolean},
	configport.AdminDiagnosticsEnabled:      {validate: validateBoolean},
	configport.DatabaseURL:                  {secret: true},
	configport.WeComSecret:                  {secret: true},
	configport.WeComCallbackToken:           {secret: true},
	configport.WeComCallbackAESKey:          {secret: true},
	configport.AIAPIKey:                     {secret: true},
	configport.AuthJWTSecret:                {secret: true},
	configport.ExtensionAPIKeyPepper:        {secret: true},
	configport.WebhookMasterKey:             {secret: true},
}

func ValidateSetting(key configport.Key, value json.RawMessage) (json.RawMessage, error) {
	definition, ok := settingRegistry[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", configport.ErrUnknownSetting, key)
	}
	if definition.secret {
		return nil, fmt.Errorf("%w: %s", configport.ErrSecretSetting, key)
	}
	canonical, valid := definition.validate(value)
	if !valid {
		return nil, fmt.Errorf("%w: %s", configport.ErrInvalidSetting, key)
	}
	return canonical, nil
}

func ValidateReadableSetting(key configport.Key) error {
	definition, ok := settingRegistry[key]
	if !ok {
		return fmt.Errorf("%w: %s", configport.ErrUnknownSetting, key)
	}
	if definition.secret {
		return fmt.Errorf("%w: %s", configport.ErrSecretSetting, key)
	}
	return nil
}

func validateNonEmptyString(value json.RawMessage) (json.RawMessage, bool) {
	var decoded string
	if !decodeOne(value, &decoded) || strings.TrimSpace(decoded) != decoded || decoded == "" || len(decoded) > 256 {
		return nil, false
	}
	canonical, _ := json.Marshal(decoded)
	return canonical, true
}

func validatePositiveInteger(value json.RawMessage) (json.RawMessage, bool) {
	return validateIntegerRange(1, 1<<63-1)(value)
}

func validateBoolean(value json.RawMessage) (json.RawMessage, bool) {
	var decoded bool
	if !decodeOne(value, &decoded) {
		return nil, false
	}
	canonical, _ := json.Marshal(decoded)
	return canonical, true
}

func validateIntegerRange(minimum, maximum int64) func(json.RawMessage) (json.RawMessage, bool) {
	return func(value json.RawMessage) (json.RawMessage, bool) {
		var decoded int64
		if !decodeOne(value, &decoded) || decoded < minimum || decoded > maximum {
			return nil, false
		}
		canonical, _ := json.Marshal(decoded)
		return canonical, true
	}
}

func decodeOne(value json.RawMessage, target any) bool {
	decoder := json.NewDecoder(bytes.NewReader(value))
	if err := decoder.Decode(target); err != nil {
		return false
	}
	var trailing any
	return decoder.Decode(&trailing) == io.EOF
}

// ValidateRuntimeSetting keeps publishable business keys separate from local
// app-settings and deployment environment inputs. The automation limit is also
// bounded by the downstream AI plan hard cap.
func ValidateRuntimeSetting(key configport.RuntimeSettingKey, value json.RawMessage) (json.RawMessage, error) {
	switch key {
	case configport.AutomationOperationsMaxRecipientsPerRun:
		canonical, valid := validateIntegerRange(1, 5000)(value)
		if !valid {
			return nil, fmt.Errorf("%w: %s", configport.ErrInvalidSetting, key)
		}
		return canonical, nil
	default:
		return nil, fmt.Errorf("%w: %s", configport.ErrUnknownSetting, key)
	}
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
	if len(out) != 1 || len(issues) != 0 || out[0].Key != configport.AutomationOperationsMaxRecipientsPerRun {
		if len(issues) == 0 {
			issues = append(issues, configport.RuntimeValidationIssue{Key: configport.AutomationOperationsMaxRecipientsPerRun, Error: "exactly one managed runtime setting is required"})
		}
	}
	return out, issues
}
