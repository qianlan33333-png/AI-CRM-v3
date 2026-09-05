package adapter

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
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

func (legacyFacts) AudienceOwnerUserID(_ context.Context, id accessport.StaffID) (string, bool, error) {
	switch id {
	case 9:
		return "bob", true, nil
	case 19:
		return "staff-a", true, nil
	case 20:
		return "staff-b", true, nil
	}
	return "", false, nil
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

type primaryOwnerFacts []wecomport.AudiencePrimaryOwner

func (f primaryOwnerFacts) AudiencePrimaryOwners(context.Context, []customerdomain.CustomerID) ([]wecomport.AudiencePrimaryOwner, error) {
	return f, nil
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
			{CustomerID: 1, ProductCode: "paid-course", OwnerReference: "staff-a", PaidAt: &paidInside},
			{CustomerID: 2, ProductCode: "paid-course", OwnerReference: "staff-b", PaidAt: &paidInside},
			{CustomerID: 3, ProductCode: "paid-course"},
		},
		channels: []channelport.AudienceEntry{{CustomerID: 1, ChannelID: 7, ChannelCode: "channel-7", OwnerReference: "staff-a", LastEnteredAt: at.Add(-48 * time.Hour)}},
		radar:    []radarport.AudienceFirstClick{{CustomerID: 1, RadarID: 8, FirstClickedAt: at.Add(-72 * time.Hour), OwnerUserID: "staff-a"}},
		members:  []hxcport.AudienceMemberFact{{CustomerID: 1, Registered: true, IsMember: true, Tier: "pro", Status: "active", ExpiresAt: &expires, LastUsedAt: &used}},
	}
	source := CustomerSource{UoW: passthroughUoW{}, Legacy: LegacyTemplateSource{Contacts: facts, Survey: facts, Orders: facts, Channels: facts, Radar: facts, Members: facts, Owners: facts}}
	cases := []struct {
		name, parameters string
		template         segmentdsl.Template
		want             []customerdomain.CustomerID
	}{
		{"contact-registration", `{"owner_scope":"specified","owner_staff_ids":["19"],"contact_statuses":["active"],"registration_status":"registered"}`, segmentdsl.WeComContactRegistration, []customerdomain.CustomerID{1}},
		// Question conditions use AND across questions and OR inside each option list.
		{"first-questionnaire-choice", `{"questionnaire_id":"9","conditions":[{"question_id":"11","option_ids":["101","102"]},{"question_id":"12","option_ids":["202"]}],"owner_scope":"specified","owner_staff_ids":["19"]}`, segmentdsl.QuestionnaireChoiceAnswers, []customerdomain.CustomerID{1}},
		// A time window is [from,to); unknown PaidAt cannot enter it. Owner scope
		// remains effective even when the active-contact switch is false.
		{"paid-payer-window-owner", `{"product_codes":["paid-course"],"paid_at_from":"2026-09-05T09:00:00Z","paid_at_to":"2026-09-05T12:00:00Z","owner_scope":"specified","owner_staff_ids":["19"],"require_active_wecom_contact":false}`, segmentdsl.PaidOrder, []customerdomain.CustomerID{1}},
		{"channel-entry", `{"channel_codes":["channel-7"],"entered_days_min":2,"entered_days_max":3,"owner_scope":"specified","owner_staff_ids":["19"],"require_active_wecom_contact":true}`, segmentdsl.ChannelEntry, []customerdomain.CustomerID{1}},
		// The source supplies the immutable first click, so a later click cannot
		// reset this three-day elapsed result.
		{"radar-first-click", `{"radar_ids":["8"],"elapsed_min":3,"elapsed_max":4,"elapsed_unit":"day","owner_scope":"specified","owner_staff_ids":["19"]}`, segmentdsl.RadarFirstClickElapsed, []customerdomain.CustomerID{1}},
		{"member-real-usage", `{"owner_scope":"specified","owner_staff_ids":["19"],"service_period":"active","registration_status":"registered","usage_status":"used","membership_tiers":["pro"],"membership_statuses":["active"]}`, segmentdsl.MemberUsageStatus, []customerdomain.CustomerID{1}},
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

func TestLegacyTemplateSourceDoesNotBorrowCurrentContactOwnerOrExpireMembershipByGuess(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	facts := legacyFacts{contacts: []wecomport.AudienceContact{{CustomerID: 9, OwnerUserID: "staff-a", Status: "active"}}, orders: []orderport.PaidAudienceOrder{{CustomerID: 9, ProductCode: "course"}}, members: []hxcport.AudienceMemberFact{{CustomerID: 9, Registered: true, IsMember: false, Tier: "pro", Status: "expired"}}}
	source := LegacyTemplateSource{Contacts: facts, Orders: facts, Members: facts, Owners: facts}
	paid, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.PaidOrder, `{"product_codes":["course"],"paid_at_from":"","paid_at_to":"","owner_scope":"specified","owner_staff_ids":["19"],"require_active_wecom_contact":false}`), at)
	if err != nil || len(paid.CustomerIDs) != 0 {
		t.Fatalf("paid=%+v err=%v", paid, err)
	}
	active, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.MemberUsageStatus, `{"owner_scope":"specified","owner_staff_ids":["19"],"service_period":"active","registration_status":"registered","usage_status":"any","membership_tiers":[],"membership_statuses":[]}`), at)
	if err != nil || len(active.CustomerIDs) != 0 {
		t.Fatalf("active=%+v err=%v", active, err)
	}
}

