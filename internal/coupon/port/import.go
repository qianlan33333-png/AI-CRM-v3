package port

import (
	"context"
	"time"
)

// DefinitionImport is a configuration-only coupon rule. Issuance, claims,
// redemptions, orders, payment, entitlement and customer facts are excluded.
type DefinitionImport struct {
	Coupon
	Actor     int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// DefinitionImporter participates in the configuration migration's caller
// transaction; it is not a normal Coupon administration command.
type DefinitionImporter interface {
	ImportDefinition(context.Context, DefinitionImport) (Coupon, error)
}

// CutoverDefinitionImport preserves source issuance totals and public links.
// ExistingID is set only by a trusted migration source mapping. Existing rules
// must still match the supplied definition at version one; changes conflict.
type CutoverDefinitionImport struct {
	DefinitionImport
	ExistingID ID
	PublicSlug string
}
type CutoverDefinitionImporter interface {
	ImportCutoverDefinition(context.Context, CutoverDefinitionImport) (Coupon, error)
}
