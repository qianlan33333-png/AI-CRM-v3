package main

import (
	"context"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type orderCustomerDisplayNameAdapter struct {
	uow    platformport.UnitOfWork
	reader customerport.DirectoryDisplayNameReader
}

func (adapter orderCustomerDisplayNameAdapter) DisplayNames(ctx context.Context, ids []customerdomain.CustomerID) (map[customerdomain.CustomerID]string, error) {
	var result map[customerdomain.CustomerID]string
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var readErr error
		result, readErr = adapter.reader.DisplayNames(tx, ids)
		return readErr
	})
	return result, err
}
