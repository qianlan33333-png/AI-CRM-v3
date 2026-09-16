package outbound

import (
	"encoding/json"
	"testing"
)

func TestSurveyEndpointMetadataOverridesEditableFields(t *testing.T) {
	target := SurveyCompletionTarget{PushType: "template", Remark: "template", CustomParams: map[string]string{"old": "value"}}
	raw := json.RawMessage(`{"type":"subscription","expires_at_ts":1893456000,"day":365,"frequency":1,"remark":"问卷激活","custom_params":{"source":"survey"}}`)
	if !validSurveyEndpointMetadata(raw) {
		t.Fatal("expected legacy questionnaire metadata to be valid")
	}
	applySurveyEndpointMetadata(&target, raw)
	if target.PushType != "subscription" || target.ExpiresAtTS == nil || *target.ExpiresAtTS != 1893456000 || target.Day == nil || *target.Day != 365 || target.Frequency == nil || *target.Frequency != 1 || target.Remark != "问卷激活" || target.CustomParams["source"] != "survey" {
		t.Fatalf("metadata not applied: %#v", target)
	}
	if _, retained := target.CustomParams["old"]; retained {
		t.Fatal("template custom parameters must not leak into the administrator configuration")
	}
	if validSurveyEndpointMetadata(json.RawMessage(`{"day":-1}`)) {
		t.Fatal("negative service period must be rejected")
	}
}

func TestEditableSurveyEndpointRejectsPrivateAndAmbiguousDestinations(t *testing.T) {
	for _, endpoint := range []string{
		"https://localhost/hook",
		"https://service.local/hook",
		"https://127.0.0.1/hook",
		"https://10.0.0.8/hook",
		"https://169.254.169.254/latest/meta-data",
		"https://[::1]/hook",
		"https://example.com:8443/hook",
	} {
		if editableSurveyEndpoint(endpoint, false) {
			t.Fatalf("private or non-standard endpoint accepted: %s", endpoint)
		}
	}
	if !editableSurveyEndpoint("https://hooks.example.com/aicrm", false) {
		t.Fatal("public HTTPS endpoint rejected")
	}
	if !editableSurveyEndpoint("https://127.0.0.1/hook", true) {
		t.Fatal("explicit test-only loopback endpoint rejected")
	}
}
