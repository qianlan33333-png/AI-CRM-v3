package source

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// ProductAppendSchemaVersion identifies the deliberately bounded recovery
// snapshot for the three legacy active products that were absent from the
// original definition import. It is not a generic incremental-import format.
const ProductAppendSchemaVersion = "aicrm-v2-product-append-v1"

// ProductAppend keeps the complete, validated product rows selected from an
// authenticated full definition snapshot. SourceSnapshotDigest binds this
// recovery to that exact full snapshot rather than to CLI field values.
type ProductAppend struct {
	SchemaVersion        string    `json:"schema_version"`
	SourceSystem         string    `json:"source_system"`
	SourceRevision       string    `json:"source_revision"`
	SourceSnapshotDigest string    `json:"source_snapshot_digest"`
	Products             []Product `json:"products"`
}

// NewProductAppend selects exactly three explicitly approved active,
// non-service-period product rows from a structurally valid full snapshot.
// It deliberately does not impose the one-time all-definition 31-product
// baseline: that baseline belongs only to the original full import. The target
// never receives product fields from flags or ordinary HTTP input.
func NewProductAppend(snapshot Snapshot, sourceProductIDs []int64) (ProductAppend, [sha256.Size]byte, error) {
	if snapshot.Validate() != nil || len(sourceProductIDs) != 3 {
		return ProductAppend{}, [sha256.Size]byte{}, ErrInvalidSnapshot
	}
	_, sourceDigest, err := snapshot.Canonical()
	if err != nil {
		return ProductAppend{}, [sha256.Size]byte{}, ErrInvalidSnapshot
	}
	wanted := map[int64]bool{}
	for _, id := range sourceProductIDs {
		if id < 1 || wanted[id] {
			return ProductAppend{}, [sha256.Size]byte{}, ErrInvalidSnapshot
		}
		wanted[id] = true
	}
	serviceProducts := map[int64]bool{}
	for _, service := range snapshot.ServicePeriods {
		serviceProducts[service.TradeProductID] = true
	}
	out := ProductAppend{
		SchemaVersion:        ProductAppendSchemaVersion,
		SourceSystem:         snapshot.Manifest.SourceSystem,
		SourceRevision:       snapshot.Manifest.SourceRevision,
		SourceSnapshotDigest: hex.EncodeToString(sourceDigest[:]),
		Products:             make([]Product, 0, len(wanted)),
	}
	for _, product := range snapshot.Products {
		if !wanted[product.ID] {
			continue
		}
		if serviceProducts[product.ID] || product.Status != "active" || !product.Enabled {
			return ProductAppend{}, [sha256.Size]byte{}, ErrInvalidSnapshot
		}
		out.Products = append(out.Products, product)
	}
	if len(out.Products) != len(wanted) {
		return ProductAppend{}, [sha256.Size]byte{}, ErrInvalidSnapshot
	}
	_, digest, err := out.Canonical()
	if err != nil {
		return ProductAppend{}, [sha256.Size]byte{}, ErrInvalidSnapshot
	}
	return out, digest, nil
}

func (appendSnapshot ProductAppend) Canonical() ([]byte, [sha256.Size]byte, error) {
	normalizeProductAppend(&appendSnapshot)
	if appendSnapshot.Validate() != nil {
		return nil, [sha256.Size]byte{}, ErrInvalidSnapshot
	}
	raw, err := json.Marshal(appendSnapshot)
	if err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	return raw, sha256.Sum256(raw), nil
}

func (appendSnapshot ProductAppend) Validate() error {
	if appendSnapshot.SchemaVersion != ProductAppendSchemaVersion || !validText(appendSnapshot.SourceSystem, 160) || !validRevision.MatchString(appendSnapshot.SourceRevision) || !validSHA256Hex(appendSnapshot.SourceSnapshotDigest) || len(appendSnapshot.Products) != 3 {
		return ErrInvalidSnapshot
	}
	lastID := int64(0)
	codes := map[string]bool{}
	for _, product := range appendSnapshot.Products {
		if product.ID <= lastID || codes[product.ProductCode] || product.Status != "active" || !product.Enabled || !validProduct(product) {
			return ErrInvalidSnapshot
		}
		lastID = product.ID
		codes[product.ProductCode] = true
	}
	return nil
}

func normalizeProductAppend(snapshot *ProductAppend) {
	if snapshot == nil {
		return
	}
	if snapshot.Products == nil {
		snapshot.Products = []Product{}
	}
	sort.SliceStable(snapshot.Products, func(i, j int) bool { return snapshot.Products[i].ID < snapshot.Products[j].ID })
}

func validProduct(product Product) bool {
	return product.ID > 0 && validText(product.ProductCode, 200) && validText(product.Name, 200) && len(product.Description) <= 10000 && product.PriceMinor > 0 && product.Currency == "CNY" && (product.Status == "active" || product.Status == "disabled") && product.Enabled == (product.Status == "active") && !invalidTimes(product.CreatedAt, product.UpdatedAt)
}

func validSHA256Hex(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}
