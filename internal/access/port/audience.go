package port

import "context"

// AudienceOwnerResolver is the narrow Access-owned bridge from a frozen
// WeCom userid form value to a local Staff ID. Segment never reads access
// tables or accepts a caller-provided Staff ID as proof of that conversion.
type AudienceOwnerResolver interface {
	ResolveAudienceOwner(context.Context, string) (StaffID, bool, error)
}
