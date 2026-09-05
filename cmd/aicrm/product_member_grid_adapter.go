package main

import (
	"context"
	"errors"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessport "github.com/qianlan33333-png/AI-CRM-v3/internal/access/port"
)

// productMemberGridStaffDirectory is the composition-only bridge to Access.
// It verifies a collaborator target is an active staff record before Product
// persists its local metadata; Product never queries access tables itself.
type productMemberGridStaffDirectory struct{ users accessport.Repository }

func (adapter productMemberGridStaffDirectory) ActiveMemberGridStaff(ctx context.Context, id int64) (bool, error) {
	if adapter.users == nil || id < 1 {
		return false, nil
	}
	user, err := adapter.users.UserByID(ctx, id, false)
	if errors.Is(err, accessdomain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return user.Active, nil
}
