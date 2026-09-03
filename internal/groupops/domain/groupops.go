// Package domain contains pure Group Ops plan invariants.
//
// It deliberately models local staff references and opaque group references
// only. Customer, OneID, segment, audience, campaign, recipient, and Provider
// identities are outside this package.
package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
)

var (
	ErrInvalidPlan     = errors.New("invalid Group Ops plan")
	ErrInvalidDetail   = errors.New("invalid Group Ops plan detail")
	ErrInvalidNode     = errors.New("invalid Group Ops node")
	ErrInvalidMaterial = errors.New("invalid Group Ops material plan")
	ErrInvalidScope    = errors.New("Group Ops payload contains excluded scope")
)

const (
	MaxNameLength    = 128
	MaxMessageLength = 1000
	MaxMaterials     = 9
)

// ValidatePlan checks persisted plan metadata without silently normalizing it.
func ValidatePlan(value groupopsport.Plan) error {
	if value.ID < 1 || !ValidText(value.Name, MaxNameLength) || !validStatus(value.Status) || value.Revision < 1 ||
		value.CreatedBy < 1 || value.UpdatedBy < 1 || value.CreatedAt.IsZero() || value.UpdatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) {
		return ErrInvalidPlan
	}
	return nil
}

// ValidateDetail checks local ordering and fail-closed safety facts.
func ValidateDetail(value groupopsport.Detail) error {
	if ValidatePlan(value.Plan) != nil || value.ProviderExecutionEligible || value.RealExternalCallExecuted ||
		!validMembers(value.Members) || !validAssets(value.GroupAssets) || !validNodes(value.Nodes) || !validWebhook(value.WebhookDescriptor) {
		return ErrInvalidDetail
	}
	return nil
}

func ValidateMaterialPlan(value groupopsport.MaterialPlan) error {
	if len(value.References) > MaxMaterials {
		return ErrInvalidMaterial
	}
	seen := make(map[string]struct{}, len(value.References))
	counts := map[string]int{}
	for _, reference := range value.References {
		if reference.ID < 1 || reference.Kind == "" {
			return ErrInvalidMaterial
		}
		key := reference.Kind + ":" + formatID(reference.ID)
		if _, exists := seen[key]; exists {
			return ErrInvalidMaterial
		}
		seen[key] = struct{}{}
		counts[reference.Kind]++
		switch reference.Kind {
		case "image":
			if counts[reference.Kind] > 3 {
				return ErrInvalidMaterial
			}
		case "miniprogram":
			if counts[reference.Kind] > 1 {
				return ErrInvalidMaterial
			}
		case "attachment":
		case "group_invite":
			if counts[reference.Kind] > 1 {
				return ErrInvalidMaterial
			}
		default:
			return ErrInvalidMaterial
		}
	}
	return nil
}

// ValidateNode enforces the draft node shape. Legacy free-form material
// references remain representable for read compatibility, but a caller must
// not treat them as provider-ready content.
func ValidateNode(value groupopsport.Node) error {
	if value.ID < 1 || value.Position < 1 || !ValidOpaque(value.MaterialRef) && value.MaterialRef != "" || ValidateMaterialPlan(value.MaterialPlan) != nil {
		return ErrInvalidNode
	}
	switch value.Kind {
	case groupopsport.NodeMessage:
		if value.DelayMinutes != 0 || !ValidText(value.MessageText, MaxMessageLength) && value.MessageText != "" || value.MessageText == "" && len(value.MaterialPlan.References) == 0 {
			return ErrInvalidNode
		}
	case groupopsport.NodeDelay:
		if strings.TrimSpace(value.MessageText) != "" || value.MaterialRef != "" || len(value.MaterialPlan.References) != 0 || value.DelayMinutes < 1 || value.DelayMinutes > 10080 {
			return ErrInvalidNode
		}
	default:
		return ErrInvalidNode
	}
	return nil
}

