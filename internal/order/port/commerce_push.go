package port

import (
	"context"
	"crypto/sha256"
	"strconv"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
)

// PaidEvent is the immutable Order-owned first-native-paid fact. It carries
// only canonical IDs and the already-frozen Order snapshot; an Outbound
// consumer must not infer a customer, mutate Order, or inspect Order tables.
type PaidEvent struct {
	ID                       int64
	OrderID                  int64
	OrderVersion             int64
	DomainEventOutboxID      int64
	CheckoutProductID        int64
	CheckoutGrossAmountMinor int64
	OccurredAt               time.Time
	SourceDigest             [32]byte
	Order                    domain.Snapshot
}

func (event PaidEvent) Valid() bool {
	return event.ValidOrderFact() && event.DomainEventOutboxID > 0 &&
		((event.CheckoutProductID == 0 && event.CheckoutGrossAmountMinor == 0) || (event.CheckoutProductID > 0 && event.CheckoutGrossAmountMinor > 0))
}

// ValidOrderFact allows Order persistence to construct the outbox fact before
// publishing the completed event to consumers. Consumers must always receive
// Valid events with the immutable Order-owned outbox ID present.
func (event PaidEvent) ValidOrderFact() bool {
	return event.ID > 0 && event.OrderID > 0 && event.OrderVersion > 0 && !event.OccurredAt.IsZero() &&
		event.SourceDigest != ([32]byte{}) && event.Order.ID == event.OrderID && event.Order.Version == event.OrderVersion &&
		event.Order.RecordOrigin == domain.RecordOriginNative && event.Order.EffectEligible && event.Order.Status == domain.StatusPaid
}

// NewPaidEventSourceDigest is stable across a lost response, callback replay,
// and later Product configuration changes. It deliberately contains no
// customer identifier or payload material.
func NewPaidEventSourceDigest(orderID, orderVersion int64) [32]byte {
	return sha256.Sum256([]byte("order.paid.v1\x00" + strconv.FormatInt(orderID, 10) + "\x00" + strconv.FormatInt(orderVersion, 10)))
}

// PaidEventConsumer is injected by composition. It joins the active Order or
// Payment Unit of Work; it never owns a second queue or transaction.
type PaidEventConsumer interface {
	ConsumePaidEventWithin(context.Context, PaidEvent) error
}
