package port

import (
	"context"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
)

type CurrentExternalContact struct {
	EmployeeUserID, ExternalUserID string
}

type CurrentExternalContactReader interface {
	CurrentExternalContact(context.Context, customerdomain.CustomerID, int64) (CurrentExternalContact, error)
}

type EntrantActionWriter interface {
	SendWelcomeMessage(context.Context, string, string) error
	AddContactTag(context.Context, string, string, string) error
}
