package channel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrAssetUnavailable = errors.New("channel acquisition asset unavailable")

type AcquisitionAsset struct {
	ID, ChannelID, ConfigVersion, AssetVersion                    int64
	Kind                                                          AcquisitionAssetKind
	SourceRefDigest, EffectRef, AcceptReceiptRef, QueueReceiptRef string
	State, ProviderAssetRef, ResultURL, ResultDigest              string
	CreatedAt, UpdatedAt                                          time.Time
}

type PublishedAssetSnapshot struct {
	Asset                                AcquisitionAsset
	ChannelCode, ChannelName, StateValue string
	SkipVerify                           bool
	StaffIDs                             []int64
}

type AssetStore interface {
	FindAssetByOperation(context.Context, int64, [32]byte) (AcquisitionAsset, [32]byte, error)
	NextAssetVersion(context.Context, int64, AcquisitionAssetKind) (int64, error)
	InsertAsset(context.Context, AcquisitionAsset, int64, [32]byte, [32]byte) (AcquisitionAsset, error)
	ListAssets(context.Context, int64, int, int64) ([]AcquisitionAsset, error)
	GetAsset(context.Context, int64, string) (AcquisitionAsset, error)
}

type PostgreSQLAssetStore struct{ pool *pgxpool.Pool }

func NewPostgreSQLAssetStore(pool *pgxpool.Pool) *PostgreSQLAssetStore {
	return &PostgreSQLAssetStore{pool: pool}
}
func (store *PostgreSQLAssetStore) FindAssetByOperation(ctx context.Context, actor int64, key [32]byte) (AcquisitionAsset, [32]byte, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return AcquisitionAsset{}, [32]byte{}, e
	}
	var a AcquisitionAsset
	var kind string
	var request []byte
	e = tx.QueryRow(ctx, `SELECT id,channel_id,config_version,asset_version,kind,source_ref_digest,effect_ref,accept_receipt_ref,queue_receipt_ref,state,provider_asset_ref,result_url,result_digest,created_at,updated_at,request_digest FROM channel_acquisition_assets WHERE created_by=$1 AND operation_key_digest=$2 FOR UPDATE`, actor, key[:]).Scan(&a.ID, &a.ChannelID, &a.ConfigVersion, &a.AssetVersion, &kind, &a.SourceRefDigest, &a.EffectRef, &a.AcceptReceiptRef, &a.QueueReceiptRef, &a.State, &a.ProviderAssetRef, &a.ResultURL, &a.ResultDigest, &a.CreatedAt, &a.UpdatedAt, &request)
	a.Kind = AcquisitionAssetKind(kind)
	var digest [32]byte
	if len(request) == 32 {
		copy(digest[:], request)
	}
	return a, digest, e
}
func (store *PostgreSQLAssetStore) NextAssetVersion(ctx context.Context, channelID int64, kind AcquisitionAssetKind) (int64, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return 0, e
	}
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('channel.asset:'||$1::text||':'||$2,0))`, channelID, kind); e != nil {
		return 0, e
	}
	var v int64
	e = tx.QueryRow(ctx, `SELECT COALESCE(max(asset_version),0)+1 FROM channel_acquisition_assets WHERE channel_id=$1 AND kind=$2`, channelID, kind).Scan(&v)
	return v, e
}
func (store *PostgreSQLAssetStore) InsertAsset(ctx context.Context, a AcquisitionAsset, actor int64, key, request [32]byte) (AcquisitionAsset, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return AcquisitionAsset{}, e
	}
	var kind string
	e = tx.QueryRow(ctx, `INSERT INTO channel_acquisition_assets(channel_id,config_version,asset_version,kind,source_ref_digest,operation_key_digest,request_digest,effect_ref,accept_receipt_ref,queue_receipt_ref,state,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id,kind,created_at,updated_at`, a.ChannelID, a.ConfigVersion, a.AssetVersion, a.Kind, a.SourceRefDigest, key[:], request[:], a.EffectRef, a.AcceptReceiptRef, a.QueueReceiptRef, a.State, actor).Scan(&a.ID, &kind, &a.CreatedAt, &a.UpdatedAt)
	a.Kind = AcquisitionAssetKind(kind)
	return a, e
}
func (store *PostgreSQLAssetStore) ListAssets(ctx context.Context, channelID int64, limit int, before int64) ([]AcquisitionAsset, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return nil, e
	}
	rows, e := tx.Query(ctx, `SELECT id,channel_id,config_version,asset_version,kind,source_ref_digest,effect_ref,accept_receipt_ref,queue_receipt_ref,state,provider_asset_ref,result_url,result_digest,created_at,updated_at FROM channel_acquisition_assets WHERE channel_id=$1 AND ($2::bigint=0 OR id<$2) ORDER BY id DESC LIMIT $3`, channelID, before, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := make([]AcquisitionAsset, 0, limit)
	for rows.Next() {
		var a AcquisitionAsset
		var kind string
		if e = rows.Scan(&a.ID, &a.ChannelID, &a.ConfigVersion, &a.AssetVersion, &kind, &a.SourceRefDigest, &a.EffectRef, &a.AcceptReceiptRef, &a.QueueReceiptRef, &a.State, &a.ProviderAssetRef, &a.ResultURL, &a.ResultDigest, &a.CreatedAt, &a.UpdatedAt); e != nil {
			return nil, e
		}
		a.Kind = AcquisitionAssetKind(kind)
		result = append(result, a)
	}
	return result, rows.Err()
}
func (store *PostgreSQLAssetStore) GetAsset(ctx context.Context, channelID int64, effectRef string) (AcquisitionAsset, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return AcquisitionAsset{}, e
	}
	var a AcquisitionAsset
	var kind string
	e = tx.QueryRow(ctx, `SELECT id,channel_id,config_version,asset_version,kind,source_ref_digest,effect_ref,accept_receipt_ref,queue_receipt_ref,state,provider_asset_ref,result_url,result_digest,created_at,updated_at FROM channel_acquisition_assets WHERE channel_id=$1 AND effect_ref=$2`, channelID, effectRef).Scan(&a.ID, &a.ChannelID, &a.ConfigVersion, &a.AssetVersion, &kind, &a.SourceRefDigest, &a.EffectRef, &a.AcceptReceiptRef, &a.QueueReceiptRef, &a.State, &a.ProviderAssetRef, &a.ResultURL, &a.ResultDigest, &a.CreatedAt, &a.UpdatedAt)
	a.Kind = AcquisitionAssetKind(kind)
	if errors.Is(e, pgx.ErrNoRows) {
		return AcquisitionAsset{}, ErrCatalogNotFound
	}
	return a, e
}

// ReadPublishedAsset is called by composition inside a short read UoW before
// Outbound performs any provider network call.
func (store *PostgreSQLAssetStore) ReadPublishedAsset(ctx context.Context, source string) (PublishedAssetSnapshot, error) {
	return store.readPublishedAsset(ctx, "a.source_ref_digest=$1", source)
}

func (store *PostgreSQLAssetStore) ReadPublishedAssetByEffect(ctx context.Context, effectRef string) (PublishedAssetSnapshot, error) {
	return store.readPublishedAsset(ctx, "a.effect_ref=$1", effectRef)
}

func (store *PostgreSQLAssetStore) readPublishedAsset(ctx context.Context, predicate, value string) (PublishedAssetSnapshot, error) {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return PublishedAssetSnapshot{}, e
	}
	var s PublishedAssetSnapshot
	var kind string
	e = tx.QueryRow(ctx, `SELECT a.id,a.channel_id,a.config_version,a.asset_version,a.kind,a.source_ref_digest,a.effect_ref,a.accept_receipt_ref,a.queue_receipt_ref,a.state,a.provider_asset_ref,a.result_url,a.result_digest,a.created_at,a.updated_at,c.code,v.name,v.scene_value,v.auto_accept_friend FROM channel_acquisition_assets a JOIN channels c ON c.id=a.channel_id JOIN channel_config_versions v ON v.channel_id=a.channel_id AND v.config_version=a.config_version WHERE `+predicate, value).Scan(&s.Asset.ID, &s.Asset.ChannelID, &s.Asset.ConfigVersion, &s.Asset.AssetVersion, &kind, &s.Asset.SourceRefDigest, &s.Asset.EffectRef, &s.Asset.AcceptReceiptRef, &s.Asset.QueueReceiptRef, &s.Asset.State, &s.Asset.ProviderAssetRef, &s.Asset.ResultURL, &s.Asset.ResultDigest, &s.Asset.CreatedAt, &s.Asset.UpdatedAt, &s.ChannelCode, &s.ChannelName, &s.StateValue, &s.SkipVerify)
	if e != nil {
		return PublishedAssetSnapshot{}, e
	}
	s.Asset.Kind = AcquisitionAssetKind(kind)
	rows, e := tx.Query(ctx, `SELECT staff_id FROM channel_assignees WHERE channel_id=$1 AND config_version=$2 ORDER BY priority`, s.Asset.ChannelID, s.Asset.ConfigVersion)
	if e != nil {
		return PublishedAssetSnapshot{}, e
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			return PublishedAssetSnapshot{}, e
		}
		s.StaffIDs = append(s.StaffIDs, id)
	}
	return s, rows.Err()
}

func (store *PostgreSQLAssetStore) CompletePublishedAsset(ctx context.Context, c channelport.AssetCompletion) error {
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return e
	}
	result, e := tx.Exec(ctx, `UPDATE channel_acquisition_assets SET state=$2,provider_asset_ref=$3,result_url=$4,result_digest=$5,updated_at=$6 WHERE effect_ref=$1 AND state IN ('queued','attempted','outcome_unknown')`, c.EffectRef, c.State, c.ProviderAssetRef, c.ResultURL, c.ResultDigest, c.CompletedAt.UTC())
	if e != nil {
		return e
	}
	if result.RowsAffected() != 1 {
		return ErrCatalogConflict
	}
	return nil
}

type AssetEffectGateway interface {
	effectport.TransactionalAccepter
	effectport.Reader
}

type AssetService struct {
	uow     platformport.UnitOfWork
	catalog channelport.CatalogStore
	store   AssetStore
	effects AssetEffectGateway
	events  channelport.CatalogEventAppender
	now     func() time.Time
}

func NewAssetService(uow platformport.UnitOfWork, catalog channelport.CatalogStore, store AssetStore, effects AssetEffectGateway, events channelport.CatalogEventAppender) *AssetService {
	return &AssetService{uow: uow, catalog: catalog, store: store, effects: effects, events: events, now: time.Now}
}
func (service *AssetService) Publish(ctx context.Context, channelID, actor int64, kind AcquisitionAssetKind, idempotency string) (AcquisitionAsset, error) {
	if service == nil || service.uow == nil || service.catalog == nil || service.store == nil || service.effects == nil || service.events == nil || channelID < 1 || actor < 1 || !validOperationKey(idempotency) || (kind != AcquisitionAssetQRCode && kind != AcquisitionAssetLink) {
		return AcquisitionAsset{}, ErrInvalidCatalogCommand
	}
	key := sha256.Sum256([]byte(idempotency))
	request := sha256.Sum256([]byte(strconv.FormatInt(channelID, 10) + "\x00" + string(kind)))
	var result AcquisitionAsset
	err := service.uow.Within(ctx, func(tx context.Context) error {
		existing, digest, findErr := service.store.FindAssetByOperation(tx, actor, key)
		if findErr == nil {
			if digest != request {
				return ErrCatalogConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(findErr, pgx.ErrNoRows) {
			return findErr
		}
		channel, getErr := service.catalog.Get(tx, channelID)
		if getErr != nil {
			return getErr
		}
		if !channel.CanPublish() {
			return ErrCatalogConflict
		}
		if kind == AcquisitionAssetQRCode && channel.Config.Carrier != "qrcode" || kind == AcquisitionAssetLink && channel.Config.Carrier != "link" {
			return ErrInvalidCatalogCommand
		}
		version, nextErr := service.store.NextAssetVersion(tx, channelID, kind)
		if nextErr != nil {
			return nextErr
		}
		source := effectport.Hash("channel.asset.source.v1", strconv.FormatInt(channelID, 10), strconv.FormatInt(channel.ConfigVersion, 10), string(kind), strconv.FormatInt(version, 10))
		envelope := effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindChannelAsset, SourceRefDigest: source, TargetRefDigest: effectport.Hash("channel.asset.target.v1", string(kind)), PayloadDigest: effectport.Hash("channel.asset.payload.v1", strconv.FormatInt(channel.ConfigVersion, 10), channel.Code, channel.Config.Name, channel.Config.SceneValue), PolicyVersionHash: effectport.Hash("channel.asset.policy", "v1")}
		projection, receipt, acceptErr := service.effects.AcceptAndQueueWithin(tx, effectport.AcceptCommand{ReceiptKey: effectport.Hash("channel.asset.accept.v1", strconv.FormatInt(actor, 10), idempotency), Envelope: envelope})
		if acceptErr != nil {
			return acceptErr
		}
		result = AcquisitionAsset{ChannelID: channelID, ConfigVersion: channel.ConfigVersion, AssetVersion: version, Kind: kind, SourceRefDigest: string(source), EffectRef: projection.ID, AcceptReceiptRef: receipt.ID, QueueReceiptRef: receipt.QueueReceiptID, State: string(projection.State)}
		result, acceptErr = service.store.InsertAsset(tx, result, actor, key, request)
		if acceptErr != nil {
			return acceptErr
		}
		payload, _ := json.Marshal(map[string]any{"channel_id": channelID, "asset_id": result.ID, "effect_ref": result.EffectRef, "kind": kind, "asset_version": version})
		return service.events.Append(tx, channelport.CatalogEvent{Type: "channel.asset.accepted", ChannelID: channelID, Version: channel.Version, ActorID: actor, OccurredAt: service.now().UTC(), IdempotencyKey: "channel:asset:" + hex.EncodeToString(key[:]), Payload: payload})
	})
	if err != nil {
		return AcquisitionAsset{}, mapCatalogError(err)
	}
	return result, nil
}
func (service *AssetService) List(ctx context.Context, channelID int64, limit int, before int64) ([]AcquisitionAsset, error) {
	if service == nil || service.uow == nil || service.store == nil || limit < 1 || limit > 51 {
		return nil, ErrInvalidCatalogCommand
	}
	var items []AcquisitionAsset
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var e error
		items, e = service.store.ListAssets(tx, channelID, limit, before)
		return e
	})
	if err != nil {
		return nil, err
	}
	for index := range items {
		projection, readErr := service.effects.Get(ctx, items[index].EffectRef)
		if readErr != nil {
			return nil, readErr
		}
		items[index].State = assetProjectionState(projection.State)
		items[index].UpdatedAt = projection.UpdatedAt
	}
	return items, nil
}
func (service *AssetService) Get(ctx context.Context, channelID int64, effectRef string) (AcquisitionAsset, error) {
	var a AcquisitionAsset
	err := service.uow.Within(ctx, func(tx context.Context) error {
		var e error
		a, e = service.store.GetAsset(tx, channelID, effectRef)
		return e
	})
	if err != nil {
		return AcquisitionAsset{}, err
	}
	projection, readErr := service.effects.Get(ctx, a.EffectRef)
	if readErr != nil {
		return AcquisitionAsset{}, readErr
	}
	a.State = assetProjectionState(projection.State)
	a.UpdatedAt = projection.UpdatedAt
	return a, nil
}

func assetProjectionState(state effectport.State) string {
	if state == effectport.StateRetryable || state == effectport.StateCancelled {
		return "final_failed"
	}
	return string(state)
}

var _ channelport.AssetCompletionWriter = (*PostgreSQLAssetStore)(nil)
