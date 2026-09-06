package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	couponstore "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effects "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/http"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformjobqueue "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/jobqueue"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	productstore "github.com/qianlan33333-png/AI-CRM-v3/internal/product/store"
)

type commerceFundsSecurity struct{}

func (commerceFundsSecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}
func (commerceFundsSecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return commerceFundsSecurity{}.Authenticate(context.Background(), nil)
}

type commerceFundsSessionVerifier struct{ fact identitydomain.VerifiedFact }

func (v commerceFundsSessionVerifier) VerifyCode(_ context.Context, code string) (identitydomain.VerifiedFact, error) {
	if code != "provider-verified-session-code" {
		return identitydomain.VerifiedFact{}, errors.New("unverified session code")
	}
	return v.fact, nil
}

type commerceFundsFailingEntitlement struct{ err error }

func (f commerceFundsFailingEntitlement) GrantPaidServicePeriodWithin(context.Context, orderport.ServicePeriodGrantCommand) (orderport.Entitlement, error) {
	return orderport.Entitlement{}, f.err
}
func (f commerceFundsFailingEntitlement) ApplyServicePeriodRefundWithin(context.Context, orderport.ServicePeriodRefundCommand) (orderport.Entitlement, error) {
	return orderport.Entitlement{}, f.err
}

// commerceFundsPushConfiguration and its sibling adapters deliberately expose
// only the three stable Ports that the paid-event consumer may read. The test
// keeps Product credentials, OneID values, and the Provider transport outside
// Order and Payment while exercising their shared PostgreSQL transaction.
type commerceFundsPushConfiguration struct {
	value productport.ExternalPushConfiguration
}

type commerceFundsProductConfigurationReader struct {
	repository *productstore.Repository
}

func (r commerceFundsProductConfigurationReader) ReadExternalPushConfigurationForOrder(ctx context.Context, id productport.ID) (productport.ExternalPushConfiguration, error) {
	if r.repository == nil {
		return productport.ExternalPushConfiguration{}, errors.New("product configuration repository is required")
	}
	return r.repository.ReadCommerceExternalPushConfigurationForOrder(ctx, id)
}

var _ productport.ExternalPushConfigurationReader = commerceFundsProductConfigurationReader{}

func (c commerceFundsPushConfiguration) ReadExternalPushConfigurationForOrder(_ context.Context, id productport.ID) (productport.ExternalPushConfiguration, error) {
	if id != c.value.ProductID {
		return productport.ExternalPushConfiguration{}, errors.New("product configuration not found")
	}
	return c.value, nil
}

type commerceFundsPushIdentityReader struct{ customerID int64 }

func (r commerceFundsPushIdentityReader) VerifiedExternalIdentityValue(_ context.Context, customerID customerdomain.CustomerID, kind identitydomain.Kind, scope string) (string, bool, error) {
	if int64(customerID) != r.customerID {
		return "", false, nil
	}
	switch {
	case kind == identitydomain.KindPhone && scope == "phone:cn11":
		return "13800138000", true, nil
	case kind == identitydomain.KindWeComExternalUserID && scope == "wecom-corp:commerce-fixture":
		return "fixture-buyer", true, nil
	case kind == identitydomain.KindMPOpenID && scope == "wechat-app:commerce-fixture":
		return "fixture-openid", true, nil
	case kind == identitydomain.KindUnionID && scope == "wechat-open-platform:commerce-fixture":
		return "fixture-unionid", true, nil
	default:
		return "", false, nil
	}
}

var _ identityport.ExternalIdentityValueReader = commerceFundsPushIdentityReader{}

type commerceFundsPushTargets struct{ target outbound.CommercePushTarget }

func (r commerceFundsPushTargets) CommercePushProviderEnabled() bool { return true }
func (r commerceFundsPushTargets) CommercePushTarget(_ context.Context, reference string) (outbound.CommercePushTarget, bool, error) {
	if reference != r.target.Reference {
		return outbound.CommercePushTarget{}, false, nil
	}
	return r.target, true, nil
}

var _ outbound.CommercePushTargetResolver = commerceFundsPushTargets{}

type commerceFundsPushDelivery struct {
	event, deliveryID, timestamp, signature string
	requestTarget                           string
	body                                    []byte
}

// commerceFundsProductPushStatuses and commerceFundsProductPushEvents keep this
// test on Product's stable Ports while exercising its real PostgreSQL store and
// Unit of Work. Saving configuration never queries delivery status or accepts an
// effect, so neither stub can hide an external-effect outcome.
type commerceFundsDisabledCommerceEffects struct{}

func (commerceFundsDisabledCommerceEffects) AcceptAndQueueWithin(context.Context, effectport.AcceptCommand) (effectport.Projection, effectport.Receipt, error) {
	return effectport.Projection{}, effectport.Receipt{}, errors.New("disabled product push must not accept an external effect")
}

type commerceFundsProductPushStatuses struct{}

func (commerceFundsProductPushStatuses) ReadExternalPushTestStatus(context.Context, productport.ID, string) (productport.ExternalPushTestStatus, error) {
	return productport.ExternalPushTestStatus{}, errors.New("status is not read while saving product configuration")
}

type commerceFundsProductPushEvents struct {
	mu    sync.Mutex
	count int
}

func (e *commerceFundsProductPushEvents) Append(_ context.Context, _ productport.Event) (productport.EventID, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.count++
	return productport.EventID(e.count), nil
}

func (e *commerceFundsProductPushEvents) Count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.count
}

