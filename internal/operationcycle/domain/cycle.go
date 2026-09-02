// Package domain contains pure operation-cycle invariants. It deliberately
// models no customer, segment, campaign, recipient, or Provider identity.
package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	operationport "github.com/qianlan33333-png/AI-CRM-v3/internal/operationcycle/port"
)

var (
	ErrInvalidStrategy = errors.New("invalid operation-cycle strategy")
	ErrInvalidRun      = errors.New("invalid operation-cycle run")
	ErrInvalidRunner   = errors.New("invalid operation-cycle runner")
	ErrInvalidAction   = errors.New("invalid operation-cycle action")
	ErrInvalidProposal = errors.New("invalid operation-cycle proposal")
	ErrInvalidScope    = errors.New("operation-cycle payload contains excluded scope")
	ErrInvalidStatus   = errors.New("invalid operation-cycle status transition")
)

const (
	StatusDraft    = "draft"
	StatusActive   = "active"
	StatusPaused   = "paused"
	StatusArchived = "archived"

	ActionQueued      = "queued"
	ActionClaimed     = "claimed"
	ActionThreadBound = "thread_bound"
	ActionTurnStarted = "turn_started"
	ActionCompleted   = "completed"
	ActionFailed      = "failed"

	ProposalPending  = "pending"
	ProposalAccepted = "accepted"
	ProposalRejected = "rejected"
)

// These aliases make the domain contracts available without forcing callers
// to depend on an adapter package for validation.
type Strategy = operationport.Strategy
type Run = operationport.Run
type Runner = operationport.Runner
type Stage = operationport.ActionRequest
type ActionRequest = operationport.ActionRequest
type Proposal = operationport.Proposal

func ValidateStrategy(value Strategy) error {
	if !ValidKey(value.Key, 120) || !ValidText(value.Title, 200) || !validStrategyStatus(value.Status) || value.Version < 1 ||
		!ValidJSON(value.Definition) || !ValidJSON(value.Snapshot) || value.UpdatedAt.IsZero() || ContainsForbidden(value.Definition) || ContainsForbidden(value.Snapshot) {
		return ErrInvalidStrategy
	}
	return nil
}

func ValidateRun(value Run) error {
	if !ValidKey(value.Key, 160) || !ValidKey(value.StrategyKey, 120) || value.Revision < 1 || !ValidJSON(value.Snapshot) || value.ReceivedAt.IsZero() || ContainsForbidden(value.Snapshot) {
		return ErrInvalidRun
	}
	return nil
}

func ValidateRunner(value Runner) error {
	if !ValidKey(value.ID, 160) || !ValidKey(value.PrincipalID, 240) || !ValidKey(value.ConnectorVersion, 120) ||
		!ValidKey(value.CodexVersion, 120) || !validCompatibilityStatus(value.CompatibilityStatus) || len(value.BindingKeys) > 32 || value.LastHeartbeatAt.IsZero() {
		return ErrInvalidRunner
	}
	for _, key := range value.BindingKeys {
		if !ValidKey(key, 240) {
			return ErrInvalidRunner
		}
	}
	return nil
}

func ValidateAction(value ActionRequest) error {
	if !ValidKey(value.ID, 160) || !ValidKey(value.StrategyKey, 120) || !ValidKey(value.RunKey, 160) ||
		!ValidKey(value.ActionKey, 120) || !ValidText(value.ActionTitle, 200) || value.StrategyVersion < 1 ||
		!ValidKey(value.RunnerID, 160) || !validActionStatus(value.Status) || !ValidKey(value.CreatedBy, 240) ||
		value.CreatedAt.IsZero() || value.UpdatedAt.IsZero() || ContainsForbidden(value.FinalResult) {
		return ErrInvalidAction
	}
	if value.ParentRequestID != "" && !ValidKey(value.ParentRequestID, 160) {
		return ErrInvalidAction
	}
	if value.ThreadID != "" && !ValidKey(value.ThreadID, 200) || value.TurnID != "" && !ValidKey(value.TurnID, 200) {
		return ErrInvalidAction
	}
	if value.Status == ActionCompleted || value.Status == ActionFailed {
		if value.CompletedAt == nil || value.CompletedAt.IsZero() {
			return ErrInvalidAction
		}
	} else if value.CompletedAt != nil {
		return ErrInvalidAction
	}
	return nil
}

