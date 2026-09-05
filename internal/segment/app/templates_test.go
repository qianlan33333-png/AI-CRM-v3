package app

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestSixFrozenDonorTemplatesAndCanonicalDefinition(t *testing.T) {
	if got := Templates(); len(got) != 6 {
		t.Fatalf("templates=%d", len(got))
	} else {
		for _, item := range got {
			if item.TemplateVersion != 1 || item.Label == "" || item.Description == "" || len(item.Fields) == 0 {
				t.Fatalf("missing frozen form metadata: %+v", item)
			}
		}
	}
	left, err := CanonicalDefinition(json.RawMessage(`{"parameters":{"owner_scope":"all","owner_staff_ids":[],"contact_statuses":["active"],"registration_status":"any"},"template_key":"wecom_contact_registration","schema_version":1}`))
	if err != nil {
		t.Fatal(err)
	}
	right, err := CanonicalDefinition(json.RawMessage(`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"all","owner_staff_ids":[],"contact_statuses":["active"],"registration_status":"any"}}`))
	if err != nil || string(left) != string(right) {
		t.Fatalf("canonical left=%s right=%s err=%v", left, right, err)
	}
}

func TestDefinitionRejectsOpenEndedCapabilities(t *testing.T) {
	invalid := []string{
		`{"schema_version":1,"template_key":"sql","parameters":{"query":"select * from customers"}}`,
		`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"sql":"select 1"}}`,
		`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"all","owner_staff_ids":[],"contact_statuses":[] ,"registration_status":"any"}}`,
		`{"schema_version":1,"template_key":"active_contacts","parameters":{"within_days":"0"}}`,
		`{"schema_version":1,"template_key":"channel_entry","parameters":{"channel_codes":["wecom"],"token":"secret"}}`,
		`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"specified","owner_staff_ids":["bob"],"contact_statuses":["active"],"registration_status":"any"}}`,
		`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"specified","owner_staff_ids":["09"],"contact_statuses":["active"],"registration_status":"any"}}`,
		`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"specified","owner_staff_ids":["0"],"contact_statuses":["active"],"registration_status":"any"}}`,
		`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"specified","owner_staff_ids":["-1"],"contact_statuses":["active"],"registration_status":"any"}}`,
		`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"specified","owner_staff_ids":["9223372036854775808"],"contact_statuses":["active"],"registration_status":"any"}}`,
		`{"schema_version":1,"template_key":"wecom_contact_registration","parameters":{"owner_scope":"specified","owner_staff_ids":["9","bob"],"contact_statuses":["active"],"registration_status":"any"}}`,
	}
	for _, raw := range invalid {
		if _, err := CanonicalDefinition(json.RawMessage(raw)); !errors.Is(err, ErrUnsupportedDefinition) {
			t.Fatalf("definition accepted: %s err=%v", raw, err)
		}
	}
}
