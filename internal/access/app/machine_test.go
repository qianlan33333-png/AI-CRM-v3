package app

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

type machineRepositoryStub struct {
	clients map[string]domain.MachineClient
	audits  []domain.MachineAudit
}

func (stub *machineRepositoryStub) MachineClientByID(_ context.Context, id string, _ bool) (domain.MachineClient, error) {
	client, ok := stub.clients[id]
	if !ok {
		return domain.MachineClient{}, domain.ErrNotFound
	}
	return client, nil
}
func (stub *machineRepositoryStub) ListMachineClients(context.Context) ([]domain.MachineClient, error) {
	items := make([]domain.MachineClient, 0, len(stub.clients))
	for _, client := range stub.clients {
		items = append(items, client)
	}
	return items, nil
}
func (stub *machineRepositoryStub) CreateMachineClient(_ context.Context, client domain.MachineClient) (domain.MachineClient, error) {
	if _, exists := stub.clients[client.ClientID]; exists {
		return domain.MachineClient{}, domain.ErrConflict
	}
	client.ID = int64(len(stub.clients) + 1)
	client.CreatedAt = time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	stub.clients[client.ClientID] = client
	return client, nil
}
func (stub *machineRepositoryStub) ReplaceMachineClient(_ context.Context, client domain.MachineClient) error {
	if _, exists := stub.clients[client.ClientID]; !exists {
		return domain.ErrNotFound
	}
	stub.clients[client.ClientID] = client
	return nil
}
func (stub *machineRepositoryStub) SetMachineClientLastUsed(_ context.Context, id int64, used time.Time) error {
	for key, client := range stub.clients {
		if client.ID == id {
			client.LastUsedAt = &used
			stub.clients[key] = client
			return nil
		}
	}
	return domain.ErrNotFound
}
func (stub *machineRepositoryStub) AppendMachineAudit(_ context.Context, audit domain.MachineAudit) error {
	stub.audits = append(stub.audits, audit)
	return nil
}

