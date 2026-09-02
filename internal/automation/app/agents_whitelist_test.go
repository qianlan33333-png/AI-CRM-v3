package app

import (
	"errors"
	"testing"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
)

func TestWhitelistAgentConfigurationFailsClosed(t *testing.T) {
	active := automationport.Agent{AgentName: "agent", AgentCode: "agent_1", AutomationType: automationport.AutomationTypeAgent, Status: automationport.AgentStatusActive}
	if _, err := normalizeCreate(active, 1); !errors.Is(err, ErrInvalidAgent) {
		t.Fatalf("active create err=%v", err)
	}
	paused := active
	paused.Status = automationport.AgentStatusPaused
	paused.ExecutionEnabled = true
	if _, err := normalizeCreate(paused, 1); !errors.Is(err, ErrInvalidAgent) {
		t.Fatalf("execution-enabled create err=%v", err)
	}
	if _, err := normalizeContent(automationport.FixedContentPackage{ImageLibraryIDs: []int64{7}}, automationport.AutomationTypeAgent); !errors.Is(err, ErrInvalidAgent) {
		t.Fatalf("legacy material reference err=%v", err)
	}
}
