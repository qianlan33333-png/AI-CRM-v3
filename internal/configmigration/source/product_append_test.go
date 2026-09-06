package source

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNewProductAppendAllowsExplicitActiveSelectionFromValid35ProductSnapshot(t *testing.T) {
	snapshot := valid35ProductAppendSourceSnapshot(t)
	if got := snapshot.Manifest.Counts["products"]; got != 35 {
		t.Fatalf("products=%d want 35", got)
	}
	if err := ValidateExpectedBaseline(snapshot); err == nil {
		t.Fatal("35-product append source unexpectedly satisfied the full-import baseline")
	}

	appendSnapshot, digest, err := NewProductAppend(snapshot, []int64{4, 2, 3})
	if err != nil {
		t.Fatalf("explicit active append selection: %v", err)
	}
	if len(appendSnapshot.Products) != 3 || appendSnapshot.Products[0].ID != 2 || appendSnapshot.Products[1].ID != 3 || appendSnapshot.Products[2].ID != 4 {
		t.Fatalf("unexpected canonical append selection: %#v", appendSnapshot.Products)
	}
	_, sourceDigest, err := snapshot.Canonical()
	if err != nil || appendSnapshot.SourceSnapshotDigest != hex.EncodeToString(sourceDigest[:]) {
		t.Fatalf("append source digest=%q err=%v", appendSnapshot.SourceSnapshotDigest, err)
	}
	if _, canonicalDigest, err := appendSnapshot.Canonical(); err != nil || canonicalDigest != digest {
		t.Fatalf("append canonical digest=%x want=%x err=%v", canonicalDigest, digest, err)
	}

	for name, ids := range map[string][]int64{
		"missing":    {2, 3, 999},
		"duplicate":  {2, 2, 3},
		"non-active": {2, 3, 32},
		"service":    {1, 2, 3},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := NewProductAppend(snapshot, ids); err == nil {
				t.Fatalf("invalid append selection accepted: %v", ids)
			}
		})
	}
}

func valid35ProductAppendSourceSnapshot(t *testing.T) Snapshot {
	t.Helper()
	snapshot := testSnapshot(t)
	now := snapshot.Manifest.SnapshotAt
	for id := int64(2); id <= 35; id++ {
		active := id < 32
		status := "disabled"
		if active {
			status = "active"
		}
		snapshot.Products = append(snapshot.Products, Product{
			ID: id, ProductCode: fmt.Sprintf("append-product-%02d", id), Name: fmt.Sprintf("追加商品%02d", id),
			PriceMinor: id * 100, Currency: "CNY", Status: status, Enabled: active, CreatedAt: now, UpdatedAt: now,
		})
	}
	// Keep the live-source shape under test: 35 product rows and two retained
	// service-period links. Product 35 is disabled and never selectable.
	snapshot.ServicePeriods = append(snapshot.ServicePeriods, ServicePeriod{ID: 9, TradeProductID: 35, DurationDays: 90, CreatedAt: now, UpdatedAt: now})
	if err := PopulateManifest(&snapshot, ProductionSourceSystem, strings.Repeat("c", 40), now); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestProductAppendValidationRequiresThreeCanonicalActiveRows(t *testing.T) {
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	appendSnapshot := ProductAppend{
		SchemaVersion:        ProductAppendSchemaVersion,
		SourceSystem:         "ai-crm-production:fixture",
		SourceRevision:       strings.Repeat("a", 40),
		SourceSnapshotDigest: strings.Repeat("b", 64),
		Products: []Product{
			{ID: 1, ProductCode: "product-1", Name: "商品一", PriceMinor: 100, Currency: "CNY", Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now},
			{ID: 2, ProductCode: "product-2", Name: "商品二", PriceMinor: 200, Currency: "CNY", Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now},
			{ID: 3, ProductCode: "product-3", Name: "商品三", PriceMinor: 300, Currency: "CNY", Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now},
		},
	}
	if err := appendSnapshot.Validate(); err != nil {
		t.Fatalf("valid append: %v", err)
	}
	if _, _, err := appendSnapshot.Canonical(); err != nil {
		t.Fatalf("canonical append: %v", err)
	}
	for name, mutate := range map[string]func(*ProductAppend){
		"not-three": func(value *ProductAppend) { value.Products = value.Products[:2] },
		"disabled":  func(value *ProductAppend) { value.Products[1].Status, value.Products[1].Enabled = "disabled", false },
		"unordered": func(value *ProductAppend) {
			value.Products[0], value.Products[1] = value.Products[1], value.Products[0]
		},
		"digest": func(value *ProductAppend) { value.SourceSnapshotDigest = "bad" },
	} {
		t.Run(name, func(t *testing.T) {
			value := appendSnapshot
			value.Products = append([]Product(nil), appendSnapshot.Products...)
			mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("invalid append accepted")
			}
		})
	}
}
