package port

import (
	"context"
	"time"
)

type AdminDistributor struct {
	ID                                            int64
	PublicNo, CustomerReference, AgreementVersion string
	Enabled, ReceiverReady                        bool
	ReceiverReason                                string
	RegisteredAt                                  time.Time
	Version                                       int64
}
type AdminOrder struct {
	AttributionID                                                                                     int64
	OrderReference                                                                                    string
	ItemLine                                                                                          int32
	ProductID                                                                                         int64
	ProductType, ProductName, DistributorPublicNo, QualificationState, QualificationEvidenceReference string
	PolicyVersion                                                                                     int64
	RateBasisPoints, WaitDays                                                                         int32
	PaidMinor                                                                                         int64
	Currency                                                                                          string
	AttributedAt                                                                                      time.Time
}
type AdminReconcileTarget string

const (
	AdminReconcileTargetNone     AdminReconcileTarget = ""
	AdminReconcileTargetSplit    AdminReconcileTarget = "split"
	AdminReconcileTargetUnfreeze AdminReconcileTarget = "unfreeze"
)

type AdminException struct {
	ExceptionID, CommissionID                                   int64
	DistributorPublicNo, OrderReference, Kind, Status           string
	UnpaidDueMinor, AlreadyPaidMinor, AmountMinor               int64
	Reason, PaymentInstructionReference                         string
	ReconcileTarget                                             AdminReconcileTarget
	CreatedAt, UpdatedAt                                        time.Time
	Version                                                     int64
	CanReconcile, CanRecordRecovery, CanRecordMerchantLiability bool
}
type AdminPage[T any] struct {
	Items      []T
	NextCursor string
}
type AdminExceptionCommand struct {
	ExceptionID, ExpectedVersion, AmountMinor             int64
	ActorScope, Reason, EvidenceReference, IdempotencyKey string
}
type AdminDistributionReadModel interface {
	ListAdminDistributors(context.Context, string, int32) (AdminPage[AdminDistributor], error)
	ListAdminOrders(context.Context, string, int32) (AdminPage[AdminOrder], error)
	ListAdminExceptions(context.Context, string, int32) (AdminPage[AdminException], error)
}
type AdminDistributionService interface {
	AdminDistributionReadModel
	DisableDistributor(context.Context, int64, int64, string, string) error
	EnableDistributor(context.Context, int64, string, string) error
	ReconcileException(context.Context, AdminExceptionCommand) error
	RecordRecovery(context.Context, AdminExceptionCommand) error
	RecordMerchantLiability(context.Context, AdminExceptionCommand) error
}
