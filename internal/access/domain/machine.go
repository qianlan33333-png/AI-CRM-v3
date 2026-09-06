package domain

import (
	"errors"
	"net/netip"
	"sort"
	"strings"
	"time"
)

var (
	ErrMachineClientDisabled  = errors.New("machine client disabled")
	ErrMachineClientExpired   = errors.New("machine client expired")
	ErrMachineCredential      = errors.New("invalid machine credentials")
	ErrMachineAudience        = errors.New("machine audience denied")
	ErrMachineScope           = errors.New("machine scope denied")
	ErrMachineSourceIP        = errors.New("machine source IP denied")
	ErrMachineReissueRequired = errors.New("machine client requires reissue")
	ErrMachineIssuerUnready   = errors.New("machine token issuer is not configured")
)

// MachineClient is Access-owned authentication state. It deliberately has no
// user roles: a machine principal receives only these explicit grants.
type MachineClient struct {
	ID              int64
	ClientID        string
	DisplayName     string
	Purpose         string
	SecretHash      string
	CredentialHint  string
	Audiences       []string
	Scopes          []string
	Capabilities    []string
	AllowedCIDRs    []string
	TokenTTLSeconds int
	ExpiresAt       *time.Time
	Enabled         bool
	ReissueRequired bool
	AuthVersion     int64
	LastUsedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type MachinePrincipal struct {
	ClientID     string
	ClientRecord int64
	Audience     string
	Scopes       []string
	Capabilities []string
	AuthVersion  int64
	DirectKey    bool
}

func (principal MachinePrincipal) HasCapability(capability string) bool {
	for _, candidate := range principal.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

// MachineAudit contains only identifiers and bounded safe facts. Credentials,
// JWTs, external identifiers, and source address values never enter it.
type MachineAudit struct {
	MachineClientID int64
	ActorAdminID    *int64
	Action          string
	Outcome         string
	Details         []byte
	CreatedAt       time.Time
}

func NormalizeMachineClientID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 3 || len(value) > 120 {
		return "", ErrInvalidInput
	}
	if !((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= '0' && value[0] <= '9')) {
		return "", ErrInvalidInput
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			continue
		}
		return "", ErrInvalidInput
	}
	return value, nil
}

func NormalizeMachineStrings(values []string, allowed map[string]struct{}) ([]string, error) {
	if len(values) == 0 || len(values) > len(allowed) {
		return nil, ErrInvalidInput
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if _, ok := allowed[value]; !ok {
			return nil, ErrInvalidInput
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil, ErrInvalidInput
	}
	sort.Strings(result)
	return result, nil
}

func NormalizeCIDRs(values []string) ([]string, error) {
	if len(values) > 20 {
		return nil, ErrInvalidInput
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, ErrInvalidInput
		}
		value = prefix.Masked().String()
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func MachineSourceAllowed(cidrs []string, source netip.Addr) bool {
	if !source.IsValid() {
		return false
	}
	if len(cidrs) == 0 {
		return true
	}
	for _, raw := range cidrs {
		prefix, err := netip.ParsePrefix(raw)
		if err == nil && prefix.Contains(source) {
			return true
		}
	}
	return false
}
