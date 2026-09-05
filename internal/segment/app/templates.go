package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
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

var legacyTemplates = []string{
	"wecom_contact_registration", "questionnaire_choice_answers", "paid_order",
	"channel_entry", "radar_first_click_elapsed", "member_usage_status",
}

// Templates is deliberately a closed catalog. Composition reports source
// availability independently; a visible template is never treated as a SQL
// escape hatch.
func Templates() []Template {
	items := make([]Template, 0, len(legacyTemplates))
	for _, key := range legacyTemplates {
		items = append(items, Template{Key: key, Available: true})
	}
	return items
}

func DefaultDefinition(templateKey string) (json.RawMessage, error) {
	var parameters map[string]any
	switch templateKey {
	case "active_contacts":
		parameters = map[string]any{"within_days": "30"}
	case "stage_any":
		parameters = map[string]any{"stages": []string{"__configure__"}}
	case "tag_any":
		parameters = map[string]any{"tag_codes": []string{"__configure__"}}
	case "owner_any":
		parameters = map[string]any{"staff_ids": []string{"__configure__"}}
	case "channel_any":
		parameters = map[string]any{"channels": []string{"__configure__"}}
	case "wecom_contact_registration":
		parameters = map[string]any{"owner_scope": "all", "owner_staff_ids": []string{}, "contact_statuses": []string{"active"}, "registration_status": "any"}
	case "questionnaire_choice_answers":
		parameters = map[string]any{"questionnaire_id": "__configure__", "conditions": []any{map[string]any{"question_id": "__configure__", "option_ids": []string{"__configure__"}}}, "owner_scope": "all", "owner_staff_ids": []string{}}
	case "paid_order":
		parameters = map[string]any{"product_codes": []string{"__configure__"}, "paid_at_from": "", "paid_at_to": "", "owner_scope": "all", "owner_staff_ids": []string{}, "require_active_wecom_contact": true}
	case "channel_entry":
		parameters = map[string]any{"channel_codes": []string{"__configure__"}, "entered_days_min": 0, "entered_days_max": nil, "owner_scope": "all", "owner_staff_ids": []string{}, "require_active_wecom_contact": true}
	case "radar_first_click_elapsed":
		parameters = map[string]any{"radar_ids": []string{"__configure__"}, "elapsed_min": 0, "elapsed_max": nil, "elapsed_unit": "day", "owner_scope": "all", "owner_staff_ids": []string{}}
	case "member_usage_status":
		parameters = map[string]any{"owner_scope": "all", "owner_staff_ids": []string{}, "service_period": "active", "registration_status": "any", "usage_status": "any", "membership_tiers": []string{}, "membership_statuses": []string{}}
	default:
		return nil, ErrUnsupportedDefinition
	}
	raw, _ := json.Marshal(DefinitionInput{SchemaVersion: 1, TemplateKey: templateKey, Parameters: rawParameters(parameters)})
	return CanonicalDefinition(raw)
}

func rawParameters(values map[string]any) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		out[key], _ = json.Marshal(value)
	}
	return out
}

func CanonicalDefinition(raw json.RawMessage) (json.RawMessage, error) {
	var input DefinitionInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		return nil, ErrUnsupportedDefinition
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || input.SchemaVersion != 1 || !validDefinition(input) {
		return nil, ErrUnsupportedDefinition
	}
	canonical, err := json.Marshal(input)
	if err != nil || len(canonical) > 16*1024 {
		return nil, ErrUnsupportedDefinition
	}
	return canonical, nil
}

