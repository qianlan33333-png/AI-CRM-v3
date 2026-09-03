package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

const (
	commerceExternalPushSaveOperation = "external_push_save"
	commerceExternalPushTestOperation = "external_push_test"
)

var ErrExternalPushNotConfigured = errors.New("product external push is not locally configured")

// CommerceExternalPushStore owns Product-local configuration, command
// receipts and immutable test bindings. It carries no Provider adapter.
type CommerceExternalPushStore interface {
	ReadCommerceExternalPushConfiguration(context.Context, productport.ID, productport.ExternalPushProductKind) (productport.ExternalPushConfiguration, error)
	LockCommerceExternalPushConfiguration(context.Context, productport.ID, productport.ExternalPushProductKind) (productport.ExternalPushConfiguration, error)
	SaveCommerceExternalPushConfiguration(context.Context, productport.ExternalPushConfiguration, time.Time) (productport.ExternalPushConfiguration, error)
	ReserveCommerceExternalPush(context.Context, Reservation) (Receipt, bool, error)
	CompleteCommerceExternalPush(context.Context, int64, json.RawMessage, time.Time) (Receipt, error)
	CommerceExternalPushTestExists(context.Context, productport.ID, productport.ExternalPushProductKind, [32]byte) (bool, error)
	CreateCommerceExternalPushTest(context.Context, productport.ExternalPushTest, [32]byte, int64) (productport.ExternalPushTest, error)
}

// ProductExternalPushEffectAccepter is the narrow adapter seam to EER. Its
// input has only server-computed digests, and successful output remains a
// local accepted/queued fact rather than a Provider receipt.
type ProductExternalPushEffectAccepter interface {
	AcceptProductExternalPushTest(context.Context, ProductExternalPushEffectCommand) (productport.ExternalPushTest, error)
}

type ProductExternalPushEffectCommand struct {
	ProductID           productport.ID
	ProductKind         productport.ExternalPushProductKind
	ConfigurationDigest [32]byte
	ReceiptKeyDigest    [32]byte
}

type CommerceExternalPushService struct {
	uow     platformport.UnitOfWork
	store   CommerceExternalPushStore
	effects ProductExternalPushEffectAccepter
	events  productport.EventAppender
	now     func() time.Time
}

var _ productport.CommerceExternalPushApplication = (*CommerceExternalPushService)(nil)

func NewCommerceExternalPushService(
	uow platformport.UnitOfWork,
	store CommerceExternalPushStore,
	effects ProductExternalPushEffectAccepter,
	events productport.EventAppender,
) (*CommerceExternalPushService, error) {
	if events == nil {
		return nil, errors.New("product external push event appender is required")
	}
	return &CommerceExternalPushService{uow: uow, store: store, effects: effects, events: events, now: time.Now}, nil
}

func (service *CommerceExternalPushService) GetExternalPushConfiguration(
	ctx context.Context,
	productID productport.ID,
	kind productport.ExternalPushProductKind,
) (productport.ExternalPushConfiguration, error) {
	if !commerceExternalPushReady(service) || ctx == nil || ctx.Err() != nil {
		return productport.ExternalPushConfiguration{}, ErrUnavailable
	}
	if productID < 1 || !validExternalPushKind(kind) {
		return productport.ExternalPushConfiguration{}, ErrInvalidProduct
	}
	var result productport.ExternalPushConfiguration
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = service.store.ReadCommerceExternalPushConfiguration(tx, productID, kind)
		return readErr
	})
	if err != nil {
		return productport.ExternalPushConfiguration{}, classifyCommerceExternalPush(err)
	}
	if !validExternalPushConfiguration(result, productID, kind) {
		return productport.ExternalPushConfiguration{}, ErrUnavailable
	}
	return result, nil
}

