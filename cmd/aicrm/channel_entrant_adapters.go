package main

import (
	"context"
	"errors"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

type entrantActionSource interface {
	ReadPublishedEntrantAction(context.Context, string) (channelport.PublishedEntrantAction, error)
}
type channelEntrantActionReaderAdapter struct {
	uow    platformport.UnitOfWork
	source entrantActionSource
}

func (adapter channelEntrantActionReaderAdapter) ReadPublishedEntrantAction(ctx context.Context, source string) (channelport.PublishedEntrantAction, error) {
	var result channelport.PublishedEntrantAction
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = adapter.source.ReadPublishedEntrantAction(tx, source)
		return readErr
	})
	return result, err
}

type entrantStaffReader interface {
	UserByID(context.Context, int64, bool) (accessdomain.User, error)
}
type entrantRelationshipReader interface {
	IsActive(context.Context, string, string, customerdomain.CustomerID) (bool, error)
}
type channelCurrentContactAdapter struct {
	uow           platformport.UnitOfWork
	corpID        string
	staff         entrantStaffReader
	relationships entrantRelationshipReader
	identities    identityport.ExternalIdentityValueReader
}

func (adapter channelCurrentContactAdapter) CurrentExternalContact(ctx context.Context, customerID customerdomain.CustomerID, staffID int64) (wecomport.CurrentExternalContact, error) {
	var result wecomport.CurrentExternalContact
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		user, err := adapter.staff.UserByID(tx, staffID, false)
		if err != nil || !user.Active || user.WeComUserID == "" {
			return errors.New("current channel staff unavailable")
		}
		active, err := adapter.relationships.IsActive(tx, adapter.corpID, user.WeComUserID, customerID)
		if err != nil || !active {
			return errors.New("current WeCom relationship unavailable")
		}
		value, found, err := adapter.identities.VerifiedExternalIdentityValue(tx, customerID, identitydomain.KindWeComExternalUserID, "wecom-corp:"+adapter.corpID)
		if err != nil || !found {
			return errors.New("current WeCom identity unavailable")
		}
		result = wecomport.CurrentExternalContact{EmployeeUserID: user.WeComUserID, ExternalUserID: value}
		return nil
	})
	return result, err
}

type channelProviderTagAdapter struct {
	uow  platformport.UnitOfWork
	tags tagport.ProviderTagBindingReader
}

func (adapter channelProviderTagAdapter) ProviderTagID(ctx context.Context, tagID int64) (string, bool, error) {
	var value string
	var found bool
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		value, found, readErr = adapter.tags.ProviderTagID(tx, tagID)
		return readErr
	})
	return value, found, err
}

var _ channelport.PublishedEntrantActionReader = channelEntrantActionReaderAdapter{}
var _ wecomport.CurrentExternalContactReader = channelCurrentContactAdapter{}
var _ tagport.ProviderTagBindingReader = channelProviderTagAdapter{}
var _ wecom.WelcomeGrantRedeemer = (*wecom.PostgreSQLWelcomeGrantStore)(nil)
