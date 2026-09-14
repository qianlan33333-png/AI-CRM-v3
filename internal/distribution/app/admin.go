package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type adminStore interface {
	ReadDistributorWithin(context.Context, int64, bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error)
	SetDistributorEnabledWithin(context.Context, int64, int64, bool, time.Time) (distributiondomain.Distributor, error)
	ReadAdminExceptionWithin(context.Context, int64, bool) (distributionstore.AdminExceptionDetail, error)
	UpdateAdminExceptionWithin(context.Context, distributionstore.AdminExceptionDetail, int64, time.Time) (distributionstore.AdminExceptionDetail, error)
	ReadCommissionWithin(context.Context, int64, bool) (distributiondomain.Commission, error)
	AppendCommissionAdjustmentWithin(context.Context, distributionstore.CommissionAdjustment) error
	ReadOperationReceiptWithin(context.Context, string, string, string) (distributionstore.OperationReceipt, bool, error)
	AppendOperationReceiptWithin(context.Context, string, string, string, [sha256.Size]byte, string, int64, time.Time) error
	AppendAuditWithin(context.Context, string, string, int64, string, any, time.Time) error
	AppendOutboxWithin(context.Context, string, string, int64, any, time.Time) error
}

type paymentInstructionReader interface {
	ReconcileProfitSharing(context.Context, string) (paymentport.ProfitSharingInstruction, error)
	ReconcileProfitSharingUnfreeze(context.Context, string) (paymentport.ProfitSharingUnfreeze, error)
}

// AdminService records staff governance over frozen Distribution facts. It
// never calls a Payment provider, never creates a payment instruction, and
// never declares a commission paid; due-check settlement owns that transition.
type AdminService struct {
	uow     platformport.UnitOfWork
	store   adminStore
	payment paymentInstructionReader
	now     func() time.Time
}

func NewAdminService(uow platformport.UnitOfWork, store adminStore, payment paymentInstructionReader) (*AdminService, error) {
	if uow == nil || store == nil || payment == nil {
		return nil, distributionport.ErrUnavailable
	}
	return &AdminService{uow: uow, store: store, payment: payment, now: time.Now}, nil
}

func (s *AdminService) SetDistributorEnabled(ctx context.Context, command distributionport.AdminDistributorCommand, enabled bool) error {
	if s == nil || s.uow == nil || s.store == nil || !validAdminDistributorCommand(command) {
		return distributionport.ErrConflict
	}
	operation := "distributor_enable"
	if !enabled {
		operation = "distributor_disable"
	}
	payload := map[string]any{"distributor_id": command.DistributorID, "expected_version": command.ExpectedVersion, "enabled": enabled, "reason": command.Reason}
	digest := adminDigest(operation, payload)
	return s.uow.Within(ctx, func(tx context.Context) error {
		if replay, err := adminReplay(s.store, tx, operation, command.ActorScope, command.IdempotencyKey, digest); err != nil || replay {
			return err
		}
		current, _, err := s.store.ReadDistributorWithin(tx, command.DistributorID, true)
		if err != nil {
			return err
		}
		if current.Version != command.ExpectedVersion {
			return distributionport.ErrConflict
		}
		updated, err := s.store.SetDistributorEnabledWithin(tx, current.ID, command.ExpectedVersion, enabled, s.now())
		if err != nil {
			return err
		}
		now := s.now().UTC()
		eventType := "distribution.distributor_enabled.v1"
		if !enabled {
			eventType = "distribution.distributor_disabled.v1"
		}
		result := map[string]any{"distributor_id": updated.ID, "enabled": updated.Enabled, "reason": command.Reason, "version": updated.Version}
		if err = s.store.AppendOperationReceiptWithin(tx, operation, command.ActorScope, command.IdempotencyKey, digest, "distributor", updated.ID, now); err != nil {
			return err
		}
		if err = s.store.AppendAuditWithin(tx, eventType, "distributor", updated.ID, command.ActorScope, result, now); err != nil {
			return err
		}
		return s.store.AppendOutboxWithin(tx, eventType, "distribution."+operation+":"+command.IdempotencyKey, updated.ID, result, now)
	})
}

