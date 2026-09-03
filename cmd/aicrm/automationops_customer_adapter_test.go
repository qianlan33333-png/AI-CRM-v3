package main

import (
	"context"
	"testing"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
)

type canonicalReaderStub struct {
	roots map[customerdomain.CustomerID]customerdomain.CustomerID
}

func (s canonicalReaderStub) ResolveCanonicalCustomer(_ context.Context, id customerdomain.CustomerID) (customerport.CanonicalCustomer, error) {
	return customerport.CanonicalCustomer{RequestedCustomerID: id, CustomerID: s.roots[id], Merged: s.roots[id] != id}, nil
}

func TestAutomationOpsCanonicalAdapterOnlyReturnsCustomerOwnedRoots(t *testing.T) {
	adapter := automationOpsCanonicalCustomers{resolver: canonicalReaderStub{roots: map[customerdomain.CustomerID]customerdomain.CustomerID{2: 9, 3: 3}}}
	got, err := adapter.CanonicalCustomers(context.Background(), []customerdomain.CustomerID{2, 3})
	if err != nil || len(got) != 2 || got[0] != 9 || got[1] != 3 {
		t.Fatalf("got=%v err=%v", got, err)
	}
}
