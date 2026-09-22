package wecom

import (
	"errors"
	"testing"

	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
)

func TestIsSkippableContactDescriptionErrorKeepsDirectorySyncMoving(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "replan required", err: outboundport.ErrContactDescriptionReplanRequired, want: true},
		{name: "in flight", err: outboundport.ErrContactDescriptionInFlight, want: true},
		{name: "outcome unknown", err: outboundport.ErrContactDescriptionOutcomeUnknown, want: true},
		{name: "wrapped relationship-local error", err: errors.Join(errors.New("contact description"), outboundport.ErrContactDescriptionReplanRequired), want: true},
		{name: "provider failure", err: errors.New("provider unavailable"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSkippableContactDescriptionError(tt.err); got != tt.want {
				t.Fatalf("isSkippableContactDescriptionError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
