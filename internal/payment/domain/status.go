// Package domain contains payment and external-result state values.
package domain

type EffectState string

const (
	EffectAccepted       EffectState = "accepted"
	EffectQueued         EffectState = "queued"
	EffectAttempted      EffectState = "attempted"
	EffectExecuted       EffectState = "executed"
	EffectOutcomeUnknown EffectState = "outcome_unknown"
	EffectReconciled     EffectState = "reconciled"
)

func (state EffectState) Valid() bool {
	switch state {
	case EffectAccepted, EffectQueued, EffectAttempted, EffectExecuted, EffectOutcomeUnknown, EffectReconciled:
		return true
	default:
		return false
	}
}
