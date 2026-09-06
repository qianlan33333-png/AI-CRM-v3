package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"encoding/json"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformoutbox "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/outbox"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var (
	ErrOwnerHandoffInvalid   = errors.New("owner handoff command invalid")
	ErrOwnerHandoffExpired   = errors.New("owner handoff preview expired")
	ErrOwnerHandoffForbidden = errors.New("owner handoff target unavailable")
	ErrOwnerHandoffDrift     = errors.New("owner handoff preview changed")
)

type OwnerHandoffStore interface {
	CreateOwnerHandoffPreview(context.Context, customerport.OwnerHandoffPreviewRecord) (customerport.OwnerHandoffPreview, error)
	LoadOwnerHandoffPreview(context.Context, string, bool) (customerport.OwnerHandoffPreviewRecord, error)
	CreateLocalOnlyOwnerHandoffBatch(context.Context, customerport.OwnerHandoffBatchRecord) (customerport.OwnerHandoffBatch, error)
	CreateWeComOwnerHandoffBatch(context.Context, customerport.OwnerHandoffBatchRecord) (customerport.OwnerHandoffBatch, error)
	BindOwnerHandoffEffect(context.Context, customerport.OwnerHandoffEffectBinding) error
	OwnerHandoffBatchByIdempotency(context.Context, int64, string) (customerport.OwnerHandoffBatch, [32]byte, bool, error)
	LocalOwner(context.Context, customerdomain.CustomerID, bool) (customerport.LocalOwner, bool, error)
	AssignLocalOwner(context.Context, customerdomain.CustomerID, int64, int64, string, time.Time) (customerport.LocalOwner, error)
}

type ownerHandoffStaffReader interface {
	UserByID(context.Context, int64, bool) (accessdomain.User, error)
}

type OwnerHandoffService struct {
	uow      platformport.UnitOfWork
	store    OwnerHandoffStore
	staff    ownerHandoffStaffReader
	resolver customerport.OwnerHandoffCandidateResolver
	audit    interface {
		Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
	}
	outbox  platformoutbox.Appender
	effects effectport.TransactionalAccepter
	now     func() time.Time
	newID   func() (string, error)
}

func NewOwnerHandoffService(uow platformport.UnitOfWork, store OwnerHandoffStore, staff ownerHandoffStaffReader, resolver customerport.OwnerHandoffCandidateResolver, audit interface {
	Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
}, outbox platformoutbox.Appender) (*OwnerHandoffService, error) {
	if uow == nil || store == nil || staff == nil || resolver == nil || audit == nil || outbox == nil {
		return nil, errors.New("owner handoff dependencies are required")
	}
	return &OwnerHandoffService{uow: uow, store: store, staff: staff, resolver: resolver, audit: audit, outbox: outbox, now: time.Now, newID: ownerHandoffID}, nil
}

// SetExternalEffectAccepter installs the established EER transactional port.
// Provider mode fails closed until composition supplies this dependency; it
// never silently degrades to local_only.
func (service *OwnerHandoffService) SetExternalEffectAccepter(accepter effectport.TransactionalAccepter) error {
	if service == nil || accepter == nil {
		return errors.New("owner handoff external-effects accepter is required")
	}
	service.effects = accepter
	return nil
}

func (service *OwnerHandoffService) PreviewOwnerHandoff(ctx context.Context, command customerport.OwnerHandoffPreviewCommand) (customerport.OwnerHandoffPreview, error) {
	if err := validOwnerHandoffPreview(command); err != nil {
		return customerport.OwnerHandoffPreview{}, err
	}
	var out customerport.OwnerHandoffPreview
	err := service.uow.Within(ctx, func(txctx context.Context) error {
		source, err := service.staff.UserByID(txctx, command.SourceStaffID, false)
		if err != nil {
			return ErrOwnerHandoffForbidden
		}
		// Legacy local_only permits a source employee which has since been
		// disabled. The target is the only party that must remain active.
		if source.ID != command.SourceStaffID {
			return ErrOwnerHandoffForbidden
		}
		target, err := service.staff.UserByID(txctx, command.TargetStaffID, false)
		if err != nil || !target.Active {
			return ErrOwnerHandoffForbidden
		}
		candidates, err := service.resolver.ResolveOwnerHandoffCandidates(txctx, command.SourceStaffID, command.TargetStaffID, command.CorpScope, command.CustomerIDs)
		if err != nil {
			return err
		}
		if len(candidates) != len(command.CustomerIDs) {
			return ErrOwnerHandoffDrift
		}
		for index, candidate := range candidates {
			if candidate.CustomerID != command.CustomerIDs[index] {
				return ErrOwnerHandoffDrift
			}
		}
		id, err := service.newID()
		if err != nil {
			return err
		}
		requestDigest := ownerHandoffPreviewDigest(command, candidates)
		draft := customerport.OwnerHandoffPreviewRecord{ActorAdminUserID: command.ActorAdminUserID, Preview: customerport.OwnerHandoffPreview{ID: id, Mode: command.Mode, SourceStaffID: command.SourceStaffID, TargetStaffID: command.TargetStaffID, CorpScope: command.CorpScope, Hash: hex.EncodeToString(requestDigest[:]), ConfirmationPhrase: command.ConfirmationPhrase, ExpiresAt: service.now().UTC().Add(30 * time.Minute)}, WelcomeMessage: command.WelcomeMessage, Candidates: candidates, RequestDigest: requestDigest}
		out, err = service.store.CreateOwnerHandoffPreview(txctx, draft)
		return err
	})
	return out, err
}

