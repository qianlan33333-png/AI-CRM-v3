package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidInput       = errors.New("invalid access input")
	ErrAuthentication     = errors.New("authentication_required")
	ErrPermissionDenied   = errors.New("permission_denied")
	ErrCSRFRequired       = errors.New("csrf_required")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrRateLimited        = errors.New("login rate limited")
	ErrConflict           = errors.New("access record conflicts with existing data")
	ErrNotFound           = errors.New("access record not found")
)

type Role string

const (
	RoleSuperAdmin Role = "super_admin"
	RoleAdmin      Role = "admin"
	RoleViewer     Role = "viewer"
)

func (role Role) Valid() bool {
	switch role {
	case RoleSuperAdmin, RoleAdmin, RoleViewer:
		return true
	default:
		return false
	}
}

func NormalizeRoles(values []Role) ([]Role, error) {
	seen := make(map[Role]struct{}, len(values))
	roles := make([]Role, 0, len(values))
	for _, role := range values {
		if !role.Valid() {
			return nil, ErrInvalidInput
		}
		if _, exists := seen[role]; exists {
			continue
		}
		seen[role] = struct{}{}
		roles = append(roles, role)
	}
	if len(roles) == 0 {
		return nil, ErrInvalidInput
	}
	return roles, nil
}

type User struct {
	ID             int64
	Username       string
	PasswordHash   string
	DisplayName    string
	WeComUserID    string
	Active         bool
	SessionVersion int64
	Roles          []Role
	LastLoginAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (user User) HasRole(expected Role) bool {
	for _, role := range user.Roles {
		if role == expected {
			return true
		}
	}
	return false
}

type Session struct {
	ID              int64
	TokenDigest     [32]byte
	CSRFTokenDigest [32]byte
	AdminUserID     int64
	SessionVersion  int64
	ExpiresAt       time.Time
	RevokedAt       *time.Time
	RevokedReason   string
	CreatedAt       time.Time
	LastSeenAt      time.Time
	User            User
}

type LoginAudit struct {
	AdminUserID      *int64
	IdentifierDigest [32]byte
	RemoteDigest     [32]byte
	Outcome          string
	Reason           string
	CreatedAt        time.Time
}

type LoginRateLimit struct {
	KeyDigest       [32]byte
	WindowStartedAt time.Time
	FailureCount    int
	BlockedUntil    *time.Time
	UpdatedAt       time.Time
}

type AccessAudit struct {
	ActorAdminUserID  int64
	TargetAdminUserID int64
	Action            string
	Details           json.RawMessage
	CreatedAt         time.Time
}

type Principal struct {
	Kind       Kind
	InternalID int64
	Roles      []Role
}

func (principal Principal) IsSuperAdmin() bool {
	if principal.Kind != KindAdmin && principal.Kind != KindStaff {
		return false
	}
	for _, role := range principal.Roles {
		if role == RoleSuperAdmin {
			return true
		}
	}
	return false
}

func NormalizeUsername(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) < 3 || len(value) > 120 || strings.ContainsAny(value, "\x00\r\n\t ") {
		return "", ErrInvalidInput
	}
	return value, nil
}
