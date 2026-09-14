package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

const promotionCredentialTTL = 30 * 24 * time.Hour

// promotionProductScanMaximum bounds one public request while still allowing
// qualification filtering to advance past a Product-owned source page that
// contains no promotable rows.
const promotionProductScanMaximum = int32(500)

type promotionStore interface {
	ReadDistributorByCustomerWithin(context.Context, int64, bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error)
	ReadDistributorWithin(context.Context, int64, bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error)
	ReadProductPolicyWithin(context.Context, int64, distributiondomain.ProductType) (distributiondomain.Policy, error)
	ReadProductPolicyForUpdateWithin(context.Context, int64, distributiondomain.ProductType) (distributiondomain.Policy, error)
	ProductPolicyIDWithin(context.Context, int64, distributiondomain.ProductType) (int64, error)
	InsertPromotionCredentialWithin(context.Context, distributiondomain.PromotionCredential) (distributiondomain.PromotionCredential, error)
	ReadPromotionCredentialByDigestWithin(context.Context, [32]byte, bool) (distributiondomain.PromotionCredential, error)
	ExpirePromotionCredentialWithin(context.Context, distributiondomain.PromotionCredential, time.Time) error
	InsertAttributionWithin(context.Context, distributiondomain.Attribution, int64) (distributiondomain.Attribution, bool, error)
	AppendAuditWithin(context.Context, string, string, int64, string, any, time.Time) error
	AppendOutboxWithin(context.Context, string, string, int64, any, time.Time) error
}

// PromotionService owns opaque credential issuance and Order's checkout-time
// attribution callback. The browser never chooses commission, policy, seller,
// payment recipient, or qualification evidence.
type PromotionService struct {
	uow               platformport.UnitOfWork
	store             promotionStore
	qualification     *QualificationService
	products          productport.ProductOptionReader
	saleableProduct   productport.SidebarProductShareReader
	lineage           identityport.LockedCanonicalLineageReader
	promotionOrigin   string
	settlementEnabled bool
	now               func() time.Time
}

// SetSettlementEnabled controls whether a valid checkout becomes a financial
// Distribution attribution. Public registration and policy reads remain
// available while the Payment profit-sharing capability is closed, but Order
// then treats every checkout as ordinary and cannot freeze funds.
func (s *PromotionService) SetSettlementEnabled(enabled bool) {
	if s != nil {
		s.settlementEnabled = enabled
	}
}

func NewPromotionService(uow platformport.UnitOfWork, store promotionStore, qualification *QualificationService, products productport.ProductOptionReader, saleable productport.SidebarProductShareReader, lineage identityport.LockedCanonicalLineageReader, promotionOrigin string) (*PromotionService, error) {
	if uow == nil || store == nil || qualification == nil || products == nil || saleable == nil || lineage == nil || !validPromotionOrigin(promotionOrigin) {
		return nil, distributionport.ErrUnavailable
	}
	return &PromotionService{uow: uow, store: store, qualification: qualification, products: products, saleableProduct: saleable, lineage: lineage, promotionOrigin: strings.TrimRight(promotionOrigin, "/"), now: time.Now}, nil
}