func (service *OwnerHandoffService) ConfirmOwnerHandoff(ctx context.Context, command customerport.OwnerHandoffConfirmCommand) (customerport.OwnerHandoffBatch, error) {
	if command.ActorAdminUserID < 1 || command.PreviewID == "" || command.PreviewHash == "" || strings.TrimSpace(command.ConfirmationPhrase) == "" || strings.TrimSpace(command.IdempotencyKey) != command.IdempotencyKey || len(command.IdempotencyKey) < 8 {
		return customerport.OwnerHandoffBatch{}, ErrOwnerHandoffInvalid
	}
	var out customerport.OwnerHandoffBatch
	err := service.uow.Within(ctx, func(txctx context.Context) error {
		confirmDigest := sha256.Sum256([]byte(command.PreviewID + "\x00" + command.PreviewHash + "\x00" + command.ConfirmationPhrase))
		if prior, priorDigest, found, findErr := service.store.OwnerHandoffBatchByIdempotency(txctx, command.ActorAdminUserID, command.IdempotencyKey); findErr != nil {
			return findErr
		} else if found {
			if priorDigest != confirmDigest {
				return ErrOwnerHandoffDrift
			}
			out = prior
			return nil
		}
		draft, err := service.store.LoadOwnerHandoffPreview(txctx, command.PreviewID, true)
		if err != nil {
			return err
		}
		if draft.ActorAdminUserID != command.ActorAdminUserID || draft.Preview.Hash != command.PreviewHash || draft.Preview.ConfirmationPhrase != command.ConfirmationPhrase {
			return ErrOwnerHandoffDrift
		}
		if !service.now().UTC().Before(draft.Preview.ExpiresAt) {
			return ErrOwnerHandoffExpired
		}
		target, err := service.staff.UserByID(txctx, draft.Preview.TargetStaffID, false)
		if err != nil || !target.Active {
			return ErrOwnerHandoffForbidden
		}
		customerIDs := make([]customerdomain.CustomerID, 0, len(draft.Candidates))
		for _, candidate := range draft.Candidates {
			customerIDs = append(customerIDs, candidate.CustomerID)
		}
		current, resolveErr := service.resolver.ResolveOwnerHandoffCandidates(txctx, draft.Preview.SourceStaffID, draft.Preview.TargetStaffID, draft.Preview.CorpScope, customerIDs)
		if resolveErr != nil || !sameOwnerHandoffCandidates(draft.Candidates, current) {
			return ErrOwnerHandoffDrift
		}
		if draft.Preview.Mode == customerport.OwnerHandoffWeComThenCRM {
			if service.effects == nil {
				return ErrOwnerHandoffForbidden
			}
			lines := make([]customerport.OwnerHandoffLine, 0, len(draft.Candidates))
			for _, candidate := range draft.Candidates {
				state := candidate.State
				if state == "ready" {
					state = "queued"
				}
				lines = append(lines, customerport.OwnerHandoffLine{Line: int64(len(lines) + 1), CustomerID: candidate.CustomerID, State: state})
			}
			batch, createErr := service.store.CreateWeComOwnerHandoffBatch(txctx, customerport.OwnerHandoffBatchRecord{Preview: draft, ActorID: command.ActorAdminUserID, Idempotency: command.IdempotencyKey, RequestDigest: confirmDigest, Lines: lines})
			if createErr != nil {
				return createErr
			}
			for _, line := range batch.Lines {
				if line.State != "queued" {
					continue
				}
				accept := effectport.AcceptCommand{ReceiptKey: effectport.Hash("customer-owner-handoff.accept.v1", batch.ID, strconv.FormatInt(line.Line, 10)), Envelope: effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindCustomerOwnerHandoff, SourceRefDigest: effectport.Hash("customer-owner-handoff.v1", "source-ref", batch.ID, strconv.FormatInt(line.Line, 10)), TargetRefDigest: effectport.Hash("customer-owner-handoff.v1", "target-ref", batch.ID, strconv.FormatInt(line.Line, 10)), PayloadDigest: effectport.Hash("customer-owner-handoff.v1", "payload-ref", batch.ID, strconv.FormatInt(line.Line, 10)), PolicyVersionHash: effectport.Hash("customer-owner-handoff.v1", "policy-ref", batch.ID, strconv.FormatInt(line.Line, 10))}}
				// The opaque payload/policy EER digests are checked against the frozen
				// Customer snapshot by the Outbound provider immediately before call.
				projection, receipt, acceptErr := service.effects.AcceptAndQueueWithin(txctx, accept)
				if acceptErr != nil {
					return acceptErr
				}
				if bindErr := service.store.BindOwnerHandoffEffect(txctx, customerport.OwnerHandoffEffectBinding{BatchID: batch.ID, Line: line.Line, EffectID: projection.ID, ReceiptID: receipt.ID}); bindErr != nil {
					return bindErr
				}
				if factErr := service.appendProviderAcceptedFacts(txctx, command.ActorAdminUserID, draft.Preview.ID, line, service.now().UTC()); factErr != nil {
					return factErr
				}
			}
			loaded, _, found, readErr := service.store.OwnerHandoffBatchByIdempotency(txctx, command.ActorAdminUserID, command.IdempotencyKey)
			if readErr != nil {
				return readErr
			}
			if !found {
				return ErrOwnerHandoffDrift
			}
			out = loaded
			return nil
		}
		if draft.Preview.Mode != customerport.OwnerHandoffLocalOnly {
			return ErrOwnerHandoffDrift
		}
		lines := make([]customerport.OwnerHandoffLine, 0, len(draft.Candidates))
		for _, candidate := range draft.Candidates {
			line := customerport.OwnerHandoffLine{Line: int64(len(lines) + 1), CustomerID: candidate.CustomerID, State: candidate.State}
			if candidate.State != "ready" {
				lines = append(lines, line)
				continue
			}
			owner, found, readErr := service.store.LocalOwner(txctx, candidate.CustomerID, true)
			if readErr != nil || (found && owner.Version != candidate.ExpectedLocalVersion) || (!found && candidate.ExpectedLocalVersion != 0) {
				line.State = "cas_conflict"
				lines = append(lines, line)
				continue
			}
			if _, readErr = service.store.AssignLocalOwner(txctx, candidate.CustomerID, draft.Preview.TargetStaffID, candidate.ExpectedLocalVersion, "owner_handoff_local_only", service.now().UTC()); readErr != nil {
				if !errors.Is(readErr, customerport.ErrOwnerHandoffConflict) {
					return readErr
				}
				line.State = "cas_conflict"
			} else {
				line.State = "local_updated"
				if readErr = service.appendLocalOnlyFacts(txctx, command.ActorAdminUserID, draft.Preview.ID, line, service.now().UTC()); readErr != nil {
					return readErr
				}
			}
			lines = append(lines, line)
		}
		out, err = service.store.CreateLocalOnlyOwnerHandoffBatch(txctx, customerport.OwnerHandoffBatchRecord{Preview: draft, ActorID: command.ActorAdminUserID, Idempotency: command.IdempotencyKey, RequestDigest: confirmDigest, Lines: lines})
		return err
	})
	return out, err
}

