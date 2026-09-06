package main

import (
	"context"
	"errors"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// customerTagCommandGate is a Composition-only bridge across the stable Access,
// WeCom and Tag read ports. It returns no provider identifiers to Customer.
type customerTagCommandGate struct {
	uow           platformport.UnitOfWork
	corpID        string
	owners        wecomport.AudiencePrimaryOwnerReader
	staff         accessport.Repository
	relationships entrantRelationshipReader
	tags          tagport.ProviderTagBindingReader
	identities    identityport.ExternalIdentityValueReader
}

func (g customerTagCommandGate) FreezeTagCommandTarget(ctx context.Context, t customerport.TagCommandTarget) (customerport.FrozenTagCommandTarget, error) {
	var out customerport.FrozenTagCommandTarget
	err := g.uow.Within(ctx, func(tx context.Context) error {
		var user accessdomain.User
		var err error
		if t.StaffID > 0 {
			user, err = g.staff.UserByID(tx, t.StaffID, false)
		} else {
			owners, ownerErr := g.owners.AudiencePrimaryOwners(tx, []customerportCustomerID{t.CustomerID})
			if ownerErr != nil || len(owners) != 1 || owners[0].Status != "known" || owners[0].CorpScope != "wecom-corp:"+g.corpID || owners[0].OwnerUserID == "" {
				return errors.New("customer tag owner unavailable")
			}
			user, err = g.staff.UserByWeComUserID(tx, owners[0].OwnerUserID, false)
		}
		if err != nil || !user.Active || user.WeComUserID == "" {
			return errors.New("customer tag staff unavailable")
		}
		active, err := g.relationships.IsActive(tx, g.corpID, user.WeComUserID, t.CustomerID)
		if err != nil || !active {
			return errors.New("customer tag relationship unavailable")
		}
		external, found, identityErr := g.identities.VerifiedExternalIdentityValue(tx, t.CustomerID, identitydomain.KindWeComExternalUserID, "wecom-corp:"+g.corpID)
		if identityErr != nil || !found {
			return errors.New("customer tag identity unavailable")
		}
		add, ok := tagProviderIDs(tx, g.tags, t.AddTagIDs)
		if !ok {
			return errors.New("customer tag binding unavailable")
		}
		remove, ok := tagProviderIDs(tx, g.tags, t.RemoveTagIDs)
		if !ok {
			return errors.New("customer tag binding unavailable")
		}
		t.StaffID = user.ID
		out = customerport.FrozenTagCommandTarget{TagCommandTarget: t, BindingDigest: string(effectport.Hash("customer.tag.command.binding.v1", providerIDJoin(add), providerIDJoin(remove))), TargetDigest: string(effectport.Hash("customer.tag.command.target.v1", user.WeComUserID, external))}
		return nil
	})
	return out, err
}

// Type alias keeps the boundary explicit without making Customer import WeCom.
type customerportCustomerID = customerdomain.CustomerID

func tagProviderIDs(ctx context.Context, tags tagport.ProviderTagBindingReader, ids []int64) ([]string, bool) {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		value, found, err := tags.ProviderTagID(ctx, id)
		if err != nil || !found {
			return nil, false
		}
		out = append(out, value)
	}
	return out, true
}
func providerIDJoin(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += id
	}
	return out
}

// customerTagCommandReaderAdapter keeps Customer's transactional store behind
// its stable port while Outbound executes after EER committed.
type customerTagCommandSource interface {
	ReadTagCommandDispatch(context.Context, string) (customerport.TagCommandDispatch, error)
}
type customerTagCommandReaderAdapter struct {
	uow    platformport.UnitOfWork
	source customerTagCommandSource
}

func (a customerTagCommandReaderAdapter) ReadTagCommandDispatch(ctx context.Context, source string) (customerport.TagCommandDispatch, error) {
	var result customerport.TagCommandDispatch
	err := a.uow.Within(ctx, func(tx context.Context) error {
		var e error
		result, e = a.source.ReadTagCommandDispatch(tx, source)
		return e
	})
	return result, err
}

var _ customerport.TagCommandTargetGate = customerTagCommandGate{}
var _ customerport.TagCommandDispatchReader = customerTagCommandReaderAdapter{}
