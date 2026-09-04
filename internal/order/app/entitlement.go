package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type EntitlementStore interface {
	ListCustomerEntitlements(context.Context, int64, int32) (orderport.EntitlementPage, error)
	FindEntitlementReceipt(context.Context, [32]byte) (orderport.Entitlement, [32]byte, string, bool, error)
	UpdateEntitlementRemark(context.Context, orderport.RemarkCommand, [32]byte, [32]byte, time.Time) (orderport.Entitlement, error)
	RecordEntitlementConflict(context.Context, orderport.RemarkCommand, [32]byte, [32]byte, orderport.Entitlement, time.Time) error
	ImportHistoricalEntitlement(context.Context, orderport.HistoricalEntitlement) (orderport.Entitlement, bool, error)
}

type EntitlementApplication struct {
	uow   platformport.UnitOfWork
	store EntitlementStore
	now   func() time.Time
}

func NewEntitlementApplication(uow platformport.UnitOfWork, store EntitlementStore) (*EntitlementApplication, error) {
	if uow == nil || store == nil {
		return nil, errors.New("order entitlement dependencies are required")
	}
	return &EntitlementApplication{uow: uow, store: store, now: time.Now}, nil
}

func (s *EntitlementApplication) ListCustomerEntitlements(ctx context.Context, customerID int64, limit int32) (orderport.EntitlementPage, error) {
	if customerID < 1 || limit < 1 || limit > 100 {
		return orderport.EntitlementPage{}, orderport.ErrConflict
	}
	var page orderport.EntitlementPage
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		var err error
		page, err = s.store.ListCustomerEntitlements(txctx, customerID, limit)
		return err
	})
	return page, err
}

func (s *EntitlementApplication) UpdateEntitlementRemark(ctx context.Context, command orderport.RemarkCommand) (orderport.Entitlement, error) {
	command.Remark = strings.TrimSpace(command.Remark)
	if command.EntitlementID < 1 || command.CustomerID < 1 || command.EmployeeID == "" || len(command.EmployeeID) > 1024 || len(command.Remark) > 500 || command.ExpectedVersion < 1 || len(command.IdempotencyKey) < 8 || len(command.IdempotencyKey) > 200 {
		return orderport.Entitlement{}, orderport.ErrConflict
	}
	payload, _ := json.Marshal([]any{command.EntitlementID, command.CustomerID, command.EmployeeID, command.Remark, command.ExpectedVersion})
	keyDigest, payloadDigest := sha256.Sum256([]byte(command.IdempotencyKey)), sha256.Sum256(payload)
	var result orderport.Entitlement
	conflicted := false
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		prior, priorPayload, outcome, found, err := s.store.FindEntitlementReceipt(txctx, keyDigest)
		if err != nil {
			return err
		}
		if found {
			if priorPayload != payloadDigest {
				return orderport.ErrConflict
			}
			result = prior
			conflicted = outcome == "version_conflict"
			return nil
		}
		result, err = s.store.UpdateEntitlementRemark(txctx, command, keyDigest, payloadDigest, s.now().UTC())
		if errors.Is(err, orderport.ErrConflict) {
			page, readErr := s.store.ListCustomerEntitlements(txctx, command.CustomerID, 100)
			if readErr != nil {
				return readErr
			}
			for _, item := range page.Items {
				if item.ID == command.EntitlementID {
					result = item
					break
				}
			}
			if result.ID == 0 {
				return orderport.ErrNotFound
			}
			if recErr := s.store.RecordEntitlementConflict(txctx, command, keyDigest, payloadDigest, result, s.now().UTC()); recErr != nil {
				return recErr
			}
			conflicted = true
			return nil
		}
		return err
	})
	if err == nil && conflicted {
		err = orderport.ErrConflict
	}
	return result, err
}

func (s *EntitlementApplication) ImportHistoricalEntitlement(ctx context.Context, input orderport.HistoricalEntitlement) (orderport.Entitlement, bool, error) {
	if input.SourceSystem == "" || input.SourceKey == "" || input.CustomerID < 1 || input.ServiceProductID < 1 || input.ProductName == "" || input.SourceDigest == ([32]byte{}) || input.StartAt.IsZero() || input.EndAt.Before(input.StartAt) {
		return orderport.Entitlement{}, false, orderport.ErrConflict
	}
	var result orderport.Entitlement
	var created bool
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		var err error
		result, created, err = s.store.ImportHistoricalEntitlement(txctx, input)
		return err
	})
	return result, created, err
}

var _ orderport.EntitlementService = (*EntitlementApplication)(nil)
var _ orderport.HistoricalEntitlementImporter = (*EntitlementApplication)(nil)
