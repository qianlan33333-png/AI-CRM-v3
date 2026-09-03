package app

import (
	"encoding/json"
	"testing"
	"time"

	automationdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/domain"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

func TestPolicyExecutionConfigurationMatchesAgentAndPackage(t *testing.T) {
	version := automationdomain.PolicyVersion{PackageID: 9, ActionKind: automationport.ActionOutboundMessage, ActionConfig: json.RawMessage(`{"agent_id":17}`)}
	configuration := segmentport.ExecutionConfiguration{PackageID: 9, AgentID: 17, AgentPublishedVersion: 3, SenderStaffIDs: []int64{7}, Ready: true}
	if !policyExecutionConfigurationMatches(version, configuration) {
		t.Fatal("expected matching published fixed-script configuration")
	}
	configuration.AgentID = 18
	if policyExecutionConfigurationMatches(version, configuration) {
		t.Fatal("policy must not execute with a different agent binding")
	}
}

func TestNextAllowedExecutionCrossMidnight(t *testing.T) {
	raw := json.RawMessage(`{"timezone":"Asia/Shanghai","start":"22:00","end":"07:30"}`)
	inside := time.Date(2026, 9, 4, 15, 15, 0, 0, time.UTC) // 23:15 Asia/Shanghai.
	want := time.Date(2026, 9, 4, 23, 30, 0, 0, time.UTC)
	if got := nextAllowedExecution(inside, raw); !got.Equal(want) {
		t.Fatalf("scheduled at %s, want %s", got, want)
	}
	outside := time.Date(2026, 9, 4, 2, 0, 0, 0, time.UTC) // 10:00 Asia/Shanghai.
	if got := nextAllowedExecution(outside, raw); !got.IsZero() {
		t.Fatalf("outside quiet hours should execute immediately, got %s", got)
	}
}

func TestNextAllowedExecutionSameDay(t *testing.T) {
	raw := json.RawMessage(`{"timezone":"Asia/Shanghai","start":"09:00","end":"10:00"}`)
	inside := time.Date(2026, 9, 4, 1, 30, 0, 0, time.UTC)
	want := time.Date(2026, 9, 4, 2, 0, 0, 0, time.UTC)
	if got := nextAllowedExecution(inside, raw); !got.Equal(want) {
		t.Fatalf("scheduled at %s, want %s", got, want)
	}
}