func TestRadarFirstClickUsesHistoricalOwnerAndFailsClosedWhenAbsent(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	facts := legacyFacts{
		// The current contact belongs to staff-a. The first click was recorded
		// for staff-b and therefore must not be reassigned from this relation.
		contacts: []wecomport.AudienceContact{{CustomerID: 1, OwnerUserID: "staff-a", Status: "active"}},
		radar: []radarport.AudienceFirstClick{
			{CustomerID: 1, RadarID: 8, FirstClickEventID: 10, FirstClickedAt: at.Add(-72 * time.Hour), OwnerUserID: "staff-b"},
			{CustomerID: 2, RadarID: 8, FirstClickEventID: 11, FirstClickedAt: at.Add(-72 * time.Hour), OwnerUserID: ""},
		},
	}
	source := LegacyTemplateSource{Contacts: facts, Radar: facts, Owners: facts}
	all, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.RadarFirstClickElapsed, `{"radar_ids":["8"],"elapsed_min":3,"elapsed_max":4,"elapsed_unit":"day","owner_scope":"all","owner_staff_ids":[]}`), at)
	if err != nil {
		t.Fatal(err)
	}
	assertAudienceIDs(t, all, 1, 2)
	staffA, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.RadarFirstClickElapsed, `{"radar_ids":["8"],"elapsed_min":3,"elapsed_max":4,"elapsed_unit":"day","owner_scope":"specified","owner_staff_ids":["19"]}`), at)
	if err != nil || len(staffA.CustomerIDs) != 0 {
		t.Fatalf("staff-a result=%+v err=%v", staffA, err)
	}
	staffB, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.RadarFirstClickElapsed, `{"radar_ids":["8"],"elapsed_min":3,"elapsed_max":4,"elapsed_unit":"day","owner_scope":"specified","owner_staff_ids":["20"]}`), at)
	if err != nil {
		t.Fatal(err)
	}
	assertAudienceIDs(t, staffB, 1)
}

