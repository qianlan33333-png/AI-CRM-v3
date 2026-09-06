package outbound

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

var (
	ErrCommercePushInvalid  = errors.New("invalid commerce push intent")
	ErrCommercePushConflict = errors.New("commerce push intent conflict")
)

// CommercePushIdentity selects one already-verified, explicitly scoped OneID
// value. The value is used only in memory while building or sending a frozen
// payload and never enters EER, an audit row, or a log.
type CommercePushIdentity struct {
	Kind  identitydomain.Kind
	Scope string
}

func (v CommercePushIdentity) valid() bool {
	return identitydomain.ValidateNamespace(v.Kind, v.Scope) == nil
}

// CommercePushTarget is deployment configuration resolved from the opaque
// Product reference. Its target Slot survives endpoint/secret rotation, so it
// rather than a mutable policy digest participates in paid-event idempotency.
type CommercePushTarget struct {
	Reference  string
	Slot       string
	Endpoint   string
	SigningKey []byte
	Version    string
	TenantID   string

	BuyerID, BuyerOpenID, BuyerUnionID, BuyerPhone, BeneficiaryPhone CommercePushIdentity
	PushType, Remark                                                 string
	Day, Frequency                                                   *int64
	CustomParams                                                     map[string]string
	AllowLoopbackHTTP                                                bool // fixture-only
}

func (t CommercePushTarget) policyDigest() [32]byte {
	params := cloneCommercePushParams(t.CustomParams)
	value := struct {
		Reference, Slot, Endpoint, Version, TenantID                     string
		BuyerID, BuyerOpenID, BuyerUnionID, BuyerPhone, BeneficiaryPhone CommercePushIdentity
		PushType, Remark                                                 string
		Day, Frequency                                                   *int64
		CustomParams                                                     map[string]string
	}{t.Reference, t.Slot, t.Endpoint, t.Version, t.TenantID, t.BuyerID, t.BuyerOpenID, t.BuyerUnionID, t.BuyerPhone, t.BeneficiaryPhone, t.PushType, t.Remark, t.Day, t.Frequency, params}
	raw, _ := json.Marshal(value)
	return sha256.Sum256(raw)
}

func (t CommercePushTarget) valid() bool {
	if !validCommerceText(t.Reference, 128) || !validCommerceText(t.Slot, 128) || !validCommerceText(t.Version, 128) ||
		!t.BuyerID.valid() || !t.BuyerOpenID.valid() || !t.BuyerUnionID.valid() || !t.BuyerPhone.valid() || !t.BeneficiaryPhone.valid() ||
		len(t.SigningKey) > 4096 || strings.TrimSpace(t.PushType) != t.PushType || strings.TrimSpace(t.Remark) != t.Remark || len(t.PushType) > 200 || len(t.Remark) > 2000 ||
		(t.Day != nil && *t.Day < 0) || (t.Frequency != nil && *t.Frequency < 0) || !validCommerceParams(t.CustomParams) {
		return false
	}
	return validCommerceEndpoint(t.Endpoint, t.AllowLoopbackHTTP)
}