func (service *CommerceExternalPushService) SaveExternalPushConfiguration(
	ctx context.Context,
	command productport.SaveExternalPushConfigurationCommand,
) (productport.ExternalPushConfiguration, error) {
	if !commerceExternalPushReady(service) || ctx == nil || ctx.Err() != nil {
		return productport.ExternalPushConfiguration{}, ErrUnavailable
	}
	if !validSaveCommerceExternalPush(command) {
		return productport.ExternalPushConfiguration{}, ErrInvalidProduct
	}
	now := service.now().UTC()
	if now.IsZero() {
		return productport.ExternalPushConfiguration{}, ErrUnavailable
	}
	payloadDigest := commerceExternalPushSaveDigest(command)
	reservation := commerceExternalPushReservation(commerceExternalPushSaveOperation, command.Actor, command.IdempotencyKey, payloadDigest, now)
	var result productport.ExternalPushConfiguration
	err := service.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := service.store.ReserveCommerceExternalPush(tx, reservation)
		if reserveErr != nil {
			return reserveErr
		}
		if !validCommerceExternalPushReceipt(receipt, reservation) {
			return ErrUnavailable
		}
		if subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
			return ErrConflict
		}
		if !owned {
			return decodeCommerceExternalPushSnapshot(receipt.ResultSnapshot, &result, command.ProductID, command.ProductKind)
		}
		value := productport.ExternalPushConfiguration{
			ProductID: command.ProductID, ProductKind: command.ProductKind,
			Enabled: command.Enabled, ConfigurationReference: command.ConfigurationReference,
		}
		result, reserveErr = service.store.SaveCommerceExternalPushConfiguration(tx, value, now)
		if reserveErr != nil {
			return reserveErr
		}
		if !validExternalPushConfiguration(result, command.ProductID, command.ProductKind) ||
			result.Enabled != command.Enabled || result.ConfigurationReference != command.ConfigurationReference {
			return ErrUnavailable
		}
		if eventErr := service.appendEvent(tx, productport.EventExternalPushConfigurationSaved, command.ProductID, command.ProductKind, command.Actor, reservation.KeyDigest, map[string]any{
			"enabled": result.Enabled,
		}); eventErr != nil {
			return eventErr
		}
		return service.completeCommerceExternalPush(tx, receipt.ID, result, now)
	})
	if err != nil {
		return productport.ExternalPushConfiguration{}, classifyCommerceExternalPush(err)
	}
	return result, nil
}

func (service *CommerceExternalPushService) QueueExternalPushTest(
	ctx context.Context,
	command productport.QueueExternalPushTestCommand,
) (productport.ExternalPushTest, error) {
	if !commerceExternalPushReady(service) || service.effects == nil || ctx == nil || ctx.Err() != nil {
		return productport.ExternalPushTest{}, ErrUnavailable
	}
	if !validQueueCommerceExternalPushTest(command) {
		return productport.ExternalPushTest{}, ErrInvalidProduct
	}
	now := service.now().UTC()
	if now.IsZero() {
		return productport.ExternalPushTest{}, ErrUnavailable
	}
	payloadDigest := commerceExternalPushTestDigest(command)
	reservation := commerceExternalPushReservation(commerceExternalPushTestOperation, command.Actor, command.IdempotencyKey, payloadDigest, now)
	var result productport.ExternalPushTest
	err := service.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, reserveErr := service.store.ReserveCommerceExternalPush(tx, reservation)
		if reserveErr != nil {
			return reserveErr
		}
		if !validCommerceExternalPushReceipt(receipt, reservation) {
			return ErrUnavailable
		}
		if subtle.ConstantTimeCompare(receipt.PayloadDigest[:], reservation.PayloadDigest[:]) != 1 {
			return ErrConflict
		}
		if !owned {
			return decodeCommerceExternalPushTestSnapshot(receipt.ResultSnapshot, &result, command.ProductID, command.ProductKind)
		}
		configuration, readErr := service.store.LockCommerceExternalPushConfiguration(tx, command.ProductID, command.ProductKind)
		if readErr != nil {
			return readErr
		}
		if !validExternalPushConfiguration(configuration, command.ProductID, command.ProductKind) {
			return ErrUnavailable
		}
		if !configuration.Enabled {
			return ErrExternalPushNotConfigured
		}
		configurationDigest := commerceExternalPushConfigurationDigest(configuration)
		exists, readErr := service.store.CommerceExternalPushTestExists(tx, command.ProductID, command.ProductKind, configurationDigest)
		if readErr != nil {
			return readErr
		}
		if exists {
			return ErrConflict
		}
		effect, acceptErr := service.effects.AcceptProductExternalPushTest(tx, ProductExternalPushEffectCommand{
			ProductID: command.ProductID, ProductKind: command.ProductKind,
			ConfigurationDigest: configurationDigest, ReceiptKeyDigest: reservation.KeyDigest,
		})
		if acceptErr != nil {
			return acceptErr
		}
		if !validExternalPushTest(effect, command.ProductID, command.ProductKind) {
			return ErrUnavailable
		}
		result, readErr = service.store.CreateCommerceExternalPushTest(tx, effect, configurationDigest, receipt.ID)
		if readErr != nil {
			return readErr
		}
		if !validExternalPushTest(result, command.ProductID, command.ProductKind) || result.EffectID != effect.EffectID || result.State != effect.State {
			return ErrUnavailable
		}
		if eventErr := service.appendEvent(tx, productport.EventExternalPushTestAccepted, command.ProductID, command.ProductKind, command.Actor, reservation.KeyDigest, map[string]any{
			"effect_id":                   result.EffectID,
			"state":                       result.State,
			"provider_accepted":           result.ProviderAccepted,
			"delivery_proven":             result.DeliveryProven,
			"real_external_call_executed": result.RealExternalCallExecuted,
		}); eventErr != nil {
			return eventErr
		}
		return service.completeCommerceExternalPush(tx, receipt.ID, result, now)
	})
	if err != nil {
		return productport.ExternalPushTest{}, classifyCommerceExternalPush(err)
	}
	return result, nil
}