func (service *OwnerHandoffService) appendProviderAcceptedFacts(ctx context.Context, actorID int64, previewID string, line customerport.OwnerHandoffLine, at time.Time) error {
	key, err := idempotency.Parse("customer-owner-handoff-provider:" + previewID + ":" + int64String(line.Line))
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"mode": "wecom_then_crm", "result": "queued"})
	if _, err = service.audit.Append(ctx, platformaudit.Event{IdempotencyKey: key, Action: "customer.owner_handoff.wecom_accepted", ActorType: "admin", ActorID: int64String(actorID), ResourceType: "customer", ResourceID: int64String(int64(line.CustomerID)), Payload: payload, OccurredAt: at}); err != nil && !errors.Is(err, platformaudit.ErrDuplicateEvent) {
		return err
	}
	_, err = service.outbox.Append(ctx, platformoutbox.Event{AggregateType: "customer", AggregateID: int64String(int64(line.CustomerID)), Type: "customer.owner_handoff.wecom_accepted.v1", Version: 1, IdempotencyKey: string(key), Payload: payload, OccurredAt: at})
	return err
}

func (service *OwnerHandoffService) appendLocalOnlyFacts(ctx context.Context, actorID int64, previewID string, line customerport.OwnerHandoffLine, at time.Time) error {
	key, err := idempotency.Parse("customer-owner-handoff-local:" + previewID + ":" + int64String(line.Line))
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"mode": "local_only", "result": line.State})
	if _, err = service.audit.Append(ctx, platformaudit.Event{IdempotencyKey: key, Action: "customer.owner_handoff.local_updated", ActorType: "admin", ActorID: int64String(actorID), ResourceType: "customer", ResourceID: int64String(int64(line.CustomerID)), Payload: payload, OccurredAt: at}); err != nil && !errors.Is(err, platformaudit.ErrDuplicateEvent) {
		return err
	}
	_, err = service.outbox.Append(ctx, platformoutbox.Event{AggregateType: "customer", AggregateID: int64String(int64(line.CustomerID)), Type: "customer.owner_handoff.local_updated.v1", Version: 1, IdempotencyKey: string(key), Payload: payload, OccurredAt: at})
	return err
}

