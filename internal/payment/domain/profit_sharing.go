package domain

import (
	"errors"
	"strings"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

var ErrProfitSharing = errors.New("invalid profit sharing transition")

type ProfitSharingState string

type ProfitSharingReceiverState string

const (
	ProfitSharingReceiverAccepted       ProfitSharingReceiverState = "accepted"
	ProfitSharingReceiverReady          ProfitSharingReceiverState = "ready"
	ProfitSharingReceiverOutcomeUnknown ProfitSharingReceiverState = "outcome_unknown"
	ProfitSharingReceiverFinalFailed    ProfitSharingReceiverState = "final_failed"
)

func (s ProfitSharingReceiverState) Valid() bool {
	return s == ProfitSharingReceiverAccepted || s == ProfitSharingReceiverReady || s == ProfitSharingReceiverOutcomeUnknown || s == ProfitSharingReceiverFinalFailed
}

type ProfitSharingReceiver struct {
	ID, CustomerID, IdentityID int64
	AppID, AppScope            string
	Channel                    Channel
	AccountDigest              string
	State                      ProfitSharingReceiverState
	EffectID                   string
	Version                    int64
	CreatedAt, UpdatedAt       time.Time
}

const (
	ProfitSharingAccepted       ProfitSharingState = "accepted"
	ProfitSharingSettling       ProfitSharingState = "settling"
	ProfitSharingOutcomeUnknown ProfitSharingState = "outcome_unknown"
	ProfitSharingPaid           ProfitSharingState = "paid"
	ProfitSharingCancelled      ProfitSharingState = "cancelled"
	ProfitSharingException      ProfitSharingState = "exception"
)

func (s ProfitSharingState) Valid() bool {
	switch s {
	case ProfitSharingAccepted, ProfitSharingSettling, ProfitSharingOutcomeUnknown, ProfitSharingPaid, ProfitSharingCancelled, ProfitSharingException:
		return true
	default:
		return false
	}
}

func (s ProfitSharingState) Reserving() bool {
	return s == ProfitSharingAccepted || s == ProfitSharingSettling || s == ProfitSharingOutcomeUnknown
}

type ProfitSharingInstruction struct {
	ID, PaymentID, ReceiverID      int64
	SettlementRef, ProviderOrderNo string
	// These digests are immutable command fingerprints.  A settlement replay
	// must match all of them; returning an old instruction for a mutated
	// recipient or frozen Distribution fact would otherwise hide a money
	// command conflict.
	IdempotencyKeyDigest, SourceRefDigest, PayloadDigest, PolicyVersionHash string
	AmountMinor                                                             int64
	Currency                                                                string
	State                                                                   ProfitSharingState
	EffectID                                                                string
	DeadlineAt                                                              time.Time
	ReceiverConfirmedSuccess, OutcomeKnown                                  bool
	Version                                                                 int64
	CreatedAt, UpdatedAt                                                    time.Time
}

type ProfitSharingFunding struct {
	SuccessfulRefundMinor, RequestedRefundMinor, ProcessingRefundMinor, OutcomeUnknownRefundMinor int64
	ReservedSplitMinor                                                                            int64
}

func (v ProfitSharingFunding) RefundExposure() bool {
	return v.RequestedRefundMinor > 0 || v.ProcessingRefundMinor > 0 || v.OutcomeUnknownRefundMinor > 0
}

type ProfitSharingUnfreeze struct {
	ID, PaymentID                                                           int64
	ProviderOrderNo                                                         string
	Reason                                                                  string
	IdempotencyKeyDigest, SourceRefDigest, PayloadDigest, PolicyVersionHash string
	State, EffectID                                                         string
	OutcomeKnown                                                            bool
	Version                                                                 int64
	CreatedAt, UpdatedAt                                                    time.Time
}

type ProfitSharingProviderIntent struct {
	ReceiverID, InstructionID, UnfreezeID int64
	PayloadDigest                         effectport.Digest
}

func (v ProfitSharingInstruction) Cancel(expected int64, reason string, now time.Time) (ProfitSharingInstruction, error) {
	if expected != v.Version || !v.State.Reserving() || strings.TrimSpace(reason) != reason || reason == "" || len(reason) > 500 || now.Before(v.UpdatedAt) {
		return ProfitSharingInstruction{}, ErrProfitSharing
	}
	v.State = ProfitSharingCancelled
	v.OutcomeKnown = true
	v.Version++
	v.UpdatedAt = now.UTC()
	return v, nil
}
