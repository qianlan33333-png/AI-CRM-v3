package store

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"

	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var _ couponport.CutoverDefinitionImporter = (*Repository)(nil)
var errCutoverConflict = errors.New("coupon cutover definition conflict")
var cutoverSlug = regexp.MustCompile(`^[a-z][a-z0-9-]{5,119}$`)

// ImportCutoverDefinition never creates claims or dispatches effects. Source
// totals can initially fill an imported zero counter, but cannot overwrite an
// already nonzero differing counter. This deliberately fails closed if target
// claiming has begun or a previous cutover's counter has diverged.
func (r *Repository) ImportCutoverDefinition(ctx context.Context, in couponport.CutoverDefinitionImport) (couponport.Coupon, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return couponport.Coupon{}, err
	}
	desired := in.Coupon
	if in.Actor < 1 || desired.ID != 0 || desired.IssuedCount < 0 || desired.IssuedCount > desired.TotalIssueLimit || (in.PublicSlug != "" && !cutoverSlug.MatchString(in.PublicSlug)) {
		return couponport.Coupon{}, errCutoverConflict
	}
	var rule couponport.Coupon
	if in.ExistingID == 0 {
		definition := in.DefinitionImport
		definition.IssuedCount = 0
		rule, err = r.ImportDefinition(ctx, definition)
	} else {
		rule, err = r.get(ctx, tx, in.ExistingID, true)
		if err == nil && (rule.Version != 1 || !sameCutoverDefinition(rule, desired)) {
			err = errCutoverConflict
		}
	}
	if err != nil {
		return couponport.Coupon{}, err
	}
	var existingSlug string
	var claims int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(public_slug,''),(SELECT count(*) FROM coupon_customer_claims WHERE coupon_id=$1) FROM coupon_rules WHERE id=$1 FOR UPDATE`, rule.ID).Scan(&existingSlug, &claims); err != nil {
		return couponport.Coupon{}, err
	}
	if claims > desired.IssuedCount || (rule.IssuedCount != 0 && rule.IssuedCount != desired.IssuedCount) || (existingSlug != "" && existingSlug != in.PublicSlug) {
		return couponport.Coupon{}, errCutoverConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE coupon_rules SET public_slug=NULLIF($2,''),issued_count=$3 WHERE id=$1 AND version=1`, rule.ID, in.PublicSlug, desired.IssuedCount); err != nil {
		return couponport.Coupon{}, err
	}
	return r.get(ctx, tx, rule.ID, false)
}

func sameCutoverDefinition(a, b couponport.Coupon) bool {
	// Exclude only owner metadata, derived availability and the separately
	// reconciled counter. Every business definition field and target is compared.
	clean := func(c couponport.Coupon) []byte {
		c.ID = 0
		c.Version = 0
		c.CreatedBy = 0
		c.UpdatedBy = 0
		c.CreatedAt = b.CreatedAt
		c.UpdatedAt = b.UpdatedAt
		c.ClaimStartsAt = c.ClaimStartsAt.UTC()
		c.ClaimEndsAt = c.ClaimEndsAt.UTC()
		if c.UseStartsAt != nil {
			v := c.UseStartsAt.UTC()
			c.UseStartsAt = &v
		}
		if c.UseEndsAt != nil {
			v := c.UseEndsAt.UTC()
			c.UseEndsAt = &v
		}
		c.AvailabilityStatus = ""
		c.IssuedCount = 0
		c.TargetRefs = append([]string{}, c.TargetRefs...)
		sort.Strings(c.TargetRefs)
		raw, _ := json.Marshal(c)
		return raw
	}
	return string(clean(a)) == string(clean(b))
}