// ReconcileException reads Payment's existing, opaque instruction projection
// before its transaction. It only records the query observation; Payment's
// due-check owns all Provider queries and the receiver-confirmed paid state.
func (s *AdminService) ReconcileException(ctx context.Context, command distributionport.AdminExceptionCommand) error {
	if s == nil || s.uow == nil || s.store == nil || s.payment == nil || !validAdminExceptionCommand(command, false, false) {
		return distributionport.ErrConflict
	}
	// The receipt is keyed solely by immutable browser command input, so an
	// exact retry never issues a second Provider query merely to reproduce its
	// former observation.
	digest := adminDigest("exception_reconcile", map[string]any{"exception_id": command.ExceptionID, "expected_version": command.ExpectedVersion})
	if err := s.uow.Within(ctx, func(tx context.Context) error {
		replay, err := adminReplay(s.store, tx, "exception_reconcile", command.ActorScope, command.IdempotencyKey, digest)
		if err != nil {
			return err
		}
		if replay {
			return errAdminReplay
		}
		return nil
	}); errors.Is(err, errAdminReplay) {
		return nil
	} else if err != nil {
		return err
	}

	var initial distributionstore.AdminExceptionDetail
	if err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		initial, err = s.store.ReadAdminExceptionWithin(tx, command.ExceptionID, false)
		return err
	}); err != nil {
		return err
	}
	if initial.Version != command.ExpectedVersion || !adminActionableException(initial) || !validAdminReconcileTarget(initial) {
		return distributionport.ErrConflict
	}

	// This call is deliberately outside the Distribution UoW. The target is
	// derived from persisted Distribution evidence only; browser input never
	// supplies a Payment reference or result.
	observation, err := s.reconcilePayment(ctx, initial)
	if err != nil {
		return err
	}
	return s.uow.Within(ctx, func(tx context.Context) error {
		if replay, err := adminReplay(s.store, tx, "exception_reconcile", command.ActorScope, command.IdempotencyKey, digest); err != nil || replay {
			return err
		}
		current, err := s.store.ReadAdminExceptionWithin(tx, command.ExceptionID, true)
		if err != nil {
			return err
		}
		if current.Version != command.ExpectedVersion || !adminActionableException(current) || current.ReconcileTarget != initial.ReconcileTarget || !validAdminReconcileTarget(current) {
			return distributionport.ErrConflict
		}
		now := s.now().UTC()
		current.Version++
		current.Status = "open"
		current.Reason = observation.reason
		// The unfreeze evidence is its trusted reconciliation target. Retain it
		// so failed or unknown observations remain safely queryable.
		if current.ReconcileTarget != distributionport.AdminReconcileTargetUnfreeze {
			current.EvidenceReference = observation.evidenceReference
		}
		if observation.succeeded {
			current.Status = "resolved"
		}
		updated, err := s.store.UpdateAdminExceptionWithin(tx, current, command.ExpectedVersion, now)
		if err != nil {
			return err
		}
		result := map[string]any{"exception_id": updated.ID, "status": updated.Status, "reconcile_target": current.ReconcileTarget, "payment_state": observation.state, "payment_failure_class": observation.failureClass, "outcome_known": observation.outcomeKnown, "succeeded": observation.succeeded, "version": updated.Version}
		if err = s.store.AppendOperationReceiptWithin(tx, "exception_reconcile", command.ActorScope, command.IdempotencyKey, digest, "exception", updated.ID, now); err != nil {
			return err
		}
		if err = s.store.AppendAuditWithin(tx, "distribution.exception_reconciled.v1", "exception", updated.ID, command.ActorScope, result, now); err != nil {
			return err
		}
		return s.store.AppendOutboxWithin(tx, "distribution.exception_reconciled.v1", "distribution.exception_reconcile:"+command.IdempotencyKey, updated.ID, result, now)
	})
}

type adminPaymentObservation struct {
	state, reason, evidenceReference, failureClass string
	outcomeKnown, succeeded                        bool
}

func validAdminReconcileTarget(value distributionstore.AdminExceptionDetail) bool {
	switch value.ReconcileTarget {
	case distributionport.AdminReconcileTargetSplit:
		return value.InstructionReference != ""
	case distributionport.AdminReconcileTargetUnfreeze:
		return value.Kind == "unfreeze_final_failed" && value.EvidenceReference != ""
	default:
		return false
	}
}