func (s *PromotionService) ListPromotionProducts(ctx context.Context, actor distributionport.TrustedSessionActor, cursor string, limit int32) (distributionport.PromotionPage, error) {
	if s == nil || !actor.Valid() || limit < 1 || limit > productport.ProductOptionMaximumLimit {
		return distributionport.PromotionPage{}, distributionport.ErrConflict
	}
	offset, err := promotionProductOffset(cursor)
	if err != nil {
		return distributionport.PromotionPage{}, distributionport.ErrConflict
	}
	// Product owns discoverability/pagination. Distribution deliberately does
	// not query a Product table; its current saleable reader below is the
	// authoritative lifecycle check before a row is shown as promotable. The
	// cursor is Product's bounded offset, never a Distribution table key.
	var distributor distributiondomain.Distributor
	var receiver distributionport.ReceiverReadiness
	err = s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		distributor, receiver, err = s.store.ReadDistributorByCustomerWithin(tx, actor.CustomerID, false)
		return err
	})
	if errors.Is(err, distributionport.ErrNotFound) {
		return distributionport.PromotionPage{}, distributionport.ErrUnauthorized
	}
	if err != nil {
		return distributionport.PromotionPage{}, err
	}
	page := distributionport.PromotionPage{Items: make([]distributionport.PromotionProduct, 0, limit)}
	scanned := int32(0)
	for int32(len(page.Items)) < limit && scanned < promotionProductScanMaximum {
		remaining := promotionProductScanMaximum - scanned
		// The cursor is a raw Product offset. Fetch no more than this client
		// page can return, otherwise accepting an early option would skip later
		// eligible rows from the same Product source page on the next cursor.
		sourceLimit := limit - int32(len(page.Items))
		if remaining < sourceLimit {
			sourceLimit = remaining
		}
		options, readErr := s.products.ListProductOptions(ctx, productport.ProductOptionQuery{ProductType: productport.ProductOptionAll, Limit: sourceLimit, Offset: offset})
		if readErr != nil {
			return distributionport.PromotionPage{}, readErr
		}
		if options.Offset != offset || options.Total < 0 || len(options.Items) > int(sourceLimit) {
			return distributionport.PromotionPage{}, distributionport.ErrUnavailable
		}
		next := int64(offset) + int64(len(options.Items))
		if next > int64(productport.ProductOptionMaximumOffset) || next > options.Total {
			return distributionport.PromotionPage{}, distributionport.ErrUnavailable
		}
		if len(options.Items) == 0 {
			if int64(offset) != options.Total {
				return distributionport.PromotionPage{}, distributionport.ErrUnavailable
			}
			return page, nil
		}
		offset = int32(next)
		scanned += int32(len(options.Items))
		for _, option := range options.Items {
			kind, ok := distributionProductType(option.ProductType)
			if !ok || option.ID < 1 || option.PriceMinor < 0 || option.Currency != "CNY" {
				continue
			}
			// This current Product read excludes draft/disabled/archived products.
			saleable, saleableErr := s.saleableProduct.ReadSidebarShareProduct(ctx, option.ProductType, option.ID)
			if saleableErr != nil {
				continue
			}
			var policy distributiondomain.Policy
			var qualification distributiondomain.Qualification
			readErr := s.uow.Within(ctx, func(tx context.Context) error {
				var err error
				policy, err = s.store.ReadProductPolicyWithin(tx, int64(option.ID), kind)
				if errors.Is(err, distributionport.ErrNotFound) {
					return nil
				}
				if err != nil {
					return err
				}
				if !policy.Enabled || !distributor.Enabled {
					return nil
				}
				qualification, err = s.qualification.CheckWithin(tx, distributor.CustomerID, int64(option.ID), kind)
				return err
			})
			if readErr != nil || !policy.Enabled || !distributor.Enabled || !qualification.AllowsPromotion() {
				continue
			}
			estimated, calcErr := distributiondomain.CalculateCommission(option.PriceMinor, policy.CommissionRateBasisPoints)
			if calcErr != nil {
				continue
			}
			purchaseURL := "/p/" + saleable.Code
			if kind == distributiondomain.ProductTypeServicePeriod {
				purchaseURL = "/s/" + saleable.Code
			}
			ready := s.settlementEnabled && receiver.Ready && receiver.AppID == actor.AppID
			block := ""
			if !ready {
				if !s.settlementEnabled {
					block = "profit_sharing_provider_disabled"
				} else {
					block = receiver.Reason
				}
				if block == "" {
					block = "receiver_not_ready"
				}
			}
			page.Items = append(page.Items, distributionport.PromotionProduct{ProductID: int64(option.ID), ProductType: kind, CoverURL: saleable.CoverURL, Name: saleable.Name, PurchaseURL: purchaseURL, PriceMinor: option.PriceMinor, Currency: option.Currency, CommissionRateBasisPoints: policy.CommissionRateBasisPoints, EstimatedCommissionMinor: estimated, WaitDays: policy.WaitDays, PromotionReady: ready, PromotionBlockReason: block})
			if int32(len(page.Items)) == limit {
				break
			}
		}
		if int64(offset) >= options.Total {
			return page, nil
		}
		if int32(len(page.Items)) == limit || scanned == promotionProductScanMaximum {
			page.NextCursor = strconv.FormatInt(int64(offset), 10)
			return page, nil
		}
	}
	if scanned > 0 {
		page.NextCursor = strconv.FormatInt(int64(offset), 10)
	}
	return page, nil
}

