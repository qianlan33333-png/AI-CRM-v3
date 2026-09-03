package migration

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
)

var ErrInvalidManifest = errors.New("invalid commerce migration manifest")
var ErrIncompleteSource = errors.New("commerce migration source coverage is incomplete")

const SchemaVersion = "aicrm-commerce-history-v2"

type Coverage struct {
	Identities        bool `json:"identities"`
	WeChatPayOrders   bool `json:"wechat_pay_orders"`
	WeChatPayRefunds  bool `json:"wechat_pay_refunds"`
	WeChatShopOrders  bool `json:"wechat_shop_orders"`
	WeChatShopRefunds bool `json:"wechat_shop_refunds"`
	AlipayOrders      bool `json:"alipay_orders"`
}

func (coverage Coverage) Complete() bool {
	return coverage.Identities && coverage.WeChatPayOrders && coverage.WeChatPayRefunds && coverage.WeChatShopOrders && coverage.WeChatShopRefunds && coverage.AlipayOrders
}

type IdentityRow struct {
	SourceKey string `json:"source_key"`
	Kind      string `json:"kind"`
	Scope     string `json:"scope"`
	Value     string `json:"value"`
	Source    string `json:"source"`
}

type OrderRow struct {
	Provider              orderdomain.Provider `json:"provider"`
	SourceKey             string               `json:"source_key"`
	MerchantOrderNo       string               `json:"merchant_order_no"`
	ProviderTransactionNo string               `json:"provider_transaction_no"`
	PayerIdentityKey      string               `json:"payer_identity_key"`
	AmountMinor           int64                `json:"amount_minor"`
	Currency              string               `json:"currency"`
	Status                string               `json:"status"`
	ProductCode           string               `json:"product_code"`
	ProductName           string               `json:"product_name"`
	CreatedAt             time.Time            `json:"created_at"`
	UpdatedAt             time.Time            `json:"updated_at"`
}

type RefundRow struct {
	Provider         orderdomain.Provider `json:"provider"`
	SourceKey        string               `json:"source_key"`
	MerchantOrderNo  string               `json:"merchant_order_no"`
	RefundNo         string               `json:"refund_no"`
	ProviderRefundNo string               `json:"provider_refund_no"`
	AmountMinor      int64                `json:"amount_minor"`
	Reason           string               `json:"reason"`
	OccurredAt       time.Time            `json:"occurred_at"`
}

type Manifest struct {
	SchemaVersion string        `json:"schema_version"`
	RunKey        string        `json:"run_key"`
	Coverage      Coverage      `json:"coverage"`
	Identities    []IdentityRow `json:"identities"`
	Orders        []OrderRow    `json:"orders"`
	Refunds       []RefundRow   `json:"refunds"`
	Digest        [32]byte      `json:"-"`
}

func Load(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.SchemaVersion != SchemaVersion || strings.TrimSpace(manifest.RunKey) != manifest.RunKey || manifest.RunKey == "" {
		return Manifest{}, ErrInvalidManifest
	}
	manifest.Digest = sha256.Sum256(raw)
	if err = manifest.Validate(false); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (manifest Manifest) Validate(requireComplete bool) error {
	if requireComplete && !manifest.Coverage.Complete() {
		return ErrIncompleteSource
	}
	identityKeys := make(map[string]struct{}, len(manifest.Identities))
	for _, row := range manifest.Identities {
		if !valid(row.SourceKey, 200) || !valid(row.Kind, 64) || !valid(row.Scope, 256) || !valid(row.Value, 1024) || !valid(row.Source, 128) {
			return ErrInvalidManifest
		}
		if _, exists := identityKeys[row.SourceKey]; exists {
			return ErrInvalidManifest
		}
		identityKeys[row.SourceKey] = struct{}{}
	}
	orderKeys := make(map[string]struct{}, len(manifest.Orders))
	merchantKeys := make(map[string]int64, len(manifest.Orders))
	for _, row := range manifest.Orders {
		if _, ok := identityKeys[row.PayerIdentityKey]; !ok || !valid(row.SourceKey, 200) || !valid(row.MerchantOrderNo, 200) || len(row.ProviderTransactionNo) > 200 || strings.TrimSpace(row.ProviderTransactionNo) != row.ProviderTransactionNo || row.AmountMinor < 1 || row.Currency != "CNY" || row.CreatedAt.IsZero() || row.UpdatedAt.Before(row.CreatedAt) || !valid(row.ProductCode, 200) || !valid(row.ProductName, 500) {
			return ErrInvalidManifest
		}
		if row.Provider != orderdomain.ProviderWeChatPay && row.Provider != orderdomain.ProviderWeChatShop && row.Provider != orderdomain.ProviderAlipay {
			return ErrInvalidManifest
		}
		key := string(row.Provider) + "\x00" + row.SourceKey
		if _, exists := orderKeys[key]; exists {
			return ErrInvalidManifest
		}
		orderKeys[key] = struct{}{}
		merchantKey := string(row.Provider) + "\x00" + row.MerchantOrderNo
		if _, exists := merchantKeys[merchantKey]; exists {
			return ErrInvalidManifest
		}
		merchantKeys[merchantKey] = row.AmountMinor
	}
	refundKeys := make(map[string]struct{}, len(manifest.Refunds))
	refunded := make(map[string]int64, len(manifest.Refunds))
	for _, row := range manifest.Refunds {
		merchantKey := string(row.Provider) + "\x00" + row.MerchantOrderNo
		amount, orderExists := merchantKeys[merchantKey]
		refundKey := string(row.Provider) + "\x00" + row.SourceKey
		if (row.Provider != orderdomain.ProviderWeChatPay && row.Provider != orderdomain.ProviderWeChatShop) || !orderExists || !valid(row.SourceKey, 200) || !valid(row.MerchantOrderNo, 200) || !valid(row.RefundNo, 200) || len(row.ProviderRefundNo) > 200 || strings.TrimSpace(row.ProviderRefundNo) != row.ProviderRefundNo || row.AmountMinor < 1 || !valid(row.Reason, 500) || row.OccurredAt.IsZero() {
			return ErrInvalidManifest
		}
		if _, exists := refundKeys[refundKey]; exists {
			return ErrInvalidManifest
		}
		refundKeys[refundKey] = struct{}{}
		refunded[merchantKey] += row.AmountMinor
		if refunded[merchantKey] > amount {
			return ErrInvalidManifest
		}
	}
	return nil
}

type Summary struct {
	IdentityRows, OrderRows, PaymentRows, RefundRows int
	AmountMinor, RefundMinor                         int64
	Providers                                        []string
	Complete                                         bool
}

func (manifest Manifest) Summary() Summary {
	providers := map[string]struct{}{}
	result := Summary{IdentityRows: len(manifest.Identities), OrderRows: len(manifest.Orders), RefundRows: len(manifest.Refunds), Complete: manifest.Coverage.Complete()}
	for _, row := range manifest.Orders {
		result.AmountMinor += row.AmountMinor
		providers[string(row.Provider)] = struct{}{}
		if row.Status == string(orderdomain.StatusPaid) && row.Provider != orderdomain.ProviderAlipay {
			result.PaymentRows++
		}
	}
	for _, row := range manifest.Refunds {
		result.RefundMinor += row.AmountMinor
	}
	for provider := range providers {
		result.Providers = append(result.Providers, provider)
	}
	sort.Strings(result.Providers)
	return result
}

func valid(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value
}
