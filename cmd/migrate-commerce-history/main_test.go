package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectAllowsIncompleteButDryRunFailsClosed(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "snapshot.json")
	raw := `{"schema_version":"aicrm-commerce-history-v3","run_key":"test-run","coverage":{"identities":true},"subjects":[],"identities":[],"identity_quarantines":[],"orders":[],"refunds":[]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--mode=inspect", "--snapshot=" + path}); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--mode=dry-run", "--snapshot=" + path}); err == nil {
		t.Fatal("dry-run accepted incomplete source coverage")
	}
}

func TestOrderOnlyDryRunAcceptsOnlyFloatingWeChatPaySnapshot(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "orders.json")
	raw := `{"schema_version":"aicrm-commerce-history-v3","run_key":"orders-only-test","coverage":{"wechat_pay_orders":true},"subjects":[],"identities":[],"identity_quarantines":[],"orders":[{"provider":"wechat_pay","source_key":"source-1","merchant_order_no":"merchant-1","provider_transaction_no":"transaction-1","payer_identity_key":"","payer_subject_key":"","beneficiary_subject_key":"","amount_minor":100,"currency":"CNY","status":"paid","items":[{"line_no":1,"product_code":"product-1","product_name":"Product 1","unit_amount_minor":100,"quantity":1,"line_amount_minor":100}],"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}],"refunds":[]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--order-only", "--mode=dry-run", "--snapshot=" + path}); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--mode=dry-run", "--snapshot=" + path}); err == nil {
		t.Fatal("full-commerce dry-run accepted order-only source coverage")
	}
}

func TestOrderOnlyRejectsIdentityCoverage(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "orders.json")
	raw := `{"schema_version":"aicrm-commerce-history-v3","run_key":"orders-only-test","coverage":{"identities":true,"wechat_pay_orders":true},"subjects":[],"identities":[],"identity_quarantines":[],"orders":[{"provider":"wechat_pay","source_key":"source-1","merchant_order_no":"merchant-1","provider_transaction_no":"","payer_identity_key":"","payer_subject_key":"","beneficiary_subject_key":"","amount_minor":100,"currency":"CNY","status":"paid","items":[{"line_no":1,"product_code":"product-1","product_name":"Product 1","unit_amount_minor":100,"quantity":1,"line_amount_minor":100}],"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}],"refunds":[]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--order-only", "--mode=inspect", "--snapshot=" + path}); err == nil {
		t.Fatal("order-only mode accepted identity coverage")
	}
}
