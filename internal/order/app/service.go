// Package app coordinates Order transactions. It does not resolve external
// identities or call payment providers.
package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

const (
	DefaultLimit = int32(50)
	MaximumLimit = int32(100)
)

type Receipt struct {
	ID                       int64
	Operation                string
	ActorScope               string
	KeyDigest, PayloadDigest [32]byte
	State                    string
	ResultSnapshot           json.RawMessage
}

type Reservation struct {
	Operation                string
	ActorScope               string
	KeyDigest, PayloadDigest [32]byte
	CreatedAt                time.Time
}

type ImportReceipt struct {
	RunID        string
	SourceDigest [32]byte
	OrderID      int64
}

type Cursor struct {
	CreatedAt time.Time
	ID        int64
}

type ListFilter struct {
	Offset      int32
	Provider    domain.Provider
	Status      domain.Status
	OrderRef    string
	CustomerID  int64
	Product     string
	CreatedFrom *time.Time
	CreatedTo   *time.Time
}

type ExportReceipt struct {
	ID            int64
	Actor         int64
	KeyDigest     [32]byte
	FilterDigest  [32]byte
	RowCount      int
	ByteCount     int
	ContentDigest [32]byte
	CreatedAt     time.Time
}

// Store is private to Order app/store. Cross-domain callers use port only.
type Store interface {
	Reserve(context.Context, Reservation) (Receipt, bool, error)
	Complete(context.Context, int64, json.RawMessage, time.Time) (Receipt, error)
	Insert(context.Context, domain.Order, int64, time.Time) (domain.Order, error)
	Get(context.Context, int64, bool) (domain.Order, error)
	List(context.Context, *Cursor, int32, ListFilter) ([]domain.Order, error)
	Count(context.Context, ListFilter) (int64, error)
	FindByReference(context.Context, string) ([]domain.Order, error)
	Export(context.Context, ListFilter, int32) ([]domain.Order, error)
	RecordExport(context.Context, ExportReceipt) (ExportReceipt, bool, error)
	UpdateSettlement(context.Context, domain.Order, domain.StatusEvent, string) (domain.Order, error)
	Import(context.Context, string, [32]byte, domain.Order) (domain.Order, bool, error)
}

type Service struct {
	uow   platformport.UnitOfWork
	store Store
	now   func() time.Time
}

func NewService(uow platformport.UnitOfWork, store Store) *Service {
	return &Service{uow: uow, store: store, now: time.Now}
}

func (s *Service) Create(ctx context.Context, command orderport.CreateCommand) (domain.Snapshot, error) {
	if !ready(s) || command.Actor < 1 || !validKey(command.IdempotencyKey) || command.Input.RecordOrigin == domain.RecordOriginHistory {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	now := s.now().UTC()
	command.Input.RecordOrigin = domain.RecordOriginNative
	command.Input.CreatedAt = now
	order, err := domain.NewOrder(command.Input)
	if err != nil {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	// Creation time is assigned by the server and must not rotate the logical
	// command digest when the same idempotency key is replayed.
	digestInput := command.Input
	digestInput.CreatedAt = time.Time{}
	payload, err := json.Marshal(digestInput)
	if err != nil {
		return domain.Snapshot{}, orderport.ErrUnavailable
	}
	reservation := Reservation{
		Operation: "create", ActorScope: fmt.Sprintf("admin:%d", command.Actor),
		KeyDigest: sha256.Sum256([]byte(command.IdempotencyKey)), PayloadDigest: sha256.Sum256(payload), CreatedAt: now,
	}
	var result domain.Snapshot
	err = s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := s.store.Reserve(tx, reservation)
		if reserveErr != nil {
			return reserveErr
		}
		if !validReceipt(receipt, reservation) || subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
			return orderport.ErrConflict
		}
		if !owned {
			if receipt.State != "completed" || json.Unmarshal(receipt.ResultSnapshot, &result) != nil {
				return orderport.ErrUnavailable
			}
			_, restoreErr := domain.Restore(result)
			return restoreErr
		}
		persisted, insertErr := s.store.Insert(tx, order, command.Actor, now)
		if insertErr != nil {
			return insertErr
		}
		result = persisted.Snapshot()
		snapshot, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return marshalErr
		}
		completed, completeErr := s.store.Complete(tx, receipt.ID, snapshot, now)
		if completeErr != nil || completed.State != "completed" {
			return orderport.ErrUnavailable
		}
		return nil
	})
	if err != nil {
		return domain.Snapshot{}, classify(err)
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, id int64) (domain.Snapshot, error) {
	if !ready(s) || id < 1 {
		return domain.Snapshot{}, orderport.ErrNotFound
	}
	var result domain.Order
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var getErr error
		result, getErr = s.store.Get(tx, id, false)
		return getErr
	})
	if err != nil {
		return domain.Snapshot{}, classify(err)
	}
	return result.Snapshot(), nil
}

