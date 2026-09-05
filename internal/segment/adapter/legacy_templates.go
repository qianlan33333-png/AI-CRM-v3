package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strconv"
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

// LegacyTemplateSource composes Owner read Ports. It has no SQL, identity
// matching, Provider call, or customer write. Each condition fails closed when
// its factual Owner is unavailable.
type LegacyTemplateSource struct {
	Contacts wecomport.AudienceContactReader
	Survey   surveyport.AudienceChoiceAnswerReader
	Orders   orderport.PaidAudienceReader
	Channels channelport.AudienceEntryReader
	Radar    radarport.AudienceFirstClickReader
	Members  hxcport.AudienceMemberReader
	Owners   accessport.AudienceOwnerReferenceReader
}

func (s LegacyTemplateSource) Evaluate(ctx context.Context, definition segmentport.Definition, reference time.Time) (segmentport.Evaluation, error) {
	var ast segmentdsl.AST
	if json.Unmarshal(definition.Expression, &ast) != nil || ast.SchemaVersion != 1 || reference.IsZero() {
		return segmentport.Evaluation{}, ErrCustomerReadUnavailable
	}
	var ids []int64
	var err error
	if ast.Parameters, err = s.ownerReferences(ctx, ast.Parameters); err != nil {
		return segmentport.Evaluation{}, err
	}
	switch ast.Template {
	case segmentdsl.WeComContactRegistration:
		ids, err = s.wecom(ctx, ast.Parameters, reference)
	case segmentdsl.QuestionnaireChoiceAnswers:
		ids, err = s.questionnaire(ctx, ast.Parameters, reference)
	case segmentdsl.PaidOrder:
		ids, err = s.paid(ctx, ast.Parameters, reference)
	case segmentdsl.ChannelEntry:
		ids, err = s.channel(ctx, ast.Parameters, reference)
	case segmentdsl.RadarFirstClickElapsed:
		ids, err = s.radar(ctx, ast.Parameters, reference)
	case segmentdsl.MemberUsageStatus:
		ids, err = s.member(ctx, ast.Parameters, reference)
	default:
		return segmentport.Evaluation{}, ErrCustomerReadUnavailable
	}
	if err != nil {
		return segmentport.Evaluation{}, err
	}
	customers := make([]customerdomain.CustomerID, 0, len(ids))
	for _, id := range ids {
		customers = append(customers, customerdomain.CustomerID(id))
	}
	digest := sha256.Sum256([]byte(string(ast.Template) + "\x00" + reference.UTC().Format(time.RFC3339Nano)))
	return segmentport.Evaluation{CustomerIDs: customers, ReferenceAt: reference.UTC(), Watermarks: []segmentport.SourceWatermark{{Source: "owner.audience-facts.v1", AsOf: reference.UTC(), Fresh: true, SafeDigest: digest}}}, nil
}

func (s LegacyTemplateSource) ownerReferences(ctx context.Context, params map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	raw, exists := params["owner_staff_ids"]
	if !exists {
		return params, nil
	}
	var ids []string
	if json.Unmarshal(raw, &ids) != nil {
		return nil, ErrCustomerReadUnavailable
	}
	var scope string
	_ = json.Unmarshal(params["owner_scope"], &scope)
	needsResolver := false
	for _, value := range ids {
		if id, err := strconv.ParseInt(value, 10, 64); err == nil && id > 0 {
			needsResolver = true
		}
	}
	if s.Owners == nil && scope == "specified" && needsResolver {
		return nil, ErrCustomerReadUnavailable
	}
	if !needsResolver {
		return params, nil
	}
	if s.Owners == nil {
		return params, nil
	}
	values := []string{}
	for _, value := range ids {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id < 1 {
			continue
		}
		provider, found, err := s.Owners.AudienceOwnerUserID(ctx, accessport.StaffID(id))
		if err != nil {
			return nil, err
		}
		if found {
			values = append(values, provider)
		}
	}
	encoded, _ := json.Marshal(values)
	params["owner_staff_ids"] = encoded
	local, _ := json.Marshal(ids)
	params["owner_local_staff_ids"] = local
	return params, nil
}

