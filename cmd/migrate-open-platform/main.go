// Command migrate-open-platform imports a sealed, non-secret historical client
// manifest. It never connects to a donor runtime, restores no credential, and
// does not run automatically during installation or application startup.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

const historySchemaVersion = "aicrm-open-platform-machine-history-v1"

var sourceRevision = regexp.MustCompile(`^[a-f0-9]{40}$`)

type historicalManifest struct {
	SchemaVersion  string                `json:"schema_version"`
	SourceSystem   string                `json:"source_system"`
	SourceRevision string                `json:"source_revision"`
	ImportRunID    string                `json:"import_run_id"`
	Clients        []historicalClientRow `json:"clients"`
}

type historicalClientRow struct {
	SourceRowID     string                  `json:"source_row_id"`
	ClientID        string                  `json:"client_id"`
	DisplayName     string                  `json:"display_name"`
	Purpose         string                  `json:"purpose"`
	AllowedCIDRs    []string                `json:"allowed_cidrs"`
	OwnerScope      accessdomain.OwnerScope `json:"owner_scope,omitempty"`
	TokenTTLSeconds int                     `json:"token_ttl_seconds"`
	ExpiresAt       *time.Time              `json:"expires_at,omitempty"`
}

type importResult struct {
	Imported int `json:"imported"`
	Replayed int `json:"replayed"`
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate-open-platform", flag.ContinueOnError)
	mode := fs.String("mode", "inspect", "inspect|dry-run|apply|verify")
	manifestPath := fs.String("manifest", "", "non-secret historical machine-client manifest")
	wantDigest := fs.String("manifest-sha256", "", "canonical manifest digest confirmation")
	confirmApply := fs.Bool("confirm-apply", false, "confirm inert historical credential write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *manifestPath == "" {
		return errors.New("manifest is required")
	}
	manifest, digest, err := loadManifest(*manifestPath)
	if err != nil {
		return err
	}
	summary := map[string]any{"manifest_sha256": hex.EncodeToString(digest[:]), "clients": len(manifest.Clients)}
	switch *mode {
	case "inspect":
		summary["mode"] = "inspect"
		return writeSummary(summary)
	case "dry-run":
		if *wantDigest != hex.EncodeToString(digest[:]) {
			return errors.New("manifest-sha256 confirmation mismatch")
		}
		summary["mode"] = "dry-run"
		summary["eligible"] = true
		return writeSummary(summary)
	case "apply":
		if *wantDigest != hex.EncodeToString(digest[:]) {
			return errors.New("manifest-sha256 confirmation mismatch")
		}
		if !*confirmApply {
			return errors.New("apply requires --confirm-apply")
		}
		result, applyErr := applyManifest(ctx, manifest)
		if applyErr != nil {
			return applyErr
		}
		summary["mode"] = "apply"
		summary["result"] = result
		return writeSummary(summary)
	case "verify":
		if *wantDigest != hex.EncodeToString(digest[:]) {
			return errors.New("manifest-sha256 confirmation mismatch")
		}
		result, verifyErr := verifyManifest(ctx, manifest)
		if verifyErr != nil {
			return verifyErr
		}
		summary["mode"] = "verify"
		summary["result"] = result
		return writeSummary(summary)
	default:
		return errors.New("unknown mode")
	}
}

func loadManifest(path string) (historicalManifest, [sha256.Size]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return historicalManifest{}, [sha256.Size]byte{}, errors.New("read historical machine manifest")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest historicalManifest
	if err = decoder.Decode(&manifest); err != nil {
		return historicalManifest{}, [sha256.Size]byte{}, errors.New("decode historical machine manifest")
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return historicalManifest{}, [sha256.Size]byte{}, errors.New("decode historical machine manifest")
	}
	canonical, digest, err := canonicalManifest(manifest)
	if err != nil {
		return historicalManifest{}, [sha256.Size]byte{}, err
	}
	return canonical, digest, nil
}