func cloneCommercePushParams(source map[string]string) map[string]string {
	out := make(map[string]string, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}
func validCommerceParams(values map[string]string) bool {
	if len(values) > 64 {
		return false
	}
	for key, value := range values {
		if !validCommerceText(key, 128) || len(value) > 4096 || !utf8.ValidString(value) || strings.TrimSpace(value) != value || reservedCommercePayloadField(key) {
			return false
		}
	}
	return true
}
func validCommerceText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

// CommercePushTargetResolver belongs to composition. Product persists only a
// reference; endpoint, secret, active state, and identity scopes stay in the
// runtime adapter and can be revoked before a queued Provider call.
type CommercePushTargetResolver interface {
	CommercePushProviderEnabled() bool
	CommercePushTarget(context.Context, string) (CommercePushTarget, bool, error)
}

// CommercePayloadCipher holds a configuration-owned content key in memory.
// It never serializes raw payloads, so the durable intent is safe to read from
// the operator timeline without exposing identity values.
type CommercePayloadCipher interface {
	EncryptCommercePayload([]byte, []byte) ([]byte, int16, error)
	DecryptCommercePayload([]byte, int16, []byte) ([]byte, error)
}

type CommercePayloadAESGCM struct{ key []byte }

func NewCommercePayloadAESGCM(encoded string) (*CommercePayloadAESGCM, error) {
	key, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, ErrCommercePushInvalid
	}
	return &CommercePayloadAESGCM{key: append([]byte(nil), key...)}, nil
}
func (c *CommercePayloadAESGCM) EncryptCommercePayload(raw, aad []byte) ([]byte, int16, error) {
	if c == nil || len(c.key) != 32 || len(raw) == 0 || len(raw) > 64<<10 {
		return nil, 0, ErrCommercePushInvalid
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, 0, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, 0, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, 0, err
	}
	return append(nonce, aead.Seal(nil, nonce, raw, aad)...), 1, nil
}
func (c *CommercePayloadAESGCM) DecryptCommercePayload(ciphertext []byte, version int16, aad []byte) ([]byte, error) {
	if c == nil || len(c.key) != 32 || version != 1 {
		return nil, ErrCommercePushInvalid
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) <= aead.NonceSize() {
		return nil, ErrCommercePushInvalid
	}
	return aead.Open(nil, ciphertext[:aead.NonceSize()], ciphertext[aead.NonceSize():], aad)
}

// CommercePushService owns the encrypted intent and its EER binding. It is
// called only with an existing Order/Product transaction, so Order settlement,
// intent, audit/outbox, EER acceptance, and River enqueue commit together.
type CommercePushService struct {
	pool       *pgxpool.Pool
	uow        platformport.UnitOfWork
	effects    effectport.TransactionalAccepter
	products   productport.ExternalPushConfigurationReader
	identities identityport.ExternalIdentityValueReader
	targets    CommercePushTargetResolver
	cipher     CommercePayloadCipher
	now        func() time.Time
}

func NewCommercePushService(pool *pgxpool.Pool, uow platformport.UnitOfWork, effects effectport.TransactionalAccepter, products productport.ExternalPushConfigurationReader, identities identityport.ExternalIdentityValueReader, targets CommercePushTargetResolver, cipher CommercePayloadCipher) (*CommercePushService, error) {
	if pool == nil || uow == nil || effects == nil || products == nil || identities == nil || targets == nil {
		return nil, ErrCommercePushInvalid
	}
	return &CommercePushService{pool: pool, uow: uow, effects: effects, products: products, identities: identities, targets: targets, cipher: cipher, now: time.Now}, nil
}

func (s *CommercePushService) ConsumePaidEventWithin(ctx context.Context, event orderport.PaidEvent) error {
	if s == nil || !event.Valid() {
		return ErrCommercePushInvalid
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return err
	}
	for _, item := range event.Order.Items {
		if item.ProductID == nil || *item.ProductID < 1 {
			// An Order line without a canonical Product cannot have Product-owned
			// configuration. It is deliberately not guessed or dispatched.
			continue
		}
		if err := s.consumeOrderItemWithin(ctx, event, item); err != nil {
			return err
		}
	}
	return nil
}

