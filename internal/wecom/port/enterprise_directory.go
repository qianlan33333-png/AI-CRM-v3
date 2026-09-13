package port

import (
	"context"
	"errors"
)

// ErrEnterpriseEmployeeNotFound is a definitive Provider response for an
// exact employee userid. It is deliberately separate from unavailable reads:
// callers may use it to fall back from an identifier-shaped display-name
// query, but must never hide timeouts or permission failures as no match.
var ErrEnterpriseEmployeeNotFound = errors.New("wecom enterprise employee not found")

// EnterpriseEmployee is the minimum provider-verified projection needed for
// Access administration. It is not a customer identity and is never a source
// of authorization by itself.
type EnterpriseEmployee struct {
	UserID      string
	DisplayName string
}

// EnterpriseEmployeeIDPage is a bounded page from the application's visible
// corporate directory. The cursor is supplied by WeCom and must be treated as
// opaque by callers.
type EnterpriseEmployeeIDPage struct {
	UserIDs    []string
	NextCursor string
}

// EnterpriseEmployeeDirectory is a read-only boundary for the application's
// visible corporate employee scope. It deliberately does not reuse the
// external-contact follow-user directory: that list is only a customer-owner
// subset and cannot establish who may be granted CRM access.
type EnterpriseEmployeeDirectory interface {
	EnterpriseDirectoryReady() bool
	ListEnterpriseEmployeeIDs(context.Context, string, int) (EnterpriseEmployeeIDPage, error)
	ReadEnterpriseEmployee(context.Context, string) (EnterpriseEmployee, error)
}
