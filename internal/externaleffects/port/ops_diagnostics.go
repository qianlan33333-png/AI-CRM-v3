package port

import (
	"context"
	"time"
)

// OpsDiagnosticReader exposes aggregate execution facts without payloads,
// raw targets, or cross-domain table access.
type OpsDiagnosticReader interface {
	ReadOpsDiagnosticCounts(context.Context, time.Time) (map[string]int64, error)
}