func (s *CommercePushService) consumeOrderItemWithin(ctx context.Context, event orderport.PaidEvent, item orderdomain.ItemSnapshot) error {
	sourceReference := commerceOrderSourceReference(event, item.LineNo)
	targetSlot := commerceProductSlot(*item.ProductID)
	if existing, found, err := commerceIntentExists(ctx, sourceReference, targetSlot, event.SourceDigest, *item.ProductID); err != nil || found {
		return err
	}
	configuration, err := s.products.ReadExternalPushConfigurationForOrder(ctx, productport.ID(*item.ProductID))
	if err != nil || !configuration.Enabled {
		return s.planCommercePushWithin(ctx, commercePlannedIntent{sourceKind: "order_paid", sourceReference: sourceReference, orderEventID: event.ID, productID: *item.ProductID, productKind: configuration.ProductKind, targetReference: commerceTargetReference(configuration), targetSlot: targetSlot, revision: configuration.Revision, sourceDigest: event.SourceDigest, state: "planned_disabled"})
	}
	target, found, err := s.targets.CommercePushTarget(ctx, configuration.ConfigurationReference)
	if err != nil {
		return err
	}
	if !found || !target.valid() || !s.targets.CommercePushProviderEnabled() {
		state := "planned_target_unavailable"
		if found && target.valid() && !s.targets.CommercePushProviderEnabled() {
			state = "planned_disabled"
		}
		return s.planCommercePushWithin(ctx, commercePlannedIntent{sourceKind: "order_paid", sourceReference: sourceReference, orderEventID: event.ID, productID: *item.ProductID, productKind: configuration.ProductKind, targetReference: configuration.ConfigurationReference, targetSlot: targetSlot, revision: configuration.Revision, sourceDigest: event.SourceDigest, state: state})
	}
	body, missing, err := s.paidPayload(ctx, event, item, target, commerceDeliveryID(event.ID, item.LineNo, targetSlot))
	if err != nil {
		return err
	}
	if missing || s.cipher == nil {
		state := "planned_identity_unavailable"
		if !missing {
			state = "planned_payload_protection_unavailable"
		}
		return s.planCommercePushWithin(ctx, commercePlannedIntent{sourceKind: "order_paid", sourceReference: sourceReference, orderEventID: event.ID, productID: *item.ProductID, productKind: configuration.ProductKind, targetReference: configuration.ConfigurationReference, targetSlot: targetSlot, revision: configuration.Revision, sourceDigest: event.SourceDigest, state: state})
	}
	return s.acceptCommercePushWithin(ctx, commerceAcceptedIntent{sourceKind: "order_paid", sourceReference: sourceReference, orderEventID: event.ID, productID: *item.ProductID, productKind: configuration.ProductKind, targetReference: configuration.ConfigurationReference, targetSlot: targetSlot, revision: configuration.Revision, sourceDigest: event.SourceDigest, target: target, body: body})
}

func (s *CommercePushService) AcceptExternalPushTestWithin(ctx context.Context, in productport.ExternalPushTestIntent) (productport.ExternalPushTest, error) {
	if s == nil || in.ProductID < 1 || in.ConfigurationRevision < 1 || in.ReceiptKeyDigest == ([32]byte{}) || !validCommercePushKind(in.ProductKind) || !validCommerceText(in.ConfigurationReference, 128) {
		return productport.ExternalPushTest{}, ErrCommercePushInvalid
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return productport.ExternalPushTest{}, err
	}
	configuration, err := s.products.ReadExternalPushConfigurationForOrder(ctx, in.ProductID)
	if err != nil || !configuration.Enabled || configuration.ProductKind != in.ProductKind || configuration.ConfigurationReference != in.ConfigurationReference || configuration.Revision != in.ConfigurationRevision {
		return productport.ExternalPushTest{}, ErrCommercePushConflict
	}
	target, found, err := s.targets.CommercePushTarget(ctx, in.ConfigurationReference)
	if err != nil {
		return productport.ExternalPushTest{}, err
	}
	if !found || !target.valid() || !s.targets.CommercePushProviderEnabled() || s.cipher == nil {
		return productport.ExternalPushTest{}, ErrCommercePushConflict
	}
	sourceDigest := sha256.Sum256(append([]byte("commerce-push-test\x00"), in.ReceiptKeyDigest[:]...))
	sourceReference := "synthetic:" + hex.EncodeToString(in.ReceiptKeyDigest[:])
	targetSlot := commerceProductSlot(int64(in.ProductID))
	if existing, found, err := commerceIntentExists(ctx, sourceReference, targetSlot, sourceDigest, int64(in.ProductID)); err != nil {
		return productport.ExternalPushTest{}, err
	} else if found {
		return productport.ExternalPushTest{ProductID: in.ProductID, ProductKind: in.ProductKind, EffectID: existing.effectID, State: existing.state, CreatedAt: existing.createdAt}, nil
	}
	body, err := commerceSyntheticPayload(in.ProductID, configuration.ProductName, target, commerceDeliveryIDFromDigest(in.ReceiptKeyDigest, targetSlot), s.now().UTC())
	if err != nil {
		return productport.ExternalPushTest{}, err
	}
	accepted, err := s.acceptCommercePushWithin(ctx, commerceAcceptedIntent{sourceKind: "synthetic_test", sourceReference: sourceReference, productID: int64(in.ProductID), productKind: in.ProductKind, targetReference: in.ConfigurationReference, targetSlot: targetSlot, revision: in.ConfigurationRevision, sourceDigest: sourceDigest, target: target, body: body})
	if err != nil {
		return productport.ExternalPushTest{}, err
	}
	return productport.ExternalPushTest{ProductID: in.ProductID, ProductKind: in.ProductKind, EffectID: accepted.effectID, State: accepted.state, CreatedAt: accepted.createdAt}, nil
}