func TestPostgreSQLProductExternalPushBusinessParametersRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := productstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	var ordinaryID, serviceID int64
	ordinaryProjection := `{"schema_version":1,"status":"enabled","enabled":true}`
	serviceProjection := `{"schema_version":1,"status":"service_period_enabled","enabled":true}`
	if err = pool.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('push-product','外推商品',1200,'CNY',1,1,$1::jsonb) RETURNING id`, ordinaryProjection).Scan(&ordinaryID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('push-service','外推周期商品',1200,'CNY',1,1,$1::jsonb) RETURNING id`, serviceProjection).Scan(&serviceID); err != nil {
		t.Fatal(err)
	}
	day, frequency := int64(30), int64(1)
	value := productport.ExternalPushConfiguration{
		ProductID: productport.ID(ordinaryID), ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: "product-push-roundtrip",
		PushType: "member_open", Day: &day, Frequency: &frequency, Remark: "保留业务备注", CustomParams: map[string]any{"number": json.Number("9007199254740993"), "flag": false, "nil": nil, "nested": []any{" 空白 ", map[string]any{"k": true}}},
	}
	now := time.Date(2026, 9, 6, 5, 0, 0, 0, time.UTC)
	var saved, read, orderRead productport.ExternalPushConfiguration
	if err = uow.Within(ctx, func(tx context.Context) error {
		var saveErr error
		saved, saveErr = repository.SaveCommerceExternalPushConfiguration(tx, value, now)
		return saveErr
	}); err != nil {
		t.Fatal(err)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		read, readErr = repository.ReadCommerceExternalPushConfiguration(tx, productport.ID(ordinaryID), productport.ExternalPushWeChatPay)
		if readErr != nil {
			return readErr
		}
		orderRead, readErr = repository.ReadCommerceExternalPushConfigurationForOrder(tx, productport.ID(ordinaryID))
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 || read.Revision != 1 || orderRead.ProductKind != productport.ExternalPushWeChatPay || orderRead.PushType != "member_open" || orderRead.Day == nil || *orderRead.Day != 30 || orderRead.Frequency == nil || *orderRead.Frequency != 1 || orderRead.Remark != "保留业务备注" || !commerceFundsJSONEquivalent(t, orderRead.CustomParams, value.CustomParams) {
		t.Fatalf("saved=%#v read=%#v order=%#v", saved, read, orderRead)
	}
	if got, ok := orderRead.CustomParams["number"].(json.Number); !ok || got.String() != "9007199254740993" {
		t.Fatalf("PostgreSQL round trip changed the frozen integer: %#v", orderRead.CustomParams)
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		service, readErr := repository.ReadCommerceExternalPushConfigurationForOrder(tx, productport.ID(serviceID))
		if readErr != nil {
			return readErr
		}
		if service.ProductKind != productport.ExternalPushServicePeriod {
			return errors.New("service-period product classified as ordinary")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE product_external_push_configurations SET custom_params='[]'::jsonb WHERE product_id=$1`, ordinaryID); err == nil {
		t.Fatal("database accepted a non-object custom_params shape")
	}
}

func TestPostgreSQLProductExternalPushFirstBusinessSaveCAS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := productstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	var productID int64
	if err = pool.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('push-first-cas','首次 CAS 商品',1200,'CNY',1,1,'{"schema_version":1,"status":"enabled","enabled":true}'::jsonb) RETURNING id`).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	events := &commerceFundsProductPushEvents{}
	service, err := productapp.NewCommerceExternalPushService(uow, repository, nil, commerceFundsProductPushStatuses{}, events)
	if err != nil {
		t.Fatal(err)
	}
	// The application uses the immutable save receipt and then locks the
	// Product row. Two different administrators holding the default revision
	// must therefore produce one persisted version and one stale conflict.
	commands := []productport.SaveExternalPushConfigurationCommand{
		{ProductID: productport.ID(productID), ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: "product-push-first-cas", BusinessParametersSet: true, PushType: "member_open", CustomParams: map[string]any{"big": json.Number("9007199254740993")}, ExpectedRevision: 0, Actor: 41, IdempotencyKey: "commerce-push-pg-first-cas-a"},
		{ProductID: productport.ID(productID), ProductKind: productport.ExternalPushWeChatPay, Enabled: true, ConfigurationReference: "product-push-first-cas", BusinessParametersSet: true, PushType: "member_open", CustomParams: map[string]any{"big": json.Number("9007199254740993")}, ExpectedRevision: 0, Actor: 42, IdempotencyKey: "commerce-push-pg-first-cas-b"},
	}
	start := make(chan struct{})
	results := make(chan error, len(commands))
	var group sync.WaitGroup
	for _, command := range commands {
		command := command
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, saveErr := service.SaveExternalPushConfiguration(ctx, command)
			results <- saveErr
		}()
	}
	close(start)
	group.Wait()
	close(results)
	var successes, conflicts int
	for result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, productapp.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent first-save error: %v", result)
		}
	}
	if successes != 1 || conflicts != 1 || events.Count() != 1 {
		t.Fatalf("first-save CAS successes=%d conflicts=%d events=%d", successes, conflicts, events.Count())
	}
	var revision, receipts int64
	if err = pool.QueryRow(ctx, `SELECT version FROM product_external_push_configurations WHERE product_id=$1 AND product_kind='wechat_pay'`, productID).Scan(&revision); err != nil || revision != 1 {
		t.Fatalf("stored first-save version=%d err=%v", revision, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM product_operation_receipts WHERE operation='external_push_save'`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("first-save receipts=%d err=%v", receipts, err)
	}
}

func TestPostgreSQLUnconfiguredPaidOrderPlansDisabledCommercePushOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	products, err := productstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 6, 7, 0, 0, 0, time.UTC)
	var customerID, productID, orderID, outboxID, paidEventID int64
	if err = pool.QueryRow(ctx, `INSERT INTO customers DEFAULT VALUES RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO products(product_code,name,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('push-unconfigured','未配置外推商品',1200,'CNY',1,1,'{"schema_version":1,"status":"enabled","enabled":true}'::jsonb) RETURNING id`).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at) VALUES('wechat_pay','native-checkout','push-unconfigured-source','push-unconfigured-merchant',$1,$1,1200,'CNY','paid','native',TRUE,2,$2,$2) RETURNING id`, customerID, now).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_code,product_name,unit_amount_minor,quantity,line_amount_minor) VALUES($1,1,$2,'push-unconfigured','未配置外推商品',1200,1,1200)`, orderID, productID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO order_outbox(event_type,idempotency_key,aggregate_id,payload,occurred_at) VALUES('order.paid.v1',$1,$2,'{}'::jsonb,$3) RETURNING id`, "order.paid.v1:"+strconv.FormatInt(orderID, 10), orderID, now).Scan(&outboxID); err != nil {
		t.Fatal(err)
	}
	sourceDigest := orderport.NewPaidEventSourceDigest(orderID, 2)
	if err = pool.QueryRow(ctx, `INSERT INTO order_paid_events(order_id,order_version,source_digest,occurred_at) VALUES($1,2,$2,$3) RETURNING id`, orderID, sourceDigest[:], now).Scan(&paidEventID); err != nil {
		t.Fatal(err)
	}
	service, err := outbound.NewCommercePushService(pool, uow, commerceFundsDisabledCommerceEffects{}, commerceFundsProductConfigurationReader{repository: products}, commerceFundsPushIdentityReader{customerID: customerID}, commerceFundsPushTargets{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	productRef := productID
	event := orderport.PaidEvent{ID: paidEventID, OrderID: orderID, OrderVersion: 2, DomainEventOutboxID: outboxID, OccurredAt: now, SourceDigest: sourceDigest, Order: orderdomain.Snapshot{
		ID: orderID, Provider: orderdomain.ProviderWeChatPay, SourceSystem: "native-checkout", SourceKey: "push-unconfigured-source", MerchantOrderNo: "push-unconfigured-merchant", PayerCustomerID: &customerID, BeneficiaryCustomerID: &customerID,
		Amount: orderdomain.Money{AmountMinor: 1200, Currency: "CNY"}, Status: orderdomain.StatusPaid, Items: []orderdomain.ItemSnapshot{{LineNo: 1, ProductID: &productRef, ProductCode: "push-unconfigured", ProductName: "未配置外推商品", UnitAmountMinor: 1200, Quantity: 1, LineAmountMinor: 1200}}, RecordOrigin: orderdomain.RecordOriginNative, EffectEligible: true, Version: 2, CreatedAt: now, UpdatedAt: now,
	}}
	for attempt := 0; attempt < 2; attempt++ {
		if err = uow.Within(ctx, func(tx context.Context) error { return service.ConsumePaidEventWithin(tx, event) }); err != nil {
			t.Fatalf("unconfigured paid consume attempt=%d err=%v", attempt, err)
		}
	}
	var state, targetReference string
	var revision int64
	var effects, audits, outbox int
	err = pool.QueryRow(ctx, `SELECT intent.state,intent.target_reference,intent.product_configuration_revision,
  (SELECT count(*) FROM external_effects WHERE kind=$2),
  (SELECT count(*) FROM outbound_commerce_push_audit_events audit WHERE audit.intent_id=intent.id),
  (SELECT count(*) FROM outbound_commerce_push_outbox outbox WHERE outbox.intent_id=intent.id)
FROM outbound_commerce_push_intents intent WHERE intent.order_paid_event_id=$1`, paidEventID, effectport.KindCommerceProductPush).Scan(&state, &targetReference, &revision, &effects, &audits, &outbox)
	if err != nil || state != "planned_disabled" || targetReference != "unconfigured" || revision != 0 || effects != 0 || audits != 1 || outbox != 1 {
		t.Fatalf("unconfigured paid intent state/ref/revision/effects/audits/outbox=%q/%q/%d/%d/%d/%d err=%v", state, targetReference, revision, effects, audits, outbox, err)
	}
}

func commerceFundsJSONEquivalent(t *testing.T, left, right any) bool {
	t.Helper()
	leftRaw, leftErr := json.Marshal(left)
	rightRaw, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		t.Fatalf("marshal values %v/%v", leftErr, rightErr)
	}
	var leftValue, rightValue any
	leftDecoder, rightDecoder := json.NewDecoder(bytes.NewReader(leftRaw)), json.NewDecoder(bytes.NewReader(rightRaw))
	leftDecoder.UseNumber()
	rightDecoder.UseNumber()
	return leftDecoder.Decode(&leftValue) == nil && rightDecoder.Decode(&rightValue) == nil && reflect.DeepEqual(leftValue, rightValue)
}