func (s *AdminService) reconcilePayment(ctx context.Context, exception distributionstore.AdminExceptionDetail) (adminPaymentObservation, error) {
	switch exception.ReconcileTarget {
	case distributionport.AdminReconcileTargetSplit:
		instruction, err := s.payment.ReconcileProfitSharing(ctx, exception.InstructionReference)
		if err != nil {
			return adminPaymentObservation{}, err
		}
		if instruction.Reference != exception.InstructionReference {
			return adminPaymentObservation{}, distributionport.ErrConflict
		}
		return adminPaymentObservation{state: instruction.State, reason: paymentObservationReason(instruction), evidenceReference: paymentObservationReference(instruction), failureClass: paymentObservationFailureClass(instruction), outcomeKnown: instruction.OutcomeKnown, succeeded: instruction.ReceiverConfirmedSuccess}, nil
	case distributionport.AdminReconcileTargetUnfreeze:
		unfreeze, err := s.payment.ReconcileProfitSharingUnfreeze(ctx, exception.EvidenceReference)
		if err != nil {
			return adminPaymentObservation{}, err
		}
		if unfreeze.Reference != exception.EvidenceReference {
			return adminPaymentObservation{}, distributionport.ErrConflict
		}
		return adminPaymentObservation{state: unfreeze.State, reason: unfreezeObservationReason(unfreeze), outcomeKnown: unfreeze.OutcomeKnown, succeeded: unfreeze.OutcomeKnown && unfreeze.State == "succeeded"}, nil
	default:
		return adminPaymentObservation{}, distributionport.ErrConflict
	}
}

func (s *AdminService) RecordRecovery(ctx context.Context, command distributionport.AdminExceptionCommand) error {
	if s == nil || s.uow == nil || s.store == nil || !validAdminExceptionCommand(command, true, true) || command.Reason != "manual_recovery" {
		return distributionport.ErrConflict
	}
	return s.recordExceptionAmount(ctx, command, "recovery", "recovery_recorded", "distribution.recovery_recorded.v1")
}

func (s *AdminService) RecordMerchantLiability(ctx context.Context, command distributionport.AdminExceptionCommand) error {
	if s == nil || s.uow == nil || s.store == nil || !validAdminExceptionCommand(command, true, false) {
		return distributionport.ErrConflict
	}
	return s.recordExceptionAmount(ctx, command, "merchant_liability", "merchant_liability_recorded", "distribution.merchant_liability_recorded.v1")
}

func (s *AdminService) recordExceptionAmount(ctx context.Context, command distributionport.AdminExceptionCommand, operation, status, eventType string) error {
	payload := map[string]any{"exception_id": command.ExceptionID, "expected_version": command.ExpectedVersion, "amount_minor": command.AmountMinor, "reason": command.Reason, "evidence_reference": command.EvidenceReference}
	digest := adminDigest(operation, payload)
	return s.uow.Within(ctx, func(tx context.Context) error {
		if replay, err := adminReplay(s.store, tx, operation, command.ActorScope, command.IdempotencyKey, digest); err != nil || replay {
			return err
		}
		current, err := s.store.ReadAdminExceptionWithin(tx, command.ExceptionID, true)
		if err != nil {
			return err
		}
		if current.Version != command.ExpectedVersion || !adminActionableException(current) || !canRecordException(current.Status) || command.AmountMinor > current.AmountMinor {
			return distributionport.ErrConflict
		}
		now := s.now().UTC()
		current.Version++
		current.Status, current.Reason = status, command.Reason
		if operation == "recovery" {
			current.EvidenceReference = command.EvidenceReference
		}
		updated, err := s.store.UpdateAdminExceptionWithin(tx, current, command.ExpectedVersion, now)
		if err != nil {
			return err
		}
		if operation == "recovery" {
			commission, readErr := s.store.ReadCommissionWithin(tx, updated.CommissionID, false)
			if readErr != nil {
				return readErr
			}
			if err = s.store.AppendCommissionAdjustmentWithin(tx, distributionstore.CommissionAdjustment{CommissionID: commission.ID, Kind: "manual_recovery", DeltaMinor: command.AmountMinor, ResultingPayableMinor: commission.CurrentPayableMinor, Reason: "manual_recovery", SourceRef: command.EvidenceReference, OccurredAt: now}); err != nil {
				return err
			}
		}
		result := map[string]any{"exception_id": updated.ID, "commission_id": updated.CommissionID, "amount_minor": command.AmountMinor, "reason": command.Reason, "evidence_reference": command.EvidenceReference, "status": updated.Status, "version": updated.Version}
		if err = s.store.AppendOperationReceiptWithin(tx, operation, command.ActorScope, command.IdempotencyKey, digest, "exception", updated.ID, now); err != nil {
			return err
		}
		if err = s.store.AppendAuditWithin(tx, eventType, "exception", updated.ID, command.ActorScope, result, now); err != nil {
			return err
		}
		return s.store.AppendOutboxWithin(tx, eventType, "distribution."+operation+":"+command.IdempotencyKey, updated.ID, result, now)
	})
}