// segmentport aliases CustomerID through customer/domain in current V3; keep
// conversion local for stable Owner-port callers.
func (s LegacyTemplateSource) contacts(ctx context.Context, reference time.Time) ([]wecomport.AudienceContact, error) {
	if s.Contacts == nil {
		return nil, ErrCustomerReadUnavailable
	}
	return s.Contacts.AudienceContacts(ctx, reference)
}
func (s LegacyTemplateSource) members(ctx context.Context, reference time.Time) ([]hxcport.AudienceMemberFact, error) {
	if s.Members == nil {
		return nil, ErrCustomerReadUnavailable
	}
	return s.Members.AudienceMemberFacts(ctx, reference)
}
func owner(params map[string]json.RawMessage, value string) bool {
	var scope string
	var values []string
	return json.Unmarshal(params["owner_scope"], &scope) == nil && json.Unmarshal(params["owner_staff_ids"], &values) == nil && (scope == "all" || contains(values, value))
}
func ownerScoped(params map[string]json.RawMessage) (bool, error) {
	var scope string
	if json.Unmarshal(params["owner_scope"], &scope) != nil || (scope != "all" && scope != "specified") {
		return false, ErrCustomerReadUnavailable
	}
	return scope == "specified", nil
}
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func contactsFor(contacts []wecomport.AudienceContact, statuses []string, params map[string]json.RawMessage) map[int64]bool {
	out := map[int64]bool{}
	for _, fact := range contacts {
		if contains(statuses, fact.Status) && owner(params, fact.OwnerUserID) {
			out[int64(fact.CustomerID)] = true
		}
	}
	return out
}
func listParam(params map[string]json.RawMessage, key string) ([]string, error) {
	var out []string
	if json.Unmarshal(params[key], &out) != nil {
		return nil, ErrCustomerReadUnavailable
	}
	return out, nil
}
func boolParam(params map[string]json.RawMessage, key string) (bool, error) {
	var out bool
	if json.Unmarshal(params[key], &out) != nil {
		return false, ErrCustomerReadUnavailable
	}
	return out, nil
}
func timeParam(params map[string]json.RawMessage, key string) (time.Time, error) {
	var raw string
	if json.Unmarshal(params[key], &raw) != nil || raw == "" {
		return time.Time{}, nil
	}
	out, e := time.Parse(time.RFC3339, raw)
	if e != nil {
		return time.Time{}, ErrCustomerReadUnavailable
	}
	return out, nil
}
func idsFrom(set map[int64]bool) []int64 {
	out := []int64{}
	for id := range set {
		out = append(out, id)
	}
	return out
}

