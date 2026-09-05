package adapter

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
	segmentdsl "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/dsl"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type legacyFacts struct {
	contacts []wecomport.AudienceContact
	survey   []surveyport.AudienceChoiceAnswer
	orders   []orderport.PaidAudienceOrder
	channels []channelport.AudienceEntry
	radar    []radarport.AudienceFirstClick
	members  []hxcport.AudienceMemberFact
}

func (f legacyFacts) AudienceContacts(context.Context, time.Time) ([]wecomport.AudienceContact, error) {
	return f.contacts, nil
}
func (f legacyFacts) FirstCompleteAudienceChoices(context.Context, time.Time) ([]surveyport.AudienceChoiceAnswer, error) {
	return f.survey, nil
}
func (f legacyFacts) PaidAudienceOrders(context.Context, time.Time) ([]orderport.PaidAudienceOrder, error) {
	return f.orders, nil
}
func (f legacyFacts) AudienceEntries(context.Context, time.Time) ([]channelport.AudienceEntry, error) {
	return f.channels, nil
}
func (f legacyFacts) AudienceFirstClicks(context.Context, time.Time) ([]radarport.AudienceFirstClick, error) {
	return f.radar, nil
}
func (f legacyFacts) AudienceMemberFacts(context.Context, time.Time) ([]hxcport.AudienceMemberFact, error) {
	return f.members, nil
}

func legacyDefinition(t *testing.T, template segmentdsl.Template, params string) segmentport.Definition {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(params), &raw); err != nil {
		t.Fatal(err)
	}
	expression, err := json.Marshal(segmentdsl.AST{SchemaVersion: 1, Template: template, Parameters: raw})
	if err != nil {
		t.Fatal(err)
	}
	return segmentport.Definition{SchemaVersion: 1, Expression: expression}
}

func assertAudienceIDs(t *testing.T, result segmentport.Evaluation, want ...customerdomain.CustomerID) {
	t.Helper()
	if len(result.CustomerIDs) != len(want) {
		t.Fatalf("customers=%v want=%v", result.CustomerIDs, want)
	}
	seen := map[customerdomain.CustomerID]bool{}
	for _, id := range result.CustomerIDs {
		seen[id] = true
	}
	for _, id := range want {
		if !seen[id] {
			t.Fatalf("customers=%v want=%v", result.CustomerIDs, want)
		}
	}
}

func TestLegacyTemplateSourcesEvaluateFrozenConditions(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	paidInside := at.Add(-2 * time.Hour)
	used := at.Add(-time.Hour)
	expires := at.Add(24 * time.Hour)
	facts := legacyFacts{
		contacts: []wecomport.AudienceContact{
			{CustomerID: 1, OwnerUserID: "staff-a", Status: "active"},
			{CustomerID: 2, OwnerUserID: "staff-b", Status: "active"},
			{CustomerID: 3, OwnerUserID: "staff-a", Status: "deleted"},
		},
		survey: []surveyport.AudienceChoiceAnswer{
			{CustomerID: 1, QuestionnaireID: 9, StaffID: "staff-a", QuestionID: 11, OptionIDs: []surveyport.ID{101}},
			{CustomerID: 1, QuestionnaireID: 9, StaffID: "staff-a", QuestionID: 12, OptionIDs: []surveyport.ID{202}},
			{CustomerID: 2, QuestionnaireID: 9, StaffID: "staff-b", QuestionID: 11, OptionIDs: []surveyport.ID{102}},
		},
		orders: []orderport.PaidAudienceOrder{
			{CustomerID: 1, ProductCode: "paid-course", PaidAt: &paidInside},
			{CustomerID: 2, ProductCode: "paid-course", PaidAt: &paidInside},
			{CustomerID: 3, ProductCode: "paid-course"},
		},
		channels: []channelport.AudienceEntry{{CustomerID: 1, ChannelID: 7, OwnerReference: "staff-a", LastEnteredAt: at.Add(-48 * time.Hour)}},
		radar:    []radarport.AudienceFirstClick{{CustomerID: 1, RadarID: 8, FirstClickedAt: at.Add(-72 * time.Hour)}},
		members:  []hxcport.AudienceMemberFact{{CustomerID: 1, Registered: true, Tier: "pro", Status: "active", ExpiresAt: &expires, LastUsedAt: &used}},
	}
	source := CustomerSource{UoW: passthroughUoW{}, Legacy: LegacyTemplateSource{Contacts: facts, Survey: facts, Orders: facts, Channels: facts, Radar: facts, Members: facts}}
	cases := []struct {
		name, parameters string
		template         segmentdsl.Template
		want             []customerdomain.CustomerID
	}{
		{"contact-registration", `{"owner_scope":"specified","owner_staff_ids":["staff-a"],"contact_statuses":["active"],"registration_status":"registered"}`, segmentdsl.WeComContactRegistration, []customerdomain.CustomerID{1}},
		// Question conditions use AND across questions and OR inside each option list.
		{"first-questionnaire-choice", `{"questionnaire_id":"9","conditions":[{"question_id":"11","option_ids":["101","102"]},{"question_id":"12","option_ids":["202"]}],"owner_scope":"specified","owner_staff_ids":["staff-a"]}`, segmentdsl.QuestionnaireChoiceAnswers, []customerdomain.CustomerID{1}},
		// A time window is [from,to); unknown PaidAt cannot enter it. Owner scope
		// remains effective even when the active-contact switch is false.
		{"paid-payer-window-owner", `{"product_codes":["paid-course"],"paid_at_from":"2026-09-05T09:00:00Z","paid_at_to":"2026-09-05T12:00:00Z","owner_scope":"specified","owner_staff_ids":["staff-a"],"require_active_wecom_contact":false}`, segmentdsl.PaidOrder, []customerdomain.CustomerID{1}},
		{"channel-entry", `{"channel_codes":["7"],"entered_days_min":2,"entered_days_max":3,"owner_scope":"specified","owner_staff_ids":["staff-a"],"require_active_wecom_contact":true}`, segmentdsl.ChannelEntry, []customerdomain.CustomerID{1}},
		// The source supplies the immutable first click, so a later click cannot
		// reset this three-day elapsed result.
		{"radar-first-click", `{"radar_ids":["8"],"elapsed_min":3,"elapsed_max":4,"elapsed_unit":"day","owner_scope":"specified","owner_staff_ids":["staff-a"]}`, segmentdsl.RadarFirstClickElapsed, []customerdomain.CustomerID{1}},
		{"member-real-usage", `{"owner_scope":"specified","owner_staff_ids":["staff-a"],"service_period":"active","registration_status":"registered","usage_status":"used","membership_tiers":["pro"],"membership_statuses":["active"]}`, segmentdsl.MemberUsageStatus, []customerdomain.CustomerID{1}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			result, err := source.Evaluate(context.Background(), legacyDefinition(t, tt.template, tt.parameters), at)
			if err != nil {
				t.Fatal(err)
			}
			assertAudienceIDs(t, result, tt.want...)
		})
	}
}
