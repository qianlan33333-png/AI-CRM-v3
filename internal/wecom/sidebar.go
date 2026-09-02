package wecom

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type SidebarPrincipal struct {
	CorpID     string
	EmployeeID string
}

type SidebarPrincipalResolver interface {
	SidebarPrincipal(context.Context) (SidebarPrincipal, error)
}

type SidebarCustomerView struct {
	CustomerID customerdomain.CustomerID `json:"customer_id"`
	Status     string                    `json:"status"`
}

type SidebarCustomerViewer interface {
	SidebarCustomer(context.Context, customerdomain.CustomerID) (SidebarCustomerView, error)
}

type ContextTokenService struct {
	CorpID        string
	SigningKey    []byte
	Relationships FollowRelationshipStore
	UOW           platformport.UnitOfWork
	TTL           time.Duration
	Now           func() time.Time
}

type contextClaims struct {
	CorpID     string `json:"corp_id"`
	EmployeeID string `json:"employee_id"`
	CustomerID int64  `json:"customer_id"`
	ExpiresAt  int64  `json:"exp"`
}

func (service ContextTokenService) Issue(ctx context.Context, principal SidebarPrincipal, customerID customerdomain.CustomerID) (string, error) {
	if principal.CorpID != service.CorpID || principal.EmployeeID == "" || customerID < 1 || !service.valid() {
		return "", ErrInvalidContext
	}
	var active bool
	if err := service.UOW.Within(ctx, func(txContext context.Context) error {
		var err error
		active, err = service.Relationships.IsActive(txContext, principal.CorpID, principal.EmployeeID, customerID)
		return err
	}); err != nil || !active {
		return "", ErrRelationship
	}
	ttl := service.TTL
	if ttl <= 0 || ttl > 15*time.Minute {
		ttl = 5 * time.Minute
	}
	claims := contextClaims{CorpID: principal.CorpID, EmployeeID: principal.EmployeeID, CustomerID: int64(customerID), ExpiresAt: service.clock()().Add(ttl).Unix()}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", ErrInvalidContext
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, service.SigningKey)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (service ContextTokenService) Verify(ctx context.Context, token string) (SidebarPrincipal, customerdomain.CustomerID, error) {
	if !service.valid() {
		return SidebarPrincipal{}, 0, ErrInvalidContext
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return SidebarPrincipal{}, 0, ErrInvalidContext
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return SidebarPrincipal{}, 0, ErrInvalidContext
	}
	mac := hmac.New(sha256.New, service.SigningKey)
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return SidebarPrincipal{}, 0, ErrInvalidContext
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return SidebarPrincipal{}, 0, ErrInvalidContext
	}
	var claims contextClaims
	if json.Unmarshal(payload, &claims) != nil || claims.CorpID != service.CorpID || claims.EmployeeID == "" || claims.CustomerID < 1 || claims.ExpiresAt < service.clock()().Unix() {
		return SidebarPrincipal{}, 0, ErrInvalidContext
	}
	principal := SidebarPrincipal{CorpID: claims.CorpID, EmployeeID: claims.EmployeeID}
	customerID := customerdomain.CustomerID(claims.CustomerID)
	var active bool
	if err = service.UOW.Within(ctx, func(txContext context.Context) error {
		active, err = service.Relationships.IsActive(txContext, claims.CorpID, claims.EmployeeID, customerID)
		return err
	}); err != nil || !active {
		return SidebarPrincipal{}, 0, ErrRelationship
	}
	return principal, customerID, nil
}

func (service ContextTokenService) valid() bool {
	return service.CorpID != "" && len(service.SigningKey) >= 32 && service.Relationships != nil && service.UOW != nil
}

func (service ContextTokenService) clock() func() time.Time {
	if service.Now != nil {
		return service.Now
	}
	return func() time.Time { return time.Now().UTC() }
}

var ErrCustomerNotFound = errors.New("sidebar customer not found")