func commerceOrderSourceReference(event orderport.PaidEvent, lineNo int32) string {
	return "order-paid:" + strconv.FormatInt(event.ID, 10) + ":line:" + strconv.FormatInt(int64(lineNo), 10)
}
func commerceProductSlot(productID int64) string {
	return "product:" + strconv.FormatInt(productID, 10)
}
func commerceTargetReference(configuration productport.ExternalPushConfiguration) string {
	if configuration.ConfigurationReference != "" {
		return configuration.ConfigurationReference
	}
	return "unconfigured"
}
func commerceDeliveryID(eventID int64, lineNo int32, slot string) string {
	d := sha256.Sum256([]byte("commerce-push-delivery.v1\x00" + strconv.FormatInt(eventID, 10) + "\x00" + strconv.FormatInt(int64(lineNo), 10) + "\x00" + slot))
	return "commerce_" + hex.EncodeToString(d[:16])
}
func commerceDeliveryIDFromDigest(digest [32]byte, slot string) string {
	d := sha256.Sum256(append(append([]byte("commerce-push-test-delivery.v1\x00"), digest[:]...), []byte("\x00"+slot)...))
	return "commerce_test_" + hex.EncodeToString(d[:16])
}
func commercePayloadAAD(sourceReference, slot string) []byte {
	return []byte("commerce-push.payload.v1\x00" + sourceReference + "\x00" + slot)
}
func validCommercePushKind(value productport.ExternalPushProductKind) bool {
	return value == productport.ExternalPushWeChatPay || value == productport.ExternalPushServicePeriod
}

type commerceIntentRecord struct {
	id        int64
	effectID  string
	state     string
	createdAt time.Time
}

func commerceIntentExists(ctx context.Context, sourceReference, targetSlot string, sourceDigest [32]byte, productID int64) (commerceIntentRecord, bool, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil { return commerceIntentRecord{}, false, err }
	var out commerceIntentRecord
	var stored []byte
	err = tx.QueryRow(ctx, `SELECT id,source_digest,product_id,COALESCE(effect_id,''),state,created_at FROM outbound_commerce_push_intents WHERE source_reference=$1 AND target_slot=$2 FOR UPDATE`, sourceReference, targetSlot).Scan(&out.id, &stored, &productID, &out.effectID, &out.state, &out.createdAt)
	if errors.Is(err, pgx.ErrNoRows) { return commerceIntentRecord{}, false, nil }
	if err != nil { return commerceIntentRecord{}, false, err }
	if len(stored) != 32 || !hmac.Equal(stored, sourceDigest[:]) || productID < 1 { return commerceIntentRecord{}, false, ErrCommercePushConflict }
	return out, true, nil
}

type commercePlannedIntent struct {
	sourceKind, sourceReference, targetReference, targetSlot, state string
	orderEventID                                              int64
	productID                                                 int64
	productKind                                               productport.ExternalPushProductKind
	revision                                                  int64
	sourceDigest                                              [32]byte
}

