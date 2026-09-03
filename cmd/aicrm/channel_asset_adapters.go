package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	channelstore "github.com/qianlan33333-png/AI-CRM-v3/internal/channel"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

type channelPublishedAssetStore interface {
	ReadPublishedAsset(context.Context, string) (channelstore.PublishedAssetSnapshot, error)
}
type channelPublishedStaffReader interface {
	UserByID(context.Context, int64, bool) (accessdomain.User, error)
}
type channelPublishedConfigAdapter struct {
	uow    platformport.UnitOfWork
	assets channelPublishedAssetStore
	users  channelPublishedStaffReader
}

func (adapter channelPublishedConfigAdapter) ReadPublishedConfig(ctx context.Context, source string) (channelport.PublishedConfig, error) {
	if adapter.uow == nil || adapter.assets == nil || adapter.users == nil {
		return channelport.PublishedConfig{}, errors.New("published channel config unavailable")
	}
	var snapshot channelstore.PublishedAssetSnapshot
	staff := []string{}
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		snapshot, readErr = adapter.assets.ReadPublishedAsset(tx, source)
		if readErr != nil {
			return readErr
		}
		for _, id := range snapshot.StaffIDs {
			user, userErr := adapter.users.UserByID(tx, id, false)
			if userErr != nil || !user.Active || user.WeComUserID == "" {
				return errors.New("published channel staff unavailable")
			}
			staff = append(staff, user.WeComUserID)
		}
		return nil
	})
	if err != nil {
		return channelport.PublishedConfig{}, err
	}
	return channelport.PublishedConfig{AssetID: snapshot.Asset.ID, ChannelID: snapshot.Asset.ChannelID, ConfigVersion: snapshot.Asset.ConfigVersion, AssetVersion: snapshot.Asset.AssetVersion, Kind: string(snapshot.Asset.Kind), ChannelCode: snapshot.ChannelCode, ChannelName: snapshot.ChannelName, StateValue: snapshot.StateValue, SkipVerify: snapshot.SkipVerify, StaffProviderRefs: staff}, nil
}

type channelAssetWriter interface {
	channelport.AssetCompletionWriter
	ReadPublishedAssetByEffect(context.Context, string) (channelstore.PublishedAssetSnapshot, error)
}
type channelStateBindingWriter interface {
	PutBinding(context.Context, channelstore.StateBinding) (channelstore.StateBinding, bool, error)
}
type channelAssetCompletionAdapter struct {
	assets   channelAssetWriter
	bindings channelStateBindingWriter
	digester wecom.StateDigester
	corpID   string
}

func (adapter channelAssetCompletionAdapter) CompletePublishedAsset(ctx context.Context, completion channelport.AssetCompletion) error {
	if adapter.assets == nil {
		return errors.New("channel asset completion unavailable")
	}
	if completion.State == "retryable_failed" {
		completion.State = "final_failed"
	}
	if completion.State == "executed" {
		if adapter.bindings == nil || adapter.digester == nil || adapter.corpID == "" {
			return errors.New("channel state binding unavailable")
		}
		var snapshot channelstore.PublishedAssetSnapshot
		// Source lookup is scoped by the effect reference without exposing any
		// provider identifier to EER.
		var err error
		// The artifact completion and State binding share EER's current tx.
		snapshot, err = adapter.assets.ReadPublishedAssetByEffect(ctx, completion.EffectRef)
		if err != nil {
			return err
		}
		state := snapshot.StateValue
		if state == "" {
			state = "ca-" + strings.TrimPrefix(snapshot.Asset.SourceRefDigest, "sha256:")[:48]
		}
		digest, err := adapter.digester.DigestState(adapter.corpID, state)
		if err != nil {
			return err
		}
		bindingDigest := sha256.Sum256([]byte(snapshot.Asset.SourceRefDigest + "\x00" + completion.EffectRef))
		_, _, err = adapter.bindings.PutBinding(ctx, channelstore.StateBinding{CorpID: adapter.corpID, DigestKeyVersion: 1, StateDigest: digest, ChannelID: snapshot.Asset.ChannelID, AssetKind: snapshot.Asset.Kind, AssetVersion: snapshot.Asset.AssetVersion, BindingDigest: bindingDigest, ActiveFrom: completion.CompletedAt})
		if err != nil {
			return err
		}
	}
	return adapter.assets.CompletePublishedAsset(ctx, completion)
}
