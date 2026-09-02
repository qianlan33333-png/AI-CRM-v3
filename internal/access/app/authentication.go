package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type Passwords interface {
	Hash(string) (string, error)
	Verify(string, string) bool
}

type AuthenticationConfig struct {
	SessionTTL   time.Duration
	Window       time.Duration
	MaxFailures  int
	BlockFor     time.Duration
	Now          func() time.Time
	DummyPHCHash string
}

type Authentication struct {
	repository accessport.Repository
	uow        platformport.UnitOfWork
	passwords  Passwords
	config     AuthenticationConfig
}

type LoginCommand struct {
	Username string
	Password string
	Remote   string
}

type IssuedSession struct {
	SessionToken string
	CSRFToken    string
	ExpiresAt    time.Time
	User         domain.User
}

func NewAuthentication(repository accessport.Repository, uow platformport.UnitOfWork, passwords Passwords, config AuthenticationConfig) (*Authentication, error) {
	if repository == nil || uow == nil || passwords == nil {
		return nil, errors.New("access authentication dependencies are required")
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = 8 * time.Hour
	}
	if config.Window <= 0 {
		config.Window = 15 * time.Minute
	}
	if config.MaxFailures <= 0 {
		config.MaxFailures = 5
	}
	if config.BlockFor <= 0 {
		config.BlockFor = 15 * time.Minute
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.DummyPHCHash == "" {
		return nil, errors.New("dummy Argon2id hash is required")
	}
	return &Authentication{repository: repository, uow: uow, passwords: passwords, config: config}, nil
}

func (service *Authentication) Login(ctx context.Context, command LoginCommand) (IssuedSession, error) {
	username, err := domain.NormalizeUsername(command.Username)
	if err != nil || command.Password == "" {
		return IssuedSession{}, domain.ErrInvalidCredentials
	}
	now := service.config.Now().UTC()
	identifierDigest := credential.Digest(username)
	remoteDigest := credential.Digest(strings.TrimSpace(command.Remote))
	rateDigest := credential.Digest(username + "\x00" + strings.TrimSpace(command.Remote))
	var issued IssuedSession
	var decisionErr error
	err = service.uow.Within(ctx, func(txContext context.Context) error {
		limit, loadErr := service.repository.LoginRateLimit(txContext, rateDigest, true)
		if loadErr != nil {
			return loadErr
		}
		if now.Sub(limit.WindowStartedAt) >= service.config.Window {
			limit.WindowStartedAt = now
			limit.FailureCount = 0
			limit.BlockedUntil = nil
		}
		if limit.BlockedUntil != nil && now.Before(*limit.BlockedUntil) {
			if err := service.auditLogin(txContext, nil, identifierDigest, remoteDigest, "rate_limited", "threshold_exceeded", now); err != nil {
				return err
			}
			decisionErr = domain.ErrRateLimited
			return nil
		}

		user, userErr := service.repository.UserByUsername(txContext, username, true)
		validPassword := false
		if userErr == nil {
			validPassword = service.passwords.Verify(command.Password, user.PasswordHash)
		} else if errors.Is(userErr, domain.ErrNotFound) {
			_ = service.passwords.Verify(command.Password, service.config.DummyPHCHash)
		} else {
			return userErr
		}
		if userErr != nil || !validPassword || !user.Active {
			limit.FailureCount++
			limit.UpdatedAt = now
			if limit.FailureCount >= service.config.MaxFailures {
				blockedUntil := now.Add(service.config.BlockFor)
				limit.BlockedUntil = &blockedUntil
			}
			if saveErr := service.repository.SaveLoginRateLimit(txContext, limit); saveErr != nil {
				return saveErr
			}
			outcome := "invalid_credentials"
			reason := "credentials_rejected"
			var userID *int64
			if userErr == nil {
				userID = &user.ID
				if !user.Active {
					outcome, reason = "disabled", "account_disabled"
				}
			}
			if err := service.auditLogin(txContext, userID, identifierDigest, remoteDigest, outcome, reason, now); err != nil {
				return err
			}
			decisionErr = domain.ErrInvalidCredentials
			return nil
		}

		limit.FailureCount = 0
		limit.BlockedUntil = nil
		limit.WindowStartedAt = now
		limit.UpdatedAt = now
		if err := service.repository.SaveLoginRateLimit(txContext, limit); err != nil {
			return err
		}
		sessionToken, sessionDigest, err := credential.IssueOpaque("as_")
		if err != nil {
			return err
		}
		csrfToken, csrfDigest, err := credential.IssueOpaque("ac_")
		if err != nil {
			return err
		}
		expires := now.Add(service.config.SessionTTL)
		if _, err = service.repository.CreateSession(txContext, domain.Session{
			TokenDigest: sessionDigest, CSRFTokenDigest: csrfDigest,
			AdminUserID: user.ID, SessionVersion: user.SessionVersion, ExpiresAt: expires,
		}); err != nil {
			return err
		}
		if err = service.repository.SetLastLogin(txContext, user.ID, now); err != nil {
			return err
		}
		if err = service.repository.AppendLoginAudit(txContext, domain.LoginAudit{
			AdminUserID: &user.ID, IdentifierDigest: identifierDigest, RemoteDigest: remoteDigest,
			Outcome: "succeeded", Reason: "local_password", CreatedAt: now,
		}); err != nil {
			return err
		}
		issued = IssuedSession{SessionToken: sessionToken, CSRFToken: csrfToken, ExpiresAt: expires, User: user}
		return nil
	})
	if err != nil {
		return IssuedSession{}, err
	}
	if decisionErr != nil {
		return IssuedSession{}, decisionErr
	}
	return issued, nil
}

func (service *Authentication) Authenticate(ctx context.Context, sessionToken string) (domain.Principal, error) {
	if strings.TrimSpace(sessionToken) == "" {
		return domain.Principal{}, domain.ErrAuthentication
	}
	now := service.config.Now().UTC()
	var principal domain.Principal
	err := service.uow.Within(ctx, func(txContext context.Context) error {
		session, err := service.repository.SessionByTokenDigest(txContext, credential.Digest(sessionToken), false)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return domain.ErrAuthentication
			}
			return err
		}
		if session.RevokedAt != nil || !now.Before(session.ExpiresAt) || !session.User.Active || session.SessionVersion != session.User.SessionVersion {
			return domain.ErrAuthentication
		}
		if len(session.User.Roles) == 0 {
			return domain.ErrPermissionDenied
		}
		if err = service.repository.TouchSession(txContext, session.ID, now); err != nil {
			return err
		}
		principal = domain.Principal{Kind: domain.KindAdmin, InternalID: session.User.ID, Roles: session.User.Roles}
		return nil
	})
	return principal, err
}