func (service *CommerceExternalPushService) completeCommerceExternalPush(ctx context.Context, receiptID int64, result any, now time.Time) error {
	snapshot, err := json.Marshal(result)
	if err != nil {
		return ErrUnavailable
	}
	receipt, err := service.store.CompleteCommerceExternalPush(ctx, receiptID, snapshot, now)
	if err != nil || receipt.ID != receiptID || receipt.State != "completed" || !jsonEquivalent(receipt.ResultSnapshot, snapshot) {
		return ErrUnavailable
	}
	return nil
}

func (service *CommerceExternalPushService) appendEvent(ctx context.Context, eventType string, productID productport.ID, kind productport.ExternalPushProductKind, actor int64, keyDigest [32]byte, fields map[string]any) error {
	if service == nil || service.events == nil {
		return ErrUnavailable
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["product_id"] = productID
	fields["product_kind"] = kind
	fields["actor"] = actor
	payload, err := json.Marshal(fields)
	if err != nil {
		return ErrUnavailable
	}
	_, err = service.events.Append(ctx, productport.Event{
		Type: eventType, Payload: payload, OccurredAt: service.now().UTC(),
		IdempotencyKey: eventType + ":" + hex.EncodeToString(keyDigest[:]),
	})
	return err
}

func commerceExternalPushReservation(operation string, actor int64, key string, payload [32]byte, now time.Time) Reservation {
	return Reservation{Operation: operation, ActorScope: fmt.Sprintf("admin:%d", actor), KeyDigest: sha256.Sum256([]byte(key)), PayloadDigest: payload, CreatedAt: now}
}

func commerceExternalPushSaveDigest(command productport.SaveExternalPushConfigurationCommand) [32]byte {
	payload, _ := json.Marshal(struct {
		ProductID              productport.ID                      `json:"product_id"`
		ProductKind            productport.ExternalPushProductKind `json:"product_kind"`
		Enabled                bool                                `json:"enabled"`
		ConfigurationReference string                              `json:"configuration_reference"`
	}{command.ProductID, command.ProductKind, command.Enabled, command.ConfigurationReference})
	return sha256.Sum256(payload)
}

func commerceExternalPushTestDigest(command productport.QueueExternalPushTestCommand) [32]byte {
	payload, _ := json.Marshal(struct {
		ProductID   productport.ID                      `json:"product_id"`
		ProductKind productport.ExternalPushProductKind `json:"product_kind"`
	}{command.ProductID, command.ProductKind})
	return sha256.Sum256(payload)
}

func commerceExternalPushConfigurationDigest(value productport.ExternalPushConfiguration) [32]byte {
	payload, _ := json.Marshal(struct {
		ProductID              productport.ID                      `json:"product_id"`
		ProductKind            productport.ExternalPushProductKind `json:"product_kind"`
		Enabled                bool                                `json:"enabled"`
		ConfigurationReference string                              `json:"configuration_reference"`
	}{value.ProductID, value.ProductKind, value.Enabled, value.ConfigurationReference})
	return sha256.Sum256(payload)
}

func validSaveCommerceExternalPush(command productport.SaveExternalPushConfigurationCommand) bool {
	return command.ProductID > 0 && validExternalPushKind(command.ProductKind) && command.Actor > 0 && validIdempotencyKey(command.IdempotencyKey) &&
		validExternalPushConfiguration(productport.ExternalPushConfiguration{ProductID: command.ProductID, ProductKind: command.ProductKind, Enabled: command.Enabled, ConfigurationReference: command.ConfigurationReference, UpdatedAt: time.Unix(1, 0)}, command.ProductID, command.ProductKind)
}

func validQueueCommerceExternalPushTest(command productport.QueueExternalPushTestCommand) bool {
	return command.ProductID > 0 && validExternalPushKind(command.ProductKind) && command.Actor > 0 && validIdempotencyKey(command.IdempotencyKey)
}

func validExternalPushKind(value productport.ExternalPushProductKind) bool {
	return value == productport.ExternalPushWeChatPay || value == productport.ExternalPushServicePeriod
}

func validExternalPushConfiguration(value productport.ExternalPushConfiguration, productID productport.ID, kind productport.ExternalPushProductKind) bool {
	if value.ProductID != productID || value.ProductKind != kind || productID < 1 || !validExternalPushKind(kind) || value.UpdatedAt.IsZero() {
		return false
	}
	if !value.Enabled {
		return value.ConfigurationReference == ""
	}
	return validCommerceExternalPushReference(value.ConfigurationReference)
}

func validCommerceExternalPushReference(value string) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 128 || strings.TrimSpace(value) != value || strings.Contains(value, "://") {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("-_.:", character) {
			continue
		}
		return false
	}
	return true
}

