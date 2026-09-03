package channel

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

var ErrEntrantActionUnavailable = errors.New("channel entrant action unavailable")

type EntrantActionStore struct {
	effects   effectport.TransactionalAccepter
	materials channelport.WelcomeMaterialSnapshotResolver
}

func NewEntrantActionStore(effects effectport.TransactionalAccepter, materials channelport.WelcomeMaterialSnapshotResolver) *EntrantActionStore {
	return &EntrantActionStore{effects: effects, materials: materials}
}

type entrantActionConfig struct {
	ChannelID, ConfigVersion, EntryTagID int64
	WelcomeMessage, Strategy             string
	WelcomeMaterials                     channelport.WelcomeMaterialPlan
	Assignees                            []channeldomain.Assignee
}

func (store *EntrantActionStore) AcceptEntrantActions(ctx context.Context, command channelport.EntrantActionCommand) error {
	if store == nil || store.effects == nil || command.CallbackID == "" || command.CustomerID < 1 || command.OccurredAt.IsZero() || command.Resolution.Status != channeldomain.StateAttributed || !command.Resolution.Valid() {
		return ErrEntrantActionUnavailable
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	config, err := readEntrantActionConfig(ctx, tx, command.Resolution)
	if err != nil {
		return err
	}
	staffID, err := chooseEntrantStaff(ctx, tx, config, command.CallbackID, command.OccurredAt)
	if err != nil {
		return err
	}
	assignmentDigest := sha256.Sum256([]byte(command.CallbackID + "\x00" + strconv.FormatInt(config.ChannelID, 10) + "\x00" + strconv.FormatInt(config.ConfigVersion, 10) + "\x00" + strconv.FormatInt(int64(command.CustomerID), 10) + "\x00" + strconv.FormatInt(staffID, 10) + "\x00" + config.Strategy))
	var assignmentID int64
	err = tx.QueryRow(ctx, `INSERT INTO channel_entrant_assignments(callback_id,channel_id,config_version,customer_id,staff_id,strategy,assignment_digest,assigned_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(callback_id) DO NOTHING RETURNING id`, command.CallbackID, config.ChannelID, config.ConfigVersion, command.CustomerID, staffID, config.Strategy, assignmentDigest[:], command.OccurredAt.UTC()).Scan(&assignmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existing []byte
		err = tx.QueryRow(ctx, `SELECT id,assignment_digest FROM channel_entrant_assignments WHERE callback_id=$1`, command.CallbackID).Scan(&assignmentID, &existing)
		if err != nil || len(existing) != sha256.Size || string(existing) != string(assignmentDigest[:]) {
			return ErrEntrantActionUnavailable
		}
	} else if err != nil {
		return err
	}
	materialConfigured := len(config.WelcomeMaterials.ImageIDs)+len(config.WelcomeMaterials.MiniProgramIDs)+len(config.WelcomeMaterials.AttachmentIDs)+len(config.WelcomeMaterials.GroupInviteIDs) > 0
	if (config.WelcomeMessage != "" || materialConfigured) && command.WelcomeGrantRef != "" {
		var materialSnapshot json.RawMessage
		materialDigest := ""
		if materialConfigured {
			if store.materials == nil {
				return ErrEntrantActionUnavailable
			}
			materialSnapshot, materialDigest, err = store.materials.ResolveWelcomeMaterialSnapshot(ctx, config.WelcomeMaterials, command.OccurredAt.UTC().Add(10*time.Minute))
			if err != nil || len(materialSnapshot) == 0 || !effectport.ValidDigest(effectport.Digest(materialDigest)) {
				return ErrEntrantActionUnavailable
			}
		}
		if err = store.acceptAction(ctx, tx, assignmentID, command, config, staffID, "welcome", command.WelcomeGrantRef, 0, materialSnapshot, materialDigest); err != nil {
			return err
		}
	}
	if config.EntryTagID > 0 {
		if err = store.acceptAction(ctx, tx, assignmentID, command, config, staffID, "entry_tag", "", config.EntryTagID, nil, ""); err != nil {
			return err
		}
	}
	return nil
}

func readEntrantActionConfig(ctx context.Context, tx pgx.Tx, resolution channeldomain.StateResolution) (entrantActionConfig, error) {
	providerKind := string(AcquisitionAssetQRCode)
	if resolution.Asset.Kind == "link" {
		providerKind = string(AcquisitionAssetLink)
	}
	var config entrantActionConfig
	var entryTagID *int64
	err := tx.QueryRow(ctx, `SELECT a.channel_id,a.config_version,v.welcome_message,v.entry_tag_id,v.assignment_strategy,v.welcome_image_ids,v.welcome_miniprogram_ids,v.welcome_attachment_ids,v.welcome_group_invite_ids FROM channel_acquisition_assets a JOIN channel_config_versions v ON v.channel_id=a.channel_id AND v.config_version=a.config_version WHERE a.channel_id=$1 AND a.kind=$2 AND a.asset_version=$3 AND a.state IN ('executed','reconciled')`, resolution.Asset.ChannelID, providerKind, resolution.Asset.AssetVersion).Scan(&config.ChannelID, &config.ConfigVersion, &config.WelcomeMessage, &entryTagID, &config.Strategy, &config.WelcomeMaterials.ImageIDs, &config.WelcomeMaterials.MiniProgramIDs, &config.WelcomeMaterials.AttachmentIDs, &config.WelcomeMaterials.GroupInviteIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return entrantActionConfig{}, ErrEntrantActionUnavailable
	}
	if err != nil {
		return entrantActionConfig{}, err
	}
	if entryTagID != nil {
		config.EntryTagID = *entryTagID
	}
	rows, err := tx.Query(ctx, `SELECT staff_id,priority,COALESCE(ratio_percent,0),COALESCE(max_scans_24h,0) FROM channel_assignees WHERE channel_id=$1 AND config_version=$2 ORDER BY priority`, config.ChannelID, config.ConfigVersion)
	if err != nil {
		return entrantActionConfig{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var assignee channeldomain.Assignee
		if err = rows.Scan(&assignee.StaffID, &assignee.Priority, &assignee.Ratio, &assignee.MaxScans24h); err != nil {
			return entrantActionConfig{}, err
		}
		config.Assignees = append(config.Assignees, assignee)
	}
	if err = rows.Err(); err != nil || len(config.Assignees) == 0 {
		return entrantActionConfig{}, ErrEntrantActionUnavailable
	}
	return config, nil
}

func chooseEntrantStaff(ctx context.Context, tx pgx.Tx, config entrantActionConfig, callbackID string, occurredAt time.Time) (int64, error) {
	switch channeldomain.AssignmentStrategy(config.Strategy) {
	case channeldomain.StrategyRatio:
		digest := sha256.Sum256([]byte("channel.assignment.ratio.v1\x00" + callbackID))
		point, cumulative := int(binary.BigEndian.Uint64(digest[:8])%100)+1, 0
		for _, item := range config.Assignees {
			cumulative += item.Ratio
			if point <= cumulative {
				return item.StaffID, nil
			}
		}
	case channeldomain.StrategyCapSwitch:
		for _, item := range config.Assignees {
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM channel_entrant_assignments WHERE channel_id=$1 AND config_version=$2 AND staff_id=$3 AND assigned_at>=$4 AND assigned_at<$5`, config.ChannelID, config.ConfigVersion, item.StaffID, occurredAt.UTC().Add(-24*time.Hour), occurredAt.UTC()).Scan(&count); err != nil {
				return 0, err
			}
			if count < item.MaxScans24h {
				return item.StaffID, nil
			}
		}
	}
	return 0, ErrEntrantActionUnavailable
}

func (store *EntrantActionStore) acceptAction(ctx context.Context, tx pgx.Tx, assignmentID int64, command channelport.EntrantActionCommand, config entrantActionConfig, staffID int64, kind, grant string, tagID int64, materialSnapshot json.RawMessage, materialDigest string) error {
	source := effectport.Hash("channel.entrant.action.source.v1", command.CallbackID, kind)
	target := effectport.Hash("channel.entrant.action.target.v1", strconv.FormatInt(int64(command.CustomerID), 10), strconv.FormatInt(staffID, 10))
	payload := effectport.Hash("channel.entrant.action.payload.v2", strconv.FormatInt(config.ChannelID, 10), strconv.FormatInt(config.ConfigVersion, 10), kind, config.WelcomeMessage, strconv.FormatInt(tagID, 10), materialDigest)
	effectKind := effectport.KindChannelWelcome
	if kind == "entry_tag" {
		effectKind = effectport.KindChannelEntryTag
	}
	projection, receipt, err := store.effects.AcceptAndQueueWithin(ctx, effectport.AcceptCommand{ReceiptKey: effectport.Hash("channel.entrant.action.accept.v1", command.CallbackID, kind), Envelope: effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectKind, SourceRefDigest: source, TargetRefDigest: target, PayloadDigest: payload, PolicyVersionHash: effectport.Hash("channel.entrant.action.policy", "v1")}})
	if err != nil {
		return err
	}
	if len(materialSnapshot) == 0 {
		materialSnapshot = json.RawMessage(`{"schema_version":2,"node_kind":"message","attachments":[]}`)
	}
	_, err = tx.Exec(ctx, `INSERT INTO channel_entrant_actions(callback_id,assignment_id,channel_id,config_version,customer_id,staff_id,action_kind,welcome_grant_ref,local_tag_id,welcome_material_snapshot,source_ref_digest,effect_ref,accept_receipt_ref,queue_receipt_ref,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11,$12,$13,$14,$15) ON CONFLICT(callback_id,action_kind) DO NOTHING`, command.CallbackID, assignmentID, config.ChannelID, config.ConfigVersion, command.CustomerID, staffID, kind, nullableString(grant), nullableInt64(tagID), materialSnapshot, source, projection.ID, receipt.ID, receipt.QueueReceiptID, projection.State)
	return err
}

func (*EntrantActionStore) ReadPublishedEntrantAction(ctx context.Context, source string) (channelport.PublishedEntrantAction, error) {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return channelport.PublishedEntrantAction{}, err
	}
	var result channelport.PublishedEntrantAction
	var grant *string
	var tag *int64
	err = tx.QueryRow(ctx, `SELECT a.id,a.channel_id,a.config_version,a.customer_id,a.staff_id,a.action_kind,a.effect_ref,a.welcome_grant_ref,a.local_tag_id,a.welcome_material_snapshot,v.welcome_message FROM channel_entrant_actions a JOIN channel_config_versions v ON v.channel_id=a.channel_id AND v.config_version=a.config_version WHERE a.source_ref_digest=$1`, source).Scan(&result.ActionID, &result.ChannelID, &result.ConfigVersion, &result.CustomerID, &result.StaffID, &result.Kind, &result.EffectRef, &grant, &tag, &result.WelcomeMaterialSnapshot, &result.WelcomeMessage)
	if grant != nil {
		result.WelcomeGrantRef = *grant
	}
	if tag != nil {
		result.LocalTagID = *tag
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return channelport.PublishedEntrantAction{}, ErrEntrantActionUnavailable
	}
	return result, err
}

func (*EntrantActionStore) CompleteEntrantAction(ctx context.Context, completion channelport.EntrantActionCompletion) error {
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE channel_entrant_actions SET state=$2,result_digest=$3,updated_at=$4 WHERE effect_ref=$1 AND state IN ('queued','attempted','outcome_unknown','retryable_failed')`, completion.EffectRef, completion.State, completion.ResultDigest, completion.CompletedAt.UTC())
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrEntrantActionUnavailable
	}
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func nullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}
