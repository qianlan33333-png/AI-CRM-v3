package migration

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
)

var ErrInvalidManifest = errors.New("invalid commerce migration manifest")
var ErrIncompleteSource = errors.New("commerce migration source coverage is incomplete")

const SchemaVersion = "aicrm-commerce-history-v3"

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

// SubjectRow is the authoritative source-person grouping. It prevents a
// multi-channel historical user from being provisioned as several OneID roots.
type SubjectRow struct {
	SourceKey    string   `json:"source_key"`
	IdentityKeys []string `json:"identity_keys"`
}

// IdentityQuarantineRow accounts for an identity source row that cannot be
// safely normalized (for example a UnionID without Open Platform scope). Safe
// evidence may contain digests and counts only, never the raw identifier.
type IdentityQuarantineRow struct {
	SourceKey      string `json:"source_key"`
	ReasonCode     string `json:"reason_code"`
	EvidenceDigest string `json:"evidence_digest"`
}

var sha256Evidence = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type OrderRow struct {
	Provider              orderdomain.Provider `json:"provider"`
	SourceKey             string               `json:"source_key"`
	MerchantOrderNo       string               `json:"merchant_order_no"`
	ProviderTransactionNo string               `json:"provider_transaction_no"`
	PayerIdentityKey      string               `json:"payer_identity_key"`
	PayerSubjectKey       string               `json:"payer_subject_key"`
	BeneficiarySubjectKey string               `json:"beneficiary_subject_key"`
	AmountMinor           int64                `json:"amount_minor"`
	Currency              string               `json:"currency"`
	Status                string               `json:"status"`
	Items                 []ItemRow            `json:"items"`
	CreatedAt             time.Time            `json:"created_at"`
	UpdatedAt             time.Time            `json:"updated_at"`
}

type ItemRow struct {
	LineNo          int32  `json:"line_no"`
	ProductCode     string `json:"product_code"`
	ProductName     string `json:"product_name"`
	UnitAmountMinor int64  `json:"unit_amount_minor"`
	Quantity        int32  `json:"quantity"`
	LineAmountMinor int64  `json:"line_amount_minor"`
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
	SchemaVersion       string                  `json:"schema_version"`
	RunKey              string                  `json:"run_key"`
	Coverage            Coverage                `json:"coverage"`
	Subjects            []SubjectRow            `json:"subjects"`
	Identities          []IdentityRow           `json:"identities"`
	IdentityQuarantines []IdentityQuarantineRow `json:"identity_quarantines"`
	Orders              []OrderRow              `json:"orders"`
	Refunds             []RefundRow             `json:"refunds"`
	Digest              [32]byte                `json:"-"`
}

func Load(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	return Parse(raw)
}

