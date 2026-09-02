// Package domain contains immutable order and money values.
package domain

import (
	"errors"
	"strings"
)

type Status string

const (
	StatusCreated   Status = "created"
	StatusPaying    Status = "paying"
	StatusPaid      Status = "paid"
	StatusCancelled Status = "cancelled"
)

type Money struct {
	AmountMinor int64
	Currency    string
}

var ErrInvalidMoney = errors.New("invalid money")

func NewMoney(amountMinor int64, currency string) (Money, error) {
	normalizedCurrency := strings.ToUpper(strings.TrimSpace(currency))
	if amountMinor < 0 || len(normalizedCurrency) != 3 {
		return Money{}, ErrInvalidMoney
	}
	return Money{AmountMinor: amountMinor, Currency: normalizedCurrency}, nil
}
