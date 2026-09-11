package main

import (
	"encoding/json"
	"testing"
)

func normalizeTestOrder() rawRow {
	return rawRow{"id": json.Number("1"), "out_trade_no": "fixture-order", "order_id": "fixture-shop-order", "amount_total": json.Number("1000"), "currency": "CNY", "status": "paid", "business_status": "deal", "product_count": json.Number("1"), "product_code": "fixture", "product_name": "Fixture", "created_at": "2026-09-01T00:00:00+08:00", "updated_at": "2026-09-02T00:00:00+08:00"}
}
func TestNormalizeFinancialStatusKeepsSourceAmounts(t *testing.T) {
	for _, v := range []struct {
		source string
		refund int64
		want   string
	}{{"paid", 0, "paid"}, {"paid", 400, "partially_refunded"}, {"paid", 1000, "refunded"}, {"closed", 0, "closed"}, {"failed", 0, "payment_failed"}} {
		r := normalizeTestOrder()
		r["status"] = v.source
		r["refunded_amount_total"] = json.Number(jsonNumber(v.refund))
		o, e := normalizeOrder("wechat_pay_orders", r)
		if e != nil || o.Status != v.want || o.AmountMinor != 1000 || o.SourceKey != "aicrm-production:wechat_pay_orders:1" {
			t.Fatalf("status mapping %s: %v", v.source, e)
		}
	}
	bad := normalizeTestOrder()
	bad["status"] = "unrecognized"
	if _, e := normalizeOrder("wechat_pay_orders", bad); e == nil {
		t.Fatal("unknown status guessed")
	}
	bad = normalizeTestOrder()
	bad["status"] = "closed"
	bad["refunded_amount_total"] = json.Number("1")
	if _, e := normalizeOrder("wechat_pay_orders", bad); e == nil {
		t.Fatal("closed refund contradiction accepted")
	}
}
func jsonNumber(n int64) string { b, _ := json.Marshal(n); return string(b) }
func TestNormalizeNonSuccessRefundNeverBecomesCompleted(t *testing.T) {
	for _, state := range []string{"failed", "closed", "PROCESSING", "requested"} {
		r := normalizeTestOrder()
		r["status"] = state
		r["out_refund_no"] = "fixture-refund"
		r["refund_amount_total"] = json.Number("20")
		v, e := normalizeRefund("wechat_pay_refunds", r)
		if e != nil || v.Status != state || v.Completed() {
			t.Fatalf("non-success %s changed", state)
		}
	}
	r := normalizeTestOrder()
	if _, e := normalizeRefund("wechat_shop_refunds", r); e == nil {
		t.Fatal("unreviewed nonzero shop refund mapped")
	}
}
