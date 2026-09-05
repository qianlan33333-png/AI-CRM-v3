package source

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHistorySnapshotCanonicalPreservesAbsentAndEmptyReferences(t *testing.T) {
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	empty := ""
	snap := HistorySnapshot{Plans: []HistoryPlan{{ID: 1, PlanCode: "", Name: "历史", PlanType: "standard", Status: "draft", OwnerReference: &empty, CreatedAt: now, UpdatedAt: now}}, Nodes: []HistoryNode{{ID: 2, PlanID: 1, DayIndex: 1, TriggerTime: "09:00", SortOrder: 1, Status: "active", ContentPackage: json.RawMessage(`{}`), Attachments: json.RawMessage(`[]`), CreatedAt: now, UpdatedAt: now}}}
	if err := PopulateHistoryManifest(&snap, ProductionSourceSystem, strings.Repeat("a", 40), now); err != nil {
		t.Fatal(err)
	}
	raw, _, err := snap.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, err := ParseHistory(raw)
	if err != nil || parsed.Plans[0].OwnerReference == nil || *parsed.Plans[0].OwnerReference != "" || parsed.Plans[0].CreatedByReference != nil {
		t.Fatalf("protected source reference was normalized: parsed=%#v err=%v", parsed.Plans[0], err)
	}
}
