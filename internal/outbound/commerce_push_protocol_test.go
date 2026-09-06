package outbound

import (
	"context"
	"strings"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

type commercePushIdentityStub map[identitydomain.Kind]string

func (s commercePushIdentityStub) VerifiedExternalIdentityValue(_ context.Context, _ customerdomain.CustomerID, kind identitydomain.Kind, _ string) (string, bool, error) {
	value, found := s[kind]
	return value, found, nil
}

func TestCommercePaidPayloadUsesFrozenLegacyFieldNamesForEveryProduct(t *testing.T) {
	payer, beneficiary, productID := int64(31), int64(77), int64(9)
	at := time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC)
	service := &CommercePushService{identities: commercePushIdentityStub{
		identitydomain.KindPhone:               "13900000000",
		identitydomain.KindWeComExternalUserID: "payer-external",
		identitydomain.KindMPOpenID:            "payer-openid-abcdefgh",
		identitydomain.KindUnionID:             "payer-union",
	}}
	event := orderport.PaidEvent{ID: 41, OrderID: 82, OrderVersion: 3, DomainEventOutboxID: 53, OccurredAt: at, Order: orderdomain.Snapshot{
		ID: 82, Version: 3, MerchantOrderNo: "trade-82", ProviderTransactionNo: "wx-transaction-82", PayerCustomerID: &payer, BeneficiaryCustomerID: &beneficiary,
		Amount: orderdomain.Money{AmountMinor: 9900, Currency: "CNY"},
	}}
	item := orderdomain.ItemSnapshot{LineNo: 1, ProductID: &productID, ProductCode: "member-9", ProductName: "会员九", UnitAmountMinor: 9900}
	day, frequency := int64(30), int64(1)
	target := CommercePushTarget{
		BuyerID: CommercePushIdentity{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:main"}, BuyerOpenID: CommercePushIdentity{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:mpmain"}, BuyerUnionID: CommercePushIdentity{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:main"}, BuyerPhone: CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"}, BeneficiaryPhone: CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"},
		PushType: "member_open", Day: &day, Frequency: &frequency, Remark: "new member",
	}
	body, missing, err := service.paidPayload(context.Background(), event, item, target, "commerce_fixture")
	if err != nil || missing {
		t.Fatalf("paid payload missing=%t err=%v", missing, err)
	}
	const expected = `{"phone_number":"13900000000","type":"member_open","day":30,"frequency":1,"remark":"new member","submitted_at":"2026-09-06T09:02:03+08:00","questionnaire_title":"微信支付开通黄小璨会员","delivery_id":"commerce_fixture","event":"transaction.paid","order":{"id":"82","order_no":"trade-82","out_trade_no":"trade-82","status":"paid","paid_amount":9900,"paid_at":"2026-09-06T01:02:03Z","pay_channel":"wechat"},"product":{"id":"9","code":"member-9","name":"会员九","price":9900},"buyer":{"id":"payer-external","openid":"paye***efgh","unionid":"payer-union","phone":"13900000000"},"transaction":{"transaction_id":"wx-transaction-82","trade_state":"SUCCESS","success_time":"2026-09-06T01:02:03Z"},"domain_event_outbox_id":53}`
	if got := string(body); got != expected {
		t.Fatalf("legacy paid payload mismatch\n got: %s\nwant: %s", got, expected)
	}
	if strings.Contains(string(body), `"OrderNo"`) || strings.Contains(string(body), `"OpenID"`) || strings.Contains(string(body), `"custom_params"`) {
		t.Fatalf("Go or synthetic-only fields leaked into frozen paid payload: %s", body)
	}
}

