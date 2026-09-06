package port

import "context"

// MessageArchiveStaff is the narrow, read-only Access projection required to
// render staff already referenced by archive-owned facts. Callers must provide
// only IDs they obtained from their own authorization-scoped records.
type MessageArchiveStaff struct {
	ID          int64
	DisplayName string
}

type MessageArchiveStaffDirectory interface {
	MessageArchiveStaff(context.Context, []int64) ([]MessageArchiveStaff, error)
}
