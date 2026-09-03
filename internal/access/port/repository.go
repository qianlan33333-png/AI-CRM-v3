package port

import (
	"context"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

// Repository owns only access tables. Mutating methods must reject contexts
// without the transaction installed by platform UnitOfWork.
type Repository interface {
	CountUsers(context.Context) (int64, error)
	UserByID(context.Context, int64, bool) (domain.User, error)
	UserByUsername(context.Context, string, bool) (domain.User, error)
	UserByWeComUserID(context.Context, string, bool) (domain.User, error)
	ListUsers(context.Context) ([]domain.User, error)
	CreateUser(context.Context, domain.User) (domain.User, error)
	BootstrapUser(context.Context, domain.User) (domain.User, bool, error)
	SetActive(context.Context, int64, bool, time.Time) error
	// ReserveLoginAccessRequest records the full desired state for the frozen
	// AdminOps compatibility route. It is scoped to the acting admin and must
	// share the transaction with access mutations; exact replays return false
	// and payload drift returns domain.ErrConflict.
	ReserveLoginAccessRequest(context.Context, int64, string, [32]byte, time.Time) (bool, error)
	SetWeComUserID(context.Context, int64, string, time.Time) error
	ReplaceRoles(context.Context, int64, []domain.Role, time.Time) error
	SetPasswordHash(context.Context, int64, string, time.Time) error
	SetLastLogin(context.Context, int64, time.Time) error

	CreateSession(context.Context, domain.Session) (domain.Session, error)
	SessionByTokenDigest(context.Context, [32]byte, bool) (domain.Session, error)
	TouchSession(context.Context, int64, time.Time) error
	RevokeSession(context.Context, [32]byte, string, time.Time) (bool, error)

	LoginRateLimit(context.Context, [32]byte, bool) (domain.LoginRateLimit, error)
	SaveLoginRateLimit(context.Context, domain.LoginRateLimit) error
	AppendLoginAudit(context.Context, domain.LoginAudit) error
	AppendAccessAudit(context.Context, domain.AccessAudit) error
}
