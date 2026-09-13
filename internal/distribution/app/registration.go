package app

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"strings"
	"time"

	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	distributionstore "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/store"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type registrationStore interface {
	ActiveAgreementWithin(context.Context) (distributionstore.Agreement, error)
	ReadDistributorByCustomerWithin(context.Context, int64, bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error)
	InsertDistributorWithin(context.Context, int64, string, string, time.Time) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error)
	UpdateReceiverReadinessWithin(context.Context, int64, int64, distributionport.ReceiverReadiness, time.Time) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error)
	AppendAuditWithin(context.Context, string, string, int64, string, any, time.Time) error
	AppendOutboxWithin(context.Context, string, string, int64, any, time.Time) error
}

// RegistrationService owns a Distributor record. Receiver preparation is a
// separately retryable Payment intent: an unavailable Payment account never
// silently prevents a customer from registering or causes a duplicate public
// distributor number.
type RegistrationService struct {
	uow        platformport.UnitOfWork
	store      registrationStore
	settlement paymentport.DistributionSettlementPort
	now        func() time.Time
}

func NewRegistrationService(uow platformport.UnitOfWork, store registrationStore, settlement paymentport.DistributionSettlementPort) (*RegistrationService, error) {
	if uow == nil || store == nil || settlement == nil {
		return nil, distributionport.ErrUnavailable
	}
	return &RegistrationService{uow: uow, store: store, settlement: settlement, now: time.Now}, nil
}

func (s *RegistrationService) CurrentAgreement(ctx context.Context) (distributionport.Agreement, error) {
	if s == nil || s.uow == nil || s.store == nil {
		return distributionport.Agreement{}, distributionport.ErrUnavailable
	}
	var agreement distributionstore.Agreement
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		agreement, err = s.store.ActiveAgreementWithin(tx)
		return err
	})
	if err != nil {
		return distributionport.Agreement{}, err
	}
	return distributionport.Agreement{Version: agreement.Version, Content: agreement.Content}, nil
}

func (s *RegistrationService) Profile(ctx context.Context, actor distributionport.TrustedSessionActor) (distributionport.DistributorProfile, error) {
	if s == nil || s.uow == nil || s.store == nil || !actor.Valid() {
		return distributionport.DistributorProfile{}, distributionport.ErrUnauthorized
	}
	var agreement distributionstore.Agreement
	var distributor distributiondomain.Distributor
	var readiness distributionport.ReceiverReadiness
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		agreement, err = s.store.ActiveAgreementWithin(tx)
		if err != nil {
			return err
		}
		distributor, readiness, err = s.store.ReadDistributorByCustomerWithin(tx, actor.CustomerID, false)
		if errors.Is(err, distributionport.ErrNotFound) {
			return nil
		}
		return err
	})
	if err != nil {
		return distributionport.DistributorProfile{}, err
	}
	if distributor.ID == 0 {
		return distributionport.DistributorProfile{CurrentAgreementVersion: agreement.Version, RegistrationRequired: true}, nil
	}
	return distributionport.DistributorProfile{Distributor: distributor, Receiver: readiness, CurrentAgreementVersion: agreement.Version}, nil
}

func (s *RegistrationService) Register(ctx context.Context, command distributionport.RegisterCommand) (distributionport.DistributorProfile, error) {
	if s == nil || s.uow == nil || s.store == nil || !command.Actor.Valid() || command.AgreementVersion != strings.TrimSpace(command.AgreementVersion) || command.AgreementVersion == "" || len(command.AgreementVersion) > 100 || command.IdempotencyKey != strings.TrimSpace(command.IdempotencyKey) || len(command.IdempotencyKey) < 16 || len(command.IdempotencyKey) > 200 {
		return distributionport.DistributorProfile{}, distributionport.ErrConflict
	}
	var profile distributionport.DistributorProfile
	err := s.uow.Within(ctx, func(tx context.Context) error {
		agreement, err := s.store.ActiveAgreementWithin(tx)
		if err != nil {
			return err
		}
		if agreement.Version != command.AgreementVersion {
			return distributionport.ErrConflict
		}
		existing, readiness, err := s.store.ReadDistributorByCustomerWithin(tx, command.Actor.CustomerID, true)
		if err == nil {
			profile = distributionport.DistributorProfile{Distributor: existing, Receiver: readiness, CurrentAgreementVersion: agreement.Version}
			return nil
		}
		if !errors.Is(err, distributionport.ErrNotFound) {
			return err
		}
		now := s.now().UTC()
		var saved distributiondomain.Distributor
		for attempt := 0; attempt < 3; attempt++ {
			publicNo, numberErr := newPublicNumber()
			if numberErr != nil {
				return distributionport.ErrUnavailable
			}
			saved, readiness, err = s.store.InsertDistributorWithin(tx, command.Actor.CustomerID, publicNo, agreement.Version, now)
			if err == nil {
				break
			}
			if !errors.Is(err, distributionport.ErrConflict) {
				return err
			}
		}
		if saved.ID < 1 {
			return distributionport.ErrUnavailable
		}
		payload := map[string]any{"distributor_id": saved.ID, "agreement_version": agreement.Version}
		if err = s.store.AppendAuditWithin(tx, "distribution.distributor_registered.v1", "distributor", saved.ID, "customer:"+decimal(command.Actor.CustomerID), payload, now); err != nil {
			return err
		}
		if err = s.store.AppendOutboxWithin(tx, "distribution.distributor_registered.v1", "distribution.register:"+command.IdempotencyKey, saved.ID, payload, now); err != nil {
			return err
		}
		profile = distributionport.DistributorProfile{Distributor: saved, Receiver: readiness, CurrentAgreementVersion: agreement.Version}
		return nil
	})
	if err != nil {
		return distributionport.DistributorProfile{}, err
	}
	return profile, nil
}