func CanTransitionPlanStatus(from, to groupopsport.PlanStatus) bool {
	if !validStatus(from) || !validStatus(to) || from == groupopsport.PlanArchived {
		return false
	}
	if from == to {
		return true
	}
	switch from {
	case groupopsport.PlanDraft:
		return to == groupopsport.PlanActive || to == groupopsport.PlanPaused || to == groupopsport.PlanArchived
	case groupopsport.PlanActive:
		return to == groupopsport.PlanPaused || to == groupopsport.PlanArchived
	case groupopsport.PlanPaused:
		return to == groupopsport.PlanActive || to == groupopsport.PlanArchived
	default:
		return false
	}
}

// ContainsForbidden rejects identity and execution scopes outside Group Ops.
// Human-readable text is not rejected merely because it contains a word such
// as “audience”; only concrete field names in structured JSON are blocked.
func ContainsForbidden(value json.RawMessage) bool {
	if len(value) == 0 {
		return false
	}
	var decoded any
	if !json.Valid(value) || json.Unmarshal(value, &decoded) != nil {
		return true
	}
	return containsForbiddenValue(decoded)
}

func containsForbiddenValue(value any) bool {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(key))
			switch normalized {
			case "customerid", "customerids", "openid", "openids", "unionid", "unionids", "externaluserid", "externaluserids", "phone", "phonenumber", "mobile", "segmentid", "segmentids", "audienceid", "audienceids", "campaignid", "campaignids", "recipientid", "recipientids":
				return true
			}
			if strings.Contains(normalized, "secret") || strings.Contains(normalized, "credential") || containsForbiddenValue(child) {
				return true
			}
		}
	case []any:
		for _, child := range item {
			if containsForbiddenValue(child) {
				return true
			}
		}
	}
	return false
}

func ValidText(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && utf8.ValidString(value) && utf8.RuneCountInString(value) <= maximum
}

func ValidOpaque(value string) bool {
	if !ValidText(value, MaxNameLength) || strings.Contains(value, "://") {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("-_.:", character) {
			continue
		}
		return false
	}
	return true
}

func validStatus(value groupopsport.PlanStatus) bool {
	return value == groupopsport.PlanDraft || value == groupopsport.PlanActive || value == groupopsport.PlanPaused || value == groupopsport.PlanArchived
}

func validMembers(items []groupopsport.Member) bool {
	if items == nil {
		return false
	}
	for index, item := range items {
		if item.StaffID < 1 || index > 0 && items[index-1].StaffID >= item.StaffID {
			return false
		}
	}
	return true
}

func validAssets(items []groupopsport.GroupAsset) bool {
	if items == nil {
		return false
	}
	for index, item := range items {
		if item.ID < 1 || !ValidOpaque(item.AssetRef) || index > 0 && (items[index-1].AssetRef > item.AssetRef || items[index-1].AssetRef == item.AssetRef && items[index-1].ID >= item.ID) {
			return false
		}
	}
	return true
}

func validNodes(items []groupopsport.Node) bool {
	if items == nil {
		return false
	}
	for index, item := range items {
		if item.Position != int32(index+1) || ValidateNode(item) != nil {
			return false
		}
	}
	return true
}

func validWebhook(value groupopsport.WebhookDescriptor) bool {
	if value.Reference == "" {
		return !value.Configured && value.Path == "" && value.URL == "" && value.SignatureAlgorithm == "" && value.SignatureHeader == "" && value.TimestampHeader == "" && value.NonceHeader == "" && value.ClientIDHeader == "" && value.ClientID == ""
	}
	return value.Configured && ValidOpaque(value.Reference) && value.Path != "" && value.URL != "" && value.SignatureAlgorithm == groupopsport.WebhookSignatureAlgorithm && value.SignatureHeader == groupopsport.WebhookSignatureHeader && value.TimestampHeader == groupopsport.WebhookTimestampHeader && value.NonceHeader == groupopsport.WebhookNonceHeader && value.ClientIDHeader == groupopsport.WebhookClientIDHeader && value.ClientID == groupopsport.WebhookClientID
}

func formatID(value int64) string {
	// Avoid pulling a formatter into every adapter; decimal identity is stable
	// and only used to detect duplicate typed references.
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	negative := value < 0
	if negative {
		value = -value
	}
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		buffer[index] = '-'
	}
	return string(buffer[index:])
}
