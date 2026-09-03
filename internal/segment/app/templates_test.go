package app

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestFiveClosedTemplatesAndCanonicalDefinition(t *testing.T) {
	if got := Templates(); len(got) != 5 {
		t.Fatalf("templates=%d", len(got))
	}
	left, err := CanonicalDefinition(json.RawMessage(`{"parameters":{"stages":["new","active"]},"template_key":"stage_any","schema_version":1}`))
	if err != nil {
		t.Fatal(err)
	}
	right, err := CanonicalDefinition(json.RawMessage(`{"schema_version":1,"template_key":"stage_any","parameters":{"stages":["new","active"]}}`))
	if err != nil || string(left) != string(right) {
		t.Fatalf("canonical left=%s right=%s err=%v", left, right, err)
	}
}

func TestDefinitionRejectsOpenEndedCapabilities(t *testing.T) {
	invalid := []string{
		`{"schema_version":1,"template_key":"sql","parameters":{"query":"select * from customers"}}`,
		`{"schema_version":1,"template_key":"tag_any","parameters":{"sql":"select 1"}}`,
		`{"schema_version":1,"template_key":"tag_any","parameters":{"tag_codes":[]}}`,
		`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"0"}}`,
		`{"schema_version":1,"template_key":"channel_any","parameters":{"channels":["wecom"],"token":"secret"}}`,
	}
	for _, raw := range invalid {
		if _, err := CanonicalDefinition(json.RawMessage(raw)); !errors.Is(err, ErrUnsupportedDefinition) {
			t.Fatalf("definition accepted: %s err=%v", raw, err)
		}
	}
}
