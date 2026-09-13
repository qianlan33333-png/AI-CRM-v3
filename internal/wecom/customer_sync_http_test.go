package wecom

import (
	"testing"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

func TestCustomerSyncMayWriteRequiresAdminKindAndDailyRole(t *testing.T) {
	tests := []struct {
		name      string
		principal accessdomain.Principal
		want      bool
	}{
		{name: "super administrator", principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}, want: true},
		{name: "administrator", principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 2, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, want: true},
		{name: "viewer", principal: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 3, Roles: []accessdomain.Role{accessdomain.RoleViewer}}},
		{name: "staff cannot borrow admin role", principal: accessdomain.Principal{Kind: accessdomain.KindStaff, InternalID: 4, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := customerSyncMayWrite(test.principal); got != test.want {
				t.Fatalf("customerSyncMayWrite=%t want=%t", got, test.want)
			}
		})
	}
}
