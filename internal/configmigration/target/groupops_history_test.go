package target

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/configmigration/source"
)

func TestHistoryRecordsPreserveTextReferencesAndQuarantine(t *testing.T) {
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	userid, blank := "9", ""
	snap := source.HistorySnapshot{
		Plans:              []source.HistoryPlan{{ID: 7, PlanCode: "", Name: "历史计划", PlanType: "standard", Status: "disabled", OwnerReference: &userid, CreatedByReference: &blank, UpdatedByReference: &userid, CreatedAt: now, UpdatedAt: now}, {ID: 8, PlanCode: "bad", Name: " leading", PlanType: "standard", Status: "disabled", CreatedAt: now, UpdatedAt: now}},
		DirectoryChats:     []source.HistoryDirectoryChat{{ChatReference: "chat-7", DisplayName: "群", OwnerReference: &userid, MemberCount: 2, Status: "active", RecordedAt: now}},
		DirectorySnapshots: []source.HistoryDirectorySnapshot{{ChatReference: "chat-7", DisplayName: "群", OwnerReference: &blank, OwnerName: "", InternalMemberCount: 1, ExternalMemberCount: 1, Status: "active", RecordedAt: now}},
		Groups:             []source.HistoryGroup{{ID: 11, PlanID: 7, ChatReference: "chat-7", DisplayName: "群", OwnerReference: &userid, InternalMemberCount: 1, ExternalMemberCount: 1, Status: "active", CreatedAt: now}, {ID: 12, PlanID: 8, ChatReference: "chat-8", DisplayName: "群", InternalMemberCount: 1, ExternalMemberCount: 1, Status: "active", CreatedAt: now}},
		Nodes:              []source.HistoryNode{{ID: 21, PlanID: 7, DayIndex: 1, TriggerTime: "09:00", SortOrder: 1, Status: "active", ContentPackage: json.RawMessage(`{"text":"hello"}`), Attachments: json.RawMessage(`[]`), CreatedAt: now, UpdatedAt: now}},
	}
	if err := source.PopulateHistoryManifest(&snap, source.ProductionSourceSystem, strings.Repeat("a", 40), now); err != nil {
		t.Fatal(err)
	}
	records, err := historyRecords(snap)
	if err != nil {
		t.Fatal(err)
	}
	var planFound, groupMissingParent, nodeFound bool
	for _, record := range records {
		if record.SourceKind == "plans" && record.SourceKey == "7" {
			planFound = record.Plan != nil && record.Plan.OwnerStaffID == nil && record.Plan.SourceOwnerReference != nil && *record.Plan.SourceOwnerReference == "9" && record.Plan.SourceCreatedByReference != nil && *record.Plan.SourceCreatedByReference == ""
		}
		if record.SourceKind == "groups" && record.SourceKey == "12" {
			groupMissingParent = record.QuarantineReason == "missing_plan"
		}
		if record.SourceKind == "nodes" && record.SourceKey == "21" {
			nodeFound = record.Node != nil && record.Node.TriggerTime == "09:00" && record.Node.TextContent == "" && string(record.Node.Attachments) == `[]` && strings.Contains(string(record.Node.ContentPackage), `"source_content_package":{"text":"hello"}`)
		}
	}
	if !planFound || !groupMissingParent || !nodeFound {
		t.Fatalf("records do not preserve protected source facts: %#v", records)
	}
}
