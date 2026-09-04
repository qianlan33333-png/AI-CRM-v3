package outbound

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// SidebarJSSDKExpiry is the durable timeout boundary for a browser-owned
// JSSDK effect. It never invokes a Provider. If a one-time grant was not
// completed before its River deadline, it closes the intent as final_failed.
type SidebarJSSDKExpiry struct{}

func (SidebarJSSDKExpiry) Execute(_ context.Context, envelope effectport.Envelope, _ effectport.Attempt) (effectport.AdapterResult, error) {
	if envelope.Kind != effectport.KindSidebarJSSDKSend {
		return effectport.AdapterResult{}, errors.New("unsupported sidebar effect")
	}
	return effectport.AdapterResult{
		Completion:    effectport.StateFinalFailed,
		ReceiptDigest: effectport.Hash("sidebar.jssdk.grant.expired", string(envelope.PayloadDigest)),
	}, nil
}

func (SidebarJSSDKExpiry) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, _ effectport.Attempt, result effectport.AdapterResult) error {
	if envelope.Kind != effectport.KindSidebarJSSDKSend || result.Completion != effectport.StateFinalFailed || result.CallAttempted || result.RealExternalCallExecuted {
		return errors.New("invalid sidebar expiry completion")
	}
	tx, err := platformpostgres.RequireTransaction(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	receiptDigest := sha256.Sum256([]byte(result.ReceiptDigest))
	var intentID int64
	err = tx.QueryRow(ctx, `UPDATE outbound_sidebar_send_intents SET state='final_failed',updated_at=$2 WHERE effect_id=$1 AND state='queued' RETURNING id`, effectRef, now).Scan(&intentID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbound_sidebar_send_audit_events(intent_id,operation,payload_digest,occurred_at) VALUES($1,'expire',$2,$3)`, intentID, receiptDigest[:], now); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbound_sidebar_send_outbox(event_type,intent_id,payload,idempotency_digest,occurred_at) VALUES('outbound.sidebar_send.expired.v1',$1,jsonb_build_object('intent_id',$1,'effect_id',$2,'state','final_failed'),$3,$4)`, intentID, effectRef, receiptDigest[:], now)
	return err
}

var _ effectport.ProviderAdapter = SidebarJSSDKExpiry{}
var _ effectport.CompletionSink = SidebarJSSDKExpiry{}