func sameOwnerHandoffCandidates(frozen, current []customerport.OwnerHandoffCandidate) bool {
	if len(frozen) != len(current) {
		return false
	}
	for index := range frozen {
		if frozen[index].CustomerID != current[index].CustomerID || frozen[index].State != current[index].State || frozen[index].RelationshipDigest != current[index].RelationshipDigest {
			return false
		}
	}
	return true
}

func validOwnerHandoffPreview(command customerport.OwnerHandoffPreviewCommand) error {
	if command.ActorAdminUserID < 1 || (command.Mode != customerport.OwnerHandoffLocalOnly && command.Mode != customerport.OwnerHandoffWeComThenCRM) || command.SourceStaffID < 1 || command.TargetStaffID < 1 || command.SourceStaffID == command.TargetStaffID || !strings.HasPrefix(command.CorpScope, "wecom-corp:") || len(command.CustomerIDs) == 0 || len(command.CustomerIDs) > 20000 || strings.TrimSpace(command.ConfirmationPhrase) == "" || len([]rune(command.WelcomeMessage)) > 4000 {
		return ErrOwnerHandoffInvalid
	}
	seen := make(map[customerdomain.CustomerID]struct{}, len(command.CustomerIDs))
	for _, customerID := range command.CustomerIDs {
		if customerID < 1 {
			return ErrOwnerHandoffInvalid
		}
		if _, exists := seen[customerID]; exists {
			return ErrOwnerHandoffInvalid
		}
		seen[customerID] = struct{}{}
	}
	return nil
}

func ownerHandoffPreviewDigest(command customerport.OwnerHandoffPreviewCommand, candidates []customerport.OwnerHandoffCandidate) [32]byte {
	parts := []string{"owner-handoff-preview-v1", string(command.Mode), int64String(command.SourceStaffID), int64String(command.TargetStaffID), command.CorpScope, command.WelcomeMessage, command.ConfirmationPhrase}
	for _, customerID := range command.CustomerIDs {
		parts = append(parts, "requested", int64String(int64(customerID)))
	}
	for _, candidate := range candidates {
		parts = append(parts, "frozen", int64String(int64(candidate.CustomerID)), int64String(candidate.ExpectedLocalOwnerID), int64String(candidate.ExpectedLocalVersion), candidate.State, candidate.Reason, hex.EncodeToString(candidate.RelationshipDigest[:]))
		for _, value := range []string{candidate.SourceUserID, candidate.TargetUserID, candidate.ExternalUserID} {
			if value == "" {
				parts = append(parts, "")
			} else {
				digest := sha256.Sum256([]byte(value))
				parts = append(parts, hex.EncodeToString(digest[:]))
			}
		}
	}
	return sha256.Sum256([]byte(strings.Join(parts, "\x00")))
}

func int64String(value int64) string { return strconv.FormatInt(value, 10) }

func ownerHandoffID() (string, error) {
	bytes := make([]byte, 18)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "owner_handoff_" + hex.EncodeToString(bytes), nil
}

var _ customerport.OwnerHandoffService = (*OwnerHandoffService)(nil)