func (s *Service) List(ctx context.Context, query orderport.ListQuery) (orderport.Page, error) {
	if !ready(s) || !validListQuery(query) {
		return orderport.Page{}, orderport.ErrUnavailable
	}
	if query.Limit == 0 {
		query.Limit = DefaultLimit
	}
	if query.Limit < 1 || query.Limit > MaximumLimit {
		return orderport.Page{}, orderport.ErrConflict
	}
	var before *Cursor
	if query.Cursor != "" {
		decoded, err := decodeCursor(query.Cursor)
		if err != nil {
			return orderport.Page{}, orderport.ErrConflict
		}
		before = &decoded
	}
	var rows []domain.Order
	var total int64
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var listErr error
		total, listErr = s.store.Count(tx, filterFrom(query))
		if listErr != nil {
			return listErr
		}
		rows, listErr = s.store.List(tx, before, query.Limit+1, filterFrom(query))
		return listErr
	})
	if err != nil {
		return orderport.Page{}, classify(err)
	}
	if len(rows) > int(query.Limit)+1 {
		return orderport.Page{}, orderport.ErrUnavailable
	}
	page := orderport.Page{Items: make([]domain.Snapshot, 0, min(len(rows), int(query.Limit))), Total: total}
	for index, order := range rows {
		if index == int(query.Limit) {
			break
		}
		page.Items = append(page.Items, order.Snapshot())
	}
	if len(rows) > int(query.Limit) {
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeCursor(Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return page, nil
}

func (s *Service) GetByReference(ctx context.Context, reference string) (domain.Snapshot, error) {
	if !ready(s) || !validScope(reference) {
		return domain.Snapshot{}, orderport.ErrNotFound
	}
	var matches []domain.Order
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var findErr error
		matches, findErr = s.store.FindByReference(tx, reference)
		return findErr
	})
	if err != nil {
		return domain.Snapshot{}, classify(err)
	}
	if len(matches) == 0 {
		return domain.Snapshot{}, orderport.ErrNotFound
	}
	if len(matches) != 1 {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	return matches[0].Snapshot(), nil
}

func (s *Service) PreviewExport(ctx context.Context, query orderport.ListQuery) (orderport.ExportPreview, error) {
	if !ready(s) || !validListQuery(query) {
		return orderport.ExportPreview{}, orderport.ErrConflict
	}
	var rows []domain.Order
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var exportErr error
		rows, exportErr = s.store.Export(tx, filterFrom(query), 10001)
		return exportErr
	})
	if err != nil {
		return orderport.ExportPreview{}, classify(err)
	}
	return orderport.ExportPreview{Rows: min(len(rows), 10000), Truncated: len(rows) > 10000}, nil
}

func (s *Service) ExportCSV(ctx context.Context, query orderport.ListQuery, actor int64, idempotencyKey string) (orderport.ExportResult, error) {
	if !ready(s) || actor < 1 || !validKey(idempotencyKey) || !validListQuery(query) {
		return orderport.ExportResult{}, orderport.ErrConflict
	}
	filterPayload, _ := json.Marshal(filterFrom(query))
	receipt := ExportReceipt{Actor: actor, KeyDigest: sha256.Sum256([]byte(idempotencyKey)), FilterDigest: sha256.Sum256(filterPayload), CreatedAt: s.now().UTC()}
	var result orderport.ExportResult
	err := s.uow.Within(ctx, func(tx context.Context) error {
		rows, exportErr := s.store.Export(tx, filterFrom(query), 10001)
		if exportErr != nil {
			return exportErr
		}
		if len(rows) > 10000 {
			return orderport.ErrConflict
		}
		content := encodeCSV(rows)
		if len(content) > 5<<20 {
			return orderport.ErrConflict
		}
		receipt.RowCount, receipt.ByteCount, receipt.ContentDigest = len(rows), len(content), sha256.Sum256(content)
		stored, _, recordErr := s.store.RecordExport(tx, receipt)
		if recordErr != nil {
			return recordErr
		}
		if stored.FilterDigest != receipt.FilterDigest || stored.ContentDigest != receipt.ContentDigest || stored.RowCount != receipt.RowCount || stored.ByteCount != receipt.ByteCount {
			return orderport.ErrConflict
		}
		result = orderport.ExportResult{ReceiptID: stored.ID, Rows: len(rows), Bytes: len(content), Content: content, ContentDigest: receipt.ContentDigest}
		return nil
	})
	if err != nil {
		return orderport.ExportResult{}, classify(err)
	}
	return result, nil
}

