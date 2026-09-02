// Package outbound is the sole owner of WeCom business-write intent.  This
// adapter accepts only opaque tag-catalog sync intent; it has no customer or
// provider credential data and delegates durable execution to EER.
package outbound

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
)

type TagCatalogSyncAccepter struct {
	effects effectport.TransactionalAccepter
}

func NewTagCatalogSyncAccepter(effects effectport.TransactionalAccepter) (*TagCatalogSyncAccepter, error) {
	if effects == nil {
		return nil, errors.New("external effects transaction accepter is required")
	}
	return &TagCatalogSyncAccepter{effects}, nil
}
func (a *TagCatalogSyncAccepter) EnqueueSync(ctx context.Context, job tagport.SyncJob) (tagport.SyncEffectReceipt, error) {
	if a == nil || a.effects == nil || job.ReceiptID < 1 || job.Actor < 1 || job.IdempotencyKey == "" {
		return tagport.SyncEffectReceipt{}, errors.New("invalid tag catalog sync intent")
	}
	key := effectport.Hash("outbound.wecom.tag_catalog.sync", strconv.FormatInt(job.Actor, 10), job.IdempotencyKey)
	envelope := effectport.Envelope{Owner: effectport.OwnerOutbound, Kind: effectport.KindWeComTagCatalog,
		SourceRefDigest:   effectport.Hash("tag.sync.receipt", strconv.FormatInt(job.ReceiptID, 10)),
		TargetRefDigest:   effectport.Hash("wecom.tag.catalog", "single-enterprise"),
		PayloadDigest:     effectport.Hash("tag.sync.payload", string(job.Kind), job.TraceID),
		PolicyVersionHash: effectport.Hash("outbound.wecom.tag_catalog.policy", "v1")}
	p, receipt, err := a.effects.AcceptAndQueueWithin(ctx, effectport.AcceptCommand{ReceiptKey: key, Envelope: envelope})
	if err != nil {
		return tagport.SyncEffectReceipt{}, err
	}
	if p.ID == "" || p.QueueJobID < 1 || receipt.ID == "" {
		return tagport.SyncEffectReceipt{}, errors.New("incomplete external effect acceptance")
	}
	if receipt.QueueReceiptID == "" {
		return tagport.SyncEffectReceipt{}, errors.New("external effect queue receipt is missing")
	}
	return tagport.SyncEffectReceipt{QueueJobID: p.QueueJobID, EffectID: parseEffectID(p.ID), EffectRef: p.ID, EffectState: string(p.State), AcceptReceiptID: receipt.ID, QueueReceiptID: receipt.QueueReceiptID}, nil
}
func parseEffectID(value string) int64 {
	var id int64
	_, _ = fmt.Sscanf(value, "eer_%d", &id)
	return id
}
