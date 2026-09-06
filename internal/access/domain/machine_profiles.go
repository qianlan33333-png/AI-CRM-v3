package domain

// SystemMachineProfile freezes the external-integration service profiles that
// existed in the dd8d60d auth platform. They are registry/import facts, not
// administrator-selectable UI templates: the compatibility page remains
// limited to external_api and mcp.
type SystemMachineProfile struct {
	Purpose      string
	Audiences    []string
	Scopes       []string
	Capabilities []string
}

var systemMachineProfiles = map[string]SystemMachineProfile{
	"identity": {
		Purpose: "identity", Audiences: []string{"external_integration"}, Scopes: []string{"read"}, Capabilities: []string{"identity_resolve"},
	},
	"group_broadcast": {
		Purpose: "group_broadcast", Audiences: []string{"external_integration"}, Scopes: []string{"write"}, Capabilities: []string{"group_broadcast_execute"},
	},
	"campaign_agent": {
		Purpose: "campaign_agent", Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"}, Capabilities: []string{
			"campaign_draft_create", "campaign_preparation_commit", "campaign_preparation_create", "campaign_preparation_read", "campaign_status_read",
			"customer_read_limited", "customer_resolve_read", "material_create", "material_read", "operation_cycle_context_read", "operation_cycle_strategy_propose",
		},
	},
	"ops_reporter": {
		Purpose: "ops_reporter", Audiences: []string{"external_integration"}, Scopes: []string{"write"}, Capabilities: []string{"operation_cycle_report_write"},
	},
	"operation_runner": {
		Purpose: "operation_runner", Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"}, Capabilities: []string{"operation_cycle_action_claim", "operation_cycle_action_event_write", "operation_cycle_runner_heartbeat"},
	},
}

// SystemMachineProfileForPurpose returns a copy so callers cannot mutate the
// frozen registry for subsequent requests.
func SystemMachineProfileForPurpose(purpose string) (SystemMachineProfile, bool) {
	profile, ok := systemMachineProfiles[purpose]
	if !ok {
		return SystemMachineProfile{}, false
	}
	profile.Audiences = append([]string(nil), profile.Audiences...)
	profile.Scopes = append([]string(nil), profile.Scopes...)
	profile.Capabilities = append([]string(nil), profile.Capabilities...)
	return profile, true
}

// IsMachinePurpose covers persisted V3 purposes. It deliberately excludes the
// donor's internal_worker profiles, since their workers are not machine HTTP
// callers and their routes must never be opened through this platform.
func IsMachinePurpose(purpose string) bool {
	if purpose == "external_agent" || purpose == "mcp" || purpose == "direct_api_key" {
		return true
	}
	_, ok := SystemMachineProfileForPurpose(purpose)
	return ok
}

// IsLegacyAdminManagedMachinePurpose is the narrow old management-page set.
// Direct keys use their own fixed page action; only external_api and mcp are
// regular API-client templates.
func IsLegacyAdminManagedMachinePurpose(purpose string) bool {
	return purpose == "external_agent" || purpose == "mcp"
}