func (service *Authentication) AuthorizeCSRF(ctx context.Context, sessionToken, csrfCookie, csrfRequest string) (domain.Principal, error) {
	if sessionToken == "" || csrfCookie == "" || csrfRequest == "" {
		return domain.Principal{}, domain.ErrCSRFRequired
	}
	now := service.config.Now().UTC()
	var principal domain.Principal
	err := service.uow.Within(ctx, func(txContext context.Context) error {
		session, err := service.repository.SessionByTokenDigest(txContext, credential.Digest(sessionToken), false)
		if err != nil || session.RevokedAt != nil || !now.Before(session.ExpiresAt) || !session.User.Active || session.SessionVersion != session.User.SessionVersion {
			return domain.ErrAuthentication
		}
		if !credential.Matches(csrfCookie, session.CSRFTokenDigest) || !credential.Matches(csrfRequest, session.CSRFTokenDigest) {
			return domain.ErrCSRFRequired
		}
		principal = domain.Principal{Kind: domain.KindAdmin, InternalID: session.User.ID, Roles: session.User.Roles}
		return nil
	})
	return principal, err
}

func (service *Authentication) Logout(ctx context.Context, sessionToken, csrfCookie, csrfRequest string) error {
	if _, err := service.AuthorizeCSRF(ctx, sessionToken, csrfCookie, csrfRequest); err != nil {
		return err
	}
	return service.uow.Within(ctx, func(txContext context.Context) error {
		_, err := service.repository.RevokeSession(txContext, credential.Digest(sessionToken), "logout", service.config.Now().UTC())
		return err
	})
}

func (service *Authentication) auditLogin(ctx context.Context, userID *int64, identifier, remote [32]byte, outcome, reason string, now time.Time) error {
	return service.repository.AppendLoginAudit(ctx, domain.LoginAudit{
		AdminUserID: userID, IdentifierDigest: identifier, RemoteDigest: remote,
		Outcome: outcome, Reason: reason, CreatedAt: now,
	})
}