func canonicalManifest(manifest historicalManifest) (historicalManifest, [sha256.Size]byte, error) {
	manifest.SchemaVersion = strings.TrimSpace(manifest.SchemaVersion)
	manifest.SourceSystem = strings.TrimSpace(manifest.SourceSystem)
	manifest.SourceRevision = strings.TrimSpace(manifest.SourceRevision)
	manifest.ImportRunID = strings.TrimSpace(manifest.ImportRunID)
	if manifest.SchemaVersion != historySchemaVersion || manifest.SourceSystem != "ai-crm" || !sourceRevision.MatchString(manifest.SourceRevision) || manifest.ImportRunID == "" || len(manifest.ImportRunID) > 160 || len(manifest.Clients) > 100000 {
		return historicalManifest{}, [sha256.Size]byte{}, errors.New("invalid historical machine manifest")
	}
	sort.Slice(manifest.Clients, func(left, right int) bool {
		return manifest.Clients[left].SourceRowID < manifest.Clients[right].SourceRowID
	})
	seenRows, seenClients := map[string]struct{}{}, map[string]struct{}{}
	for index := range manifest.Clients {
		row := &manifest.Clients[index]
		row.SourceRowID = strings.TrimSpace(row.SourceRowID)
		row.ClientID = strings.TrimSpace(row.ClientID)
		row.DisplayName = strings.TrimSpace(row.DisplayName)
		row.Purpose = strings.TrimSpace(row.Purpose)
		if row.ExpiresAt != nil {
			at := row.ExpiresAt.UTC()
			row.ExpiresAt = &at
		}
		if row.SourceRowID == "" || len(row.SourceRowID) > 240 || row.ClientID == "" || row.DisplayName == "" || !accessdomain.IsMachinePurpose(row.Purpose) {
			return historicalManifest{}, [sha256.Size]byte{}, errors.New("invalid historical machine client")
		}
		if _, exists := seenRows[row.SourceRowID]; exists {
			return historicalManifest{}, [sha256.Size]byte{}, errors.New("duplicate historical source row")
		}
		if _, exists := seenClients[row.ClientID]; exists {
			return historicalManifest{}, [sha256.Size]byte{}, errors.New("duplicate historical client id")
		}
		seenRows[row.SourceRowID], seenClients[row.ClientID] = struct{}{}, struct{}{}
		if row.TokenTTLSeconds == 0 {
			row.TokenTTLSeconds = 1800
		}
		if _, err := accessdomain.NormalizeMachineClientID(row.ClientID); err != nil || row.TokenTTLSeconds < 60 || row.TokenTTLSeconds > 3600 {
			return historicalManifest{}, [sha256.Size]byte{}, errors.New("invalid historical machine client")
		}
		if _, err := accessdomain.NormalizeCIDRs(row.AllowedCIDRs); err != nil {
			return historicalManifest{}, [sha256.Size]byte{}, errors.New("invalid historical machine client")
		}
		if _, err := accessdomain.NormalizeOwnerScope(row.OwnerScope.JSON()); err != nil {
			return historicalManifest{}, [sha256.Size]byte{}, errors.New("invalid historical machine client")
		}
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return historicalManifest{}, [sha256.Size]byte{}, errors.New("canonicalize historical machine manifest")
	}
	return manifest, sha256.Sum256(raw), nil
}

func applyManifest(ctx context.Context, manifest historicalManifest) (importResult, error) {
	service, closeService, err := machineHistoryService(ctx)
	if err != nil {
		return importResult{}, err
	}
	defer closeService()
	result := importResult{}
	for _, row := range manifest.Clients {
		digest, err := sourceRowDigest(row)
		if err != nil {
			return importResult{}, err
		}
		imported, err := service.ImportHistorical(ctx, accessport.HistoricalMachineImportInput{ImportRunID: manifest.ImportRunID, SourceRowID: row.SourceRowID, SourceRowDigest: digest, ClientID: row.ClientID, DisplayName: row.DisplayName, Purpose: row.Purpose, AllowedCIDRs: row.AllowedCIDRs, OwnerScope: row.OwnerScope, TokenTTLSeconds: row.TokenTTLSeconds, ExpiresAt: row.ExpiresAt})
		if err != nil {
			return importResult{}, err
		}
		if imported.Replayed {
			result.Replayed++
		} else {
			result.Imported++
		}
	}
	return result, nil
}

func verifyManifest(ctx context.Context, manifest historicalManifest) (importResult, error) {
	service, closeService, err := machineHistoryService(ctx)
	if err != nil {
		return importResult{}, err
	}
	defer closeService()
	result := importResult{}
	for _, row := range manifest.Clients {
		digest, err := sourceRowDigest(row)
		if err != nil {
			return importResult{}, err
		}
		item, verifyErr := service.VerifyHistorical(ctx, accessport.HistoricalMachineImportInput{ImportRunID: manifest.ImportRunID, SourceRowID: row.SourceRowID, SourceRowDigest: digest, ClientID: row.ClientID, DisplayName: row.DisplayName, Purpose: row.Purpose, AllowedCIDRs: row.AllowedCIDRs, OwnerScope: row.OwnerScope, TokenTTLSeconds: row.TokenTTLSeconds, ExpiresAt: row.ExpiresAt})
		if verifyErr != nil || item.Replayed || item.Outcome != "reissue_required" {
			return importResult{}, errors.New("historical machine import verification failed")
		}
		result.Replayed++
	}
	return result, nil
}

func machineHistoryService(ctx context.Context) (*accessapp.MachineService, func(), error) {
	databaseURL, err := platformconfig.DatabaseURL()
	if err != nil {
		return nil, nil, errors.New("target database is unavailable")
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{URL: databaseURL})
	if err != nil {
		return nil, nil, errors.New("target database is unavailable")
	}
	unit, err := platformpostgres.NewUnitOfWork(pool)
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	service, err := accessapp.NewMachineService(accessstore.NewPostgreSQL(), unit, credential.PasswordHasher{}, accessapp.MachineConfig{})
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	return service, pool.Close, nil
}

func sourceRowDigest(row historicalClientRow) ([sha256.Size]byte, error) {
	raw, err := json.Marshal(row)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(raw), nil
}

func writeSummary(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Println(string(encoded))
	return err
}