func validExternalPushTest(value productport.ExternalPushTest, productID productport.ID, kind productport.ExternalPushProductKind) bool {
	return value.ProductID == productID && value.ProductKind == kind && validExternalPushKind(kind) && validCommerceExternalPushEffectID(value.EffectID) &&
		(value.State == "accepted" || value.State == "queued") && !value.ProviderAccepted && !value.DeliveryProven && !value.RealExternalCallExecuted && !value.AutoRetryAllowed && !value.CreatedAt.IsZero()
}

func validCommerceExternalPushEffectID(value string) bool {
	if !strings.HasPrefix(value, "eer_") || len(value) < 5 {
		return false
	}
	for index, character := range value[4:] {
		if character < '0' || character > '9' || index == 0 && character == '0' {
			return false
		}
	}
	return true
}

func validCommerceExternalPushReceipt(receipt Receipt, reservation Reservation) bool {
	return receipt.ID > 0 && receipt.Operation == reservation.Operation && receipt.ActorScope == reservation.ActorScope &&
		subtle.ConstantTimeCompare(receipt.KeyDigest[:], reservation.KeyDigest[:]) == 1 &&
		(receipt.State == "in_progress" || receipt.State == "completed")
}

func decodeCommerceExternalPushSnapshot(raw json.RawMessage, target *productport.ExternalPushConfiguration, productID productport.ID, kind productport.ExternalPushProductKind) error {
	if target == nil || json.Unmarshal(raw, target) != nil || !validExternalPushConfiguration(*target, productID, kind) {
		return ErrUnavailable
	}
	canonical, err := json.Marshal(*target)
	if err != nil || !jsonEquivalent(canonical, raw) {
		return ErrUnavailable
	}
	return nil
}

func decodeCommerceExternalPushTestSnapshot(raw json.RawMessage, target *productport.ExternalPushTest, productID productport.ID, kind productport.ExternalPushProductKind) error {
	if target == nil || json.Unmarshal(raw, target) != nil || !validExternalPushTest(*target, productID, kind) {
		return ErrUnavailable
	}
	canonical, err := json.Marshal(*target)
	if err != nil || !jsonEquivalent(canonical, raw) {
		return ErrUnavailable
	}
	return nil
}

func commerceExternalPushReady(service *CommerceExternalPushService) bool {
	return service != nil && service.uow != nil && service.store != nil && service.events != nil && service.now != nil
}

func classifyCommerceExternalPush(err error) error {
	switch {
	case errors.Is(err, ErrInvalidProduct), errors.Is(err, ErrNotFound), errors.Is(err, ErrConflict), errors.Is(err, ErrExternalPushNotConfigured):
		return err
	case errors.Is(err, productport.ErrProductReadNotFound):
		return ErrNotFound
	case errors.Is(err, productport.ErrProductConflict):
		return ErrConflict
	default:
		return ErrUnavailable
	}
}
