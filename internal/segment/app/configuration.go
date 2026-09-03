package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	segmentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/domain"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

var (
	ErrInvalid     = errors.New("invalid audience configuration request")
	ErrNotFound    = errors.New("audience configuration not found")
	ErrConflict    = errors.New("audience configuration conflict")
	ErrNotReady    = errors.New("audience execution capability is not ready")
	ErrUnavailable = errors.New("audience configuration unavailable")
)

type Store interface {
	ListGroups(context.Context) ([]segmentdomain.Group, error)
	CreateGroup(context.Context, segmentdomain.Group) (segmentdomain.Group, error)
	LockGroup(context.Context, int64) (segmentdomain.Group, error)
	UpdateGroup(context.Context, segmentdomain.Group, int64) (segmentdomain.Group, error)
	DeleteEmptyGroup(context.Context, int64, int64) error
	ListPackages(context.Context, int, int, bool) ([]segmentdomain.Package, error)
	CountPackages(context.Context, bool) (int64, error)
	GetPackage(context.Context, int64) (segmentdomain.Package, error)
	LockPackage(context.Context, int64) (segmentdomain.Package, error)
	CreatePackage(context.Context, segmentdomain.Package) (segmentdomain.Package, error)
	UpdatePackage(context.Context, segmentdomain.Package, int64) (segmentdomain.Package, error)
	NextCopyCode(context.Context, string) (string, error)
	CurrentConfiguration(context.Context, int64) (segmentdomain.ConfigurationVersion, error)
	NextConfigurationVersion(context.Context, int64) (int64, error)
	CreateConfigurationVersion(context.Context, segmentdomain.ConfigurationVersion) (segmentdomain.ConfigurationVersion, error)
	SetCurrentConfiguration(context.Context, int64, int64, int64, int64, time.Time) (segmentdomain.Package, error)
	Reserve(context.Context, segmentstore.Reservation) (segmentstore.Receipt, bool, error)
	Complete(context.Context, int64, json.RawMessage, time.Time) (segmentstore.Receipt, error)
	AppendMutationFacts(context.Context, segmentstore.MutationFact) (int64, error)
}

type Service struct {
	uow             platformport.UnitOfWork
	store           Store
	now             func() time.Time
	allowActivation bool
}

type GroupCommand struct {
	ID, ExpectedVersion int64
	Name                string
	SortOrder           int
	Actor               int64
	IdempotencyKey      string
}

type PackageCreateCommand struct {
	Name, TemplateKey string
	GroupID           *int64
	Actor             int64
	IdempotencyKey    string
}

type PackageUpdateCommand struct {
	ID, ExpectedVersion int64
	Name                string
	GroupID             *int64
	Actor               int64
	IdempotencyKey      string
}

type VersionCommand struct {
	ID, ExpectedVersion int64
	Actor               int64
	IdempotencyKey      string
}

type ConfigurationCommand struct {
	PackageID, ExpectedPackageVersion int64
	Definition                        json.RawMessage
	RefreshCronUTC                    string
	Actor                             int64
	IdempotencyKey                    string
}

type PackagePage struct {
	Items  []segmentdomain.Package `json:"items"`
	Total  int64                   `json:"total"`
	Limit  int                     `json:"limit"`
	Offset int                     `json:"offset"`
}

func NewService(uow platformport.UnitOfWork, store Store) *Service {
	return &Service{uow: uow, store: store, now: time.Now}
}

func (s *Service) ListGroups(ctx context.Context) ([]segmentdomain.Group, error) {
	if !s.ready() {
		return nil, ErrUnavailable
	}
	var result []segmentdomain.Group
	err := s.uow.Within(ctx, func(tx context.Context) error { var err error; result, err = s.store.ListGroups(tx); return err })
	return result, classify(err)
}

func (s *Service) CreateGroup(ctx context.Context, command GroupCommand) (segmentdomain.Group, error) {
	now := s.now().UTC()
	group, err := segmentdomain.NewGroup(command.Name, command.SortOrder, command.Actor, now)
	if err != nil {
		return segmentdomain.Group{}, ErrInvalid
	}
	payload := mutationPayload("create_group", command)
	result, err := s.mutate(ctx, "create_group", command.Actor, command.IdempotencyKey, payload, func(tx context.Context) (any, segmentstore.MutationFact, error) {
		created, createErr := s.store.CreateGroup(tx, group)
		return created, fact("group", created.ID, "create", "audience.group.created.v1", command.Actor, command.IdempotencyKey, now), createErr
	})
	var created segmentdomain.Group
	if err == nil {
		err = json.Unmarshal(result, &created)
	}
	return created, classify(err)
}

