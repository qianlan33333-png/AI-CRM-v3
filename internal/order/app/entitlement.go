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
	ListServicePeriodMembers(context.Context, orderport.ServicePeriodMemberQuery) (orderport.ServicePeriodMemberPage, error)
	GetCustomerServicePeriodEntitlement(context.Context, int64, int64) (orderport.Entitlement, bool, error)
	FindEntitlementReceipt(context.Context, [32]byte) (orderport.Entitlement, [32]byte, string, bool, error)
	UpdateEntitlementRemark(context.Context, orderport.RemarkCommand, [32]byte, [32]byte, time.Time) (orderport.Entitlement, error)
	RecordEntitlementConflict(context.Context, orderport.RemarkCommand, [32]byte, [32]byte, orderport.Entitlement, time.Time) error
	ImportHistoricalEntitlement(context.Context, orderport.HistoricalEntitlement) (orderport.Entitlement, bool, error)
}

// EntitlementFulfillmentStore keeps payment-derived grant/refund facts within
// Order's existing entitlement aggregate. Every method requires the caller's
// PostgreSQL transaction; it does not query Payment or Product tables.
type EntitlementFulfillmentStore interface {
	GrantPaidServicePeriod(context.Context, orderport.ServicePeriodGrantCommand, [32]byte) (orderport.Entitlement, error)
	ApplyServicePeriodRefund(context.Context, orderport.ServicePeriodRefundCommand, [32]byte) (orderport.Entitlement, error)
	RecordHistoricalServicePeriodSource(context.Context, orderport.HistoricalServicePeriodSourceCommand) error
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

func (s *EntitlementApplication) ListServicePeriodMembers(ctx context.Context, query orderport.ServicePeriodMemberQuery) (orderport.ServicePeriodMemberPage, error) {
	if s == nil || query.ServiceProductID < 1 || query.Limit < 1 || query.Limit > 100 || (query.State != "" && query.State != "all" && query.State != "active" && query.State != "expired" && query.State != "removed") || (query.Source != "" && query.Source != "paid_order" && query.Source != "manual") || (query.Sort != "" && query.Sort != "updated_at_desc" && query.Sort != "starts_at_desc") || len(query.Cursor) > 1024 {
		return orderport.ServicePeriodMemberPage{}, orderport.ErrConflict
	}
	var page orderport.ServicePeriodMemberPage
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		var readErr error
		page, readErr = s.store.ListServicePeriodMembers(txctx, query)
		return readErr
	})
	return page, err
}

func (s *EntitlementApplication) GetCustomerServicePeriodEntitlement(ctx context.Context, customerID, serviceProductID int64) (orderport.Entitlement, bool, error) {
	if s == nil || customerID < 1 || serviceProductID < 1 {
		return orderport.Entitlement{}, false, orderport.ErrConflict
	}
	var item orderport.Entitlement
	var found bool
	err := s.uow.Within(ctx, func(txctx context.Context) error {
		var readErr error
		item, found, readErr = s.store.GetCustomerServicePeriodEntitlement(txctx, customerID, serviceProductID)
		return readErr
	})
	return item, found, err
}

