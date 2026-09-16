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