func (s *Service) UpdateGroup(ctx context.Context, command GroupCommand) (segmentdomain.Group, error) {
	if command.ID < 1 || command.ExpectedVersion < 1 {
		return segmentdomain.Group{}, ErrInvalid
	}
	now := s.now().UTC()
	result, err := s.mutate(ctx, "update_group", command.Actor, command.IdempotencyKey, mutationPayload("update_group", command), func(tx context.Context) (any, segmentstore.MutationFact, error) {
		group, updateErr := s.store.LockGroup(tx, command.ID)
		if updateErr == nil {
			updateErr = group.Update(command.Name, command.SortOrder, command.ExpectedVersion, command.Actor, now)
		}
		if updateErr == nil {
			group, updateErr = s.store.UpdateGroup(tx, group, command.ExpectedVersion)
		}
		return group, fact("group", command.ID, "update", "audience.group.updated.v1", command.Actor, command.IdempotencyKey, now), updateErr
	})
	var updated segmentdomain.Group
	if err == nil {
		err = json.Unmarshal(result, &updated)
	}
	return updated, classify(err)
}

func (s *Service) DeleteGroup(ctx context.Context, command VersionCommand) error {
	_, err := s.mutate(ctx, "delete_group", command.Actor, command.IdempotencyKey, mutationPayload("delete_group", command), func(tx context.Context) (any, segmentstore.MutationFact, error) {
		deleteErr := s.store.DeleteEmptyGroup(tx, command.ID, command.ExpectedVersion)
		return map[string]any{"id": command.ID, "deleted": deleteErr == nil}, fact("group", command.ID, "delete", "audience.group.deleted.v1", command.Actor, command.IdempotencyKey, s.now().UTC()), deleteErr
	})
	return classify(err)
}

func (s *Service) ListPackages(ctx context.Context, limit, offset int, includeArchived bool) (PackagePage, error) {
	if !s.ready() || limit < 1 || limit > 100 || offset < 0 || offset > 1_000_000 {
		return PackagePage{}, ErrInvalid
	}
	page := PackagePage{Limit: limit, Offset: offset}
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		page.Items, err = s.store.ListPackages(tx, limit, offset, includeArchived)
		if err == nil {
			page.Total, err = s.store.CountPackages(tx, includeArchived)
		}
		return err
	})
	return page, classify(err)
}

func (s *Service) GetPackage(ctx context.Context, id int64) (segmentdomain.Package, error) {
	if !s.ready() || id < 1 {
		return segmentdomain.Package{}, ErrNotFound
	}
	var result segmentdomain.Package
	err := s.uow.Within(ctx, func(tx context.Context) error { var err error; result, err = s.store.GetPackage(tx, id); return err })
	return result, classify(err)
}

func (s *Service) CreatePackage(ctx context.Context, command PackageCreateCommand) (segmentdomain.Package, error) {
	definition, err := DefaultDefinition(command.TemplateKey)
	if err != nil {
		return segmentdomain.Package{}, ErrInvalid
	}
	now := s.now().UTC()
	keyDigest := sha256.Sum256([]byte(command.IdempotencyKey))
	code := "audience-" + hex.EncodeToString(keyDigest[:8])
	result, err := s.mutate(ctx, "create_package", command.Actor, command.IdempotencyKey, mutationPayload("create_package", command), func(tx context.Context) (any, segmentstore.MutationFact, error) {
		item, createErr := segmentdomain.NewPackage(code, command.Name, command.GroupID, command.Actor, now)
		if createErr == nil {
			item, createErr = s.store.CreatePackage(tx, item)
		}
		if createErr == nil {
			configuration, configErr := segmentdomain.NewConfigurationVersion(item.ID, 1, definition, "", command.Actor, now)
			if configErr == nil {
				configuration, configErr = s.store.CreateConfigurationVersion(tx, configuration)
			}
			if configErr == nil {
				item, configErr = s.store.SetCurrentConfiguration(tx, item.ID, configuration.ID, item.Version, command.Actor, now)
			}
			if configErr == nil {
				_, configErr = s.store.AppendMutationFacts(tx, fact("configuration", configuration.ID, "create", "audience.configuration.created.v1", command.Actor, "configuration:"+command.IdempotencyKey, now))
			}
			createErr = configErr
		}
		return item, fact("package", item.ID, "create", "audience.package.created.v1", command.Actor, command.IdempotencyKey, now), createErr
	})
	var created segmentdomain.Package
	if err == nil {
		err = json.Unmarshal(result, &created)
	}
	return created, classify(err)
}

