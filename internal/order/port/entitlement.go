package port

import (
	"context"
	"time"
)

type Entitlement struct {
	ID               int64     `json:"id"`
	CustomerID       int64     `json:"customer_id,omitempty"`
	ServiceProductID int64     `json:"service_product_id"`
	ProductName      string    `json:"title"`
	LastOrderID      *int64    `json:"last_order_id,omitempty"`
	Status           string    `json:"status"`
	StartAt          time.Time `json:"start_at"`
	EndAt            time.Time `json:"end_at"`
	Remark           string    `json:"remark"`
	Version          int64     `json:"version"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type EntitlementPage struct {
	Items []Entitlement `json:"items"`
	Total int64         `json:"total"`
}

type RemarkCommand struct {
	EntitlementID   int64
	CustomerID      int64
	EmployeeID      string
	Remark          string
	ExpectedVersion int64
	IdempotencyKey  string
}

type EntitlementService interface {
	ListCustomerEntitlements(context.Context, int64, int32) (EntitlementPage, error)
	UpdateEntitlementRemark(context.Context, RemarkCommand) (Entitlement, error)
}

type HistoricalEntitlement struct {
	SourceSystem     string
	SourceKey        string
	CustomerID       int64
	ServiceProductID int64
	ProductName      string
	LastOrderID      *int64
	Status           string
	StartAt          time.Time
	EndAt            time.Time
	Remark           string
	SourceDigest     [32]byte
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type HistoricalEntitlementImporter interface {
	ImportHistoricalEntitlement(context.Context, HistoricalEntitlement) (Entitlement, bool, error)
}