func validListQuery(query orderport.ListQuery) bool {
	if query.CustomerID < 0 || query.Offset < 0 || query.Offset > 1000000 || (query.Cursor != "" && query.Offset != 0) || len(query.OrderRef) > 200 || len(query.Product) > 200 {
		return false
	}
	if query.Provider != "" && query.Provider != domain.ProviderWeChatPay && query.Provider != domain.ProviderWeChatShop && query.Provider != domain.ProviderAlipay {
		return false
	}
	if query.Status != "" {
		switch query.Status {
		case domain.StatusPendingPayment, domain.StatusPaid, domain.StatusPartiallyRefunded, domain.StatusRefunded, domain.StatusCancelled, domain.StatusPaymentFailed, domain.StatusClosed:
		default:
			return false
		}
	}
	return query.CreatedFrom == nil || query.CreatedTo == nil || !query.CreatedFrom.After(*query.CreatedTo)
}

func filterFrom(query orderport.ListQuery) ListFilter {
	return ListFilter{Offset: query.Offset, Provider: query.Provider, Status: query.Status, OrderRef: strings.TrimSpace(query.OrderRef), CustomerID: query.CustomerID, Product: strings.TrimSpace(query.Product), CreatedFrom: query.CreatedFrom, CreatedTo: query.CreatedTo}
}

func encodeCSV(orders []domain.Order) []byte {
	var builder strings.Builder
	builder.WriteString("created_at,merchant_order_no,provider_transaction_no,provider,payer_customer_id,beneficiary_customer_id,product_code,product_name,amount_minor,currency,status,record_origin\r\n")
	for _, order := range orders {
		snapshot := order.Snapshot()
		productCode, productName := "", ""
		if len(snapshot.Items) > 0 {
			productCode, productName = snapshot.Items[0].ProductCode, snapshot.Items[0].ProductName
		}
		values := []string{snapshot.CreatedAt.UTC().Format(time.RFC3339Nano), snapshot.MerchantOrderNo, snapshot.ProviderTransactionNo, string(snapshot.Provider), optionalID(snapshot.PayerCustomerID), optionalID(snapshot.BeneficiaryCustomerID), productCode, productName, strconv.FormatInt(snapshot.Amount.AmountMinor, 10), snapshot.Amount.Currency, string(snapshot.Status), string(snapshot.RecordOrigin)}
		for index, value := range values {
			if index > 0 {
				builder.WriteByte(',')
			}
			builder.WriteString(csvCell(value))
		}
		builder.WriteString("\r\n")
	}
	return []byte(builder.String())
}

func optionalID(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}

