package app

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	distributiondomain "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/domain"
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type promotionPaginationStore struct{ now time.Time }

func (s promotionPaginationStore) ReadDistributorByCustomerWithin(context.Context, int64, bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error) {
	return distributiondomain.Distributor{ID: 1, CustomerID: 11, PublicNo: "DABC123", AgreementVersion: "v1", Enabled: true, RegisteredAt: s.now, Version: 1}, distributionport.ReceiverReadiness{Ready: true, AppID: "app-1"}, nil
}
func (s promotionPaginationStore) ReadDistributorWithin(context.Context, int64, bool) (distributiondomain.Distributor, distributionport.ReceiverReadiness, error) {
	return s.ReadDistributorByCustomerWithin(context.Background(), 11, false)
}
func (s promotionPaginationStore) ReadProductPolicyWithin(_ context.Context, id int64, kind distributiondomain.ProductType) (distributiondomain.Policy, error) {
	return distributiondomain.Policy{ProductID: id, ProductType: kind, Enabled: id > 0, CommissionRateBasisPoints: 1000, WaitDays: 1, Version: 1, CreatedAt: s.now, UpdatedAt: s.now}, nil
}
func (s promotionPaginationStore) ReadProductPolicyForUpdateWithin(ctx context.Context, id int64, kind distributiondomain.ProductType) (distributiondomain.Policy, error) {
	return s.ReadProductPolicyWithin(ctx, id, kind)
}
func (promotionPaginationStore) ProductPolicyIDWithin(context.Context, int64, distributiondomain.ProductType) (int64, error) {
	return 1, nil
}
func (promotionPaginationStore) InsertPromotionCredentialWithin(context.Context, distributiondomain.PromotionCredential) (distributiondomain.PromotionCredential, error) {
	return distributiondomain.PromotionCredential{}, nil
}
func (promotionPaginationStore) ReadPromotionCredentialByDigestWithin(context.Context, [32]byte, bool) (distributiondomain.PromotionCredential, error) {
	return distributiondomain.PromotionCredential{}, distributionport.ErrNotFound
}
func (promotionPaginationStore) ExpirePromotionCredentialWithin(context.Context, distributiondomain.PromotionCredential, time.Time) error {
	return nil
}
func (promotionPaginationStore) InsertAttributionWithin(context.Context, distributiondomain.Attribution, int64) (distributiondomain.Attribution, bool, error) {
	return distributiondomain.Attribution{}, false, nil
}
func (promotionPaginationStore) AppendAuditWithin(context.Context, string, string, int64, string, any, time.Time) error {
	return nil
}
func (promotionPaginationStore) AppendOutboxWithin(context.Context, string, string, int64, any, time.Time) error {
	return nil
}

var _ promotionStore = promotionPaginationStore{}

type promotionPaginationProducts struct {
	items   []productport.ProductOption
	queries []productport.ProductOptionQuery
}

func (s *promotionPaginationProducts) ListProductOptions(_ context.Context, query productport.ProductOptionQuery) (productport.ProductOptionPage, error) {
	s.queries = append(s.queries, query)
	start := int(query.Offset)
	if start > len(s.items) {
		start = len(s.items)
	}
	end := start + int(query.Limit)
	if end > len(s.items) {
		end = len(s.items)
	}
	return productport.ProductOptionPage{Items: append([]productport.ProductOption(nil), s.items[start:end]...), Total: int64(len(s.items)), Limit: query.Limit, Offset: query.Offset}, nil
}

type promotionPaginationSaleable struct{}

func (promotionPaginationSaleable) ReadSidebarShareProduct(_ context.Context, kind productport.ProductOptionType, id productport.ID) (productport.SidebarShareProduct, error) {
	return productport.SidebarShareProduct{ID: id, ProductType: kind, Code: "p" + strconv.FormatInt(int64(id), 10), Name: "商品" + strconv.FormatInt(int64(id), 10), CoverURL: "https://cdn.example.test/cover.png"}, nil
}

type promotionPaginationOrders struct{ now time.Time }

func (s promotionPaginationOrders) ListQualificationPurchaseEvidenceWithin(_ context.Context, query orderport.QualificationPurchaseQuery) ([]orderport.QualificationPurchaseEvidence, error) {
	return []orderport.QualificationPurchaseEvidence{{OrderID: query.ProductID, OrderItemLine: 1, ProductID: query.ProductID, PayerCustomerID: 11, BeneficiaryCustomerID: 11, ItemPaidMinor: 1000, PaymentConfirmedAt: s.now, RecordOrigin: "native"}}, nil
}

