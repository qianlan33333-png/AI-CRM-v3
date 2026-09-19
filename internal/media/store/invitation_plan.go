package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	e "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	d "github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	pg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"strconv"
	"strings"
	"time"
)

const invitationSelect = `SELECT m.id,m.name,m.title,m.description,COALESCE(m.cover_image_id,0),m.enabled,m.version,m.join_url,COALESCE(p.public_token,''),COALESCE(p.mode,''),p.threshold,COALESCE(p.state,'legacy'),COALESCE(p.current_chat_id,''),COALESCE(p.bindings,'[]'::jsonb) FROM media_group_invites m LEFT JOIN media_invitation_plans p ON m.id=p.invite_id `

func scanInvitation(row pgx.Row) (v p.InvitationPlan, err error) {
	var raw []byte
	err = row.Scan(&v.ID, &v.Name, &v.Title, &v.Description, &v.CoverImageID, &v.Enabled, &v.Version, &v.JoinURL, &v.Token, &v.Mode, &v.Threshold, &v.State, &v.CurrentChatID, &raw)
	if err == nil {
		err = json.Unmarshal(raw, &v.Bindings)
	}
	return
}
func (r *Repository) ReadInvitationPlan(ctx context.Context, id int64) (v p.InvitationPlan, err error) {
	err = r.Within(ctx, func(ctx context.Context) error {
		tx, _ := pg.RequireTransaction(ctx)
		var e error
		v, e = scanInvitation(tx.QueryRow(ctx, invitationSelect+`WHERE m.id=$1 AND m.archived_at IS NULL`, id))
		if e != nil {
			return e
		}
		return r.hydrateInvitation(ctx, &v)
	})
	return
}
func (r *Repository) ReadPublicInvitation(ctx context.Context, token string) (v p.InvitationPlan, err error) {
	err = r.Within(ctx, func(ctx context.Context) error {
		tx, _ := pg.RequireTransaction(ctx)
		var e error
		v, e = scanInvitation(tx.QueryRow(ctx, invitationSelect+`WHERE p.public_token=$1 AND m.archived_at IS NULL`, token))
		if e != nil {
			return e
		}
		return r.hydrateInvitation(ctx, &v)
	})
	return
}
func (r *Repository) hydrateInvitation(ctx context.Context, v *p.InvitationPlan) error {
	tx, _ := pg.RequireTransaction(ctx)
	for i := range v.Bindings {
		err := tx.QueryRow(ctx, `SELECT state,qr_code FROM media_invitation_codes WHERE chat_id=$1`, v.Bindings[i].ChatID).Scan(&v.Bindings[i].CodeState, &v.Bindings[i].QRCode)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	return nil
}
func (r *Repository) InvitationPlanIDs(ctx context.Context, activeOnly bool) (ids []int64, err error) {
	ids = []int64{}
	err = r.Within(ctx, func(ctx context.Context) error {
		tx, _ := pg.RequireTransaction(ctx)
		rows, e := tx.Query(ctx, `SELECT m.id FROM media_group_invites m LEFT JOIN media_invitation_plans p ON p.invite_id=m.id WHERE m.archived_at IS NULL AND (NOT $1 OR (m.enabled AND p.invite_id IS NOT NULL)) ORDER BY m.id DESC`, activeOnly)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if e = rows.Scan(&id); e != nil {
				return e
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	return
}
func (r *Repository) SaveInvitationPlan(ctx context.Context, input p.InvitationInput, actor int64, key, origin string, effects e.TransactionalAccepter) (out p.InvitationPlan, err error) {
	if d.ValidateInvitationInput(input) != nil || actor < 1 || key == "" || effects == nil {
		return out, ErrInvalid
	}
	raw, _ := json.Marshal(input)
	err = r.Within(ctx, func(ctx context.Context) error {
		replay, owned, err := r.reserve(ctx, "invitation.plan.save", "group_invite", actor, key, string(raw))
		if err != nil {
			return err
		}
		if !owned {
			return json.Unmarshal(replay, &out)
		}
		tx, _ := pg.RequireTransaction(ctx)
		bindings := []p.InvitationBinding{}
		var old p.InvitationPlan
		if input.ID > 0 {
			old, err = scanInvitation(tx.QueryRow(ctx, invitationSelect+`WHERE m.id=$1 AND m.archived_at IS NULL FOR UPDATE OF m`, input.ID))
			if errors.Is(err, pgx.ErrNoRows) {
				// Explicitly upgrading a legacy material retains its numeric ID.
				err = tx.QueryRow(ctx, `SELECT id,version FROM media_group_invites WHERE id=$1 AND archived_at IS NULL FOR UPDATE`, input.ID).Scan(&old.ID, &old.Version)
			}
			if err != nil {
				return err
			}
			if old.Version != input.Version {
				return ErrConflict
			}
		}
		// Retired entries stay as history. Active entry cannot be silently reordered.
		for _, b := range old.Bindings {
			if b.Retired {
				bindings = append(bindings, p.InvitationBinding{ChatID: b.ChatID, Retired: true})
			}
		}
		for _, id := range input.ChatIDs {
			retired := false
			for _, b := range bindings {
				if b.ChatID == id {
					retired = true
				}
			}
			if !retired {
				bindings = append(bindings, p.InvitationBinding{ChatID: id})
			}
		}
		if old.Mode != "" && input.Mode != old.Mode {
			return ErrConflict
		}
		if old.Mode == "sequence" && old.CurrentChatID != "" {
			next := ""
			for _, b := range bindings {
				if !b.Retired {
					next = b.ChatID
					break
				}
			}
			if next != old.CurrentChatID {
				return ErrConflict
			}
		}
		token := old.Token
		if token == "" {
			bytes := make([]byte, 24)
			if _, err = rand.Read(bytes); err != nil {
				return err
			}
			token = hex.EncodeToString(bytes)
		}
		joinURL := strings.TrimRight(origin, "/") + "/gi/" + token
		var previousCover *int64
		if input.ID > 0 {
			if err = tx.QueryRow(ctx, `SELECT cover_image_id FROM media_group_invites WHERE id=$1`, input.ID).Scan(&previousCover); err != nil {
				return err
			}
		}
		var cover *int64
		if input.CoverImageID > 0 {
			cover = &input.CoverImageID
		}
		id := input.ID
		if id == 0 {
			err = tx.QueryRow(ctx, `INSERT INTO media_group_invites(name,title,description,join_url,cover_image_id,enabled,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$7) RETURNING id`, input.Name, input.Title, input.Description, joinURL, cover, input.Enabled, actor).Scan(&id)
		} else {
			_, err = tx.Exec(ctx, `UPDATE media_group_invites SET name=$2,title=$3,description=$4,join_url=$5,cover_image_id=$6,enabled=$7,version=version+1,updated_by=$8,updated_at=clock_timestamp() WHERE id=$1`, id, input.Name, input.Title, input.Description, joinURL, cover, input.Enabled, actor)
		}
		if err != nil {
			return err
		}
		if err = replaceLocalImageReference(ctx, "media.group_invite.cover", id, previousCover, cover); err != nil {
			return err
		}
		braw, _ := json.Marshal(bindings)
		_, err = tx.Exec(ctx, `INSERT INTO media_invitation_plans(invite_id,public_token,mode,threshold,bindings) VALUES($1,$2,$3,$4,$5) ON CONFLICT(invite_id) DO UPDATE SET mode=EXCLUDED.mode,threshold=EXCLUDED.threshold,bindings=EXCLUDED.bindings`, id, token, input.Mode, input.Threshold, braw)
		if err != nil {
			return err
		}
		for _, b := range bindings {
			if b.Retired {
				continue
			}
			source := e.Hash("media.invitation.code.v1", b.ChatID)
			result, err := tx.Exec(ctx, `INSERT INTO media_invitation_codes(chat_id,source_digest) VALUES($1,$2) ON CONFLICT(chat_id) DO NOTHING`, b.ChatID, string(source))
			if err != nil {
				return err
			}
			if result.RowsAffected() == 0 {
				continue
			}
			env := e.Envelope{Owner: e.OwnerOutbound, Kind: e.KindInvitationCode, SourceRefDigest: source, TargetRefDigest: e.Hash("invitation.target.v1", b.ChatID), PayloadDigest: e.Hash("invitation.single-code.v1", b.ChatID, "auto_create_room=0"), PolicyVersionHash: e.Hash("invitation.code.policy.v1")}
			projection, _, err := effects.AcceptAndQueueWithin(ctx, e.AcceptCommand{ReceiptKey: source, Envelope: env})
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE media_invitation_codes SET effect_id=$2 WHERE chat_id=$1`, b.ChatID, projection.ID)
			if err != nil {
				return err
			}
		}
		out, err = scanInvitation(tx.QueryRow(ctx, invitationSelect+`WHERE m.id=$1`, id))
		if err != nil {
			return err
		}

		if input.Observations != nil {
			if err = r.hydrateInvitation(ctx, &out); err != nil {
				return err
			}
			evaluated := d.EvaluateInvitation(out, input.Observations, time.Now().UTC())
			raw, _ := json.Marshal(evaluated.Bindings)
			if _, err = tx.Exec(ctx, `UPDATE media_invitation_plans SET state=$2,current_chat_id=$3,bindings=$4 WHERE invite_id=$1`, id, evaluated.State, evaluated.CurrentChatID, raw); err != nil {
				return err
			}
			if old.CurrentChatID != evaluated.CurrentChatID {
				if _, err = tx.Exec(ctx, `INSERT INTO media_invitation_changes(invite_id,operation,from_chat,to_chat,actor_id) VALUES($1,'evaluate',$2,$3,$4)`, id, old.CurrentChatID, evaluated.CurrentChatID, actor); err != nil {
					return err
				}
			}
			out = evaluated
		}
		if _, err = tx.Exec(ctx, `INSERT INTO media_invitation_changes(invite_id,operation,actor_id) VALUES($1,'save',$2)`, id, actor); err != nil {
			return err
		}
		return r.complete(ctx, "invitation.plan.save", "group_invite", actor, key, id, out, "media.invitation_plan_saved")
	})
	return
}
func (r *Repository) ApplyInvitationEvaluation(ctx context.Context, before, after p.InvitationPlan) error {
	return r.Within(ctx, func(ctx context.Context) error {
		tx, _ := pg.RequireTransaction(ctx)
		var version int64
		if err := tx.QueryRow(ctx, `SELECT version FROM media_group_invites WHERE id=$1 FOR UPDATE`, before.ID).Scan(&version); err != nil {
			return err
		}
		if version != before.Version {
			return p.ErrInvitationVersion
		}
		raw, _ := json.Marshal(after.Bindings)
		if before.State == after.State && before.CurrentChatID == after.CurrentChatID && sameRetired(before.Bindings, after.Bindings) {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE media_invitation_plans SET state=$2,current_chat_id=$3,bindings=$4 WHERE invite_id=$1`, before.ID, after.State, after.CurrentChatID, raw); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE media_group_invites SET version=version+1,updated_at=clock_timestamp() WHERE id=$1`, before.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO media_invitation_changes(invite_id,operation,from_chat,to_chat) VALUES($1,'evaluate',$2,$3)`, before.ID, before.CurrentChatID, after.CurrentChatID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO media_outbox(event_type,aggregate_kind,aggregate_id,payload) VALUES('media.invitation_evaluated','group_invite',$1,$2::jsonb)`, before.ID, `{"state":`+strconv.Quote(after.State)+`}`)
		return err
	})
}
func (r *Repository) ReadInvitationCodeIntent(ctx context.Context, source string) (v p.InvitationCodeIntent, err error) {
	err = r.Within(ctx, func(ctx context.Context) error {
		tx, _ := pg.RequireTransaction(ctx)
		return tx.QueryRow(ctx, `SELECT chat_id,source_digest,effect_id::text FROM media_invitation_codes WHERE source_digest=$1`, source).Scan(&v.ChatID, &v.SourceDigest, &v.EffectID)
	})
	return
}

// EER calls this within its completion transaction; do not open another UoW.
func (r *Repository) CompleteInvitationCode(ctx context.Context, v p.InvitationCodeCompletion) error {
	tx, err := pg.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE media_invitation_codes SET state=$2,config_id=$3,qr_code=$4 WHERE effect_id=$1`, v.EffectID, v.State, v.ConfigID, v.QRCode)
	return err
}
func (r *Repository) InvitationHistory(ctx context.Context, id int64) (out []p.InvitationSwitch, err error) {
	out = []p.InvitationSwitch{}
	err = r.Within(ctx, func(ctx context.Context) error {
		tx, _ := pg.RequireTransaction(ctx)
		rows, e := tx.Query(ctx, `SELECT from_chat,to_chat,at FROM media_invitation_changes WHERE invite_id=$1 AND operation='evaluate' ORDER BY id DESC LIMIT 100`, id)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var v p.InvitationSwitch
			if e = rows.Scan(&v.From, &v.To, &v.At); e != nil {
				return e
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	return
}

func sameRetired(a, b []p.InvitationBinding) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Retired != b[i].Retired {
			return false
		}
	}
	return true
}
