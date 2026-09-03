package app

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
)

var ErrUnsupportedDefinition = errors.New("audience definition capability is unavailable")

type Template struct {
	Key               string `json:"key"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

type DefinitionInput struct {
	SchemaVersion int                        `json:"schema_version"`
	TemplateKey   string                     `json:"template_key"`
	Parameters    map[string]json.RawMessage `json:"parameters"`
}

var templateParameters = map[string]string{
	"active_contacts": "within_days",
	"stage_any":       "stages",
	"tag_any":         "tag_codes",
	"owner_any":       "staff_ids",
	"channel_any":     "channels",
}

func Templates() []Template {
	return []Template{
		{Key: "active_contacts", Available: true},
		{Key: "stage_any", Available: false, UnavailableReason: "customer stage port is not available"},
		{Key: "tag_any", Available: false, UnavailableReason: "canonical tag membership port is not available"},
		{Key: "owner_any", Available: false, UnavailableReason: "internal staff ownership port is not available"},
		{Key: "channel_any", Available: false, UnavailableReason: "channel attribution membership port is not available"},
	}
}

func DefaultDefinition(templateKey string) (json.RawMessage, error) {
	parameter, ok := templateParameters[templateKey]
	if !ok {
		return nil, ErrUnsupportedDefinition
	}
	value := any([]string{"__configure__"})
	if templateKey == "active_contacts" {
		value = "30"
	}
	return canonicalDefinition(DefinitionInput{SchemaVersion: 1, TemplateKey: templateKey, Parameters: map[string]json.RawMessage{parameter: mustJSON(value)}})
}

func CanonicalDefinition(raw json.RawMessage) (json.RawMessage, error) {
	var input DefinitionInput
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		return nil, ErrUnsupportedDefinition
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, ErrUnsupportedDefinition
	}
	return canonicalDefinition(input)
}

func canonicalDefinition(input DefinitionInput) (json.RawMessage, error) {
	parameter, ok := templateParameters[input.TemplateKey]
	if !ok || input.SchemaVersion != 1 || len(input.Parameters) != 1 {
		return nil, ErrUnsupportedDefinition
	}
	raw, ok := input.Parameters[parameter]
	if !ok || !validParameter(input.TemplateKey, raw) {
		return nil, ErrUnsupportedDefinition
	}
	// encoding/json sorts map keys, yielding a stable digest input.
	canonical, err := json.Marshal(input)
	if err != nil || len(canonical) > 16*1024 {
		return nil, ErrUnsupportedDefinition
	}
	return canonical, nil
}

func validParameter(templateKey string, raw json.RawMessage) bool {
	if templateKey == "active_contacts" {
		var value string
		if json.Unmarshal(raw, &value) != nil || value == "" || len(value) > 3 {
			return false
		}
		for _, digit := range value {
			if digit < '0' || digit > '9' {
				return false
			}
		}
		return value != "0"
	}
	var values []string
	if json.Unmarshal(raw, &values) != nil || len(values) == 0 || len(values) > 100 {
		return false
	}
	seen := map[string]struct{}{}
	for _, value := range values {
		if value == "" || strings.TrimSpace(value) != value || len(value) > 200 {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func mustJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}