// TestPostgreSQLCommerceFundsHTTPJourney validates the actual composition-root
// journey: provider-verified public session, self selection, coupon reserve,
// signed payment settlement, service-period grant, partial refund and a later
// refund. A forced fulfillment failure proves Payment, Order, Coupon and the
// entitlement facts roll back in one UoW; concurrent callback/refund requests
// then prove the successful lifecycle cannot duplicate those facts.
func TestPostgreSQLCommerceFundsHTTPJourney(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	wrapped, err := platformpostgres.Wrap(pool, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.Close()
	uow, err := platformpostgres.NewUnitOfWork(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	orders, err := orderstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	coupons, err := couponstore.NewPostgreSQL(pool, uow)
	if err != nil {
		t.Fatal(err)
	}
	couponCheckout, err := couponapp.NewCheckoutService(uow, coupons)
	if err != nil {
		t.Fatal(err)
	}
	orderService := orderapp.NewService(uow, orders)
	if err = orderService.SetCheckoutCouponCoordinator(couponCheckout); err != nil {
		t.Fatal(err)
	}

	workers := river.NewWorkers()
	effectsModule := effects.NewModuleRegistration()
	if err = effectsModule.RegisterWorkers(workers); err != nil {
		t.Fatal(err)
	}
	insertClient, err := platformjobqueue.NewInsertClient(pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	effectStore, err := effects.NewRepository(pool, insertClient)
	if err != nil {
		t.Fatal(err)
	}

	var customerID int64
	if err = pool.QueryRow(ctx, "INSERT INTO customers DEFAULT VALUES RETURNING id").Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	sessions, err := paymentsession.NewService(uow, checkoutRecoveryProvisioner{identityID: 901, customerID: customerdomain.CustomerID(customerID)}, paymentsession.NewPostgreSQL(), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	product := productport.CheckoutProduct{ID: 17, ProductType: productport.ProductOptionServicePeriod, Code: "period-17", Name: "三十天服务期", PriceMinor: 1200, Currency: "CNY", Version: 4, ServicePeriodDurationDays: 30}
	paymentService := paymentapp.NewService(uow, paymentstore.NewPostgreSQL(), orderService, sessions, effectStore, effectStore)
	if err = paymentService.SetCheckoutProductReader(checkoutRecoveryProductReader{product: product}); err != nil {
		t.Fatal(err)
	}
	if err = paymentService.SetPaymentChannelAppIDs("app", ""); err != nil {
		t.Fatal(err)
	}

	apiKey := []byte("0123456789abcdef0123456789abcdef")
	platformKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := paymentprovider.NewCallbackVerifier(map[string]*rsa.PublicKey{"local-platform": &platformKey.PublicKey}, apiKey, "app", "mch")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := paymenthttp.NewHandler(paymentService, verifier, commerceFundsSecurity{}, true)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:local-app", Value: "verified-local-openid", Source: "local-provider"})
	if err != nil {
		t.Fatal(err)
	}
	if err = handler.SetTrustedSessionIssuer(commerceFundsSessionVerifier{fact: fact}, sessions); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	var deliveryLock sync.Mutex
	var deliveries []commerceFundsPushDelivery
	receiver := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, readErr := io.ReadAll(http.MaxBytesReader(writer, request.Body, 64<<10))
		if readErr != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		deliveryLock.Lock()
		deliveries = append(deliveries, commerceFundsPushDelivery{
			event: request.Header.Get("X-AICRM-Event"), deliveryID: request.Header.Get("X-AICRM-Delivery-Id"),
			timestamp: request.Header.Get("X-AICRM-Timestamp"), signature: request.Header.Get("X-AICRM-Signature"), requestTarget: request.URL.RequestURI(), body: append([]byte(nil), body...),
		})
		deliveryLock.Unlock()
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	commerceTarget := outbound.CommercePushTarget{
		Reference: "commerce-funds-target", Slot: "commerce-funds-slot", Endpoint: receiver.URL + "/legacy/push?tenant=commerce&mode=paid", SigningKey: []byte("commerce-funds-signing-key"), Version: "legacy-v1", TenantID: "aicrm", AllowLoopbackHTTP: true,
		BuyerID:          outbound.CommercePushIdentity{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:commerce-fixture"},
		BuyerOpenID:      outbound.CommercePushIdentity{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:commerce-fixture"},
		BuyerUnionID:     outbound.CommercePushIdentity{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:commerce-fixture"},
		BuyerPhone:       outbound.CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"},
		BeneficiaryPhone: outbound.CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"},
	}
	if err = outbound.ValidateCommercePushTarget(commerceTarget); err != nil {
		t.Fatal(err)
	}
	commerceCipher, err := outbound.NewCommercePayloadAESGCM(base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	if err != nil {
		t.Fatal(err)
	}
	commercePush, err := outbound.NewCommercePushService(pool, uow, effectStore, commerceFundsPushConfiguration{value: productport.ExternalPushConfiguration{ProductID: product.ID, ProductKind: productport.ExternalPushServicePeriod, Enabled: true, ConfigurationReference: commerceTarget.Reference, PushType: "service_period", Day: commerceFundsInt64(30), Frequency: commerceFundsInt64(1), Remark: "commerce-funds-fixture", CustomParams: map[string]any{"nested": map[string]any{"not": "paid payload"}}, Revision: 1, ProductName: product.Name, UpdatedAt: now}}, commerceFundsPushIdentityReader{customerID: customerID}, commerceFundsPushTargets{target: commerceTarget}, commerceCipher)
	if err != nil {
		t.Fatal(err)
	}
	commerceCompletion, err := outbound.NewCommercePushCompletionSink(commercePush)
	if err != nil {
		t.Fatal(err)
	}
	if err = effectStore.SetCompletionSink(commerceCompletion); err != nil {
		t.Fatal(err)
	}
	commerceProvider, err := outbound.NewCommercePushProvider(true, commercePush, commerceFundsPushTargets{target: commerceTarget}, commerceCipher)
	if err != nil {
		t.Fatal(err)
	}
	if err = orderService.SetPaidEventConsumer(commercePush); err != nil {
		t.Fatal(err)
	}
	days := int32(7)
	rules := couponapp.NewService(uow, coupons, commerceCheckoutProductFacts{17: {ID: 17, ProductType: productport.ProductOptionServicePeriod, Currency: "CNY", PriceMinor: product.PriceMinor}}, coupons)
	rule, err := rules.Create(ctx, couponport.UpsertCommand{Coupon: couponport.Coupon{Name: "资金联合券", DiscountAmountTotal: 200, TotalIssueLimit: 1, PerUserIssueLimit: 1, ClaimStartsAt: now.Add(-time.Hour), ClaimEndsAt: now.Add(time.Hour), ValidityMode: couponport.ValidityRelativeDays, RelativeValidityDays: &days, TargetRefs: []string{"service_period:17"}}, Actor: 1, IdempotencyKey: "commerce-funds-rule-create-0001"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rules.Publish(ctx, rule.ID, 1, "commerce-funds-rule-publish-0001"); err != nil {
		t.Fatal(err)
	}
	claim, err := couponCheckout.Claim(ctx, couponport.ClaimCommand{CouponID: rule.ID, HolderCustomerID: customerID, ActorScope: "commerce-funds-payer", IdempotencyKey: "commerce-funds-claim-0001", ClaimedAt: now})
	if err != nil {
		t.Fatal(err)
	}

	issue := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/sessions", bytes.NewReader(commerceFundsJSON(t, map[string]string{"code": "provider-verified-session-code"})))
	issue.Header.Set("Content-Type", "application/json")
	issued := httptest.NewRecorder()
	handler.ServeHTTP(issued, issue)
	if issued.Code != http.StatusCreated {
		t.Fatalf("issue status=%d body=%s", issued.Code, issued.Body.String())
	}
	sessionCookie := commerceFundsCookie(t, issued.Result().Cookies(), paymentport.TrustedSessionCookieName)
	if !sessionCookie.HttpOnly || sessionCookie.Value == "" {
		t.Fatalf("unsafe session cookie=%+v", sessionCookie)
	}
	bindingRequest := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkout-session", nil)
	bindingRequest.AddCookie(sessionCookie)
	bindingResponse := httptest.NewRecorder()
	handler.ServeHTTP(bindingResponse, bindingRequest)
	binding := commerceFundsObject(t, bindingResponse, http.StatusOK)["checkout_session_binding"].(string)
	if binding == "" || binding == sessionCookie.Value {
		t.Fatalf("binding is not opaque=%q", binding)
	}

	checkoutPayload := map[string]any{"product_id": 17, "product_kind": "service_period", "coupon_claim_id": claim.ClaimID, "beneficiary_selection": "payer_self", "checkout_session_binding": binding}
	checkoutRequest := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", bytes.NewReader(commerceFundsJSON(t, checkoutPayload)))
	checkoutRequest.Header.Set("Content-Type", "application/json")
	checkoutRequest.Header.Set("Idempotency-Key", "commerce-funds-checkout-0001")
	checkoutRequest.AddCookie(sessionCookie)
	checkoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(checkoutResponse, checkoutRequest)
	checkout := commerceFundsObject(t, checkoutResponse, http.StatusAccepted)
	orderID := commerceFundsInt(t, checkout, "order_id")
	paymentID := commerceFundsInt(t, checkout, "payment_id")
	merchant := commerceFundsString(t, checkout, "merchant_order_no")
	commerceFundsAssertReserved(t, ctx, pool, orderID, paymentID, claim.ClaimID)

	unknownBody, unknownHeaders := commerceFundsSignedCallback(t, platformKey, apiKey, "commerce-funds-unknown", "TRANSACTION.SUCCESS", map[string]any{"appid": "app", "mchid": "mch", "out_trade_no": "v3pay_unknown_funds", "transaction_id": "tx-unknown", "trade_state": "SUCCESS", "success_time": now.Format(time.RFC3339Nano), "amount": map[string]any{"total": 1000, "currency": "CNY"}})
	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/payment", unknownBody, unknownHeaders))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("out-of-order status=%d body=%s", unknown.Code, unknown.Body.String())
	}

	paymentBody, paymentHeaders := commerceFundsSignedCallback(t, platformKey, apiKey, "commerce-funds-payment", "TRANSACTION.SUCCESS", map[string]any{"appid": "app", "mchid": "mch", "out_trade_no": merchant, "transaction_id": "tx-commerce-funds", "trade_state": "SUCCESS", "success_time": now.Add(time.Second).Format(time.RFC3339Nano), "amount": map[string]any{"total": 1000, "currency": "CNY"}})
	badHeaders := paymentHeaders.Clone()
	badHeaders.Set("Wechatpay-Signature", "bad")
	bad := httptest.NewRecorder()
	handler.ServeHTTP(bad, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/payment", paymentBody, badHeaders))
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("invalid signature status=%d body=%s", bad.Code, bad.Body.String())
	}
	commerceFundsAssertRollback(t, ctx, pool, orderID, paymentID, merchant, 0)

	if err = orderService.SetServicePeriodEntitlementCoordinator(commerceFundsFailingEntitlement{err: errors.New("forced entitlement failure")}); err != nil {
		t.Fatal(err)
	}
	failed := httptest.NewRecorder()
	handler.ServeHTTP(failed, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/payment", paymentBody, paymentHeaders))
	if failed.Code != http.StatusServiceUnavailable {
		t.Fatalf("forced settlement status=%d body=%s", failed.Code, failed.Body.String())
	}
	commerceFundsAssertRollback(t, ctx, pool, orderID, paymentID, merchant, 0)
	commerceFundsAssertPushRollback(t, ctx, pool)

	fulfillment, err := orderapp.NewEntitlementFulfillmentApplication(orders)
	if err != nil {
		t.Fatal(err)
	}
	if err = orderService.SetServicePeriodEntitlementCoordinator(fulfillment); err != nil {
		t.Fatal(err)
	}
	callbacks := make(chan int, 2)
	var callbackWait sync.WaitGroup
	for range 2 {
		callbackWait.Add(1)
		go func() {
			defer callbackWait.Done()
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/payment", paymentBody, paymentHeaders))
			callbacks <- response.Code
		}()
	}
	callbackWait.Wait()
	close(callbacks)
	for code := range callbacks {
		if code != http.StatusOK {
			t.Fatalf("concurrent payment callback status=%d", code)
		}
	}
	commerceFundsAssertPaid(t, ctx, pool, orderID, paymentID, merchant)
	effectID, generation, riverJobID := commerceFundsAssertPushQueued(t, ctx, pool, orderID)
	deliveryLock.Lock()
	queuedDeliveries := len(deliveries)
	deliveryLock.Unlock()
	if queuedDeliveries != 0 {
		t.Fatalf("Provider was called before EER worker attempted the effect: deliveries=%d", queuedDeliveries)
	}
	if err = effectStore.RunAttempt(ctx, effectID, generation, riverJobID, commerceProvider); err != nil {
		t.Fatal(err)
	}
	commerceFundsAssertPushDelivered(t, ctx, pool, effectID, commerceTarget.SigningKey, &deliveryLock, deliveries)

	firstRefund := commerceFundsRequestRefund(t, handler, paymentID, 300, "commerce-funds-first-refund", "commerce-funds-first-refund-key")
	firstRefundBody, firstRefundHeaders := commerceFundsSignedCallback(t, platformKey, apiKey, "commerce-funds-refund-1", "REFUND.SUCCESS", map[string]any{"appid": "app", "mchid": "mch", "out_refund_no": firstRefund, "refund_id": "provider-refund-1", "refund_status": "SUCCESS", "success_time": now.Add(2 * time.Second).Format(time.RFC3339Nano), "amount": map[string]any{"refund": 300, "total": 1000, "currency": "CNY"}})
	firstRefundResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstRefundResponse, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/refund", firstRefundBody, firstRefundHeaders))
	if firstRefundResponse.Code != http.StatusOK {
		t.Fatalf("partial refund status=%d body=%s", firstRefundResponse.Code, firstRefundResponse.Body.String())
	}
	replayedRefund := httptest.NewRecorder()
	handler.ServeHTTP(replayedRefund, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/refund", firstRefundBody, firstRefundHeaders))
	if replayedRefund.Code != http.StatusOK {
		t.Fatalf("duplicate partial refund status=%d body=%s", replayedRefund.Code, replayedRefund.Body.String())
	}
	firstEnd, firstUpdated := commerceFundsRefundedEntitlement(t, ctx, pool, orderID, 300)

	type refundAttempt struct {
		code     int
		refundNo string
	}
	attempts := make(chan refundAttempt, 2)
	var refundWait sync.WaitGroup
	for index := 0; index < 2; index++ {
		index := index
		refundWait.Add(1)
		go func() {
			defer refundWait.Done()
			result := commerceFundsRefundRequest(handler, paymentID, 700, "commerce-funds-final-refund-"+strconv.Itoa(index), "commerce-funds-final-refund-key-"+strconv.Itoa(index))
			attempts <- refundAttempt{code: result.code, refundNo: result.refundNo}
		}()
	}
	refundWait.Wait()
	close(attempts)
	var finalRefund string
	var accepted, conflicted int
	for attempt := range attempts {
		if attempt.code == http.StatusAccepted {
			accepted++
			finalRefund = attempt.refundNo
		} else if attempt.code == http.StatusConflict {
			conflicted++
		} else {
			t.Fatalf("concurrent refund status=%d", attempt.code)
		}
	}
	if accepted != 1 || conflicted != 1 || finalRefund == "" {
		t.Fatalf("concurrent refunds accepted=%d conflicted=%d refund=%q", accepted, conflicted, finalRefund)
	}
	finalRefundBody, finalRefundHeaders := commerceFundsSignedCallback(t, platformKey, apiKey, "commerce-funds-refund-2", "REFUND.SUCCESS", map[string]any{"appid": "app", "mchid": "mch", "out_refund_no": finalRefund, "refund_id": "provider-refund-2", "refund_status": "SUCCESS", "success_time": now.Add(3 * time.Second).Format(time.RFC3339Nano), "amount": map[string]any{"refund": 700, "total": 1000, "currency": "CNY"}})
	finalRefundResponse := httptest.NewRecorder()
	handler.ServeHTTP(finalRefundResponse, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/refund", finalRefundBody, finalRefundHeaders))
	if finalRefundResponse.Code != http.StatusOK {
		t.Fatalf("final refund status=%d body=%s", finalRefundResponse.Code, finalRefundResponse.Body.String())
	}
	duplicateFinal := httptest.NewRecorder()
	handler.ServeHTTP(duplicateFinal, commerceFundsCallbackRequest("/api/public/wechat-pay/callbacks/refund", finalRefundBody, finalRefundHeaders))
	if duplicateFinal.Code != http.StatusOK {
		t.Fatalf("duplicate final refund status=%d body=%s", duplicateFinal.Code, duplicateFinal.Body.String())
	}
	commerceFundsAssertFinal(t, ctx, pool, orderID, paymentID, merchant, firstEnd, firstUpdated)
}

func commerceFundsInt64(value int64) *int64 { return &value }

func commerceFundsJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
func commerceFundsObject(t *testing.T, response *httptest.ResponseRecorder, want int) map[string]any {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status=%d body=%s want=%d", response.Code, response.Body.String(), want)
	}
	var object map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &object); err != nil {
		t.Fatal(err)
	}
	return object
}
func commerceFundsInt(t *testing.T, value map[string]any, key string) int64 {
	t.Helper()
	number, ok := value[key].(float64)
	if !ok || number < 1 || number != float64(int64(number)) {
		t.Fatalf("invalid %s=%#v", key, value[key])
	}
	return int64(number)
}
func commerceFundsString(t *testing.T, value map[string]any, key string) string {
	t.Helper()
	text, ok := value[key].(string)
	if !ok || text == "" {
		t.Fatalf("invalid %s=%#v", key, value[key])
	}
	return text
}
func commerceFundsCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("missing %s cookie", name)
	return nil
}

func commerceFundsRequestRefund(t *testing.T, handler http.Handler, paymentID, amount int64, refundNo, key string) string {
	t.Helper()
	result := commerceFundsRefundRequest(handler, paymentID, amount, refundNo, key)
	if result.code != http.StatusAccepted || result.refundNo != refundNo {
		t.Fatalf("refund status=%d refund=%q body=%s", result.code, result.refundNo, result.body)
	}
	return result.refundNo
}
func commerceFundsRefundRequest(handler http.Handler, paymentID, amount int64, refundNo, key string) struct {
	code     int
	refundNo string
	body     string
} {
	request := httptest.NewRequest(http.MethodPost, "/api/admin/payments/"+strconv.FormatInt(paymentID, 10)+"/refunds", bytes.NewReader(commerceFundsJSONNoTest(map[string]any{"amount_minor": amount, "refund_no": refundNo, "reason": "用户申请退款"})))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var object map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &object)
	got, _ := object["out_refund_no"].(string)
	return struct {
		code     int
		refundNo string
		body     string
	}{code: response.Code, refundNo: got, body: response.Body.String()}
}
func commerceFundsJSONNoTest(value any) []byte {
	body, _ := json.Marshal(value)
	return body
}

func commerceFundsAssertPushRollback(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var events, intents, effects, jobs int
	err := pool.QueryRow(ctx, `SELECT
  (SELECT count(*) FROM order_paid_events),
  (SELECT count(*) FROM outbound_commerce_push_intents),
  (SELECT count(*) FROM external_effects WHERE kind=$1),
  (SELECT count(*) FROM external_effect_jobs job JOIN external_effects effect ON effect.id=job.effect_id WHERE effect.kind=$1)`, effectport.KindCommerceProductPush).Scan(&events, &intents, &effects, &jobs)
	if err != nil || events != 0 || intents != 0 || effects != 0 || jobs != 0 {
		t.Fatalf("paid external push did not roll back events/intents/effects/jobs=%d/%d/%d/%d err=%v", events, intents, effects, jobs, err)
	}
}

func commerceFundsAssertPushQueued(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID int64) (int64, int64, int64) {
	t.Helper()
	var paidEvents, intents, audits, outbox int
	var effectID, generation, riverJobID int64
	var intentState, effectState string
	err := pool.QueryRow(ctx, `SELECT
  (SELECT count(*) FROM order_paid_events WHERE order_id=$1),
  (SELECT count(*) FROM outbound_commerce_push_intents intent JOIN order_paid_events event ON event.id=intent.order_paid_event_id WHERE event.order_id=$1),
  (SELECT count(*) FROM outbound_commerce_push_audit_events),
  (SELECT count(*) FROM outbound_commerce_push_outbox),
  effect.id,effect.generation,job.river_job_id,intent.state,effect.state
FROM outbound_commerce_push_intents intent
JOIN order_paid_events event ON event.id=intent.order_paid_event_id
JOIN external_effects effect ON effect.id=substring(intent.effect_id FROM 5)::bigint
JOIN external_effect_jobs job ON job.effect_id=effect.id AND job.generation=effect.generation
WHERE event.order_id=$1`, orderID).Scan(&paidEvents, &intents, &audits, &outbox, &effectID, &generation, &riverJobID, &intentState, &effectState)
	if err != nil || paidEvents != 1 || intents != 1 || audits != 1 || outbox != 1 || effectID < 1 || generation < 1 || riverJobID < 1 || intentState != "queued" || effectState != string(effectport.StateQueued) {
		t.Fatalf("paid push queue facts paid_events/intents/audits/outbox/effect/generation/job/intent/effect=%d/%d/%d/%d/%d/%d/%d/%q/%q err=%v", paidEvents, intents, audits, outbox, effectID, generation, riverJobID, intentState, effectState, err)
	}
	return effectID, generation, riverJobID
}

func commerceFundsAssertPushDelivered(t *testing.T, ctx context.Context, pool *pgxpool.Pool, effectID int64, signingKey []byte, lock *sync.Mutex, deliveries []commerceFundsPushDelivery) {
	t.Helper()
	lock.Lock()
	deferred := append([]commerceFundsPushDelivery(nil), deliveries...)
	lock.Unlock()
	if len(deferred) != 1 {
		t.Fatalf("signed commerce provider deliveries=%d", len(deferred))
	}
	delivery := deferred[0]
	mac := hmac.New(sha256.New, signingKey)
	_, _ = mac.Write([]byte(delivery.timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(delivery.body)
	expectedSignature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if delivery.event != "transaction.paid" || delivery.deliveryID == "" || delivery.timestamp == "" || delivery.requestTarget != "/legacy/push?tenant=commerce&mode=paid" || !hmac.Equal([]byte(delivery.signature), []byte(expectedSignature)) {
		t.Fatalf("legacy signed delivery contract event=%q delivery_present=%t timestamp_present=%t request_target=%q signature_match=%t", delivery.event, delivery.deliveryID != "", delivery.timestamp != "", delivery.requestTarget, hmac.Equal([]byte(delivery.signature), []byte(expectedSignature)))
	}
	var body struct {
		PhoneNumber string `json:"phone_number"`
		PushType    string `json:"type"`
		Day         *int64 `json:"day"`
		Frequency   *int64 `json:"frequency"`
		Remark      string `json:"remark"`
		Event       string `json:"event"`
		DeliveryID  string `json:"delivery_id"`
		Order       struct {
			Status     string `json:"status"`
			PaidAmount int64  `json:"paid_amount"`
		} `json:"order"`
		Product struct {
			Price int64 `json:"price"`
		} `json:"product"`
		Buyer       struct{ ID, OpenID, UnionID, Phone string } `json:"buyer"`
		Transaction struct {
			TransactionID string `json:"transaction_id"`
			TradeState    string `json:"trade_state"`
			SuccessTime   string `json:"success_time"`
		} `json:"transaction"`
		DomainEventOutboxID int64 `json:"domain_event_outbox_id"`
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(delivery.body, &body) != nil || json.Unmarshal(delivery.body, &raw) != nil || body.PhoneNumber != "13800138000" || body.PushType != "service_period" || body.Day == nil || *body.Day != 30 || body.Frequency == nil || *body.Frequency != 1 || body.Remark != "commerce-funds-fixture" || body.Event != "transaction.paid" || body.DeliveryID != delivery.deliveryID || body.Order.Status != "paid" || body.Order.PaidAmount != 1000 || body.Product.Price != 1200 || body.Buyer.ID != "fixture-buyer" || body.Buyer.OpenID != "fixt***enid" || body.Buyer.UnionID != "fixture-unionid" || body.Buyer.Phone != "13800138000" || body.Transaction.TransactionID != "tx-commerce-funds" || body.Transaction.TradeState != "SUCCESS" || body.Transaction.SuccessTime == "" || body.DomainEventOutboxID < 1 {
		t.Fatalf("legacy paid payload did not preserve frozen member, transaction, and payer facts")
	}
	if _, found := raw["custom_params"]; found {
		t.Fatalf("synthetic-only custom params leaked into a paid delivery")
	}
	var expectedOutboxID int64
	var expectedOccurredAt time.Time
	err := pool.QueryRow(ctx, `SELECT outbox.id,event.occurred_at
FROM outbound_commerce_push_intents intent
JOIN order_paid_events event ON event.id=intent.order_paid_event_id
JOIN order_outbox outbox ON outbox.idempotency_key=('order.paid.v1:' || event.id::text)
WHERE intent.effect_id=$1`, "eer_"+strconv.FormatInt(effectID, 10)).Scan(&expectedOutboxID, &expectedOccurredAt)
	if err != nil || body.DomainEventOutboxID != expectedOutboxID || body.Transaction.SuccessTime != expectedOccurredAt.UTC().Format(time.RFC3339) {
		t.Fatalf("paid delivery did not carry its immutable order outbox fact id=%d want=%d occurred=%q want=%q err=%v", body.DomainEventOutboxID, expectedOutboxID, body.Transaction.SuccessTime, expectedOccurredAt.UTC().Format(time.RFC3339), err)
	}
	var intentState, effectState string
	var calls int
	var attempted, executed bool
	err = pool.QueryRow(ctx, `SELECT intent.state,effect.state,effect.attempt_count,attempt.call_attempted,attempt.real_external_call_executed
FROM outbound_commerce_push_intents intent
JOIN external_effects effect ON effect.id=substring(intent.effect_id FROM 5)::bigint
JOIN external_effect_attempts attempt ON attempt.effect_id=effect.id AND attempt.number=1
WHERE effect.id=$1`, effectID).Scan(&intentState, &effectState, &calls, &attempted, &executed)
	if err != nil || intentState != "provider_accepted" || effectState != string(effectport.StateExecuted) || calls != 1 || !attempted || !executed {
		t.Fatalf("commerce effect completion intent/effect/calls/attempted/executed=%q/%q/%d/%t/%t err=%v", intentState, effectState, calls, attempted, executed, err)
	}
}

func commerceFundsAssertReserved(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, paymentID, claimID int64) {
	t.Helper()
	var orderStatus, paymentStatus, redemptionStatus, claimStatus string
	var payable int64
	err := pool.QueryRow(ctx, "SELECT (SELECT status FROM orders WHERE id=$1),(SELECT status FROM payments WHERE id=$2),(SELECT status FROM coupon_order_redemptions WHERE claim_id=$3),(SELECT status FROM coupon_customer_claims WHERE id=$3),(SELECT payable_amount_minor FROM order_checkout_snapshots WHERE order_id=$1)", orderID, paymentID, claimID).Scan(&orderStatus, &paymentStatus, &redemptionStatus, &claimStatus, &payable)
	if err != nil || orderStatus != "pending_payment" || paymentStatus != "awaiting_prepay" || redemptionStatus != "reserved" || claimStatus != "reserved" || payable != 1000 {
		t.Fatalf("reserved order=%q payment=%q redemption=%q claim=%q payable=%d err=%v", orderStatus, paymentStatus, redemptionStatus, claimStatus, payable, err)
	}
}
func commerceFundsAssertRollback(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, paymentID int64, merchant string, callbacks int) {
	t.Helper()
	var orderStatus, paymentStatus, redemptionStatus, claimStatus string
	var callbackCount, entitlementCount, consumeCount int
	err := pool.QueryRow(ctx, "SELECT (SELECT status FROM orders WHERE id=$1),(SELECT status FROM payments WHERE id=$2),(SELECT status FROM coupon_order_redemptions WHERE order_reference=$3),(SELECT status FROM coupon_customer_claims WHERE id=(SELECT claim_id FROM coupon_order_redemptions WHERE order_reference=$3)),(SELECT count(*) FROM payment_callback_receipts),(SELECT count(*) FROM order_service_entitlements),(SELECT count(*) FROM coupon_redemption_operation_receipts receipt JOIN coupon_order_redemptions redemption ON redemption.id=receipt.redemption_id WHERE redemption.order_reference=$3 AND receipt.operation='consume')", orderID, paymentID, merchant).Scan(&orderStatus, &paymentStatus, &redemptionStatus, &claimStatus, &callbackCount, &entitlementCount, &consumeCount)
	if err != nil || orderStatus != "pending_payment" || paymentStatus != "awaiting_prepay" || redemptionStatus != "reserved" || claimStatus != "reserved" || callbackCount != callbacks || entitlementCount != 0 || consumeCount != 0 {
		t.Fatalf("rollback order=%q payment=%q redemption=%q claim=%q callbacks=%d entitlements=%d consumes=%d err=%v", orderStatus, paymentStatus, redemptionStatus, claimStatus, callbackCount, entitlementCount, consumeCount, err)
	}
}
func commerceFundsAssertPaid(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, paymentID int64, merchant string) {
	t.Helper()
	var orderStatus, paymentStatus, redemptionStatus, claimStatus, entitlementStatus string
	var callbackCount, grantCount, consumeCount int
	err := pool.QueryRow(ctx, "SELECT (SELECT status FROM orders WHERE id=$1),(SELECT status FROM payments WHERE id=$2),(SELECT status FROM coupon_order_redemptions WHERE order_reference=$3),(SELECT status FROM coupon_customer_claims WHERE id=(SELECT claim_id FROM coupon_order_redemptions WHERE order_reference=$3)),(SELECT status FROM order_service_entitlements WHERE last_order_id=$1),(SELECT count(*) FROM payment_callback_receipts),(SELECT count(*) FROM order_entitlement_fulfillment_receipts WHERE operation='grant' AND source_order_id=$1),(SELECT count(*) FROM coupon_redemption_operation_receipts receipt JOIN coupon_order_redemptions redemption ON redemption.id=receipt.redemption_id WHERE redemption.order_reference=$3 AND receipt.operation='consume')", orderID, paymentID, merchant).Scan(&orderStatus, &paymentStatus, &redemptionStatus, &claimStatus, &entitlementStatus, &callbackCount, &grantCount, &consumeCount)
	if err != nil || orderStatus != "paid" || paymentStatus != "paid" || redemptionStatus != "consumed" || claimStatus != "redeemed" || entitlementStatus != "active" || callbackCount != 1 || grantCount != 1 || consumeCount != 1 {
		t.Fatalf("paid order=%q payment=%q redemption=%q claim=%q entitlement=%q callbacks=%d grants=%d consumes=%d err=%v", orderStatus, paymentStatus, redemptionStatus, claimStatus, entitlementStatus, callbackCount, grantCount, consumeCount, err)
	}
}
func commerceFundsRefundedEntitlement(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, amount int64) (time.Time, time.Time) {
	t.Helper()
	var status string
	var endAt, updatedAt time.Time
	var firstAmount int64
	err := pool.QueryRow(ctx, "SELECT entitlement.status,entitlement.end_at,entitlement.updated_at,receipt.refund_amount_minor FROM order_service_entitlements entitlement JOIN order_entitlement_fulfillment_receipts receipt ON receipt.operation='refund' AND receipt.source_order_id=$1 WHERE entitlement.last_order_id=$1", orderID).Scan(&status, &endAt, &updatedAt, &firstAmount)
	if err != nil || status != "refunded" || firstAmount != amount {
		t.Fatalf("partial refund entitlement status=%q amount=%d end=%s updated=%s err=%v", status, firstAmount, endAt, updatedAt, err)
	}
	return endAt, updatedAt
}
func commerceFundsAssertFinal(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID, paymentID int64, merchant string, firstEnd, firstUpdated time.Time) {
	t.Helper()
	var orderStatus, paymentStatus, redemptionStatus, entitlementStatus string
	var completedRefunds, entitlementReceipts, callbackReceipts int
	var endAt, updatedAt time.Time
	err := pool.QueryRow(ctx, "SELECT (SELECT status FROM orders WHERE id=$1),(SELECT status FROM payments WHERE id=$2),(SELECT status FROM coupon_order_redemptions WHERE order_reference=$3),(SELECT status FROM order_service_entitlements WHERE last_order_id=$1),(SELECT count(*) FROM payment_refunds WHERE payment_id=$2 AND status='completed'),(SELECT count(*) FROM order_entitlement_fulfillment_receipts WHERE operation='refund' AND source_order_id=$1),(SELECT count(*) FROM payment_callback_receipts),(SELECT end_at FROM order_service_entitlements WHERE last_order_id=$1),(SELECT updated_at FROM order_service_entitlements WHERE last_order_id=$1)", orderID, paymentID, merchant).Scan(&orderStatus, &paymentStatus, &redemptionStatus, &entitlementStatus, &completedRefunds, &entitlementReceipts, &callbackReceipts, &endAt, &updatedAt)
	if err != nil || orderStatus != "refunded" || paymentStatus != "paid" || redemptionStatus != "consumed" || entitlementStatus != "refunded" || completedRefunds != 2 || entitlementReceipts != 1 || callbackReceipts != 3 || !endAt.Equal(firstEnd) || !updatedAt.Equal(firstUpdated) {
		t.Fatalf("final order=%q payment=%q redemption=%q entitlement=%q refunds=%d receipts=%d callbacks=%d end=%s updated=%s err=%v", orderStatus, paymentStatus, redemptionStatus, entitlementStatus, completedRefunds, entitlementReceipts, callbackReceipts, endAt, updatedAt, err)
	}
}

func commerceFundsSignedCallback(t *testing.T, platformKey *rsa.PrivateKey, apiKey []byte, eventID, eventType string, payload map[string]any) ([]byte, http.Header) {
	t.Helper()
	plain := commerceFundsJSON(t, payload)
	block, err := aes.NewCipher(apiKey)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("nonce:" + eventID))
	resourceNonce := hex.EncodeToString(digest[:6])
	associated := "transaction"
	ciphertext := base64.StdEncoding.EncodeToString(gcm.Seal(nil, []byte(resourceNonce), plain, []byte(associated)))
	body := commerceFundsJSON(t, map[string]any{"id": eventID, "event_type": eventType, "resource": map[string]string{"algorithm": "AEAD_AES_256_GCM", "ciphertext": ciphertext, "nonce": resourceNonce, "associated_data": associated}})
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	headerNonce := "local-" + hex.EncodeToString(digest[6:12])
	signature := commerceFundsSign(t, platformKey, timestamp+"\n"+headerNonce+"\n"+string(body)+"\n")
	return body, http.Header{"Wechatpay-Timestamp": {timestamp}, "Wechatpay-Nonce": {headerNonce}, "Wechatpay-Serial": {"local-platform"}, "Wechatpay-Signature": {signature}}
}
func commerceFundsSign(t *testing.T, key *rsa.PrivateKey, message string) string {
	t.Helper()
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(cryptorand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(signature)
}
func commerceFundsCallbackRequest(path string, body []byte, headers http.Header) *http.Request {
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	for key, values := range headers {
		request.Header[key] = append([]string(nil), values...)
	}
	return request
}