// ApplicationTarget resolves a shared application link with current Product
// lifecycle facts. It is intentionally independent of a distributor session:
// being able to view a saleable, enabled policy is not evidence of identity,
// registration, qualification, receiver readiness, settlement availability or
// a right to issue a promotion credential.
func (s *PromotionService) ApplicationTarget(ctx context.Context, productID int64, productType distributiondomain.ProductType) (distributionport.ApplicationTarget, error) {
	if s == nil || s.uow == nil || s.store == nil || s.saleableProduct == nil || productID < 1 || !productType.Valid() {
		return distributionport.ApplicationTarget{}, distributionport.ErrConflict
	}
	var product productport.SidebarShareProduct
	err := s.uow.Within(ctx, func(tx context.Context) error {
		policy, err := s.store.ReadProductPolicyWithin(tx, productID, productType)
		if err != nil {
			return err
		}
		if !policy.Enabled {
			return distributionport.ErrNotFound
		}
		product, err = s.saleableProduct.ReadSidebarShareProduct(tx, optionType(productType), productport.ID(productID))
		if err != nil {
			if errors.Is(err, productport.ErrSaleableProductNotFound) {
				return distributionport.ErrNotFound
			}
			return distributionport.ErrUnavailable
		}
		if product.ID != productport.ID(productID) || product.ProductType != optionType(productType) || strings.TrimSpace(product.Code) == "" || strings.TrimSpace(product.Name) == "" {
			return distributionport.ErrUnavailable
		}
		return nil
	})
	if err != nil {
		return distributionport.ApplicationTarget{}, err
	}
	purchaseURL := "/p/" + product.Code
	if productType == distributiondomain.ProductTypeServicePeriod {
		purchaseURL = "/s/" + product.Code
	}
	return distributionport.ApplicationTarget{ProductID: productID, ProductType: productType, PolicyEnabled: true, ProductName: product.Name, PurchaseURL: purchaseURL}, nil
}

func promotionProductOffset(cursor string) (int32, error) {
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.ParseInt(cursor, 10, 32)
	if err != nil || offset < 1 || offset > int64(productport.ProductOptionMaximumOffset) || cursor != strconv.FormatInt(offset, 10) {
		return 0, distributionport.ErrConflict
	}
	return int32(offset), nil
}

func (s *PromotionService) IssuePromotionLink(ctx context.Context, command distributionport.IssuePromotionCommand) (distributionport.PromotionLink, error) {
	if s == nil || !s.settlementEnabled {
		return distributionport.PromotionLink{}, distributionport.ErrUnavailable
	}
	if !command.Actor.Valid() || command.ProductID < 1 || !command.ProductType.Valid() {
		return distributionport.PromotionLink{}, distributionport.ErrConflict
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return distributionport.PromotionLink{}, distributionport.ErrUnavailable
	}
	token := "dpc_" + base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	now := s.now().UTC()
	var expiresAt time.Time
	err := s.uow.Within(ctx, func(tx context.Context) error {
		distributor, readiness, err := s.store.ReadDistributorByCustomerWithin(tx, command.Actor.CustomerID, true)
		if err != nil {
			return err
		}
		if !distributor.Enabled || !readiness.Ready || readiness.AppID != command.Actor.AppID {
			return distributionport.ErrQualification
		}
		policy, err := s.store.ReadProductPolicyForUpdateWithin(tx, command.ProductID, command.ProductType)
		if err != nil || !policy.Enabled {
			return distributionport.ErrQualification
		}
		if _, err = s.saleableProduct.ReadSidebarShareProduct(tx, optionType(command.ProductType), productport.ID(command.ProductID)); err != nil {
			return distributionport.ErrQualification
		}
		qualification, err := s.qualification.CheckWithin(tx, distributor.CustomerID, command.ProductID, command.ProductType)
		if err != nil || !qualification.AllowsPromotion() {
			return distributionport.ErrQualification
		}
		expiresAt = now.Add(promotionCredentialTTL)
		credential, err := s.store.InsertPromotionCredentialWithin(tx, distributiondomain.PromotionCredential{DistributorID: distributor.ID, ProductID: command.ProductID, ProductType: command.ProductType, TokenDigest: digest, Status: distributiondomain.CredentialActive, CreatedAt: now, ExpiresAt: expiresAt})
		if err != nil {
			return err
		}
		payload := map[string]any{"credential_id": credential.ID, "product_id": command.ProductID, "expires_at": expiresAt}
		if err = s.store.AppendAuditWithin(tx, "distribution.credential_issued.v1", "credential", credential.ID, "distributor:"+decimal(distributor.ID), payload, now); err != nil {
			return err
		}
		return s.store.AppendOutboxWithin(tx, "distribution.credential_issued.v1", "distribution.credential:"+base64.RawURLEncoding.EncodeToString(digest[:]), credential.ID, payload, now)
	})
	if err != nil {
		return distributionport.PromotionLink{}, err
	}
	return distributionport.PromotionLink{URL: s.promotionOrigin + "/d/" + token, ExpiresAt: expiresAt}, nil
}

