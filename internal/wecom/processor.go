package wecom

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/provider"
)

// VerifiedIdentityReceiver is the local outbound port an identity adapter must
// implement. Only the trusted provider adapter constructs its VerifiedFact.
// Composition Root should adapt it to identity/port.VerifiedProvisioner and
// identity/port.Resolver without importing identity app/store here.
type VerifiedIdentityReceiver interface {
	ProvisionVerifiedWeComIdentity(context.Context, identitydomain.VerifiedFact) (customerdomain.CustomerID, error)
	FindVerifiedWeComIdentity(context.Context, identitydomain.VerifiedFact) (customerdomain.CustomerID, bool, error)
}

type FollowRelationship struct {
	CorpID     string
	EmployeeID string
	CustomerID customerdomain.CustomerID
	Active     bool
	UpdatedAt  time.Time
}

type FollowRelationshipStore interface {
	Upsert(ctx context.Context, relationship FollowRelationship) error
	IsActive(ctx context.Context, corpID, employeeID string, customerID customerdomain.CustomerID) (bool, error)
}

// InboxProcessor is invoked by an external oneshot worker. It deliberately has
// no ticker, scheduler, goroutine, or provider network client.
type InboxProcessor struct {
	Enabled       bool
	CorpID        string
	Inbox         *webhook.Service
	UOW           platformport.UnitOfWork
	Identity      VerifiedIdentityReceiver
	Relationships FollowRelationshipStore
	Audit         *audit.Service
}

func (processor InboxProcessor) ProcessOnce(ctx context.Context, owner string, limit int) (int, error) {
	if !processor.Enabled {
		return 0, ErrProviderDisabled
	}
	if processor.Inbox == nil || processor.UOW == nil || processor.Identity == nil || processor.Relationships == nil || processor.Audit == nil || strings.TrimSpace(processor.CorpID) != processor.CorpID || processor.CorpID == "" {
		return 0, errors.New("wecom inbox processor dependencies are required")
	}
	var deliveries []webhook.Delivery
	if err := processor.UOW.Within(ctx, func(txContext context.Context) error {
		var err error
		deliveries, err = processor.Inbox.Claim(txContext, webhook.Claim{Provider: "wecom.external_contact", Owner: owner, Limit: limit, LeaseDuration: time.Minute})
		return err
	}); err != nil {
		return 0, err
	}
	for _, delivery := range deliveries {
		if err := processor.processDelivery(ctx, delivery); err != nil {
			return 0, err
		}
	}
	return len(deliveries), nil
}

func (processor InboxProcessor) processDelivery(ctx context.Context, delivery webhook.Delivery) error {
	return processor.UOW.Within(ctx, func(txContext context.Context) error {
		var event CallbackEvent
		if err := json.Unmarshal(delivery.Payload, &event); err != nil || !event.supported() {
			_, completeErr := processor.Inbox.Complete(txContext, webhook.Completion{ID: delivery.ID, ExpectedAttempt: delivery.AttemptCount, Status: webhook.StatusFailed, LastErrorCode: "invalid_callback_payload"})
			return completeErr
		}
		fact, err := provider.VerifiedExternalContact(processor.CorpID, event.ExternalUserID, "wecom.callback")
		if err != nil {
			_, completeErr := processor.Inbox.Complete(txContext, webhook.Completion{ID: delivery.ID, ExpectedAttempt: delivery.AttemptCount, Status: webhook.StatusFailed, LastErrorCode: "invalid_verified_fact"})
			return completeErr
		}
		active := event.ChangeType != "del_follow_user"
		var customerID customerdomain.CustomerID
		if active {
			customerID, err = processor.Identity.ProvisionVerifiedWeComIdentity(txContext, fact)
			if err != nil {
				return processor.retry(txContext, delivery, "identity_provision")
			}
		} else {
			var found bool
			customerID, found, err = processor.Identity.FindVerifiedWeComIdentity(txContext, fact)
			if err != nil {
				return processor.retry(txContext, delivery, "identity_resolve")
			}
			if !found {
				_, completeErr := processor.Inbox.Complete(txContext, webhook.Completion{ID: delivery.ID, ExpectedAttempt: delivery.AttemptCount, Status: webhook.StatusProcessed, LastErrorCode: ""})
				return completeErr
			}
		}
		if err = processor.Relationships.Upsert(txContext, FollowRelationship{CorpID: processor.CorpID, EmployeeID: event.UserID, CustomerID: customerID, Active: active}); err != nil {
			return processor.retry(txContext, delivery, "follow_relationship")
		}
		auditKey, _ := idempotency.Parse("wecom:follow:" + stableEventKey(event))
		if _, err = processor.Audit.Append(txContext, audit.Event{IdempotencyKey: auditKey, Action: "wecom.follow_relationship_updated", ActorType: "provider", ResourceType: "customer", ResourceID: customerIDString(customerID), Payload: json.RawMessage(`{"source":"wecom.callback"}`)}); err != nil && !errors.Is(err, audit.ErrDuplicateEvent) {
			return processor.retry(txContext, delivery, "audit_append")
		}
		_, err = processor.Inbox.Complete(txContext, webhook.Completion{ID: delivery.ID, ExpectedAttempt: delivery.AttemptCount, Status: webhook.StatusProcessed, LastErrorCode: ""})
		return err
	})
}

func (processor InboxProcessor) retry(ctx context.Context, delivery webhook.Delivery, code string) error {
	next := time.Now().UTC().Add(time.Minute)
	_, err := processor.Inbox.Complete(ctx, webhook.Completion{ID: delivery.ID, ExpectedAttempt: delivery.AttemptCount, Status: webhook.StatusRetryable, LastErrorCode: code, NextAttemptAt: &next})
	return err
}

func customerIDString(id customerdomain.CustomerID) string {
	return strconv.FormatInt(int64(id), 10)
}
