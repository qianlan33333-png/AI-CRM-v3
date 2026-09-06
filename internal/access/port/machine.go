package port

import (
	"context"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

// MachineRepository is a separate Access-owned seam so existing session/RBAC
// repositories are not widened by machine-auth implementation details.
type MachineRepository interface {
	MachineClientByID(context.Context, string, bool) (domain.MachineClient, error)
	ListMachineClients(context.Context) ([]domain.MachineClient, error)
	CreateMachineClient(context.Context, domain.MachineClient) (domain.MachineClient, error)
	ReplaceMachineClient(context.Context, domain.MachineClient) error
	SetMachineClientLastUsed(context.Context, int64, time.Time) error
	AppendMachineAudit(context.Context, domain.MachineAudit) error
}