func (s *Service) UpdatePackage(ctx context.Context, command PackageUpdateCommand) (segmentdomain.Package, error) {
	now := s.now().UTC()
	result, err := s.mutate(ctx, "update_package", command.Actor, command.IdempotencyKey, mutationPayload("update_package", command), func(tx context.Context) (any, segmentstore.MutationFact, error) {
		item, updateErr := s.store.LockPackage(tx, command.ID)
		name := command.Name
		if name == "" {
			name = item.Name
		}
		if updateErr == nil {
			updateErr = item.UpdateDetails(name, command.GroupID, command.ExpectedVersion, command.Actor, now)
		}
		if updateErr == nil {
			item, updateErr = s.store.UpdatePackage(tx, item, command.ExpectedVersion)
		}
		return item, fact("package", command.ID, "update", "audience.package.updated.v1", command.Actor, command.IdempotencyKey, now), updateErr
	})
	var updated segmentdomain.Package
	if err == nil {
		err = json.Unmarshal(result, &updated)
	}
	return updated, classify(err)
}

func (s *Service) CopyPackage(ctx context.Context, command VersionCommand) (segmentdomain.Package, error) {
	now := s.now().UTC()
	result, err := s.mutate(ctx, "copy_package", command.Actor, command.IdempotencyKey, mutationPayload("copy_package", command), func(tx context.Context) (any, segmentstore.MutationFact, error) {
		source, copyErr := s.store.LockPackage(tx, command.ID)
		var copied segmentdomain.Package
		if copyErr == nil {
			code, codeErr := s.store.NextCopyCode(tx, source.Code)
			if codeErr == nil {
				copied, codeErr = source.Copy(code, source.Name+" 副本", command.Actor, now)
			}
			if codeErr == nil {
				copied, codeErr = s.store.CreatePackage(tx, copied)
			}
			if codeErr == nil {
				sourceConfiguration, configErr := s.store.CurrentConfiguration(tx, source.ID)
				if configErr == nil {
					configuration, createErr := segmentdomain.NewConfigurationVersion(copied.ID, 1, sourceConfiguration.Definition, sourceConfiguration.RefreshCronUTC, command.Actor, now)
					if createErr == nil {
						configuration, createErr = s.store.CreateConfigurationVersion(tx, configuration)
					}
					if createErr == nil {
						copied, createErr = s.store.SetCurrentConfiguration(tx, copied.ID, configuration.ID, copied.Version, command.Actor, now)
					}
					configErr = createErr
				}
				codeErr = configErr
			}
			copyErr = codeErr
		}
		return copied, fact("package", copied.ID, "copy", "audience.package.copied.v1", command.Actor, command.IdempotencyKey, now), copyErr
	})
	var copied segmentdomain.Package
	if err == nil {
		err = json.Unmarshal(result, &copied)
	}
	return copied, classify(err)
}

func (s *Service) TransitionPackage(ctx context.Context, command VersionCommand, target segmentdomain.Lifecycle) (segmentdomain.Package, error) {
	if target == segmentdomain.Active && !s.allowActivation {
		return segmentdomain.Package{}, ErrNotReady
	}
	now := s.now().UTC()
	result, err := s.mutate(ctx, string(target)+"_package", command.Actor, command.IdempotencyKey, mutationPayload(string(target)+"_package", command), func(tx context.Context) (any, segmentstore.MutationFact, error) {
		item, transitionErr := s.store.LockPackage(tx, command.ID)
		if transitionErr == nil {
			transitionErr = item.Transition(target, command.ExpectedVersion, command.Actor, now)
		}
		if transitionErr == nil {
			item, transitionErr = s.store.UpdatePackage(tx, item, command.ExpectedVersion)
		}
		return item, fact("package", command.ID, string(target), "audience.package."+string(target)+".v1", command.Actor, command.IdempotencyKey, now), transitionErr
	})
	var item segmentdomain.Package
	if err == nil {
		err = json.Unmarshal(result, &item)
	}
	return item, classify(err)
}