func (s *RegistrationService) PrepareReceiver(ctx context.Context, actor distributionport.TrustedSessionActor) (distributionport.ReceiverPreparationResult, error) {
	if s == nil || s.uow == nil || s.store == nil || s.settlement == nil || !actor.Valid() {
		return distributionport.ReceiverPreparationResult{}, distributionport.ErrUnauthorized
	}
	var result distributionport.ReceiverPreparationResult
	err := s.uow.Within(ctx, func(tx context.Context) error {
		distributor, _, err := s.store.ReadDistributorByCustomerWithin(tx, actor.CustomerID, true)
		if err != nil {
			return err
		}
		channel := paymentdomain.ChannelMiniProgram
		if actor.Channel == "h5_official_account" {
			channel = paymentdomain.ChannelH5Official
		}
		key := "distribution.receiver:" + decimal(distributor.ID) + ":" + actor.AppID
		prepared, err := s.settlement.PrepareProfitSharingReceiverWithin(tx, paymentport.ReceiverPreparation{CustomerID: actor.CustomerID, IdentityID: actor.IdentityID, AppID: actor.AppID, AppScope: actor.AppScope, Channel: channel, IdempotencyKey: key, SourceDigest: effectport.Hash("distribution.receiver.v1", decimal(distributor.ID), actor.AppID), PayloadDigest: effectport.Hash("distribution.receiver.payload.v1", decimal(distributor.ID), actor.AppID, actor.Channel)})
		if err != nil {
			return err
		}
		now := s.now().UTC()
		readiness := distributionport.ReceiverReadiness{Ready: prepared.Ready, Reference: prepared.Reference, AppID: actor.AppID, CheckedAt: now}
		if !prepared.Ready {
			readiness.Reason = "receiver_" + safeReceiverState(prepared.State)
		}
		updated, persisted, err := s.store.UpdateReceiverReadinessWithin(tx, distributor.ID, distributor.Version, readiness, now)
		if err != nil {
			return err
		}
		payload := map[string]any{"distributor_id": updated.ID, "receiver_state": prepared.State, "ready": persisted.Ready}
		if err = s.store.AppendAuditWithin(tx, "distribution.receiver_prepared.v1", "distributor", updated.ID, "customer:"+decimal(actor.CustomerID), payload, now); err != nil {
			return err
		}
		result = distributionport.ReceiverPreparationResult{Receiver: persisted, ActionURL: "/api/v1/distribution/receiver-preparation", RetryAfterSec: 15}
		if persisted.Ready {
			result.State, result.RetryAfterSec = "ready", 0
		} else if prepared.State == "accepted" || prepared.State == "queued" || prepared.State == "attempted" {
			result.State = "processing"
		} else {
			result.State = "unavailable"
		}
		return nil
	})
	if err != nil {
		return distributionport.ReceiverPreparationResult{}, err
	}
	return result, nil
}

func newPublicNumber() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "D" + strings.TrimRight(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), "="), nil
}

func safeReceiverState(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 100 {
		return "unavailable"
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && r != '_' {
			return "unavailable"
		}
	}
	return value
}

func decimal(value int64) string {
	if value < 1 {
		return "0"
	}
	const digits = "0123456789"
	var buffer [20]byte
	i := len(buffer)
	for value > 0 {
		i--
		buffer[i] = digits[value%10]
		value /= 10
	}
	return string(buffer[i:])
}