func TestRadarFirstClickPrefersTrustedPrimaryOwnerOverHistoricalFallback(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	facts := legacyFacts{radar: []radarport.AudienceFirstClick{
		{CustomerID: 1, RadarID: 8, FirstClickedAt: at.Add(-72 * time.Hour), OwnerUserID: "staff-b"},
		{CustomerID: 2, RadarID: 8, FirstClickedAt: at.Add(-72 * time.Hour)},
	}}
	source := LegacyTemplateSource{Radar: facts, Owners: facts, PrimaryOwners: primaryOwnerFacts{
		{CustomerID: 1, CorpScope: "wecom-corp:test", OwnerUserID: "staff-a", Status: "known"},
		{CustomerID: 2, CorpScope: "wecom-corp:test", Status: "ambiguous"},
	}}
	staffA, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.RadarFirstClickElapsed, `{"radar_ids":["8"],"elapsed_min":3,"elapsed_max":4,"elapsed_unit":"day","owner_scope":"specified","owner_staff_ids":["19"]}`), at)
	if err != nil {
		t.Fatal(err)
	}
	assertAudienceIDs(t, staffA, 1)
	staffB, err := source.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.RadarFirstClickElapsed, `{"radar_ids":["8"],"elapsed_min":3,"elapsed_max":4,"elapsed_unit":"day","owner_scope":"specified","owner_staff_ids":["20"]}`), at)
	if err != nil || len(staffB.CustomerIDs) != 0 {
		t.Fatalf("staff-b result=%+v err=%v", staffB, err)
	}
}

func TestChannelOwnerStaffIDDoesNotCollideWithProviderUserID(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	nine := int64(9)
	facts := legacyFacts{channels: []channelport.AudienceEntry{{CustomerID: 1, ChannelCode: "c", OwnerStaffID: &nine, LastEnteredAt: at.Add(-24 * time.Hour)}, {CustomerID: 2, ChannelCode: "c", OwnerReference: "9", LastEnteredAt: at.Add(-24 * time.Hour)}}}
	s := LegacyTemplateSource{Channels: facts, Owners: facts}
	r, err := s.Evaluate(context.Background(), legacyDefinition(t, segmentdsl.ChannelEntry, `{"channel_codes":["c"],"entered_days_min":1,"entered_days_max":2,"owner_scope":"specified","owner_staff_ids":["9"],"require_active_wecom_contact":false}`), at)
	if err != nil || len(r.CustomerIDs) != 1 || r.CustomerIDs[0] != 1 {
		t.Fatalf("result=%+v err=%v", r, err)
	}
}

func TestLegacyTemplateOwnerReferencesRejectNonCanonicalIDsAndNeedAccess(t *testing.T) {
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	facts := legacyFacts{channels: []channelport.AudienceEntry{{CustomerID: 1, ChannelCode: "c", LastEnteredAt: at.Add(-24 * time.Hour)}}}
	withOwners := LegacyTemplateSource{Channels: facts, Owners: facts}
	for _, ids := range []string{
		`["bob"]`, `["09"]`, `["0"]`, `["-1"]`, `[""]`, `["9223372036854775808"]`, `["19","bob"]`,
	} {
		t.Run(ids, func(t *testing.T) {
			definition := legacyDefinition(t, segmentdsl.ChannelEntry, `{"channel_codes":["c"],"entered_days_min":1,"entered_days_max":2,"owner_scope":"specified","owner_staff_ids":`+ids+`,"require_active_wecom_contact":false}`)
			if _, err := withOwners.Evaluate(context.Background(), definition, at); err == nil {
				t.Fatalf("non-canonical owner ids accepted: %s", ids)
			}
		})
	}
	definition := legacyDefinition(t, segmentdsl.ChannelEntry, `{"channel_codes":["c"],"entered_days_min":1,"entered_days_max":2,"owner_scope":"specified","owner_staff_ids":["19"],"require_active_wecom_contact":false}`)
	if _, err := (LegacyTemplateSource{Channels: facts}).Evaluate(context.Background(), definition, at); err == nil {
		t.Fatal("specified owner succeeded without Access reader")
	}
	allDefinition := legacyDefinition(t, segmentdsl.ChannelEntry, `{"channel_codes":["c"],"entered_days_min":1,"entered_days_max":2,"owner_scope":"all","owner_staff_ids":[],"require_active_wecom_contact":false}`)
	result, err := (LegacyTemplateSource{Channels: facts}).Evaluate(context.Background(), allDefinition, at)
	if err != nil {
		t.Fatal(err)
	}
	assertAudienceIDs(t, result, 1)
}
