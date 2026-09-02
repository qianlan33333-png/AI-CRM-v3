package wecom

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"time"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type OAuthPurpose string

const (
	OAuthAdmin   OAuthPurpose = "admin"
	OAuthSidebar OAuthPurpose = "sidebar"
)

type OAuthIdentity struct {
	CorpID     string
	EmployeeID string
}

type OAuthClient interface {
	AuthorizationURL(context.Context, OAuthPurpose, string, string) (string, error)
	ExchangeCode(context.Context, OAuthPurpose, string) (OAuthIdentity, error)
}

type OAuthService struct {
	Enabled      bool
	CorpID       string
	StateStore   OAuthStateStore
	UOW          platformport.UnitOfWork
	Client       OAuthClient
	AllowedPaths map[string]struct{}
	StateTTL     time.Duration
	Now          func() time.Time
	Random       func([]byte) error
}

type OAuthStart struct {
	AuthorizationURL string
	State            string
}

func (service OAuthService) Start(ctx context.Context, purpose OAuthPurpose, redirect string) (OAuthStart, error) {
	if !service.Enabled {
		return OAuthStart{}, ErrProviderDisabled
	}
	if !validPurpose(purpose) || service.StateStore == nil || service.UOW == nil || service.Client == nil || !service.allowedRedirect(redirect) {
		if !service.allowedRedirect(redirect) {
			return OAuthStart{}, ErrOpenRedirect
		}
		return OAuthStart{}, errors.New("oauth service dependencies are required")
	}
	now := service.clock()()
	ttl := service.StateTTL
	if ttl <= 0 || ttl > 15*time.Minute {
		ttl = 10 * time.Minute
	}
	state, err := service.randomState()
	if err != nil {
		return OAuthStart{}, err
	}
	nonce, err := service.randomNonce()
	if err != nil {
		return OAuthStart{}, err
	}
	if err = service.UOW.Within(ctx, func(txContext context.Context) error {
		return service.StateStore.Create(txContext, OAuthState{Purpose: purpose, Redirect: redirect, ExpiresAt: now.Add(ttl)}, oauthDigest(state), oauthDigest(nonce))
	}); err != nil {
		return OAuthStart{}, err
	}
	combinedState := state + "." + nonce
	callbackURL, err := service.Client.AuthorizationURL(ctx, purpose, combinedState, redirect)
	if err != nil {
		return OAuthStart{}, err
	}
	return OAuthStart{AuthorizationURL: callbackURL, State: combinedState}, nil
}

func (service OAuthService) ConsumeAndExchange(ctx context.Context, purpose OAuthPurpose, state, code string) (OAuthIdentity, OAuthState, error) {
	if !service.Enabled {
		return OAuthIdentity{}, OAuthState{}, ErrProviderDisabled
	}
	stateParts := strings.Split(state, ".")
	if !validPurpose(purpose) || len(stateParts) != 2 || stateParts[0] == "" || stateParts[1] == "" || code == "" || service.StateStore == nil || service.UOW == nil || service.Client == nil {
		return OAuthIdentity{}, OAuthState{}, ErrInvalidOAuth
	}
	var stored OAuthState
	err := service.UOW.Within(ctx, func(txContext context.Context) error {
		var consumeErr error
		stored, consumeErr = service.StateStore.Consume(txContext, purpose, oauthDigest(stateParts[0]), oauthDigest(stateParts[1]), service.clock()())
		return consumeErr
	})
	if err != nil {
		return OAuthIdentity{}, OAuthState{}, ErrInvalidOAuth
	}
	identity, err := service.Client.ExchangeCode(ctx, purpose, code)
	if err != nil || identity.CorpID != service.CorpID || strings.TrimSpace(identity.EmployeeID) != identity.EmployeeID || identity.EmployeeID == "" {
		return OAuthIdentity{}, OAuthState{}, ErrInvalidOAuth
	}
	return identity, stored, nil
}

func (service OAuthService) allowedRedirect(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.Contains(value, "\\") {
		return false
	}
	_, allowed := service.AllowedPaths[parsed.Path]
	return allowed
}

func (service OAuthService) randomState() (string, error) {
	bytes := make([]byte, 32)
	if err := service.random()(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func (service OAuthService) randomNonce() (string, error) {
	bytes := make([]byte, 16)
	if err := service.random()(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func (service OAuthService) clock() func() time.Time {
	if service.Now != nil {
		return service.Now
	}
	return func() time.Time { return time.Now().UTC() }
}

func (service OAuthService) random() func([]byte) error {
	if service.Random != nil {
		return service.Random
	}
	return func(value []byte) error { _, err := rand.Read(value); return err }
}

func validPurpose(value OAuthPurpose) bool { return value == OAuthAdmin || value == OAuthSidebar }
