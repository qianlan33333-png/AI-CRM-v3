package migration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type Runner struct {
	UOW        platformport.UnitOfWork
	Identities identityport.VerifiedProvisioner
	Facts      identityport.HistoricalFactFactory
	Orders     orderport.HistoricalImporter
	Payments   paymentport.HistoricalImporter
	Runs       RunStore
}

type RunStore interface {
	Begin(context.Context, string, [32]byte, int64) error
	Complete(context.Context, string, int64) error
}

type Result struct{ Identities, Orders, Payments, Refunds int }

func (runner Runner) Apply(ctx context.Context, manifest Manifest) (Result, error) {
	if runner.UOW == nil || runner.Identities == nil || runner.Facts == nil || runner.Orders == nil || runner.Payments == nil || runner.Runs == nil {
		return Result{}, errors.New("migration runner is not configured")
	}
	if err := manifest.Validate(true); err != nil {
		return Result{}, err
	}
	if err := runner.Runs.Begin(ctx, manifest.RunKey, manifest.Digest, int64(len(manifest.Identities)+len(manifest.Orders)+len(manifest.Refunds))); err != nil {
		return Result{}, err
	}
	type identityResult struct{ customerID, identityID int64 }
	identities := make(map[string]identityResult, len(manifest.Identities))
	result := Result{}
	for _, row := range manifest.Identities {
		fact, err := runner.Facts.VerifiedHistoricalFact(identityport.HistoricalVerifiedInput{Kind: row.Kind, Scope: row.Scope, Value: row.Value, Source: row.Source})
		if err != nil {
			return result, fmt.Errorf("identity %s: %w", row.SourceKey, err)
		}
		var provision identityport.ProvisionResult
		err = runner.UOW.Within(ctx, func(tx context.Context) error {
			var inner error
			provision, inner = runner.Identities.ProvisionVerifiedIdentity(tx, identityport.ProvisionCommand{Fact: fact, IdempotencyKey: "commerce-history:" + manifest.RunKey + ":" + row.SourceKey})
			return inner
		})
		if err != nil {
			return result, err
		}
		identities[row.SourceKey] = identityResult{int64(provision.CustomerID), provision.IdentityID}
		result.Identities++
	}
	orders := make(map[string]struct{ orderID, paymentID int64 }, len(manifest.Orders))
	for _, row := range manifest.Orders {
		actor := identities[row.PayerIdentityKey]
		status, err := orderStatus(row.Status)
		if err != nil {
			return result, err
		}
		snapshot := orderdomain.Snapshot{Provider: row.Provider, SourceSystem: "commerce-history", SourceKey: row.SourceKey, MerchantOrderNo: row.MerchantOrderNo, ProviderTransactionNo: row.ProviderTransactionNo, PayerCustomerID: &actor.customerID, BeneficiaryCustomerID: &actor.customerID, Amount: orderdomain.Money{AmountMinor: row.AmountMinor, Currency: row.Currency}, Status: status, Items: []orderdomain.ItemSnapshot{{LineNo: 1, ProductCode: row.ProductCode, ProductName: row.ProductName, UnitAmountMinor: row.AmountMinor, Quantity: 1, LineAmountMinor: row.AmountMinor}}, RecordOrigin: orderdomain.RecordOriginHistory, EffectEligible: false, Version: 1, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC()}
		raw, _ := json.Marshal(row)
		digest := sha256.Sum256(raw)
		imported, err := runner.Orders.ImportHistorical(ctx, orderport.HistoricalImportCommand{RunID: manifest.RunKey, SourceDigest: digest, Order: snapshot})
		if err != nil {
			return result, err
		}
		entry := struct{ orderID, paymentID int64 }{orderID: imported.ID}
		result.Orders++
		if status == orderdomain.StatusPaid && row.Provider != orderdomain.ProviderAlipay {
			transactionDigest := ""
			if row.ProviderTransactionNo != "" {
				transactionDigest = string(effectport.Hash("history.transaction", row.ProviderTransactionNo))
			}
			payment := paymentdomain.Payment{OrderID: imported.ID, Provider: paymentdomain.Provider(row.Provider), MerchantOrderNo: row.MerchantOrderNo, PayerIdentityID: actor.identityID, PayerCustomerID: actor.customerID, BeneficiaryCustomerID: actor.customerID, AmountMinor: row.AmountMinor, Currency: row.Currency, Status: paymentdomain.StatusPaid, ProviderTransactionDigest: transactionDigest, Version: 1, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC()}
			persisted, err := runner.Payments.ImportTerminalPayment(ctx, payment, digest, manifest.RunKey)
			if err != nil {
				return result, err
			}
			entry.paymentID = persisted.ID
			result.Payments++
		}
		orders[string(row.Provider)+"\x00"+row.MerchantOrderNo] = entry
	}
	for _, row := range manifest.Refunds {
		entry, ok := orders[string(row.Provider)+"\x00"+row.MerchantOrderNo]
		if !ok || entry.paymentID < 1 {
			return result, ErrInvalidManifest
		}
		raw, _ := json.Marshal(row)
		digest := sha256.Sum256(raw)
		refundDigest := ""
		if row.ProviderRefundNo != "" {
			refundDigest = string(effectport.Hash("history.refund", row.ProviderRefundNo))
		}
		refund := paymentdomain.Refund{PaymentID: entry.paymentID, Provider: paymentdomain.Provider(row.Provider), RefundNo: row.RefundNo, Reason: row.Reason, AmountMinor: row.AmountMinor, Status: paymentdomain.RefundCompleted, ProviderRefundDigest: refundDigest, Version: 1, CreatedAt: row.OccurredAt.UTC(), UpdatedAt: row.OccurredAt.UTC()}
		if _, err := runner.Payments.ImportTerminalRefund(ctx, refund, digest, manifest.RunKey); err != nil {
			return result, err
		}
		result.Refunds++
	}
	if err := runner.Runs.Complete(ctx, manifest.RunKey, int64(result.Identities+result.Orders+result.Refunds)); err != nil {
		return result, err
	}
	return result, nil
}

func orderStatus(value string) (orderdomain.Status, error) {
	switch orderdomain.Status(value) {
	case orderdomain.StatusPendingPayment, orderdomain.StatusPaid, orderdomain.StatusPartiallyRefunded, orderdomain.StatusRefunded, orderdomain.StatusCancelled, orderdomain.StatusPaymentFailed, orderdomain.StatusClosed:
		return orderdomain.Status(value), nil
	default:
		return "", ErrInvalidManifest
	}
}

var _ = time.Time{}
