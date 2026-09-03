package migration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUsesCanonicalSnakeCaseAndRejectsIncompleteFinancialShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	raw := `{
  "schema_version":"aicrm-commerce-history-v3",
  "run_key":"production-commerce-20260903",
  "coverage":{"identities":true,"wechat_pay_orders":true,"wechat_pay_refunds":true,"wechat_shop_orders":true,"wechat_shop_refunds":true,"alipay_orders":true},
  "subjects":[{"source_key":"person-1","identity_keys":["identity-1"]}],
  "identities":[{"source_key":"identity-1","kind":"mp_openid","scope":"wechat-app:app","value":"opaque","source":"provider-history:wechat-pay"}],
  "identity_quarantines":[{"source_key":"identity-no-scope","reason_code":"missing_unionid_scope","evidence_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],
  "orders":[{"provider":"wechat_pay","source_key":"order-1","merchant_order_no":"merchant-1","provider_transaction_no":"transaction-1","payer_identity_key":"identity-1","payer_subject_key":"person-1","beneficiary_subject_key":"person-1","amount_minor":100,"currency":"CNY","status":"partially_refunded","items":[{"line_no":1,"product_code":"legacy","product_name":"历史交易","unit_amount_minor":50,"quantity":2,"line_amount_minor":100}],"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-02T00:00:00Z"}],
  "refunds":[{"provider":"wechat_pay","source_key":"refund-1","merchant_order_no":"merchant-1","refund_no":"refund-1","provider_refund_no":"provider-refund-1","amount_minor":40,"reason":"历史退款","occurred_at":"2026-09-02T00:00:00Z"}]
}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Orders[0].SourceKey != "order-1" || manifest.Identities[0].SourceKey != "identity-1" || manifest.Refunds[0].ProviderRefundNo != "provider-refund-1" {
		t.Fatalf("snake_case fields were not decoded: %#v", manifest)
	}
	if summary := manifest.Summary(); summary.SubjectRows != 1 || summary.IdentityQuarantineRows != 1 || summary.PaymentRows != 1 || summary.AmountMinor != 100 || summary.RefundMinor != 40 || !summary.Complete {
		t.Fatalf("unexpected summary: %#v", summary)
	}

	manifest.Refunds[0].AmountMinor = 101
	if err := manifest.Validate(true); err == nil {
		t.Fatal("refunds exceeding the source order were accepted")
	}
}

func TestValidateRejectsIdentityAssignedToTwoSubjectsAndFloatingRefund(t *testing.T) {
	manifest := Manifest{
		SchemaVersion: SchemaVersion,
		RunKey:        "run",
		Coverage:      Coverage{Identities: true, WeChatPayOrders: true, WeChatPayRefunds: true, WeChatShopOrders: true, WeChatShopRefunds: true, AlipayOrders: true},
		Subjects:      []SubjectRow{{SourceKey: "a", IdentityKeys: []string{"i"}}, {SourceKey: "b", IdentityKeys: []string{"i"}}},
		Identities:    []IdentityRow{{SourceKey: "i", Kind: "mp_openid", Scope: "wechat-app:app", Value: "value", Source: "history"}},
	}
	if err := manifest.Validate(true); err == nil {
		t.Fatal("identity assigned to two source subjects")
	}
}