func (s LegacyTemplateSource) wecom(ctx context.Context, p map[string]json.RawMessage, at time.Time) ([]int64, error) {
	contacts, e := s.contacts(ctx, at)
	if e != nil {
		return nil, e
	}
	statuses, e := listParam(p, "contact_statuses")
	if e != nil {
		return nil, e
	}
	var registration string
	if json.Unmarshal(p["registration_status"], &registration) != nil {
		return nil, ErrCustomerReadUnavailable
	}
	set := contactsFor(contacts, statuses, p)
	if registration == "any" {
		return idsFrom(set), nil
	}
	members, e := s.members(ctx, at)
	if e != nil {
		return nil, e
	}
	registered := map[int64]bool{}
	for _, fact := range members {
		registered[int64(fact.CustomerID)] = fact.Registered
	}
	for id := range set {
		if registered[id] != (registration == "registered") {
			delete(set, id)
		}
	}
	return idsFrom(set), nil
}
func (s LegacyTemplateSource) questionnaire(ctx context.Context, p map[string]json.RawMessage, at time.Time) ([]int64, error) {
	if s.Survey == nil {
		return nil, ErrCustomerReadUnavailable
	}
	facts, e := s.Survey.FirstCompleteAudienceChoices(ctx, at)
	if e != nil {
		return nil, e
	}
	var questionnaire string
	if json.Unmarshal(p["questionnaire_id"], &questionnaire) != nil {
		return nil, ErrCustomerReadUnavailable
	}
	var conditions []struct {
		QuestionID string   `json:"question_id"`
		OptionIDs  []string `json:"option_ids"`
	}
	if json.Unmarshal(p["conditions"], &conditions) != nil {
		return nil, ErrCustomerReadUnavailable
	}
	matched := map[int64]map[string]bool{}
	for _, fact := range facts {
		if strconv.FormatInt(int64(fact.QuestionnaireID), 10) != questionnaire || !owner(p, fact.StaffID) {
			continue
		}
		for _, condition := range conditions {
			if strconv.FormatInt(int64(fact.QuestionID), 10) != condition.QuestionID {
				continue
			}
			for _, option := range fact.OptionIDs {
				if contains(condition.OptionIDs, strconv.FormatInt(int64(option), 10)) {
					if matched[int64(fact.CustomerID)] == nil {
						matched[int64(fact.CustomerID)] = map[string]bool{}
					}
					matched[int64(fact.CustomerID)][condition.QuestionID] = true
				}
			}
		}
	}
	out := map[int64]bool{}
	for customer, answers := range matched {
		if len(answers) == len(conditions) {
			out[customer] = true
		}
	}
	return idsFrom(out), nil
}
func (s LegacyTemplateSource) paid(ctx context.Context, p map[string]json.RawMessage, at time.Time) ([]int64, error) {
	if s.Orders == nil {
		return nil, ErrCustomerReadUnavailable
	}
	products, e := listParam(p, "product_codes")
	if e != nil {
		return nil, e
	}
	from, e := timeParam(p, "paid_at_from")
	if e != nil {
		return nil, e
	}
	to, e := timeParam(p, "paid_at_to")
	if e != nil {
		return nil, e
	}
	orders, e := s.Orders.PaidAudienceOrders(ctx, at)
	if e != nil {
		return nil, e
	}
	require, e := boolParam(p, "require_active_wecom_contact")
	if e != nil {
		return nil, e
	}
	scoped, e := ownerScoped(p)
	if e != nil {
		return nil, e
	}
	eligible := map[int64]bool{}
	if require {
		contacts, e := s.contacts(ctx, at)
		if e != nil {
			return nil, e
		}
		statuses := []string{"active", "deleted"}
		if require {
			statuses = []string{"active"}
		}
		eligible = contactsFor(contacts, statuses, p)
	}
	out := map[int64]bool{}
	for _, fact := range orders {
		if !contains(products, fact.ProductCode) || ((!from.IsZero() || !to.IsZero()) && fact.PaidAt == nil) || (fact.PaidAt != nil && !from.IsZero() && fact.PaidAt.Before(from)) || (fact.PaidAt != nil && !to.IsZero() && !fact.PaidAt.Before(to)) {
			continue
		}
		id := int64(fact.CustomerID)
		if scoped && !owner(p, fact.OwnerReference) {
			continue
		}
		if !require || eligible[id] {
			out[id] = true
		}
	}
	return idsFrom(out), nil
}
func (s LegacyTemplateSource) channel(ctx context.Context, p map[string]json.RawMessage, at time.Time) ([]int64, error) {
	if s.Channels == nil {
		return nil, ErrCustomerReadUnavailable
	}
	channels, e := listParam(p, "channel_codes")
	if e != nil {
		return nil, e
	}
	var minimum int
	var maximum *int
	if json.Unmarshal(p["entered_days_min"], &minimum) != nil || json.Unmarshal(p["entered_days_max"], &maximum) != nil {
		return nil, ErrCustomerReadUnavailable
	}
	require, e := boolParam(p, "require_active_wecom_contact")
	if e != nil {
		return nil, e
	}
	eligible := map[int64]bool{}
	if require {
		contacts, e := s.contacts(ctx, at)
		if e != nil {
			return nil, e
		}
		eligible = contactsFor(contacts, []string{"active"}, p)
	}
	facts, e := s.Channels.AudienceEntries(ctx, at)
	if e != nil {
		return nil, e
	}
	out := map[int64]bool{}
	localOwners, _ := listParam(p, "owner_local_staff_ids")
	scoped, e := ownerScoped(p)
	if e != nil {
		return nil, e
	}
	for _, fact := range facts {
		age := int(at.Sub(fact.LastEnteredAt).Hours() / 24)
		ownerMatch := !scoped || owner(p, fact.OwnerReference)
		if scoped && fact.OwnerStaffID != nil {
			ownerMatch = contains(localOwners, strconv.FormatInt(*fact.OwnerStaffID, 10))
		}
		if !contains(channels, fact.ChannelCode) || age < minimum || (maximum != nil && age >= *maximum) || !ownerMatch {
			continue
		}
		id := int64(fact.CustomerID)
		if !require || eligible[id] {
			out[id] = true
		}
	}
	return idsFrom(out), nil
}
func (s LegacyTemplateSource) radar(ctx context.Context, p map[string]json.RawMessage, at time.Time) ([]int64, error) {
	if s.Radar == nil {
		return nil, ErrCustomerReadUnavailable
	}
	radars, e := listParam(p, "radar_ids")
	if e != nil {
		return nil, e
	}
	var minimum int
	var maximum *int
	var unit string
	if json.Unmarshal(p["elapsed_min"], &minimum) != nil || json.Unmarshal(p["elapsed_max"], &maximum) != nil || json.Unmarshal(p["elapsed_unit"], &unit) != nil {
		return nil, ErrCustomerReadUnavailable
	}
	scoped, e := ownerScoped(p)
	if e != nil {
		return nil, e
	}
	eligible := map[int64]bool{}
	if scoped {
		contacts, e := s.contacts(ctx, at)
		if e != nil {
			return nil, e
		}
		eligible = contactsFor(contacts, []string{"active", "deleted"}, p)
	}
	facts, e := s.Radar.AudienceFirstClicks(ctx, at)
	if e != nil {
		return nil, e
	}
	scale := time.Hour
	if unit == "day" {
		scale = 24 * time.Hour
	}
	out := map[int64]bool{}
	for _, fact := range facts {
		elapsed := int(at.Sub(fact.FirstClickedAt) / scale)
		id := int64(fact.CustomerID)
		if contains(radars, strconv.FormatInt(fact.RadarID, 10)) && elapsed >= minimum && (maximum == nil || elapsed < *maximum) && (!scoped || eligible[id]) {
			out[id] = true
		}
	}
	return idsFrom(out), nil
}
func (s LegacyTemplateSource) member(ctx context.Context, p map[string]json.RawMessage, at time.Time) ([]int64, error) {
	facts, e := s.members(ctx, at)
	if e != nil {
		return nil, e
	}
	var period, registration, usage string
	var tiers, statuses []string
	if json.Unmarshal(p["service_period"], &period) != nil || json.Unmarshal(p["registration_status"], &registration) != nil || json.Unmarshal(p["usage_status"], &usage) != nil || json.Unmarshal(p["membership_tiers"], &tiers) != nil || json.Unmarshal(p["membership_statuses"], &statuses) != nil {
		return nil, ErrCustomerReadUnavailable
	}
	contacts, e := s.contacts(ctx, at)
	if e != nil {
		return nil, e
	}
	eligible := contactsFor(contacts, []string{"active", "deleted"}, p)
	out := map[int64]bool{}
	for _, fact := range facts {
		id := int64(fact.CustomerID)
		active := fact.IsMember && fact.Status == "active"
		used := fact.LastUsedAt != nil
		if !eligible[id] || (period == "active" && !active) || (period == "expired" && active) || (registration == "registered" && !fact.Registered) || (registration == "unregistered" && fact.Registered) || (usage == "used" && !used) || (usage == "unused" && used) || (len(tiers) > 0 && !contains(tiers, fact.Tier)) || (len(statuses) > 0 && !contains(statuses, fact.Status)) {
			continue
		}
		out[id] = true
	}
	return idsFrom(out), nil
}

var _ segmentport.DefinitionSource = LegacyTemplateSource{}