// Parse decodes one exact snapshot payload and binds its digest to the raw
// bytes. Unknown fields and trailing JSON are rejected so the confirmation
// digest always describes the contract that is actually applied.
func Parse(raw []byte) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || manifest.SchemaVersion != SchemaVersion || strings.TrimSpace(manifest.RunKey) != manifest.RunKey || manifest.RunKey == "" {
		return Manifest{}, ErrInvalidManifest
	}
	manifest.Digest = sha256.Sum256(raw)
	if err := manifest.Validate(false); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (manifest Manifest) Validate(requireComplete bool) error {
	if requireComplete && !manifest.Coverage.Complete() {
		return ErrIncompleteSource
	}
	identityKeys := make(map[string]struct{}, len(manifest.Identities))
	identityValues := make(map[string]struct{}, len(manifest.Identities))
	for _, row := range manifest.Identities {
		if !valid(row.SourceKey, 200) || !valid(row.Kind, 64) || !valid(row.Scope, 256) || !valid(row.Value, 1024) || !valid(row.Source, 128) {
			return ErrInvalidManifest
		}
		if _, exists := identityKeys[row.SourceKey]; exists {
			return ErrInvalidManifest
		}
		valueKey := row.Kind + "\x00" + row.Scope + "\x00" + row.Value
		if _, exists := identityValues[valueKey]; exists {
			return ErrInvalidManifest
		}
		identityKeys[row.SourceKey] = struct{}{}
		identityValues[valueKey] = struct{}{}
	}
	subjectKeys := make(map[string]struct{}, len(manifest.Subjects))
	assignedIdentities := make(map[string]struct{}, len(manifest.Identities))
	for _, row := range manifest.Subjects {
		if !valid(row.SourceKey, 200) || len(row.IdentityKeys) == 0 {
			return ErrInvalidManifest
		}
		if _, exists := subjectKeys[row.SourceKey]; exists {
			return ErrInvalidManifest
		}
		subjectKeys[row.SourceKey] = struct{}{}
		for _, key := range row.IdentityKeys {
			if _, exists := identityKeys[key]; !exists {
				return ErrInvalidManifest
			}
			if _, duplicate := assignedIdentities[key]; duplicate {
				return ErrInvalidManifest
			}
			assignedIdentities[key] = struct{}{}
		}
	}
	if len(assignedIdentities) != len(identityKeys) {
		return ErrInvalidManifest
	}
	quarantineKeys := make(map[string]struct{}, len(manifest.IdentityQuarantines))
	for _, row := range manifest.IdentityQuarantines {
		if !valid(row.SourceKey, 200) || !valid(row.ReasonCode, 100) || !sha256Evidence.MatchString(row.EvidenceDigest) {
			return ErrInvalidManifest
		}
		if _, canonical := identityKeys[row.SourceKey]; canonical {
			return ErrInvalidManifest
		}
		if _, canonicalSubject := subjectKeys[row.SourceKey]; canonicalSubject {
			return ErrInvalidManifest
		}
		if _, duplicate := quarantineKeys[row.SourceKey]; duplicate {
			return ErrInvalidManifest
		}
		quarantineKeys[row.SourceKey] = struct{}{}
	}
	orderKeys := make(map[string]struct{}, len(manifest.Orders))
	merchantKeys := make(map[string]int64, len(manifest.Orders))
	merchantStatuses := make(map[string]string, len(manifest.Orders))
	merchantResolved := make(map[string]bool, len(manifest.Orders))
	for _, row := range manifest.Orders {
		if _, err := orderStatus(row.Status); err != nil {
			return ErrInvalidManifest
		}
		_, payerIdentity := identityKeys[row.PayerIdentityKey]
		_, payerSubject := subjectKeys[row.PayerSubjectKey]
		_, beneficiarySubject := subjectKeys[row.BeneficiarySubjectKey]
		resolved := payerIdentity && payerSubject && beneficiarySubject
		floating := row.PayerIdentityKey == "" && row.PayerSubjectKey == "" && row.BeneficiarySubjectKey == ""
		if (!resolved && !floating) || !valid(row.SourceKey, 200) || !valid(row.MerchantOrderNo, 200) || len(row.ProviderTransactionNo) > 200 || strings.TrimSpace(row.ProviderTransactionNo) != row.ProviderTransactionNo || row.AmountMinor < 1 || row.Currency != "CNY" || row.CreatedAt.IsZero() || row.UpdatedAt.Before(row.CreatedAt) || len(row.Items) == 0 || len(row.Items) > 100 {
			return ErrInvalidManifest
		}
		var itemTotal int64
		lineNumbers := make(map[int32]struct{}, len(row.Items))
		for _, item := range row.Items {
			if item.LineNo < 1 || !valid(item.ProductCode, 200) || !valid(item.ProductName, 500) || item.UnitAmountMinor < 1 || item.Quantity < 1 || item.UnitAmountMinor > math.MaxInt64/int64(item.Quantity) || item.LineAmountMinor != item.UnitAmountMinor*int64(item.Quantity) || itemTotal > math.MaxInt64-item.LineAmountMinor {
				return ErrInvalidManifest
			}
			if _, duplicate := lineNumbers[item.LineNo]; duplicate {
				return ErrInvalidManifest
			}
			lineNumbers[item.LineNo] = struct{}{}
			itemTotal += item.LineAmountMinor
		}
		if itemTotal != row.AmountMinor {
			return ErrInvalidManifest
		}
		if resolved {
			ownsIdentity := false
			for _, subject := range manifest.Subjects {
				if subject.SourceKey == row.PayerSubjectKey {
					for _, key := range subject.IdentityKeys {
						ownsIdentity = ownsIdentity || key == row.PayerIdentityKey
					}
				}
			}
			if !ownsIdentity {
				return ErrInvalidManifest
			}
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
		merchantStatuses[merchantKey] = row.Status
		merchantResolved[merchantKey] = resolved
	}
	refundKeys := make(map[string]struct{}, len(manifest.Refunds))
	refunded := make(map[string]int64, len(manifest.Refunds))
	for _, row := range manifest.Refunds {
		merchantKey := string(row.Provider) + "\x00" + row.MerchantOrderNo
		amount, orderExists := merchantKeys[merchantKey]
		refundKey := string(row.Provider) + "\x00" + row.SourceKey
		if (row.Provider != orderdomain.ProviderWeChatPay && row.Provider != orderdomain.ProviderWeChatShop) || !orderExists || !merchantResolved[merchantKey] || !valid(row.SourceKey, 200) || !valid(row.MerchantOrderNo, 200) || !valid(row.RefundNo, 200) || len(row.ProviderRefundNo) > 200 || strings.TrimSpace(row.ProviderRefundNo) != row.ProviderRefundNo || row.AmountMinor < 1 || !valid(row.Reason, 500) || row.OccurredAt.IsZero() {
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
	for merchantKey, amount := range merchantKeys {
		switch merchantStatuses[merchantKey] {
		case string(orderdomain.StatusPartiallyRefunded):
			if refunded[merchantKey] <= 0 || refunded[merchantKey] >= amount {
				return ErrInvalidManifest
			}
		case string(orderdomain.StatusRefunded):
			if refunded[merchantKey] != amount {
				return ErrInvalidManifest
			}
		default:
			if refunded[merchantKey] != 0 {
				return ErrInvalidManifest
			}
		}
	}
	return nil
}

type Summary struct {
	SubjectRows, IdentityRows, IdentityQuarantineRows, OrderRows, PaymentRows, FloatingOrderRows, RefundRows int
	AmountMinor, RefundMinor                                                                                 int64
	Providers                                                                                                []string
	Complete                                                                                                 bool
}

func (manifest Manifest) Summary() Summary {
	providers := map[string]struct{}{}
	result := Summary{SubjectRows: len(manifest.Subjects), IdentityRows: len(manifest.Identities), IdentityQuarantineRows: len(manifest.IdentityQuarantines), OrderRows: len(manifest.Orders), RefundRows: len(manifest.Refunds), Complete: manifest.Coverage.Complete()}
	for _, row := range manifest.Orders {
		result.AmountMinor += row.AmountMinor
		providers[string(row.Provider)] = struct{}{}
		if row.PayerIdentityKey == "" {
			result.FloatingOrderRows++
		}
		if row.PayerIdentityKey != "" && row.Status != string(orderdomain.StatusPendingPayment) && row.Provider != orderdomain.ProviderAlipay {
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
