package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type Management struct {
	repository accessport.Repository
	uow        platformport.UnitOfWork
	passwords  Passwords
	now        func() time.Time
}

type BootstrapInput struct {
	Username    string
	Password    string
	DisplayName string
}

type AddUserInput struct {
	Username    string
	Password    string
	DisplayName string
	Roles       []domain.Role
}

// UserSummary is deliberately safe for employee-management responses.
// Password hashes and session or CSRF digests cannot be represented here.
type UserSummary struct {
	ID             int64         `json:"id"`
	Username       string        `json:"username"`
	DisplayName    string        `json:"display_name"`
	WeComUserID    string        `json:"wecom_userid"`
	Active         bool          `json:"active"`
	SessionVersion int64         `json:"session_version"`
	Roles          []domain.Role `json:"roles"`
	LastLoginAt    *time.Time    `json:"last_login_at,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

func NewManagement(repository accessport.Repository, uow platformport.UnitOfWork, passwords Passwords, now func() time.Time) (*Management, error) {
	if repository == nil || uow == nil || passwords == nil {
		return nil, errors.New("access management dependencies are required")
	}
	if now == nil {
		now = time.Now
	}
	return &Management{repository: repository, uow: uow, passwords: passwords, now: now}, nil
}

// Bootstrap is intended only for deployment configuration input. It creates the
// first super administrator exactly once and never updates an existing account.
func (service *Management) Bootstrap(ctx context.Context, input BootstrapInput) (domain.User, bool, error) {
	username, err := domain.NormalizeUsername(input.Username)
	if err != nil || strings.TrimSpace(input.DisplayName) == "" {
		return domain.User{}, false, domain.ErrInvalidInput
	}
	passwordHash, err := service.passwords.Hash(input.Password)
	if err != nil {
		return domain.User{}, false, passwordInputError(err)
	}
	var user domain.User
	var created bool
	err = service.uow.Within(ctx, func(txContext context.Context) error {
		var createErr error
		user, created, createErr = service.repository.BootstrapUser(txContext, domain.User{
			Username: username, PasswordHash: passwordHash, DisplayName: strings.TrimSpace(input.DisplayName),
			Active: true, Roles: []domain.Role{domain.RoleSuperAdmin},
		})
		if createErr != nil || !created {
			return createErr
		}
		return service.audit(txContext, user.ID, user.ID, "bootstrap", map[string]any{"roles": user.Roles})
	})
	return user, created, err
}

func (service *Management) AddUser(ctx context.Context, actor domain.Principal, input AddUserInput) (domain.User, error) {
	if err := requireSuperAdmin(actor); err != nil {
		return domain.User{}, err
	}
	username, err := domain.NormalizeUsername(input.Username)
	if err != nil || strings.TrimSpace(input.DisplayName) == "" {
		return domain.User{}, domain.ErrInvalidInput
	}
	roles, err := domain.NormalizeRoles(input.Roles)
	if err != nil {
		return domain.User{}, err
	}
	passwordHash, err := service.passwords.Hash(input.Password)
	if err != nil {
		return domain.User{}, passwordInputError(err)
	}
	var result domain.User
	err = service.uow.Within(ctx, func(txContext context.Context) error {
		result, err = service.repository.CreateUser(txContext, domain.User{
			Username: username, PasswordHash: passwordHash, DisplayName: strings.TrimSpace(input.DisplayName),
			Active: true, Roles: roles,
		})
		if err != nil {
			return err
		}
		return service.audit(txContext, actor.InternalID, result.ID, "create", map[string]any{"roles": roles})
	})
	return result, err
}

func (service *Management) ListUsers(ctx context.Context, actor domain.Principal) ([]UserSummary, error) {
	if err := requireSuperAdmin(actor); err != nil {
		return nil, err
	}
	var result []UserSummary
	err := service.uow.Within(ctx, func(txContext context.Context) error {
		users, err := service.repository.ListUsers(txContext)
		if err != nil {
			return err
		}
		result = make([]UserSummary, 0, len(users))
		for _, user := range users {
			result = append(result, summarizeUser(user))
		}
		return nil
	})
	return result, err
}

func summarizeUser(user domain.User) UserSummary {
	return UserSummary{
		ID: user.ID, Username: user.Username, DisplayName: user.DisplayName,
		WeComUserID: user.WeComUserID, Active: user.Active, SessionVersion: user.SessionVersion,
		Roles: append([]domain.Role(nil), user.Roles...), LastLoginAt: user.LastLoginAt,
		CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt,
	}
}

func (service *Management) DisableUser(ctx context.Context, actor domain.Principal, targetID int64) error {
	if err := requireSuperAdmin(actor); err != nil || targetID < 1 || actor.InternalID == targetID {
		if err != nil {
			return err
		}
		return domain.ErrPermissionDenied
	}
	return service.uow.Within(ctx, func(txContext context.Context) error {
		if err := service.repository.SetActive(txContext, targetID, false, service.now().UTC()); err != nil {
			return err
		}
		return service.audit(txContext, actor.InternalID, targetID, "disable", nil)
	})
}

func (service *Management) BindWeComUserID(ctx context.Context, actor domain.Principal, targetID int64, value string) error {
	if err := requireSuperAdmin(actor); err != nil {
		return err
	}
	value = strings.TrimSpace(value)
	if targetID < 1 {
		return domain.ErrInvalidInput
	}
	if value != "" {
		var err error
		value, err = domain.NormalizeWeComUserID(value)
		if err != nil {
			return domain.ErrInvalidInput
		}
	}
	return service.uow.Within(ctx, func(txContext context.Context) error {
		if err := service.repository.SetWeComUserID(txContext, targetID, value, service.now().UTC()); err != nil {
			return err
		}
		return service.audit(txContext, actor.InternalID, targetID, "bind_wecom_userid", map[string]any{"bound": value != ""})
	})
}

func (service *Management) ChangeRoles(ctx context.Context, actor domain.Principal, targetID int64, input []domain.Role) error {
	if err := requireSuperAdmin(actor); err != nil {
		return err
	}
	roles, err := domain.NormalizeRoles(input)
	if err != nil || targetID < 1 {
		return domain.ErrInvalidInput
	}
	if actor.InternalID == targetID && !containsRole(roles, domain.RoleSuperAdmin) {
		return domain.ErrPermissionDenied
	}
	return service.uow.Within(ctx, func(txContext context.Context) error {
		if err := service.repository.ReplaceRoles(txContext, targetID, roles, service.now().UTC()); err != nil {
			return err
		}
		return service.audit(txContext, actor.InternalID, targetID, "change_roles", map[string]any{"roles": roles})
	})
}

func (service *Management) ResetPassword(ctx context.Context, actor domain.Principal, targetID int64, password string) error {
	if err := requireSuperAdmin(actor); err != nil {
		return err
	}
	passwordHash, err := service.passwords.Hash(password)
	if targetID < 1 {
		return domain.ErrInvalidInput
	}
	if err != nil {
		return passwordInputError(err)
	}
	return service.uow.Within(ctx, func(txContext context.Context) error {
		if err := service.repository.SetPasswordHash(txContext, targetID, passwordHash, service.now().UTC()); err != nil {
			return err
		}
		return service.audit(txContext, actor.InternalID, targetID, "reset_password", nil)
	})
}

func passwordInputError(err error) error {
	if errors.Is(err, credential.ErrInvalidPassword) || errors.Is(err, domain.ErrInvalidInput) {
		return domain.ErrInvalidInput
	}
	return err
}

func requireSuperAdmin(actor domain.Principal) error {
	if err := actor.Validate(); err != nil {
		return domain.ErrAuthentication
	}
	if !actor.IsSuperAdmin() {
		return domain.ErrPermissionDenied
	}
	return nil
}

func containsRole(roles []domain.Role, expected domain.Role) bool {
	for _, role := range roles {
		if role == expected {
			return true
		}
	}
	return false
}

func (service *Management) audit(ctx context.Context, actorID, targetID int64, action string, details any) error {
	payload := []byte(`{}`)
	if details != nil {
		var err error
		payload, err = json.Marshal(details)
		if err != nil {
			return err
		}
	}
	return service.repository.AppendAccessAudit(ctx, domain.AccessAudit{
		ActorAdminUserID: actorID, TargetAdminUserID: targetID, Action: action,
		Details: payload, CreatedAt: service.now().UTC(),
	})
}
