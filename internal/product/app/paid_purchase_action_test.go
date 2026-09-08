package app

import (
	"encoding/json"
	"testing"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func TestPaidPurchaseConfigurationFreezesOnlyEnabledSafeActions(t *testing.T) {
	disabled, err := paidPurchaseConfigFromProjection(json.RawMessage(`{"schema_version":1,"wecom_tagging":{"enabled":false,"tag_ids":[9]}}`))
	if err != nil || disabled.Enabled || disabled.Mode != productport.PaidPurchaseActionNone || disabled.TagState != "disabled" || len(disabled.TagIDs) != 0 {
		t.Fatalf("disabled=%+v err=%v", disabled, err)
	}
	tagOnly, err := paidPurchaseConfigFromProjection(json.RawMessage(`{"schema_version":1,"wecom_tagging":{"enabled":true,"tag_ids":[9]}}`))
	if err != nil || tagOnly.Enabled || tagOnly.Mode != productport.PaidPurchaseActionNone || tagOnly.TagState != "queued" || len(tagOnly.TagIDs) != 1 || tagOnly.TagIDs[0] != 9 {
		t.Fatalf("tag-only=%+v err=%v", tagOnly, err)
	}
	qr, err := paidPurchaseConfigFromProjection(json.RawMessage(`{"schema_version":1,"purchase_action_enabled":true,"purchase_action_mode":"qr","lead_channel_id":7,"lead_qr_title":"添加客服","lead_qr_subtitle":"领取资料","wecom_tagging":{"enabled":true,"tag_ids":[11,3]}}`))
	if err != nil || !qr.Enabled || qr.Mode != productport.PaidPurchaseActionQR || qr.LeadChannelID != 7 || qr.TagState != "queued" || len(qr.TagIDs) != 2 || qr.TagIDs[0] != 3 || qr.TagIDs[1] != 11 {
		t.Fatalf("qr=%+v err=%v", qr, err)
	}
	redirect, err := paidPurchaseConfigFromProjection(json.RawMessage(`{"schema_version":1,"purchase_action_enabled":true,"purchase_action_mode":"redirect","completion_redirect_url":"/welcome?from=paid"}`))
	if err != nil || redirect.Mode != productport.PaidPurchaseActionRedirect || redirect.RedirectURL != "/welcome?from=paid" || redirect.TagState != "disabled" {
		t.Fatalf("redirect=%+v err=%v", redirect, err)
	}
	for _, raw := range []string{
		`{"schema_version":1,"purchase_action_enabled":true,"purchase_action_mode":"qr"}`,
		`{"schema_version":1,"purchase_action_enabled":true,"purchase_action_mode":"redirect","completion_redirect_url":"javascript:alert(1)"}`,
		`{"schema_version":1,"purchase_action_enabled":true,"purchase_action_mode":"redirect","completion_redirect_url":"//other.example/path"}`,
	} {
		if _, err = paidPurchaseConfigFromProjection(json.RawMessage(raw)); err == nil {
			t.Fatalf("unsafe configuration accepted: %s", raw)
		}
	}
}

func TestPaidPurchaseTagIDsDoNotCoerceLegacyOrAmbiguousValues(t *testing.T) {
	for _, raw := range []string{`{}`, `{"enabled":true,"tag_ids":["9"]}`, `{"enabled":true,"tag_ids":[9,9]}`, `{"enabled":true,"tag_ids":[0]}`, `{"enabled":true,"tag_ids":[9],"other":true}`} {
		values, ok := paidPurchaseTagIDs(json.RawMessage(raw))
		if raw == `{"enabled":true,"tag_ids":[9],"other":true}` {
			if !ok || len(values) != 1 || values[0] != 9 {
				t.Fatalf("safe extension=%s values=%v ok=%v", raw, values, ok)
			}
			continue
		}
		if ok || len(values) != 0 {
			t.Fatalf("unsafe tag config=%s values=%v ok=%v", raw, values, ok)
		}
	}
}
