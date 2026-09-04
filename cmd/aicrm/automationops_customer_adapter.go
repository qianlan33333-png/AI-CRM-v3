package main

import (
	"context"
	"errors"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
)

// automationOpsCanonicalCustomers follows Customer-owned canonical roots one
// ID at a time. It has no provisioning, identity binding or merge capability.
type automationOpsCanonicalCustomers struct {
	resolver customerport.CanonicalCustomerResolver
}

func (adapter automationOpsCanonicalCustomers) CanonicalCustomers(ctx context.Context, ids []customerdomain.CustomerID) ([]customerdomain.CustomerID, error) {
	if adapter.resolver == nil {
		return nil, errors.New("canonical customer reader unavailable")
	}
	result := make([]customerdomain.CustomerID, 0, len(ids))
	for _, id := range ids {
		canonical, err := adapter.resolver.ResolveCanonicalCustomer(ctx, id)
		if err != nil || canonical.CustomerID < 1 {
			return nil, errors.New("canonical customer resolution failed")
		}
		result = append(result, canonical.CustomerID)
	}
	return result, nil
}
