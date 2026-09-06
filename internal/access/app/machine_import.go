package app

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
)

var _ accessport.MachineHistoricalImporter = (*MachineService)(nil)

// ImportHistorical creates an intentionally unusable replacement record for a
// legacy caller. The generated entropy is immediately hashed and discarded:
// no old or new usable credential is emitted by this path. Rotation is the
// only way to obtain a fresh secret afterwards.
func (service *MachineService) ImportHistorical(ctx context.Context, input accessport.HistoricalMachineImportInput) (accessport.HistoricalMachineImportResult, error) {
	if err := validateHistoricalMachineImport(input); err != nil {
		return accessport.HistoricalMachineImportResult{}, err
	}
	profile, exists := domain.MachineProfileForPurpose(strings.TrimSpace(input.Purpose))
	if !exists {
		return accessport.HistoricalMachineImportResult{}, domain.ErrInvalidInput
	}
	// Active client creation rejects past expiry, while history must retain an
	// already-expired caller as an inert reissue-required record.
	expiresAt := input.ExpiresAt
	createExpiresAt := expiresAt
	if createExpiresAt != nil && !createExpiresAt.After(service.config.Now().UTC()) {
		createExpiresAt = nil
	}
	client, err := service.newMachineClient(accessport.CreateMachineClientInput{
		ClientID: input.ClientID, DisplayName: input.DisplayName, Purpose: profile.Purpose,
		Audiences: profile.Audiences, Scopes: profile.Scopes, Capabilities: profile.Capabilities,
		AllowedCIDRs: input.AllowedCIDRs, OwnerScope: input.OwnerScope, TokenTTLSeconds: input.TokenTTLSeconds, ExpiresAt: createExpiresAt,
	})
	if err != nil {
		return accessport.HistoricalMachineImportResult{}, err
	}
	entropy := make([]byte, 32)
	if _, err = rand.Read(entropy); err != nil {
		return accessport.HistoricalMachineImportResult{}, err
	}
	client.SecretHash, err = service.passwords.Hash(credentialOpaqueImportInput(entropy))
	if err != nil {
		return accessport.HistoricalMachineImportResult{}, err
	}
	client.CredentialHint = "mc_••••reissue"
	client.ExpiresAt = expiresAt
	client.Enabled = false
	client.ReissueRequired = true
	client.AuthVersion = 1

	repository, ok := service.repository.(accessport.MachineHistoricalRepository)
	if !ok {
		return accessport.HistoricalMachineImportResult{}, errors.New("machine historical repository is not configured")
	}
	var imported domain.MachineClient
	var replayed bool
	err = service.uow.Within(ctx, func(txContext context.Context) error {
		var importErr error
		imported, replayed, importErr = repository.ImportHistoricalMachineClient(txContext, input, client)
		if importErr != nil || replayed {
			return importErr
		}
		return service.audit(txContext, imported, nil, "machine_client_imported", "reissue_required")
	})
	if err != nil {
		return accessport.HistoricalMachineImportResult{}, err
	}
	return accessport.HistoricalMachineImportResult{Client: summarizeMachineClient(imported), Outcome: map[bool]string{true: "replayed", false: "reissue_required"}[replayed], Replayed: replayed}, nil
}

// VerifyHistorical confirms a prior receipt without inserting a client or
// audit record. It is safe for release-time and operator verification.
func (service *MachineService) VerifyHistorical(ctx context.Context, input accessport.HistoricalMachineImportInput) (accessport.HistoricalMachineImportResult, error) {
	if err := validateHistoricalMachineImport(input); err != nil {
		return accessport.HistoricalMachineImportResult{}, err
	}
	expected, exists := domain.MachineProfileForPurpose(strings.TrimSpace(input.Purpose))
	if !exists {
		return accessport.HistoricalMachineImportResult{}, domain.ErrInvalidInput
	}
	repository, ok := service.repository.(accessport.MachineHistoricalVerificationRepository)
	if !ok {
		return accessport.HistoricalMachineImportResult{}, errors.New("machine historical verification repository is not configured")
	}
	var stored domain.MachineClient
	err := service.uow.Within(ctx, func(txContext context.Context) error {
		var verifyErr error
		stored, verifyErr = repository.VerifyHistoricalMachineClient(txContext, input)
		return verifyErr
	})
	if err != nil {
		return accessport.HistoricalMachineImportResult{}, err
	}
	expectedCIDRs, normalizeErr := domain.NormalizeCIDRs(input.AllowedCIDRs)
	if normalizeErr != nil {
		return accessport.HistoricalMachineImportResult{}, domain.ErrInvalidInput
	}
	if stored.ClientID != input.ClientID || stored.DisplayName != strings.TrimSpace(input.DisplayName) || stored.Purpose != expected.Purpose || stored.Enabled || !stored.ReissueRequired || stored.TokenTTLSeconds != input.TokenTTLSeconds || !equalMachineStrings(stored.Audiences, expected.Audiences) || !equalMachineStrings(stored.Scopes, expected.Scopes) || !equalMachineStrings(stored.Capabilities, expected.Capabilities) || !equalMachineStrings(stored.AllowedCIDRs, expectedCIDRs) || string(stored.OwnerScope.JSON()) != string(input.OwnerScope.JSON()) || !equalMachineExpiry(stored.ExpiresAt, input.ExpiresAt) {
		return accessport.HistoricalMachineImportResult{}, domain.ErrConflict
	}
	return accessport.HistoricalMachineImportResult{Client: summarizeMachineClient(stored), Outcome: "reissue_required"}, nil
}

func equalMachineExpiry(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.UTC().Equal(right.UTC())
}

func validateHistoricalMachineImport(input accessport.HistoricalMachineImportInput) error {
	if len(strings.TrimSpace(input.ImportRunID)) < 1 || len(strings.TrimSpace(input.ImportRunID)) > 160 || len(strings.TrimSpace(input.SourceRowID)) < 1 || len(strings.TrimSpace(input.SourceRowID)) > 240 {
		return domain.ErrInvalidInput
	}
	allZero := true
	for _, value := range input.SourceRowDigest {
		if value != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return domain.ErrInvalidInput
	}
	return nil
}

func credentialOpaqueImportInput(entropy []byte) string {
	// This is never persisted or returned. Avoid an old secret or a constant
	// fallback that could become usable if an unrelated future defect regressed
	// the reissue guard.
	return "mc_import_" + string(entropy)
}