type promotionPaginationPayment struct{}

func (promotionPaginationPayment) DistributionPaymentStateWithin(context.Context, int64) (paymentport.DistributionPaymentState, error) {
	return paymentport.DistributionPaymentState{ConfirmedPaid: true}, nil
}

func promotionPaginationFixture(t *testing.T, productCount int) (*PromotionService, *promotionPaginationProducts, distributionport.TrustedSessionActor) {
	t.Helper()
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	items := make([]productport.ProductOption, 0, productCount)
	for id := 1; id <= productCount; id++ {
		items = append(items, productport.ProductOption{ID: productport.ID(id), Code: "p" + strconv.Itoa(id), ProductType: productport.ProductOptionStandard, Name: "商品", PriceMinor: 1000, Currency: "CNY"})
	}
	products := &promotionPaginationProducts{items: items}
	qualification, err := NewQualificationService(lineageStub{roots: []customerdomain.CustomerID{11}}, promotionPaginationOrders{now: now}, promotionPaginationPayment{})
	if err != nil {
		t.Fatal(err)
	}
	qualification.now = func() time.Time { return now }
	service, err := NewPromotionService(settlementUOWStub{}, promotionPaginationStore{now: now}, qualification, products, promotionPaginationSaleable{}, lineageStub{roots: []customerdomain.CustomerID{11}}, "https://crm.example.test")
	if err != nil {
		t.Fatal(err)
	}
	service.SetSettlementEnabled(true)
	return service, products, distributionport.TrustedSessionActor{CustomerID: 11, IdentityID: 12, AppID: "app-1", AppScope: "scope-1", Channel: "mini_program", OccurredAt: now}
}

func TestListPromotionProductsContinuesAfterFilteredSourcePageAndConsumesCursor(t *testing.T) {
	service, products, actor := promotionPaginationFixture(t, 102)
	// Only product 1 has qualifying purchase evidence; make the first 500 rows
	// invalid as Product options, then enable the last target through evidence.
	for index := 0; index < 100; index++ {
		products.items[index].ProductType = productport.ProductOptionType("invalid")
	}
	first, err := service.ListPromotionProducts(context.Background(), actor, "", 1)
	if err != nil || len(first.Items) != 1 || first.Items[0].ProductID != 101 || first.NextCursor != "101" {
		t.Fatalf("first page=%+v err=%v queries=%+v", first, err, products.queries)
	}
	if len(products.queries) != 101 || products.queries[0].Offset != 0 || products.queries[len(products.queries)-1].Offset != 100 {
		t.Fatalf("must continue across filtered source rows: queries=%+v", products.queries)
	}
	second, err := service.ListPromotionProducts(context.Background(), actor, first.NextCursor, 1)
	if err != nil || len(second.Items) != 1 || second.Items[0].ProductID != 102 || second.NextCursor != "" {
		t.Fatalf("cursor consumption=%+v err=%v", second, err)
	}
	if _, err = service.ListPromotionProducts(context.Background(), actor, "01", 1); !errors.Is(err, distributionport.ErrConflict) {
		t.Fatalf("noncanonical cursor error=%v", err)
	}
}

func TestListPromotionProductsCursorDoesNotSkipEligibleRows(t *testing.T) {
	service, _, actor := promotionPaginationFixture(t, 20)
	cursor := ""
	var ids []int64
	for pageNumber := 0; pageNumber < 10; pageNumber++ {
		page, err := service.ListPromotionProducts(context.Background(), actor, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			ids = append(ids, item.ProductID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(ids) != 20 {
		t.Fatalf("eligible IDs were skipped: %v", ids)
	}
	for index, id := range ids {
		if id != int64(index+1) {
			t.Fatalf("unexpected page sequence=%v", ids)
		}
	}
}

func TestListPromotionProductsScanLimitReturnsCursor(t *testing.T) {
	service, products, actor := promotionPaginationFixture(t, int(promotionProductScanMaximum)+1)
	for index := range products.items {
		products.items[index].ProductType = productport.ProductOptionType("invalid")
	}
	page, err := service.ListPromotionProducts(context.Background(), actor, "", 1)
	if err != nil || len(page.Items) != 0 || page.NextCursor != strconv.Itoa(int(promotionProductScanMaximum)) {
		t.Fatalf("bounded filtered page=%+v err=%v", page, err)
	}
}