// ResolvePromotionTarget is the public entry-point handoff. It validates only
// the opaque credential and current Product availability, then lets the HTTP
// adapter place the token into a server-controlled cookie. Qualification and
// policy are deliberately checked again inside Order's checkout UoW.
func (s *PromotionService) ResolvePromotionTarget(ctx context.Context, token string) (string, error) {
	if s == nil || !validPromotionToken(token) {
		return "", distributionport.ErrNotFound
	}
	digest := sha256.Sum256([]byte(token))
	now := s.now().UTC()
	var credential distributiondomain.PromotionCredential
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		credential, err = s.store.ReadPromotionCredentialByDigestWithin(tx, digest, true)
		if err != nil {
			return err
		}
		if credential.Status != distributiondomain.CredentialActive {
			return distributionport.ErrNotFound
		}
		if !credential.ExpiresAt.After(now) {
			if err = s.store.ExpirePromotionCredentialWithin(tx, credential, now); err != nil {
				return err
			}
			return distributionport.ErrNotFound
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	product, err := s.saleableProduct.ReadSidebarShareProduct(ctx, optionType(credential.ProductType), productport.ID(credential.ProductID))
	if err != nil {
		return "", distributionport.ErrNotFound
	}
	if credential.ProductType == distributiondomain.ProductTypeServicePeriod {
		return "/s/" + product.Code, nil
	}
	return "/p/" + product.Code, nil
}

// RecordCheckoutAttributionWithin is injected into Order. Invalid, revoked,
// expired, self, disabled, and ineligible credentials deliberately produce an
// ordinary purchase. A real persistence failure remains an error so an order
// cannot commit while pretending that a partial attribution was saved.
func (s *PromotionService) RecordCheckoutAttributionWithin(ctx context.Context, command orderport.CheckoutAttributionCommand) (orderport.CheckoutAttributionResult, error) {
	if s == nil || !s.settlementEnabled || s.store == nil || s.qualification == nil || s.lineage == nil || command.OrderID < 1 || command.OrderItemLine != 1 || command.ProductID < 1 || command.PayerCustomerID < 1 || command.BeneficiaryCustomerID < 1 || command.ItemPaidMinor < 1 || command.OccurredAt.IsZero() || !validPromotionToken(command.PromotionContext) {
		return orderport.CheckoutAttributionResult{}, nil
	}
	if _, err := platformpostgres.RequireTransaction(ctx); err != nil {
		return orderport.CheckoutAttributionResult{}, distributionport.ErrUnavailable
	}
	productType := distributiondomain.ProductType(command.ProductType)
	if !productType.Valid() {
		return orderport.CheckoutAttributionResult{}, nil
	}
	digest := sha256.Sum256([]byte(command.PromotionContext))
	credential, err := s.store.ReadPromotionCredentialByDigestWithin(ctx, digest, true)
	if errors.Is(err, distributionport.ErrNotFound) {
		return orderport.CheckoutAttributionResult{}, nil
	}
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	if credential.Status != distributiondomain.CredentialActive || credential.ProductID != command.ProductID || credential.ProductType != productType {
		return orderport.CheckoutAttributionResult{}, nil
	}
	if !credential.ExpiresAt.After(command.OccurredAt) {
		if err = s.store.ExpirePromotionCredentialWithin(ctx, credential, command.OccurredAt); err != nil {
			return orderport.CheckoutAttributionResult{}, err
		}
		return orderport.CheckoutAttributionResult{}, nil
	}
	distributor, _, err := s.store.ReadDistributorWithin(ctx, credential.DistributorID, true)
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	if !distributor.Enabled {
		return orderport.CheckoutAttributionResult{}, nil
	}
	if s.isSelfPurchaseWithin(ctx, distributor.CustomerID, command.PayerCustomerID, command.BeneficiaryCustomerID) {
		return orderport.CheckoutAttributionResult{}, nil
	}
	policy, err := s.store.ReadProductPolicyForUpdateWithin(ctx, command.ProductID, productType)
	if errors.Is(err, distributionport.ErrNotFound) || (err == nil && !policy.Enabled) {
		return orderport.CheckoutAttributionResult{}, nil
	}
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	qualification, err := s.qualification.CheckWithin(ctx, distributor.CustomerID, command.ProductID, productType)
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	if !qualification.AllowsPromotion() {
		return orderport.CheckoutAttributionResult{}, nil
	}
	policyID, err := s.store.ProductPolicyIDWithin(ctx, command.ProductID, productType)
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	attribution := distributiondomain.Attribution{OrderID: command.OrderID, OrderItemLine: command.OrderItemLine, ProductCode: command.ProductCode, ProductName: command.ProductName, DistributorID: distributor.ID, PromotionCredentialID: credential.ID, QualificationEvidenceRef: qualification.EvidenceRef, QualificationState: qualification.State, PolicyVersion: policy.Version, CommissionRateBasisPoints: policy.CommissionRateBasisPoints, WaitDays: policy.WaitDays, AttributedAt: command.OccurredAt.UTC()}
	saved, created, err := s.store.InsertAttributionWithin(ctx, attribution, policyID)
	if err != nil || !created {
		return orderport.CheckoutAttributionResult{}, err
	}
	payload := map[string]any{"attribution_id": saved.ID, "order_id": command.OrderID, "policy_version": policy.Version}
	if err = s.store.AppendAuditWithin(ctx, "distribution.attribution_created.v1", "attribution", saved.ID, "order:"+decimal(command.OrderID), payload, command.OccurredAt); err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	if err = s.store.AppendOutboxWithin(ctx, "distribution.attribution_created.v1", "distribution.attribution:order:"+decimal(command.OrderID)+":item:"+decimal(int64(command.OrderItemLine)), saved.ID, payload, command.OccurredAt); err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	commission, err := distributiondomain.CalculateCommission(command.ItemPaidMinor, policy.CommissionRateBasisPoints)
	if err != nil {
		return orderport.CheckoutAttributionResult{}, err
	}
	return orderport.CheckoutAttributionResult{Attributed: true, ProfitSharingRequired: commission > 0}, nil
}

func (s *PromotionService) isSelfPurchaseWithin(ctx context.Context, distributorCustomerID, payerCustomerID, beneficiaryCustomerID int64) bool {
	roots, err := s.lineage.LockedCanonicalLineage(ctx, customerdomain.CustomerID(distributorCustomerID))
	if err != nil || len(roots) == 0 || len(roots) > 100 {
		// Failing closed means no attribution, never a guessed relationship.
		return true
	}
	for _, root := range roots {
		if int64(root) == payerCustomerID || int64(root) == beneficiaryCustomerID {
			return true
		}
	}
	return false
}

func distributionProductType(value productport.ProductOptionType) (distributiondomain.ProductType, bool) {
	switch value {
	case productport.ProductOptionStandard:
		return distributiondomain.ProductTypeStandard, true
	case productport.ProductOptionServicePeriod:
		return distributiondomain.ProductTypeServicePeriod, true
	default:
		return "", false
	}
}

func optionType(value distributiondomain.ProductType) productport.ProductOptionType {
	if value == distributiondomain.ProductTypeServicePeriod {
		return productport.ProductOptionServicePeriod
	}
	return productport.ProductOptionStandard
}

func validPromotionOrigin(value string) bool {
	return strings.HasPrefix(value, "https://") && strings.TrimRight(value, "/") == value && len(value) <= 500
}

func validPromotionToken(value string) bool {
	if len(value) != 47 || !strings.HasPrefix(value, "dpc_") {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(value[4:])
	return err == nil
}

var _ orderport.CheckoutAttributionCoordinator = (*PromotionService)(nil)
