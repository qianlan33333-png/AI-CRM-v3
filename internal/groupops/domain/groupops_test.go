package domain

import (
	"encoding/json"
	"testing"

	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
)

func TestPlanTransitionsKeepArchivedTerminal(t *testing.T) {
	for _, test := range []struct {
		from, to groupopsport.PlanStatus
		want     bool
	}{
		{groupopsport.PlanDraft, groupopsport.PlanActive, true},
		{groupopsport.PlanActive, groupopsport.PlanPaused, true},
		{groupopsport.PlanPaused, groupopsport.PlanActive, true},
		{groupopsport.PlanArchived, groupopsport.PlanDraft, false},
		{groupopsport.PlanActive, groupopsport.PlanDraft, false},
	} {
		if got := CanTransitionPlanStatus(test.from, test.to); got != test.want {
			t.Fatalf("transition %s -> %s = %v, want %v", test.from, test.to, got, test.want)
		}
	}
}

func TestTypedMaterialAndScopeValidation(t *testing.T) {
	if err := ValidateMaterialPlan(groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{{Kind: "image", ID: 1}, {Kind: "attachment", ID: 2}}}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMaterialPlan(groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{{Kind: "image", ID: 1}, {Kind: "image", ID: 1}}}); err == nil {
		t.Fatal("duplicate typed material reference accepted")
	}
	if !ContainsForbidden(json.RawMessage(`{"customer_id":7}`)) || ContainsForbidden(json.RawMessage(`{"message":"audience is a label"}`)) {
		t.Fatal("scope validation did not fail closed on concrete identity field")
	}
}