func (s *EntitlementApplication) UpdateEntitlementRemark(ctx context.Context, command orderport.RemarkCommand) (orderport.Entitlement, error) {
	command.Remark = strings.TrimSpace(command.Remark)
	if command.EntitlementID < 1 || command.CustomerID < 1 || command.ServiceProductID < 0 || command.EmployeeID == "" || len(command.EmployeeID) > 1024 || len(command.Remark) > 500 || command.ExpectedVersion < 1 || len(command.IdempotencyKey) < 8 || len(command.IdempotencyKey) > 200 {
		return orderport.Entitlement{}, orderport.ErrConflict
	}
	payload, _ := json.Marshal([]any{command.EntitlementID, command.CustomerID, command.ServiceProductID, command.EmployeeID, command.Remark, command.ExpectedVersion})
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
				if item.ID == command.EntitlementID && (command.ServiceProductID == 0 || item.ServiceProductID == command.ServiceProductID) {
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

// EntitlementFulfillmentApplication is intentionally separate from the
// sidebar/read-and-remark application so the Payment seam remains a narrow,
// transaction-bound command port.
type EntitlementFulfillmentApplication struct {
	store EntitlementFulfillmentStore
}

var _ orderport.ServicePeriodEntitlementCoordinator = (*EntitlementFulfillmentApplication)(nil)
var _ orderport.HistoricalServicePeriodSourceCoordinator = (*EntitlementFulfillmentApplication)(nil)

func NewEntitlementFulfillmentApplication(store EntitlementFulfillmentStore) (*EntitlementFulfillmentApplication, error) {
	if store == nil {
		return nil, errors.New("order entitlement fulfillment store is required")
	}
	return &EntitlementFulfillmentApplication{store: store}, nil
}

func (s *EntitlementFulfillmentApplication) GrantPaidServicePeriodWithin(ctx context.Context, command orderport.ServicePeriodGrantCommand) (orderport.Entitlement, error) {
	command.ProductName = strings.TrimSpace(command.ProductName)
	command.PaidAt = command.PaidAt.UTC().Truncate(time.Microsecond)
	command.ProcessedAt = command.ProcessedAt.UTC().Truncate(time.Microsecond)
	if s == nil || s.store == nil || command.SourceOrderID < 1 || command.BeneficiaryCustomerID < 1 || command.ServiceProductID < 1 || command.ProductName == "" || len(command.ProductName) > 500 || command.DurationDays < 1 || command.PaidAt.IsZero() || command.ProcessedAt.IsZero() {
		return orderport.Entitlement{}, orderport.ErrConflict
	}
	// Delivery time is not part of the paid fact. Preserve the first successful
	// processing timestamp in the receipt, while retries of the same payment
	// replay even when they arrive at a later wall-clock time.
	payload, err := json.Marshal(struct {
		SourceOrderID         int64
		BeneficiaryCustomerID int64
		ServiceProductID      int64
		ProductName           string
		DurationDays          int32
		PaidAt                time.Time
	}{command.SourceOrderID, command.BeneficiaryCustomerID, command.ServiceProductID, command.ProductName, command.DurationDays, command.PaidAt})
	if err != nil {
		return orderport.Entitlement{}, orderport.ErrUnavailable
	}
	return s.store.GrantPaidServicePeriod(ctx, command, sha256.Sum256(payload))
}

func (s *EntitlementFulfillmentApplication) ApplyServicePeriodRefundWithin(ctx context.Context, command orderport.ServicePeriodRefundCommand) (orderport.Entitlement, error) {
	command.ProcessedAt = command.ProcessedAt.UTC().Truncate(time.Microsecond)
	if s == nil || s.store == nil || command.SourceOrderID < 1 || command.RefundAmountMinor < 1 || command.ProcessedAt.IsZero() {
		return orderport.Entitlement{}, orderport.ErrConflict
	}
	// A source order can revoke its period only once. The first amount and
	// processing time are stored with that receipt; later successful partial or
	// full refunds intentionally replay the same no-op result.
	payload, err := json.Marshal(struct{ SourceOrderID int64 }{command.SourceOrderID})
	if err != nil {
		return orderport.Entitlement{}, orderport.ErrUnavailable
	}
	return s.store.ApplyServicePeriodRefund(ctx, command, sha256.Sum256(payload))
}

func (s *EntitlementFulfillmentApplication) RecordHistoricalServicePeriodSourceWithin(ctx context.Context, command orderport.HistoricalServicePeriodSourceCommand) error {
	command.ServiceProductCode = strings.TrimSpace(command.ServiceProductCode)
	command.StartAt = command.StartAt.UTC().Truncate(time.Microsecond)
	command.EndAt = command.EndAt.UTC().Truncate(time.Microsecond)
	command.ImportedAt = command.ImportedAt.UTC().Truncate(time.Microsecond)
	if s == nil || s.store == nil || command.SourceOrderID < 1 || command.SourceLineNo < 1 || command.EntitlementID < 1 || command.ServiceProductID < 1 || command.ServiceProductCode == "" || len(command.ServiceProductCode) > 200 || command.DurationDays < 1 || command.StartAt.IsZero() || command.EndAt.IsZero() || !command.EndAt.After(command.StartAt) || command.ImportedAt.IsZero() {
		return orderport.ErrConflict
	}
	return s.store.RecordHistoricalServicePeriodSource(ctx, command)
}
