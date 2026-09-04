// Package h5oauth owns the one-time Official Account OAuth bridge used only to
// issue trusted Payment Sessions. It never accepts an OpenID from the browser.
package h5oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrUnavailable = errors.New("payment H5 OAuth unavailable")
var ErrInvalid = errors.New("invalid payment H5 OAuth request")
var returnPathPattern = regexp.MustCompile(`^/pay/[1-9][0-9]*$`)

type Provider interface {
	Enabled() bool
	AuthorizationURL(string) string
	Exchange(context.Context, string) (identitydomain.VerifiedFact, error)
}

type State struct {
	ReturnPath string
	ExpiresAt  time.Time
}

type Store interface {
	Create(context.Context, [32]byte, State, time.Time) error
	Consume(context.Context, [32]byte, time.Time) (State, error)
}

type PostgreSQL struct{}

func (PostgreSQL) Create(ctx context.Context, digest [32]byte, state State, now time.Time) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_h5_oauth_states(state_digest,return_path,expires_at,created_at) VALUES($1,$2,$3,$4)`, digest[:], state.ReturnPath, state.ExpiresAt, now)
	return err
}

func (PostgreSQL) Consume(ctx context.Context, digest [32]byte, now time.Time) (State, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return State{}, err
	}
	var state State
	err = tx.QueryRow(ctx, `UPDATE payment_h5_oauth_states SET consumed_at=$2 WHERE state_digest=$1 AND consumed_at IS NULL AND expires_at>$2 RETURNING return_path,expires_at`, digest[:], now).Scan(&state.ReturnPath, &state.ExpiresAt)
	if err != nil || !returnPathPattern.MatchString(state.ReturnPath) {
		return State{}, ErrInvalid
	}
	return state, nil
}

type Service struct {
	uow      platformport.UnitOfWork
	store    Store
	provider Provider
	issuer   interface {
		IssueTrusted(context.Context, paymentsession.IssueCommand) (paymentsession.Issued, error)
	}
	now func() time.Time
}

func NewService(uow platformport.UnitOfWork, store Store, provider Provider, issuer interface {
	IssueTrusted(context.Context, paymentsession.IssueCommand) (paymentsession.Issued, error)
}) (*Service, error) {
	if uow == nil || store == nil || provider == nil || issuer == nil {
		return nil, ErrInvalid
	}
	return &Service{uow: uow, store: store, provider: provider, issuer: issuer, now: time.Now}, nil
}

func (s *Service) Enabled() bool { return s != nil && s.provider != nil && s.provider.Enabled() }

func (s *Service) Start(ctx context.Context, returnPath string) (string, error) {
	if !s.Enabled() || !returnPathPattern.MatchString(returnPath) {
		return "", ErrInvalid
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", ErrUnavailable
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	now := s.now().UTC()
	if err := s.uow.Within(ctx, func(tx context.Context) error {
		return s.store.Create(tx, digest, State{ReturnPath: returnPath, ExpiresAt: now.Add(10 * time.Minute)}, now)
	}); err != nil {
		return "", ErrUnavailable
	}
	return s.provider.AuthorizationURL(token), nil
}

func (s *Service) Complete(ctx context.Context, stateToken, code string) (paymentsession.Issued, string, error) {
	if !s.Enabled() || !safe(stateToken, 128) || !safe(code, 512) {
		return paymentsession.Issued{}, "", ErrInvalid
	}
	digest := sha256.Sum256([]byte(stateToken))
	now := s.now().UTC()
	var state State
	if err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		state, err = s.store.Consume(tx, digest, now)
		return err
	}); err != nil {
		return paymentsession.Issued{}, "", ErrInvalid
	}
	fact, err := s.provider.Exchange(ctx, code) // Provider call is outside PostgreSQL transaction.
	if err != nil || !fact.Valid() {
		return paymentsession.Issued{}, "", ErrUnavailable
	}
	issued, err := s.issuer.IssueTrusted(ctx, paymentsession.IssueCommand{Fact: fact, IdempotencyKey: "payment-h5-oauth:" + base64.RawURLEncoding.EncodeToString(digest[:])})
	if err != nil || issued.Channel != "h5_official_account" {
		return paymentsession.Issued{}, "", ErrUnavailable
	}
	return issued, state.ReturnPath, nil
}

func safe(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\x00")
}