func (s *Service) PutConfiguration(ctx context.Context, command ConfigurationCommand) (segmentdomain.ConfigurationVersion, error) {
	canonical, err := CanonicalDefinition(command.Definition)
	if err != nil {
		return segmentdomain.ConfigurationVersion{}, err
	}
	if err = ValidateRefreshCronUTC(command.RefreshCronUTC); err != nil {
		return segmentdomain.ConfigurationVersion{}, err
	}
	now := s.now().UTC()
	result, err := s.mutate(ctx, "put_configuration", command.Actor, command.IdempotencyKey, mutationPayload("put_configuration", command), func(tx context.Context) (any, segmentstore.MutationFact, error) {
		item, putErr := s.store.LockPackage(tx, command.PackageID)
		if putErr == nil && item.Lifecycle != segmentdomain.Paused {
			putErr = segmentdomain.ErrActiveEdit
		}
		if putErr == nil && item.Version != command.ExpectedPackageVersion {
			putErr = segmentdomain.ErrConflict
		}
		var configuration segmentdomain.ConfigurationVersion
		if putErr == nil {
			version, nextErr := s.store.NextConfigurationVersion(tx, item.ID)
			if nextErr == nil {
				configuration, nextErr = segmentdomain.NewConfigurationVersion(item.ID, version, canonical, command.RefreshCronUTC, command.Actor, now)
			}
			if nextErr == nil {
				configuration, nextErr = s.store.CreateConfigurationVersion(tx, configuration)
			}
			if nextErr == nil {
				_, nextErr = s.store.SetCurrentConfiguration(tx, item.ID, configuration.ID, item.Version, command.Actor, now)
			}
			putErr = nextErr
		}
		return configuration, fact("configuration", configuration.ID, "put", "audience.configuration.created.v1", command.Actor, command.IdempotencyKey, now), putErr
	})
	var configuration segmentdomain.ConfigurationVersion
	if err == nil {
		err = json.Unmarshal(result, &configuration)
	}
	return configuration, classify(err)
}

func (s *Service) CurrentConfiguration(ctx context.Context, packageID int64) (segmentdomain.ConfigurationVersion, error) {
	if !s.ready() || packageID < 1 {
		return segmentdomain.ConfigurationVersion{}, ErrNotFound
	}
	var result segmentdomain.ConfigurationVersion
	err := s.uow.Within(ctx, func(tx context.Context) error {
		var err error
		result, err = s.store.CurrentConfiguration(tx, packageID)
		return err
	})
	return result, classify(err)
}

func (s *Service) mutate(ctx context.Context, operation string, actor int64, key string, payload json.RawMessage, apply func(context.Context) (any, segmentstore.MutationFact, error)) (json.RawMessage, error) {
	if !s.ready() || actor < 1 || len(key) < 16 || len(key) > 128 || strings.TrimSpace(key) != key || apply == nil {
		return nil, ErrInvalid
	}
	now := s.now().UTC()
	reservation := segmentstore.Reservation{Operation: operation, ActorScope: fmt.Sprintf("admin:%d", actor), KeyDigest: sha256.Sum256([]byte(key)), PayloadDigest: sha256.Sum256(payload), CreatedAt: now}
	var result json.RawMessage
	err := s.uow.Within(ctx, func(tx context.Context) error {
		receipt, owned, err := s.store.Reserve(tx, reservation)
		if err != nil {
			return err
		}
		if !owned {
			if receipt.State != "completed" || len(receipt.ResultSnapshot) == 0 {
				return ErrConflict
			}
			result = append(result[:0], receipt.ResultSnapshot...)
			return nil
		}
		value, event, err := apply(tx)
		if err != nil {
			return err
		}
		result, err = json.Marshal(value)
		if err != nil {
			return err
		}
		if _, err = s.store.AppendMutationFacts(tx, event); err != nil {
			return err
		}
		_, err = s.store.Complete(tx, receipt.ID, result, now)
		return err
	})
	return result, err
}

func (s *Service) ready() bool { return s != nil && s.uow != nil && s.store != nil && s.now != nil }
func mutationPayload(operation string, command any) json.RawMessage {
	raw, _ := json.Marshal(struct {
		Operation string `json:"operation"`
		Command   any    `json:"command"`
	}{operation, command})
	return raw
}
func fact(kind string, id int64, operation, event string, actor int64, key string, now time.Time) segmentstore.MutationFact {
	payload, _ := json.Marshal(map[string]any{"resource_id": id, "resource_kind": kind})
	return segmentstore.MutationFact{ResourceKind: kind, ResourceID: id, Operation: operation, EventType: event, ActorID: actor, Payload: payload, IdempotencyKey: operation + ":" + key, OccurredAt: now}
}
func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrNotFound), errors.Is(err, ErrConflict), errors.Is(err, ErrNotReady), errors.Is(err, ErrUnsupportedDefinition):
		return err
	case errors.Is(err, segmentdomain.ErrInvalid), errors.Is(err, segmentstore.ErrInvalid):
		return ErrInvalid
	case errors.Is(err, segmentdomain.ErrConflict), errors.Is(err, segmentstore.ErrConflict), errors.Is(err, segmentdomain.ErrActiveEdit), errors.Is(err, segmentdomain.ErrArchived):
		return ErrConflict
	case errors.Is(err, segmentstore.ErrNotFound):
		return ErrNotFound
	default:
		return ErrUnavailable
	}
}
