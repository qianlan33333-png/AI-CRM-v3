package port

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrHistoryInvalid     = errors.New("invalid Group Ops history")
	ErrHistoryConflict    = errors.New("Group Ops history conflict")
	ErrHistoryUnavailable = errors.New("Group Ops history unavailable")
)

type HistoricalPlan struct {
	Plan
	SourcePlanID   int64      `json:"source_plan_id"`
	SourceCode     string     `json:"source_code"`
	PlanType       string     `json:"plan_type"`
	OriginalStatus string     `json:"original_status"`
	OwnerStaffID   *int64     `json:"owner_staff_id"`
	ArchivedAt     *time.Time `json:"archived_at"`
}

type HistoricalDirectory struct {
	ID                  int64     `json:"id"`
	SourceKind          string    `json:"source_kind"`
	SourceID            *int64    `json:"source_id"`
	ChatReference       string    `json:"chat_reference"`
	DisplayName         *string   `json:"display_name"`
	OwnerStaffID        *int64    `json:"owner_staff_id"`
	OwnerName           *string   `json:"owner_name"`
	MemberCount         *int32    `json:"member_count"`
	InternalMemberCount *int32    `json:"internal_member_count"`
	ExternalMemberCount *int32    `json:"external_member_count"`
	OriginalStatus      string    `json:"original_status"`
	RecordedAt          time.Time `json:"recorded_at"`
}

type HistoricalGroup struct {
	ID                  int64      `json:"id"`
	SourceGroupID       int64      `json:"source_group_id"`
	SourcePlanID        int64      `json:"source_plan_id"`
	PlanID              int64      `json:"plan_id,string"`
	ChatReference       string     `json:"chat_reference"`
	DisplayName         string     `json:"display_name"`
	OwnerStaffID        *int64     `json:"owner_staff_id"`
	InternalMemberCount int32      `json:"internal_member_count"`
	ExternalMemberCount int32      `json:"external_member_count"`
	OriginalStatus      string     `json:"original_status"`
	CreatedAt           time.Time  `json:"created_at"`
	RemovedAt           *time.Time `json:"removed_at"`
}

type HistoricalNode struct {
	ID             int64           `json:"id"`
	SourceNodeID   int64           `json:"source_node_id"`
	SourcePlanID   int64           `json:"source_plan_id"`
	PlanID         int64           `json:"plan_id,string"`
	DayIndex       int32           `json:"day_index"`
	TriggerTime    string          `json:"trigger_time"`
	SortOrder      int32           `json:"sort_order"`
	OriginalStatus string          `json:"original_status"`
	ContentPackage json.RawMessage `json:"content_package"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type HistoricalReceipt struct {
	SourceIdentifier string
	PayloadDigest    [sha256.Size]byte
	TargetID         int64
	TargetDigest     [sha256.Size]byte
	Replayed         bool
}

type HistoricalStore interface {
	CreateHistoricalPlan(context.Context, HistoricalPlan) (HistoricalPlan, error)
	GetHistoricalPlan(context.Context, int64) (HistoricalPlan, error)
	CreateHistoricalDirectory(context.Context, HistoricalDirectory) (HistoricalDirectory, error)
	GetHistoricalDirectory(context.Context, int64) (HistoricalDirectory, error)
	CreateHistoricalGroup(context.Context, HistoricalGroup) (HistoricalGroup, error)
	GetHistoricalGroup(context.Context, int64) (HistoricalGroup, error)
	CreateHistoricalNode(context.Context, HistoricalNode) (HistoricalNode, error)
	GetHistoricalNode(context.Context, int64) (HistoricalNode, error)
}

type HistoricalJournal interface {
	LoadGroupOpsHistory(context.Context, string, string) (HistoricalReceipt, bool, error)
	RecordGroupOpsHistory(context.Context, string, HistoricalReceipt) error
}

type HistoricalReader interface {
	ListHistoricalPlans(context.Context, int32, int32) ([]HistoricalPlan, int64, error)
	ListHistoricalDirectory(context.Context, int32, int32) ([]HistoricalDirectory, int64, error)
	ListHistoricalGroups(context.Context, int64, int32, int32) ([]HistoricalGroup, int64, error)
	ListHistoricalNodes(context.Context, int64, int32, int32) ([]HistoricalNode, int64, error)
}