func (s *CommercePushService) planCommercePushWithin(ctx context.Context, in commercePlannedIntent) error {
	if s == nil || in.productID < 1 || !validCommercePushKind(in.productKind) || in.revision < 1 || in.sourceDigest == ([32]byte{}) ||
		!validCommerceText(in.sourceReference, 200) || !validCommerceText(in.targetSlot, 128) || !validCommerceText(in.targetReference, 128) ||
		!validCommercePlannedState(in.state) {
		return ErrCommercePushInvalid
	}
	if existing, found, err := commerceIntentExists(ctx, in.sourceReference, in.targetSlot, in.sourceDigest, in.productID); err != nil || found {
		return err
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil { return err }
	now := s.now().UTC()
	if now.IsZero() { return ErrCommercePushInvalid }
	var eventID any
	if in.orderEventID > 0 { eventID = in.orderEventID }
	intentDigest := commerceIntentDigest(in.sourceKind, in.sourceReference, in.productID, in.targetSlot, in.revision, in.sourceDigest, [32]byte{}, [32]byte{}, [32]byte{})
	keyDigest := sha256.Sum256([]byte("commerce-push.intent.v1\x00" + in.sourceReference + "\x00" + in.targetSlot))
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO outbound_commerce_push_intents(source_kind,source_reference,order_paid_event_id,product_id,product_kind,target_reference,target_slot,product_configuration_revision,source_digest,target_digest,payload_digest,policy_digest,receipt_key_digest,intent_digest,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16) ON CONFLICT(source_reference,target_slot) DO NOTHING RETURNING id`, in.sourceKind, in.sourceReference, eventID, in.productID, string(in.productKind), in.targetReference, in.targetSlot, in.revision, in.sourceDigest[:], zeroCommerceDigest(), zeroCommerceDigest(), zeroCommerceDigest(), keyDigest[:], intentDigest[:], in.state, now).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		_, found, readErr := commerceIntentExists(ctx, in.sourceReference, in.targetSlot, in.sourceDigest, in.productID)
		if readErr != nil || !found { return readErr }
		return nil
	}
	if err != nil { return err }
	payload, _ := json.Marshal(map[string]any{"commerce_push_intent_id": id, "source_reference": in.sourceReference, "state": in.state})
	key := sha256.Sum256([]byte("planned:" + strconv.FormatInt(id, 10)))
	if _, err = tx.Exec(ctx, `INSERT INTO outbound_commerce_push_audit_events(intent_id,operation,payload_digest,occurred_at) VALUES($1,'planned',$2,$3)`, id, sha256Bytes(payload), now); err != nil { return err }
	if _, err = tx.Exec(ctx, `INSERT INTO outbound_commerce_push_outbox(event_type,intent_id,payload,idempotency_digest,occurred_at) VALUES('outbound.commerce_push.planned.v1',$1,$2::jsonb,$3,$4)`, id, payload, key[:], now); err != nil { return err }
	return nil
}

func zeroCommerceDigest() []byte { return make([]byte, 32) }
func sha256Bytes(raw []byte) []byte { d := sha256.Sum256(raw); return d[:] }
func validCommercePlannedState(v string) bool {
	switch v {
	case "planned_disabled", "planned_target_unavailable", "planned_identity_unavailable", "planned_payload_protection_unavailable":
		return true
	}
	return false
}

type commerceAcceptedIntent struct {
	sourceKind, sourceReference, targetReference, targetSlot string
	orderEventID                                              int64
	productID                                                 int64
	productKind                                               productport.ExternalPushProductKind
	revision                                                  int64
	sourceDigest                                              [32]byte
	target                                                    CommercePushTarget
	body                                                      []byte
}

func (s *CommercePushService) acceptCommercePushWithin(ctx context.Context, in commerceAcceptedIntent) (commerceIntentRecord, error) {
	if s == nil || in.productID < 1 || in.revision < 1 || in.sourceDigest == ([32]byte{}) || !validCommercePushKind(in.productKind) || !in.target.valid() || len(in.body) == 0 || len(in.body) > 64<<10 || !json.Valid(in.body) {
		return commerceIntentRecord{}, ErrCommercePushInvalid
	}
	if existing, found, err := commerceIntentExists(ctx, in.sourceReference, in.targetSlot, in.sourceDigest, in.productID); err != nil || found {
		return existing, err
	}
	payloadDigest := sha256.Sum256(in.body)
	targetDigest := sha256.Sum256([]byte("commerce-push.target.v1\x00" + in.target.Reference + "\x00" + in.target.Slot))
	policyDigest := in.target.policyDigest()
	ciphertext, keyVersion, err := s.cipher.EncryptCommercePayload(in.body, commercePayloadAAD(in.sourceReference, in.targetSlot))
	if err != nil || keyVersion != 1 || len(ciphertext) < 29 { return commerceIntentRecord{}, ErrCommercePushInvalid }
	envelope := commerceEnvelope(in.sourceDigest, targetDigest, payloadDigest, policyDigest)
	if !envelope.Valid() { return commerceIntentRecord{}, ErrCommercePushInvalid }
	projection, receipt, err := s.effects.AcceptAndQueueWithin(ctx, effectport.AcceptCommand{ReceiptKey: effectport.Hash("outbound.commerce_push.accept.v1", in.sourceReference, in.targetSlot), Envelope: envelope})
	if err != nil { return commerceIntentRecord{}, err }
	if projection.ID == "" || receipt.QueueReceiptID == "" || (projection.State != effectport.StateAccepted && projection.State != effectport.StateQueued) { return commerceIntentRecord{}, ErrCommercePushConflict }
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil { return commerceIntentRecord{}, err }
	now := s.now().UTC()
	var eventID any
	if in.orderEventID > 0 { eventID = in.orderEventID }
	keyDigest := sha256.Sum256([]byte("commerce-push.intent.v1\x00" + in.sourceReference + "\x00" + in.targetSlot))
	intentDigest := commerceIntentDigest(in.sourceKind, in.sourceReference, in.productID, in.targetSlot, in.revision, in.sourceDigest, targetDigest, payloadDigest, policyDigest)
	var out commerceIntentRecord
	err = tx.QueryRow(ctx, `INSERT INTO outbound_commerce_push_intents(source_kind,source_reference,order_paid_event_id,product_id,product_kind,target_reference,target_slot,product_configuration_revision,source_digest,target_digest,payload_digest,policy_digest,receipt_key_digest,intent_digest,envelope_fingerprint,payload_ciphertext,payload_key_version,effect_id,queue_receipt_id,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,'queued',$20,$20) ON CONFLICT(source_reference,target_slot) DO NOTHING RETURNING id,effect_id,state,created_at`, in.sourceKind, in.sourceReference, eventID, in.productID, string(in.productKind), in.targetReference, in.targetSlot, in.revision, in.sourceDigest[:], targetDigest[:], payloadDigest[:], policyDigest[:], keyDigest[:], intentDigest[:], string(envelope.Fingerprint()), ciphertext, keyVersion, projection.ID, receipt.QueueReceiptID, now).Scan(&out.id, &out.effectID, &out.state, &out.createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		stored, found, readErr := commerceIntentExists(ctx, in.sourceReference, in.targetSlot, in.sourceDigest, in.productID)
		if readErr != nil || !found { return commerceIntentRecord{}, readErr }
		return stored, nil
	}
	if err != nil { return commerceIntentRecord{}, err }
	payload, _ := json.Marshal(map[string]any{"commerce_push_intent_id": out.id, "source_reference": in.sourceReference, "effect_id": out.effectID, "state": out.state})
	key := sha256.Sum256([]byte("queued:" + strconv.FormatInt(out.id, 10)))
	if _, err = tx.Exec(ctx, `INSERT INTO outbound_commerce_push_audit_events(intent_id,operation,payload_digest,occurred_at) VALUES($1,'accepted',$2,$3)`, out.id, sha256Bytes(payload), now); err != nil { return commerceIntentRecord{}, err }
	if _, err = tx.Exec(ctx, `INSERT INTO outbound_commerce_push_outbox(event_type,intent_id,payload,idempotency_digest,occurred_at) VALUES('outbound.commerce_push.queued.v1',$1,$2::jsonb,$3,$4)`, out.id, payload, key[:], now); err != nil { return commerceIntentRecord{}, err }
	return out, nil
}

func commerceIntentDigest(sourceKind, sourceReference string, productID int64, slot string, revision int64, source, target, payload, policy [32]byte) [32]byte {
	raw, _ := json.Marshal([]any{sourceKind, sourceReference, productID, slot, revision, hex.EncodeToString(source[:]), hex.EncodeToString(target[:]), hex.EncodeToString(payload[:]), hex.EncodeToString(policy[:])})
	return sha256.Sum256(raw)
}
func commerceEnvelope(source, target, payload, policy [32]byte) effectport.Envelope {
	return effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindCommerceProductPush, SourceRefDigest: effectport.Hash("commerce.push.source.v1", hex.EncodeToString(source[:])), TargetRefDigest: effectport.Hash("commerce.push.target.v1", hex.EncodeToString(target[:])), PayloadDigest: effectport.Hash("commerce.push.payload.v1", hex.EncodeToString(payload[:])), PolicyVersionHash: effectport.Hash("commerce.push.policy.v1", hex.EncodeToString(policy[:]))}
}