func validAdminDistributorCommand(v distributionport.AdminDistributorCommand) bool {
	return v.DistributorID > 0 && v.ExpectedVersion > 0 && validAdminText(v.ActorScope, 1, 200) && validAdminText(v.Reason, 1, 500) && validAdminText(v.IdempotencyKey, 16, 200)
}
func validAdminExceptionCommand(v distributionport.AdminExceptionCommand, requireAmount, requireEvidence bool) bool {
	return v.ExceptionID > 0 && v.ExpectedVersion > 0 && validAdminText(v.ActorScope, 1, 200) && validAdminText(v.IdempotencyKey, 16, 200) && (!requireAmount || v.AmountMinor > 0) && (!requireEvidence || validAdminText(v.EvidenceReference, 1, 500)) && v.Reason == strings.TrimSpace(v.Reason) && len(v.Reason) <= 500
}
func validAdminText(v string, min, max int) bool {
	return v == strings.TrimSpace(v) && len(v) >= min && len(v) <= max
}
func canRecordException(status string) bool { return status == "open" || status == "resolved" }

// settlement_deadline_imminent is a zero-amount scheduling warning.  It is
// retained for operational visibility but can never be reconciled or used to
// book recovery/merchant liability money.
func adminActionableException(value distributionstore.AdminExceptionDetail) bool {
	switch value.Kind {
	case "settlement_unknown", "settlement_not_paid", "settlement_deadline", "receiver_unavailable", "qualification_revoked_after_paid", "buyer_refund_after_paid", "unfreeze_final_failed":
		return true
	default:
		return false
	}
}
func adminDigest(operation string, payload any) [sha256.Size]byte {
	return sha256.Sum256([]byte(fmt.Sprintf("%s:%#v", operation, payload)))
}
func adminReplay(store adminStore, ctx context.Context, operation, actor, key string, digest [sha256.Size]byte) (bool, error) {
	receipt, found, err := store.ReadOperationReceiptWithin(ctx, operation, actor, key)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	if receipt.PayloadDigest != digest {
		return false, distributionport.ErrConflict
	}
	return true, nil
}

var errAdminReplay = errors.New("distribution admin command replay")

func paymentObservationReason(v paymentport.ProfitSharingInstruction) string {
	if failureClass := paymentObservationFailureClass(v); failureClass != "" {
		return "payment_" + failureClass
	}
	state := strings.ToLower(strings.TrimSpace(v.State))
	if state == "" {
		state = "unknown"
	}
	if len(state) > 120 {
		state = "unknown"
	}
	return "payment_" + state
}

// paymentObservationFailureClass accepts only the bounded vocabulary carried
// by Payment's stable port. Distribution never persists an arbitrary provider
// response as an operator-visible reason.
func paymentObservationFailureClass(v paymentport.ProfitSharingInstruction) string {
	switch v.FailureClass {
	case "receiver_account_abnormal", "receiver_relation_removed", "receiver_high_risk", "receiver_real_name_unverified", "merchant_permission_revoked", "receiver_receipt_limit", "payer_account_abnormal", "invalid_split_request":
		return v.FailureClass
	default:
		return ""
	}
}
func paymentObservationReference(v paymentport.ProfitSharingInstruction) string {
	return fmt.Sprintf("payment_instruction:%s:v%d", v.Reference, v.Version)
}

func unfreezeObservationReason(v paymentport.ProfitSharingUnfreeze) string {
	state := strings.ToLower(strings.TrimSpace(v.State))
	if state == "" || len(state) > 120 {
		state = "unknown"
	}
	return "payment_unfreeze_" + state
}