func TestCommercePaidPayloadDoesNotSplitProtocolByProductKind(t *testing.T) {
	payer, beneficiary, productID := int64(31), int64(77), int64(9)
	at := time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC)
	service := &CommercePushService{identities: commercePushIdentityStub{
		identitydomain.KindPhone:               "13900000000",
		identitydomain.KindWeComExternalUserID: "payer-external",
		identitydomain.KindMPOpenID:            "payer-openid-abcdefgh",
		identitydomain.KindUnionID:             "payer-union",
	}}
	event := orderport.PaidEvent{ID: 41, OrderID: 82, OrderVersion: 3, DomainEventOutboxID: 53, OccurredAt: at, Order: orderdomain.Snapshot{
		ID: 82, Version: 3, MerchantOrderNo: "trade-82", ProviderTransactionNo: "wx-transaction-82", PayerCustomerID: &payer, BeneficiaryCustomerID: &beneficiary,
		Amount: orderdomain.Money{AmountMinor: 8900, Currency: "CNY"},
	}}
	item := orderdomain.ItemSnapshot{LineNo: 1, ProductID: &productID, ProductCode: "course-9", ProductName: "课程九", UnitAmountMinor: 8900}
	target := CommercePushTarget{
		BuyerID: CommercePushIdentity{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:main"}, BuyerOpenID: CommercePushIdentity{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:mpmain"}, BuyerUnionID: CommercePushIdentity{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:main"}, BuyerPhone: CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"}, BeneficiaryPhone: CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"},
		CustomParams: map[string]any{"campaign": "fall", "source": "product"},
	}
	body, missing, err := service.paidPayload(context.Background(), event, item, target, "commerce_standard")
	if err != nil || missing {
		t.Fatalf("paid payload missing=%t err=%v", missing, err)
	}
	const expected = `{"phone_number":"13900000000","type":"","day":null,"frequency":null,"remark":"","submitted_at":"2026-09-06T09:02:03+08:00","questionnaire_title":"微信支付开通黄小璨会员","delivery_id":"commerce_standard","event":"transaction.paid","order":{"id":"82","order_no":"trade-82","out_trade_no":"trade-82","status":"paid","paid_amount":8900,"paid_at":"2026-09-06T01:02:03Z","pay_channel":"wechat"},"product":{"id":"9","code":"course-9","name":"课程九","price":8900},"buyer":{"id":"payer-external","openid":"paye***efgh","unionid":"payer-union","phone":"13900000000"},"transaction":{"transaction_id":"wx-transaction-82","trade_state":"SUCCESS","success_time":"2026-09-06T01:02:03Z"},"domain_event_outbox_id":53}`
	if got := string(body); got != expected {
		t.Fatalf("paid payload mismatch\n got: %s\nwant: %s", got, expected)
	}
	if strings.Contains(string(body), "custom_params") || !strings.Contains(string(body), "phone_number") || !strings.Contains(string(body), "questionnaire_title") {
		t.Fatalf("paid product used a non-frozen protocol: %s", body)
	}
}

func TestCommercePushSignatureUsesLegacyDotAndExactBody(t *testing.T) {
	body := []byte(`{"delivery_id":"commerce_1","event":"transaction.paid"}`)
	if got := commercePushSignature([]byte(" secret "), " 1788570123 ", body); got != "sha256=20fdc9ca74a5dfd4e162d8913c6d3feed58745aa83b01c726d44dfda679d8d8b" {
		t.Fatalf("legacy signature=%s", got)
	}
	if got := commercePushSignature(nil, "1788570123", body); got != "" {
		t.Fatalf("empty legacy secret signature=%q", got)
	}
}

func TestCommerceSyntheticPayloadUsesFrozenLegacyFieldNames(t *testing.T) {
	at := time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC)
	body, err := commerceSyntheticPayload(7, "Test", CommercePushTarget{
		Reference: "test-target", Slot: "paid", Endpoint: "http://127.0.0.1", Version: "v1", TenantID: "aicrm", AllowLoopbackHTTP: true,
		BuyerID: CommercePushIdentity{Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:main"}, BuyerOpenID: CommercePushIdentity{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:mpmain"}, BuyerUnionID: CommercePushIdentity{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:main"}, BuyerPhone: CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"}, BeneficiaryPhone: CommercePushIdentity{Kind: identitydomain.KindPhone, Scope: "phone:cn11"},
		CustomParams: map[string]any{"campaign": "control"},
	}, "commerce_test_1", at)
	if err != nil {
		t.Fatal(err)
	}
	const expected = `{"event":"external_push.test","delivery_id":"commerce_test_1","occurred_at":"2026-09-05T01:02:03Z","tenant":{"id":"aicrm"},"product":{"id":"7","name":"Test"},"custom_params":{"campaign":"control"}}`
	if string(body) != expected {
		t.Fatalf("legacy synthetic payload=%s", body)
	}
	if got := commercePushSignature([]byte("fixture-secret"), "1788570123", body); got == "" {
		t.Fatal("synthetic signature was not built")
	}
}

func TestCommerceEndpointPreservesFrozenQueryRequestTarget(t *testing.T) {
	if !validCommerceEndpoint("https://push.example.test/legacy/callback?source=commerce&mode=v1", false) {
		t.Fatal("frozen HTTPS endpoint query was rejected")
	}
	for _, raw := range []string{
		"https://user:pass@push.example.test/callback?x=1",
		"https://push.example.test/callback?x=1#fragment",
		"https://push.example.test:444/callback?x=1",
	} {
		if validCommerceEndpoint(raw, false) {
			t.Fatalf("unsafe endpoint accepted: %s", raw)
		}
	}
}