func ValidateProposal(value Proposal) error {
	if !ValidKey(value.ID, 160) || !ValidKey(value.StrategyKey, 120) || value.BaseStrategyVersion < 1 ||
		!validProposalStatus(value.Status) || !ValidJSON(value.Payload) || !ValidKey(value.CreatedBy, 240) ||
		value.CreatedAt.IsZero() || ContainsForbidden(value.Payload) {
		return ErrInvalidProposal
	}
	if value.Status == ProposalPending {
		if value.DecidedBy != "" || value.DecidedAt != nil {
			return ErrInvalidProposal
		}
	} else if !ValidKey(value.DecidedBy, 240) || value.DecidedAt == nil || value.DecidedAt.IsZero() {
		return ErrInvalidProposal
	}
	return nil
}

// CanTransitionActionStatus is the local stage state machine. A failed stage
// can terminate from any active state; no transition performs external work.
func CanTransitionActionStatus(from, to string) bool {
	if from == to && validActionStatus(from) {
		return true
	}
	if !validActionStatus(from) || !validActionStatus(to) {
		return false
	}
	if to == ActionFailed {
		return from == ActionQueued || from == ActionClaimed || from == ActionThreadBound || from == ActionTurnStarted
	}
	switch from {
	case ActionQueued:
		return to == ActionClaimed
	case ActionClaimed:
		return to == ActionThreadBound
	case ActionThreadBound:
		return to == ActionTurnStarted
	case ActionTurnStarted:
		return to == ActionCompleted
	default:
		return false
	}
}

// CanTransitionStrategyStatus models enable/pause/archive state locally.
func CanTransitionStrategyStatus(from, to string) bool {
	if !validStrategyStatus(from) || !validStrategyStatus(to) || from == StatusArchived {
		return false
	}
	if from == to {
		return true
	}
	switch from {
	case StatusDraft:
		return to == StatusActive || to == StatusPaused || to == StatusArchived
	case StatusActive:
		return to == StatusPaused || to == StatusArchived
	case StatusPaused:
		return to == StatusActive || to == StatusArchived
	default:
		return false
	}
}

func ValidKey(value string, maximum int) bool {
	value = strings.TrimSpace(value)
	return value != "" && utf8.ValidString(value) && len(value) <= maximum && !strings.ContainsAny(value, "\t\r\n")
}

func ValidText(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && utf8.ValidString(value) && len([]rune(value)) <= maximum
}

func ValidJSON(value json.RawMessage) bool { return len(value) > 0 && json.Valid(value) }

// ContainsForbidden rejects identity and execution scopes outside this local
// lifecycle. Human-readable labels such as "audience" are allowed; concrete
// customer/segment/campaign/recipient identifiers and provider effects are not.
func ContainsForbidden(value any) bool {
	switch item := value.(type) {
	case json.RawMessage:
		if len(item) == 0 {
			return false
		}
		var decoded any
		if !json.Valid(item) || json.Unmarshal(item, &decoded) != nil {
			return true
		}
		return ContainsForbidden(decoded)
	case []byte:
		return ContainsForbidden(json.RawMessage(item))
	case map[string]any:
		for key, child := range item {
			lowered := strings.ToLower(key)
			normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(lowered)
			if forbiddenKey(normalized) || normalized == "externaleffects" && resultString(item, key) != "" && resultString(item, key) != "none" {
				return true
			}
			if ContainsForbidden(child) {
				return true
			}
		}
	case []any:
		for _, child := range item {
			if ContainsForbidden(child) {
				return true
			}
		}
	case string:
		return strings.Contains(item, "/Users/") || strings.HasPrefix(item, "file://")
	}
	return false
}

func forbiddenKey(key string) bool {
	switch key {
	case "tenant", "tenantid", "tenantids", "customerid", "customerids", "externaluserid", "externaluserids", "openid", "openids", "unionid", "unionids", "mobile", "phone", "phonenumber", "segmentid", "segmentids", "campaignid", "campaignids", "recipientid", "recipientids", "audienceid", "audienceids":
		return true
	default:
		return strings.Contains(key, "credential") || strings.Contains(key, "secret")
	}
}

func resultString(value map[string]any, key string) string {
	raw, _ := value[key].(string)
	return strings.TrimSpace(raw)
}

func validStrategyStatus(value string) bool {
	return value == StatusDraft || value == StatusActive || value == StatusPaused || value == StatusArchived
}

func validCompatibilityStatus(value string) bool {
	return value == "ready" || value == "incompatible" || value == "unavailable"
}

func validActionStatus(value string) bool {
	return value == ActionQueued || value == ActionClaimed || value == ActionThreadBound || value == ActionTurnStarted || value == ActionCompleted || value == ActionFailed
}

func validProposalStatus(value string) bool {
	return value == ProposalPending || value == ProposalAccepted || value == ProposalRejected
}
