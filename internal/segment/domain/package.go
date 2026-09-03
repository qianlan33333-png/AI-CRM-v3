// Package domain contains Segment-owned audience configuration invariants.
package domain

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

var (
	ErrInvalid    = errors.New("invalid audience package")
	ErrConflict   = errors.New("audience package version conflict")
	ErrActiveEdit = errors.New("active audience package must be paused before editing")
	ErrArchived   = errors.New("archived audience package is immutable")
)

type Lifecycle string

const (
	Paused   Lifecycle = "paused"
	Active   Lifecycle = "active"
	Archived Lifecycle = "archived"
)

type Group struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	SortOrder int       `json:"sort_order"`
	Version   int64     `json:"version"`
	CreatedBy int64     `json:"created_by"`
	UpdatedBy int64     `json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Package struct {
	ID                            int64      `json:"id"`
	GroupID                       *int64     `json:"group_id,omitempty"`
	Code                          string     `json:"code"`
	Name                          string     `json:"name"`
	Lifecycle                     Lifecycle  `json:"lifecycle"`
	Version                       int64      `json:"version"`
	CurrentConfigurationVersionID *int64     `json:"current_configuration_version_id,omitempty"`
	PublishedSnapshotID           *int64     `json:"published_snapshot_id,omitempty"`
	CreatedBy                     int64      `json:"created_by"`
	UpdatedBy                     int64      `json:"updated_by"`
	CreatedAt                     time.Time  `json:"created_at"`
	UpdatedAt                     time.Time  `json:"updated_at"`
	ArchivedAt                    *time.Time `json:"archived_at,omitempty"`
}

type ConfigurationVersion struct {
	ID             int64           `json:"id"`
	PackageID      int64           `json:"package_id"`
	Version        int64           `json:"version"`
	SchemaVersion  int             `json:"schema_version"`
	Definition     json.RawMessage `json:"definition"`
	RefreshCronUTC string          `json:"refresh_cron_utc,omitempty"`
	Digest         [32]byte        `json:"digest"`
	CreatedBy      int64           `json:"created_by"`
	CreatedAt      time.Time       `json:"created_at"`
}

var codePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,119}$`)

func NewGroup(name string, sortOrder int, actor int64, now time.Time) (Group, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 100 || sortOrder < 0 || actor < 1 || now.IsZero() {
		return Group{}, ErrInvalid
	}
	return Group{Name: name, SortOrder: sortOrder, Version: 1, CreatedBy: actor, UpdatedBy: actor, CreatedAt: now, UpdatedAt: now}, nil
}

func NewPackage(code, name string, groupID *int64, actor int64, now time.Time) (Package, error) {
	code, name = strings.TrimSpace(strings.ToLower(code)), strings.TrimSpace(name)
	if !codePattern.MatchString(code) || name == "" || len([]rune(name)) > 200 || actor < 1 || now.IsZero() || invalidOptionalID(groupID) {
		return Package{}, ErrInvalid
	}
	return Package{GroupID: cloneID(groupID), Code: code, Name: name, Lifecycle: Paused, Version: 1, CreatedBy: actor, UpdatedBy: actor, CreatedAt: now, UpdatedAt: now}, nil
}

func (p Package) Copy(code, name string, actor int64, now time.Time) (Package, error) {
	if p.Lifecycle == Archived {
		return Package{}, ErrArchived
	}
	return NewPackage(code, name, p.GroupID, actor, now)
}

func (p *Package) UpdateDetails(name string, groupID *int64, expectedVersion, actor int64, now time.Time) error {
	if p == nil || expectedVersion != p.Version {
		return ErrConflict
	}
	if p.Lifecycle == Archived {
		return ErrArchived
	}
	if p.Lifecycle == Active {
		return ErrActiveEdit
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 200 || invalidOptionalID(groupID) || actor < 1 || now.IsZero() {
		return ErrInvalid
	}
	p.Name, p.GroupID, p.UpdatedBy, p.UpdatedAt = name, cloneID(groupID), actor, now
	p.Version++
	return nil
}

func (p *Package) Transition(target Lifecycle, expectedVersion, actor int64, now time.Time) error {
	if p == nil || expectedVersion != p.Version {
		return ErrConflict
	}
	if p.Lifecycle == Archived {
		return ErrArchived
	}
	if actor < 1 || now.IsZero() || (target != Paused && target != Active && target != Archived) {
		return ErrInvalid
	}
	if p.Lifecycle == target {
		return nil
	}
	p.Lifecycle, p.UpdatedBy, p.UpdatedAt = target, actor, now
	p.Version++
	if target == Archived {
		archivedAt := now
		p.ArchivedAt = &archivedAt
	}
	return nil
}

func NewConfigurationVersion(packageID, version int64, definition json.RawMessage, refreshCronUTC string, actor int64, now time.Time) (ConfigurationVersion, error) {
	definition = append(json.RawMessage(nil), definition...)
	refreshCronUTC = strings.TrimSpace(refreshCronUTC)
	var object map[string]json.RawMessage
	if packageID < 1 || version < 1 || actor < 1 || now.IsZero() || json.Unmarshal(definition, &object) != nil || object == nil || len(refreshCronUTC) > 100 {
		return ConfigurationVersion{}, ErrInvalid
	}
	return ConfigurationVersion{PackageID: packageID, Version: version, SchemaVersion: 1, Definition: definition, RefreshCronUTC: refreshCronUTC, Digest: sha256.Sum256(definition), CreatedBy: actor, CreatedAt: now}, nil
}

func invalidOptionalID(id *int64) bool { return id != nil && *id < 1 }
func cloneID(id *int64) *int64 {
	if id == nil {
		return nil
	}
	copy := *id
	return &copy
}