func validDefinition(input DefinitionInput) bool {
	owner := validOwner(input.Parameters)
	switch input.TemplateKey {
	case "active_contacts":
		// Read-only compatibility for already-persisted definitions. It is not
		// exposed in the PRD05 catalog because directory updates are not usage.
		value, ok := stringOf(input.Parameters, "within_days")
		return ok && value != "" && keys(input.Parameters, "within_days") && positiveInt(value)
	case "stage_any":
		value, ok := stringsOf(input.Parameters, "stages", 1, 100)
		return ok && validStrings(value, 1, 100) && keys(input.Parameters, "stages")
	case "tag_any":
		value, ok := stringsOf(input.Parameters, "tag_codes", 1, 100)
		return ok && validStrings(value, 1, 100) && keys(input.Parameters, "tag_codes")
	case "owner_any":
		value, ok := stringsOf(input.Parameters, "staff_ids", 1, 100)
		return ok && validStrings(value, 1, 100) && keys(input.Parameters, "staff_ids")
	case "channel_any":
		value, ok := stringsOf(input.Parameters, "channels", 1, 100)
		return ok && validStrings(value, 1, 100) && keys(input.Parameters, "channels")
	case "wecom_contact_registration":
		statuses, ok := stringsOf(input.Parameters, "contact_statuses", 1, 2)
		return owner && ok && allowed(statuses, "active", "deleted") && enum(input.Parameters, "registration_status", "any", "registered", "unregistered") && keys(input.Parameters, "owner_scope", "owner_staff_ids", "contact_statuses", "registration_status")
	case "questionnaire_choice_answers":
		questionnaire, ok := stringOf(input.Parameters, "questionnaire_id")
		if !owner || !ok || questionnaire == "" || !keys(input.Parameters, "questionnaire_id", "conditions", "owner_scope", "owner_staff_ids") {
			return false
		}
		var conditions []struct {
			QuestionID string   `json:"question_id"`
			OptionIDs  []string `json:"option_ids"`
		}
		if json.Unmarshal(input.Parameters["conditions"], &conditions) != nil || len(conditions) < 1 || len(conditions) > 50 {
			return false
		}
		seen := map[string]bool{}
		for _, condition := range conditions {
			if condition.QuestionID == "" || seen[condition.QuestionID] || !validStrings(condition.OptionIDs, 1, 100) {
				return false
			}
			seen[condition.QuestionID] = true
		}
		return true
	case "paid_order":
		products, ok := stringsOf(input.Parameters, "product_codes", 1, 100)
		from, fromOK := stringOf(input.Parameters, "paid_at_from")
		to, toOK := stringOf(input.Parameters, "paid_at_to")
		_, contactOK := boolOf(input.Parameters, "require_active_wecom_contact")
		return owner && ok && validStrings(products, 1, 100) && fromOK && toOK && validWindow(from, to) && contactOK && keys(input.Parameters, "product_codes", "paid_at_from", "paid_at_to", "owner_scope", "owner_staff_ids", "require_active_wecom_contact")
	case "channel_entry":
		channels, ok := stringsOf(input.Parameters, "channel_codes", 1, 100)
		minimum, minOK := nonNegative(input.Parameters, "entered_days_min")
		maximum, maxOK := optionalNonNegative(input.Parameters, "entered_days_max")
		_, contactOK := boolOf(input.Parameters, "require_active_wecom_contact")
		return owner && ok && validStrings(channels, 1, 100) && minOK && maxOK && (maximum == nil || *maximum > minimum) && contactOK && keys(input.Parameters, "channel_codes", "entered_days_min", "entered_days_max", "owner_scope", "owner_staff_ids", "require_active_wecom_contact")
	case "radar_first_click_elapsed":
		radars, ok := stringsOf(input.Parameters, "radar_ids", 1, 100)
		minimum, minOK := nonNegative(input.Parameters, "elapsed_min")
		maximum, maxOK := optionalNonNegative(input.Parameters, "elapsed_max")
		return owner && ok && validStrings(radars, 1, 100) && minOK && maxOK && (maximum == nil || *maximum > minimum) && enum(input.Parameters, "elapsed_unit", "hour", "day") && keys(input.Parameters, "radar_ids", "elapsed_min", "elapsed_max", "elapsed_unit", "owner_scope", "owner_staff_ids")
	case "member_usage_status":
		tiers, tiersOK := stringsOf(input.Parameters, "membership_tiers", 0, 100)
		statuses, statusesOK := stringsOf(input.Parameters, "membership_statuses", 0, 100)
		return owner && tiersOK && statusesOK && validStrings(tiers, 0, 100) && validStrings(statuses, 0, 100) && enum(input.Parameters, "service_period", "any", "active", "expired") && enum(input.Parameters, "registration_status", "any", "registered", "unregistered") && enum(input.Parameters, "usage_status", "any", "used", "unused") && keys(input.Parameters, "owner_scope", "owner_staff_ids", "service_period", "registration_status", "usage_status", "membership_tiers", "membership_statuses")
	default:
		return false
	}
}

func keys(values map[string]json.RawMessage, expected ...string) bool {
	if len(values) != len(expected) {
		return false
	}
	for _, key := range expected {
		if _, ok := values[key]; !ok {
			return false
		}
	}
	return true
}
func stringOf(values map[string]json.RawMessage, key string) (string, bool) {
	var value string
	return value, json.Unmarshal(values[key], &value) == nil && strings.TrimSpace(value) == value && len(value) <= 200
}
func stringsOf(values map[string]json.RawMessage, key string, min, max int) ([]string, bool) {
	var value []string
	return value, json.Unmarshal(values[key], &value) == nil && len(value) >= min && len(value) <= max
}
func validStrings(values []string, min, max int) bool {
	if len(values) < min || len(values) > max {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || strings.TrimSpace(value) != value || len(value) > 200 || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}
func allowed(values []string, options ...string) bool {
	for _, value := range values {
		found := false
		for _, option := range options {
			if value == option {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return validStrings(values, 1, len(options))
}
func enum(values map[string]json.RawMessage, key string, options ...string) bool {
	value, ok := stringOf(values, key)
	if !ok {
		return false
	}
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}
func boolOf(values map[string]json.RawMessage, key string) (bool, bool) {
	var value bool
	return value, json.Unmarshal(values[key], &value) == nil
}
func nonNegative(values map[string]json.RawMessage, key string) (int, bool) {
	var value int
	return value, json.Unmarshal(values[key], &value) == nil && value >= 0 && value <= 36500
}
func optionalNonNegative(values map[string]json.RawMessage, key string) (*int, bool) {
	if bytes.Equal(values[key], []byte("null")) {
		return nil, true
	}
	value, ok := nonNegative(values, key)
	return &value, ok
}
func validWindow(from, to string) bool {
	if from != "" {
		if _, err := time.Parse(time.RFC3339, from); err != nil {
			return false
		}
	}
	if to != "" {
		if _, err := time.Parse(time.RFC3339, to); err != nil {
			return false
		}
	}
	return from == "" || to == "" || from < to
}
func validOwner(values map[string]json.RawMessage) bool {
	scope, ok := stringOf(values, "owner_scope")
	ids, idsOK := stringsOf(values, "owner_staff_ids", 0, 100)
	return ok && idsOK && validStaffIDs(ids) && ((scope == "all") || (scope == "specified" && len(ids) > 0))
}

// validStaffIDs keeps a persisted definition in the local Access namespace.
// Provider userids, including numeric userids, are never valid here.
func validStaffIDs(ids []string) bool {
	if !validStrings(ids, 0, 100) {
		return false
	}
	for _, value := range ids {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id < 1 || strconv.FormatInt(id, 10) != value {
			return false
		}
	}
	return true
}
func positiveInt(value string) bool {
	if len(value) > 3 {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return value != "" && value != "0"
}