func TestMachineTokenRotateAndDisableImmediatelyInvalidateBearer(t *testing.T) {
	now := time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC)
	repository := &machineRepositoryStub{clients: map[string]domain.MachineClient{}}
	service, err := NewMachineService(repository, testUOW{}, credential.PasswordHasher{}, MachineConfig{SigningKey: []byte("01234567890123456789012345678901"), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	admin := domain.Principal{Kind: domain.KindAdmin, InternalID: 9, Roles: []domain.Role{domain.RoleSuperAdmin}}
	created, err := service.Create(context.Background(), admin, CreateMachineClientInput{
		ClientID: "partner.analytics", DisplayName: "Partner analytics", Purpose: "external_agent",
		Audiences: []string{"external_integration"}, Scopes: []string{"read"}, Capabilities: []string{"external_read"},
	})
	if err != nil || created.Secret == "" || created.Client.CredentialHint == created.Secret {
		t.Fatalf("create = %+v, %v", created, err)
	}
	if created.Client.Enabled {
		t.Fatal("API client must remain disabled until its one-time secret is checked")
	}
	if _, err = service.Activate(context.Background(), admin, created.Client.ClientID, created.Secret, true); err != nil {
		t.Fatal(err)
	}
	issued, err := service.IssueClientCredentialsToken(context.Background(), ClientCredentialsInput{ClientID: created.Client.ClientID, ClientSecret: created.Secret, Audience: "external_integration", RequestedScopes: []string{"read"}, SourceIP: netip.MustParseAddr("203.0.113.9")})
	if err != nil || issued.ExpiresIn != 1800 || issued.Scope != "read" {
		t.Fatalf("issue = %+v, %v", issued, err)
	}
	if _, err = service.AuthenticateBearer(context.Background(), issued.AccessToken, "external_integration", netip.MustParseAddr("203.0.113.9")); err != nil {
		t.Fatalf("authenticate issued token: %v", err)
	}
	rotated, err := service.Rotate(context.Background(), admin, created.Client.ClientID)
	if err != nil || rotated.Secret == created.Secret {
		t.Fatalf("rotate = %+v, %v", rotated, err)
	}
	if rotated.Client.Enabled {
		t.Fatal("rotated API client must require explicit reactivation")
	}
	if _, err = service.AuthenticateBearer(context.Background(), issued.AccessToken, "external_integration", netip.MustParseAddr("203.0.113.9")); !errors.Is(err, domain.ErrMachineCredential) && !errors.Is(err, domain.ErrMachineClientDisabled) {
		t.Fatalf("old JWT after rotation = %v", err)
	}
	if _, err = service.IssueClientCredentialsToken(context.Background(), ClientCredentialsInput{ClientID: created.Client.ClientID, ClientSecret: created.Secret, Audience: "external_integration", RequestedScopes: []string{"read"}, SourceIP: netip.MustParseAddr("203.0.113.9")}); !errors.Is(err, domain.ErrMachineCredential) {
		t.Fatalf("old secret after rotation = %v", err)
	}
	if _, err = service.Activate(context.Background(), admin, created.Client.ClientID, rotated.Secret, true); err != nil {
		t.Fatal(err)
	}
	newToken, err := service.IssueClientCredentialsToken(context.Background(), ClientCredentialsInput{ClientID: created.Client.ClientID, ClientSecret: rotated.Secret, Audience: "external_integration", RequestedScopes: []string{"read"}, SourceIP: netip.MustParseAddr("203.0.113.9")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.SetEnabled(context.Background(), admin, created.Client.ClientID, false); err != nil {
		t.Fatal(err)
	}
	if _, err = service.AuthenticateBearer(context.Background(), newToken.AccessToken, "external_integration", netip.MustParseAddr("203.0.113.9")); !errors.Is(err, domain.ErrMachineClientDisabled) {
		t.Fatalf("token after disable = %v", err)
	}
}

func TestMachineClientCredentialsEnforcesAudienceScopeAndCIDR(t *testing.T) {
	now := time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC)
	repository := &machineRepositoryStub{clients: map[string]domain.MachineClient{}}
	service, err := NewMachineService(repository, testUOW{}, credential.PasswordHasher{}, MachineConfig{SigningKey: []byte("01234567890123456789012345678901"), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	admin := domain.Principal{Kind: domain.KindAdmin, InternalID: 9, Roles: []domain.Role{domain.RoleSuperAdmin}}
	created, err := service.Create(context.Background(), admin, CreateMachineClientInput{ClientID: "mcp.agent", DisplayName: "MCP agent", Purpose: "mcp", Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"}, Capabilities: []string{"mcp_read", "mcp_execute"}, AllowedCIDRs: []string{"203.0.113.0/24"}, TokenTTLSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Activate(context.Background(), admin, created.Client.ClientID, created.Secret, true); err != nil {
		t.Fatal(err)
	}
	input := ClientCredentialsInput{ClientID: created.Client.ClientID, ClientSecret: created.Secret, Audience: "external_integration", RequestedScopes: []string{"write"}, SourceIP: netip.MustParseAddr("203.0.113.11")}
	if _, err = service.IssueClientCredentialsToken(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	input.SourceIP = netip.MustParseAddr("198.51.100.11")
	if _, err = service.IssueClientCredentialsToken(context.Background(), input); !errors.Is(err, domain.ErrMachineSourceIP) {
		t.Fatalf("CIDR mismatch = %v", err)
	}
	input.SourceIP, input.Audience = netip.MustParseAddr("203.0.113.11"), "mcp"
	if _, err = service.IssueClientCredentialsToken(context.Background(), input); !errors.Is(err, domain.ErrMachineAudience) {
		t.Fatalf("audience mismatch = %v", err)
	}
}

func TestMachineClientUpdateAndActivationKeepTheDisabledHandoff(t *testing.T) {
	now := time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC)
	repository := &machineRepositoryStub{clients: map[string]domain.MachineClient{}}
	service, err := NewMachineService(repository, testUOW{}, credential.PasswordHasher{}, MachineConfig{SigningKey: []byte("01234567890123456789012345678901"), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	admin := domain.Principal{Kind: domain.KindAdmin, InternalID: 9, Roles: []domain.Role{domain.RoleSuperAdmin}}
	created, err := service.Create(context.Background(), admin, CreateMachineClientInput{ClientID: "partner.update", DisplayName: "Initial", Purpose: "external_agent", Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"}, Capabilities: []string{"external_read", "external_write"}})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.Update(context.Background(), admin, created.Client.ClientID, UpdateMachineClientInput{DisplayName: "Updated", TokenTTLSeconds: 900, AllowedCIDRs: []string{"203.0.113.0/24"}})
	if err != nil || updated.DisplayName != "Updated" || updated.TokenTTLSeconds != 900 {
		t.Fatalf("update=%+v err=%v", updated, err)
	}
	if _, err = service.Activate(context.Background(), admin, created.Client.ClientID, "wrong", true); !errors.Is(err, domain.ErrMachineActivation) {
		t.Fatalf("wrong activation secret = %v", err)
	}
	if _, err = service.Activate(context.Background(), admin, created.Client.ClientID, created.Secret, false); !errors.Is(err, domain.ErrMachineActivation) {
		t.Fatalf("missing copy confirmation = %v", err)
	}
	if _, err = service.Activate(context.Background(), admin, created.Client.ClientID, created.Secret, true); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Update(context.Background(), admin, created.Client.ClientID, UpdateMachineClientInput{DisplayName: "Denied", TokenTTLSeconds: 900}); !errors.Is(err, domain.ErrMachineClientActive) {
		t.Fatalf("active client update = %v", err)
	}
}
