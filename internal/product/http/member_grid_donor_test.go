package http

import (
	"testing"
	"time"
)

func TestDonorGridRemainingDaysMatchesDD8CeilAndClamp(t *testing.T) {
	snapshot := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name string
		end  time.Time
		want int
	}{
		{name: "just under one day", end: snapshot.Add(24*time.Hour - time.Second), want: 1},
		{name: "exactly one day", end: snapshot.Add(24 * time.Hour), want: 1},
		{name: "just over one day", end: snapshot.Add(24*time.Hour + time.Second), want: 2},
		{name: "expired", end: snapshot.Add(-time.Second), want: 0},
		{name: "exact expiry", end: snapshot, want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := donorGridRemainingDays(test.end, snapshot); got != test.want {
				t.Fatalf("remaining days=%d want=%d", got, test.want)
			}
		})
	}
}
