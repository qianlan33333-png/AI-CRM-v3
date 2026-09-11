package adapter

import (
	"context"
	"encoding/json"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	"sort"
	"time"
)

// No Provider reads occur here: every negative membership condition requires
// an independently completed and fresh WeCom-owned snapshot.
func (s LegacyTemplateSource) memberExcludingGroupPaid(ctx context.Context, p map[string]json.RawMessage, at time.Time) ([]int64, error) {
	var chat string
	products, e := listParam(p, "excluded_product_codes")
	if e != nil || json.Unmarshal(p["exclude_group_chat"], &chat) != nil || chat == "" || s.Groups == nil || s.Orders == nil {
		return nil, ErrCustomerReadUnavailable
	}
	group, e := s.Groups.AudienceGroupMembership(ctx, s.PrimaryOwnerCorpScope, chat, at, 15*time.Minute)
	if e != nil || !group.Complete || group.UnresolvedCount != 0 || group.ObservedAt.IsZero() || group.ObservedAt.After(at) || at.Sub(group.ObservedAt) > 15*time.Minute {
		return nil, ErrCustomerReadUnavailable
	}
	contacts, e := s.contacts(ctx, at)
	if e != nil {
		return nil, e
	}
	eligible := contactsFor(contacts, []string{"active", "deleted"}, p)
	ids := make([]customerdomain.CustomerID, 0, len(eligible))
	for id := range eligible {
		ids = append(ids, customerdomain.CustomerID(id))
	}
	facts, e := s.memberFacts(ctx, ids)
	if e != nil {
		return nil, e
	}
	excluded := map[customerdomain.CustomerID]bool{}
	for _, id := range group.CustomerIDs {
		excluded[id] = true
	}
	orders, e := s.Orders.PaidAudienceOrders(ctx, at)
	if e != nil {
		return nil, e
	}
	for _, order := range orders {
		if contains(products, order.ProductCode) {
			excluded[order.CustomerID] = true
		}
	}
	out := map[int64]bool{}
	for id := range eligible {
		f, ok := facts[customerdomain.CustomerID(id)]
		if !ok || f.Availability != hxcport.SharedFactsAvailable || !f.MembershipRecordFound || f.MembershipSource == "" {
			continue
		}
		if f.ActiveAt(at) && !excluded[customerdomain.CustomerID(id)] {
			out[id] = true
		}
	}
	result := idsFrom(out)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}
