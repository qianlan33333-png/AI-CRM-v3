package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"time"
)

var ErrInvalid = errors.New("invalid payment session")
var ErrExpired = errors.New("payment session expired")
var ErrConsumed = errors.New("payment session consumed")

type Record struct {
	ID                                     int64
	TokenDigest                            [32]byte
	PayerIdentityID                        int64
	PayerCustomerID, BeneficiaryCustomerID customerdomain.CustomerID
	AppScopeDigest                         [32]byte
	Channel                                paymentdomain.Channel
	ExpiresAt                              time.Time
	ConsumedAt                             *time.Time
	CreatedAt                              time.Time
}
type Store interface {
	Insert(context.Context, Record) (Record, error)
	Consume(context.Context, [32]byte, time.Time) (Record, error)
	Lookup(context.Context, [32]byte, time.Time) (Record, error)
}
type IssueCommand struct {
	Fact                  identitydomain.VerifiedFact
	BeneficiaryCustomerID customerdomain.CustomerID
	AdminAssisted         bool
	IdempotencyKey        string
}
type Issued struct {
	Token                                  string
	ExpiresAt                              time.Time
	PayerIdentityID                        int64
	PayerCustomerID, BeneficiaryCustomerID customerdomain.CustomerID
	Channel                                paymentdomain.Channel
}
type Service struct {
	uow       platformport.UnitOfWork
	provision identityport.VerifiedProvisioner
	store     Store
	ttl       time.Duration
	now       func() time.Time
}

func NewService(uow platformport.UnitOfWork, p identityport.VerifiedProvisioner, s Store, ttl time.Duration) (*Service, error) {
	if uow == nil || p == nil || s == nil || ttl < time.Minute || ttl > 30*time.Minute {
		return nil, ErrInvalid
	}
	return &Service{uow: uow, provision: p, store: s, ttl: ttl, now: time.Now}, nil
}
func (s *Service) IssueTrusted(ctx context.Context, c IssueCommand) (Issued, error) {
	if s == nil || !c.Fact.Valid() || len(c.IdempotencyKey) < 16 {
		return Issued{}, ErrInvalid
	}
	raw := make([]byte, 32)
	if _, e := rand.Read(raw); e != nil {
		return Issued{}, e
	}
	token := "pays_" + base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	ref := c.Fact.Reference()
	channel := paymentdomain.ChannelMiniProgram
	if ref.Kind == identitydomain.KindOAOpenID {
		channel = paymentdomain.ChannelH5Official
	} else if ref.Kind != identitydomain.KindMPOpenID {
		return Issued{}, ErrInvalid
	}
	scopeDigest := sha256.Sum256([]byte(string(ref.Kind) + "\x00" + ref.Scope))
	now := s.now().UTC()
	var out Issued
	e := s.uow.Within(ctx, func(tx context.Context) error {
		p, e := s.provision.ProvisionVerifiedIdentity(tx, identityport.ProvisionCommand{Fact: c.Fact, IdempotencyKey: c.IdempotencyKey})
		if e != nil {
			return e
		}
		beneficiary := p.CustomerID
		if c.BeneficiaryCustomerID > 0 {
			if c.BeneficiaryCustomerID != p.CustomerID && !c.AdminAssisted {
				return ErrInvalid
			}
			beneficiary = c.BeneficiaryCustomerID
		}
		record, e := s.store.Insert(tx, Record{TokenDigest: digest, PayerIdentityID: p.IdentityID, PayerCustomerID: p.CustomerID, BeneficiaryCustomerID: beneficiary, AppScopeDigest: scopeDigest, Channel: channel, ExpiresAt: now.Add(s.ttl), CreatedAt: now})
		if e != nil {
			return e
		}
		out = Issued{Token: token, ExpiresAt: record.ExpiresAt, PayerIdentityID: record.PayerIdentityID, PayerCustomerID: record.PayerCustomerID, BeneficiaryCustomerID: record.BeneficiaryCustomerID, Channel: record.Channel}
		return nil
	})
	return out, e
}
func (s *Service) Consume(ctx context.Context, token string) (Record, error) {
	if s == nil || len(token) < 20 || len(token) > 100 {
		return Record{}, ErrInvalid
	}
	digest := sha256.Sum256([]byte(token))
	now := s.now().UTC()
	var out Record
	e := s.uow.Within(ctx, func(tx context.Context) error { var e error; out, e = s.store.Consume(tx, digest, now); return e })
	return out, e
}

func (s *Service) ConsumeWithin(ctx context.Context, token string, now time.Time) (paymentport.SessionActor, error) {
	if s == nil || s.store == nil || len(token) < 20 || len(token) > 100 || now.IsZero() {
		return paymentport.SessionActor{}, ErrInvalid
	}
	digest := sha256.Sum256([]byte(token))
	record, err := s.store.Consume(ctx, digest, now.UTC())
	if err != nil {
		return paymentport.SessionActor{}, err
	}
	return paymentport.SessionActor{PayerIdentityID: record.PayerIdentityID, PayerCustomerID: int64(record.PayerCustomerID), BeneficiaryCustomerID: int64(record.BeneficiaryCustomerID), Channel: record.Channel}, nil
}

func (s *Service) LookupWithin(ctx context.Context, token string, now time.Time) (paymentport.SessionActor, error) {
	if s == nil || s.store == nil || len(token) < 20 || len(token) > 100 || now.IsZero() {
		return paymentport.SessionActor{}, ErrInvalid
	}
	digest := sha256.Sum256([]byte(token))
	record, err := s.store.Lookup(ctx, digest, now.UTC())
	if err != nil {
		return paymentport.SessionActor{}, err
	}
	return paymentport.SessionActor{PayerIdentityID: record.PayerIdentityID, PayerCustomerID: int64(record.PayerCustomerID), BeneficiaryCustomerID: int64(record.BeneficiaryCustomerID), Channel: record.Channel}, nil
}