func csvCell(value string) string {
	if value != "" && strings.ContainsRune("=+-@", rune(value[0])) {
		value = "'" + value
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func (s *Service) ApplySettlement(ctx context.Context, command orderport.SettlementCommand) (domain.Snapshot, error) {
	if !ready(s) || command.OrderID < 1 || command.ExpectedVersion < 1 || !validKey(command.IdempotencyKey) || !validScope(command.ActorScope) {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	payload, _ := json.Marshal(command)
	reservation := Reservation{Operation: "settlement", ActorScope: command.ActorScope, KeyDigest: sha256.Sum256([]byte(command.IdempotencyKey)), PayloadDigest: sha256.Sum256(payload), CreatedAt: command.OccurredAt.UTC()}
	var result domain.Snapshot
	err := s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := s.store.Reserve(tx, reservation)
		if reserveErr != nil {
			return reserveErr
		}
		if !validReceipt(receipt, reservation) || subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
			return orderport.ErrConflict
		}
		if !owned {
			if receipt.State != "completed" || json.Unmarshal(receipt.ResultSnapshot, &result) != nil {
				return orderport.ErrUnavailable
			}
			_, restoreErr := domain.Restore(result)
			return restoreErr
		}
		current, getErr := s.store.Get(tx, command.OrderID, true)
		if getErr != nil {
			return getErr
		}
		updated, event, settleErr := current.ApplySettlement(command.ExpectedVersion, command.Status, command.RefundedMinor, command.OccurredAt.UTC())
		if settleErr != nil {
			return settleErr
		}
		if updated.Version != current.Version {
			updated, settleErr = s.store.UpdateSettlement(tx, updated, event, command.ActorScope)
			if settleErr != nil {
				return settleErr
			}
		}
		result = updated.Snapshot()
		snapshot, _ := json.Marshal(result)
		completed, completeErr := s.store.Complete(tx, receipt.ID, snapshot, command.OccurredAt.UTC())
		if completeErr != nil || completed.State != "completed" {
			return orderport.ErrUnavailable
		}
		return nil
	})
	if err != nil {
		return domain.Snapshot{}, classify(err)
	}
	return result, nil
}

func (s *Service) ImportHistorical(ctx context.Context, command orderport.HistoricalImportCommand) (domain.Snapshot, error) {
	if !ready(s) || !validScope(command.RunID) || command.SourceDigest == ([32]byte{}) || command.Order.RecordOrigin != domain.RecordOriginHistory || command.Order.EffectEligible {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	command.Order.ID = 0
	order, err := domain.Restore(command.Order)
	if err != nil {
		return domain.Snapshot{}, orderport.ErrConflict
	}
	var result domain.Order
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var importErr error
		result, _, importErr = s.store.Import(tx, command.RunID, command.SourceDigest, order)
		return importErr
	})
	if err != nil {
		return domain.Snapshot{}, classify(err)
	}
	return result.Snapshot(), nil
}

func ready(service *Service) bool {
	return service != nil && service.uow != nil && service.store != nil && service.now != nil
}

func validKey(value string) bool {
	return value == strings.TrimSpace(value) && len(value) >= 16 && len(value) <= 200
}
func validScope(value string) bool {
	return value == strings.TrimSpace(value) && len(value) >= 1 && len(value) <= 200
}

func validReceipt(receipt Receipt, reservation Reservation) bool {
	return receipt.ID > 0 && receipt.Operation == reservation.Operation && receipt.ActorScope == reservation.ActorScope &&
		subtle.ConstantTimeCompare(receipt.KeyDigest[:], reservation.KeyDigest[:]) == 1 && (receipt.State == "in_progress" || receipt.State == "completed")
}

func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, orderport.ErrNotFound):
		return orderport.ErrNotFound
	case errors.Is(err, orderport.ErrConflict), errors.Is(err, domain.ErrInvalidOrder), errors.Is(err, domain.ErrInvalidSettlement), errors.Is(err, domain.ErrInvalidTransition), errors.Is(err, domain.ErrVersionConflict):
		return orderport.ErrConflict
	default:
		return orderport.ErrUnavailable
	}
}

func encodeCursor(cursor Cursor) string {
	payload := cursor.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(cursor.ID, 10)
	digest := sha256.Sum256([]byte("aicrm-order-cursor-v1\x00" + payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload + "|" + hex.EncodeToString(digest[:8])))
}

func decodeCursor(value string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Cursor{}, err
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return Cursor{}, errors.New("invalid cursor")
	}
	payload := parts[0] + "|" + parts[1]
	digest := sha256.Sum256([]byte("aicrm-order-cursor-v1\x00" + payload))
	if subtle.ConstantTimeCompare([]byte(parts[2]), []byte(hex.EncodeToString(digest[:8]))) != 1 {
		return Cursor{}, errors.New("invalid cursor checksum")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return Cursor{}, err
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id < 1 {
		return Cursor{}, errors.New("invalid cursor id")
	}
	return Cursor{CreatedAt: createdAt.UTC(), ID: id}, nil
}
