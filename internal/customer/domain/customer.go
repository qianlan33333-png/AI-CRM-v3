// Package domain contains the minimal channel-neutral Customer root.
package domain

import "errors"

var ErrInvalidCustomer = errors.New("invalid customer")

type CustomerID int64
type Status string

const (
	StatusActive Status = "active"
	StatusMerged Status = "merged"
	StatusClosed Status = "closed"
)

type Customer struct {
	ID     CustomerID
	Status Status
}

func (customer Customer) Validate() error {
	if customer.ID < 1 {
		return ErrInvalidCustomer
	}
	switch customer.Status {
	case StatusActive, StatusMerged, StatusClosed:
		return nil
	default:
		return ErrInvalidCustomer
	}
}
