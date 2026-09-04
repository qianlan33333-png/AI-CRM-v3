package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	segmentdsl "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/dsl"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

var errSegmentSourceNotReady = errors.New("audience source is not ready")

type segmentSourceAdapter struct {
	uow       platformport.UnitOfWork
	customers customerport.AudienceReader
}

func (a segmentSourceAdapter) Evaluate(ctx context.Context, definition segmentport.Definition, reference time.Time) (segmentport.Evaluation, error) {
	if a.uow == nil || a.customers == nil || reference.IsZero() {
		return segmentport.Evaluation{}, errSegmentSourceNotReady
	}
	var ast segmentdsl.AST
	if json.Unmarshal(definition.Expression, &ast) != nil || ast.SchemaVersion != 1 {
		return segmentport.Evaluation{}, errSegmentSourceNotReady
	}
	if ast.Template != segmentdsl.ActiveContacts {
		return segmentport.Evaluation{}, errSegmentSourceNotReady
	}
	days, err := strconv.Atoi(ast.Predicate.Values[0])
	if err != nil {
		return segmentport.Evaluation{}, errSegmentSourceNotReady
	}
	var result segmentport.Evaluation
	err = a.uow.Within(ctx, func(tx context.Context) error {
		ids, asOf, e := a.customers.ActiveWithin(tx, reference, days)
		if e != nil {
			return e
		}
		safe := sha256.Sum256([]byte(asOf.UTC().Format(time.RFC3339Nano)))
		result = segmentport.Evaluation{CustomerIDs: ids, ReferenceAt: reference.UTC(), Watermarks: []segmentport.SourceWatermark{{Source: "customer.directory.v1", AsOf: asOf.UTC(), Fresh: !asOf.IsZero(), SafeDigest: safe}}}
		return nil
	})
	return result, err
}
