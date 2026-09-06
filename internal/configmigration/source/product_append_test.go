package source

import (
	"strings"
	"testing"
	"time"
)

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
