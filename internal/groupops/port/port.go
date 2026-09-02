// Package port defines the local-only Group Ops contract.
package port

import (
	"encoding/json"
	"time"
)

type PlanStatus string

const (
	PlanDraft    PlanStatus = "draft"
	PlanActive   PlanStatus = "active"
	PlanPaused   PlanStatus = "paused"
	PlanArchived PlanStatus = "archived"
)

type NodeKind string

const (
	NodeMessage NodeKind = "message"
	NodeDelay   NodeKind = "delay"
)

// Safety is deliberately included in every successful Group Ops response.
// The local package has no provider, runtime, webhook client, or send path.
type Safety struct {
	ProviderExecutionEligible bool `json:"provider_execution_eligible"`
	RealExternalCallExecuted  bool `json:"real_external_call_executed"`
}

func LocalSafety() Safety { return Safety{} }

type Plan struct {
	ID        int64      `json:"plan_id,string"`
	Name      string     `json:"name"`
	Status    PlanStatus `json:"status"`
	Revision  int64      `json:"revision"`
	CreatedBy int64      `json:"created_by"`
	UpdatedBy int64      `json:"updated_by"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type PlanListItem struct {
	Plan
	QueueCount int64 `json:"queue_count"`
}

type Member struct {
	StaffID int64 `json:"staff_id"`
}

type GroupAsset struct {
	ID       int64  `json:"group_asset_id,string"`
	AssetRef string `json:"asset_reference"`
}

type Node struct {
	ID           int64        `json:"node_id,string"`
	Position     int32        `json:"position"`
	Kind         NodeKind     `json:"kind"`
	MessageText  string       `json:"message_text,omitempty"`
	DelayMinutes int32        `json:"delay_minutes,omitempty"`
	MaterialRef  string       `json:"material_reference,omitempty"`
	MaterialPlan MaterialPlan `json:"material_plan"`
}

type MaterialReference struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
}

type MaterialPlan struct {
	References []MaterialReference `json:"references"`
}

type CapturedMaterialReference struct {
	Kind         string `json:"kind"`
	ID           int64  `json:"id"`
	SourceDigest string `json:"source_digest"`
}

type MaterialSourceSnapshot struct {
	References []CapturedMaterialReference `json:"references"`
	Snapshot   json.RawMessage             `json:"-"`
}

type PreparedMaterial struct {
	Snapshot   json.RawMessage
	Digest     string
	ReadyUntil time.Time
}

const (
	WebhookPathTemplate       = "/api/automation/group-ops/webhooks/{webhook_key}"
	WebhookSignatureAlgorithm = "HMAC-SHA256"
	WebhookSignatureHeader    = "X-AICRM-Signature"
	WebhookTimestampHeader    = "X-AICRM-Timestamp"
	WebhookNonceHeader        = "X-AICRM-Event-Id"
	WebhookClientIDHeader     = "X-AICRM-Client-Id"
	WebhookClientID           = "aicrm-webhook-group-ops"
)

// WebhookDescriptor contains the public integration contract only. It never
// contains a credential, token, signing secret, payload, or provider result.
type WebhookDescriptor struct {
	Configured         bool   `json:"configured"`
	Reference          string `json:"reference,omitempty"`
	Path               string `json:"path,omitempty"`
	URL                string `json:"url,omitempty"`
	SignatureAlgorithm string `json:"signature_algorithm,omitempty"`
	SignatureHeader    string `json:"signature_header,omitempty"`
	TimestampHeader    string `json:"timestamp_header,omitempty"`
	NonceHeader        string `json:"nonce_header,omitempty"`
	ClientIDHeader     string `json:"client_id_header,omitempty"`
	ClientID           string `json:"client_id,omitempty"`
	Description        string `json:"description"`
}

type Detail struct {
	Plan              Plan              `json:"plan"`
	Members           []Member          `json:"members"`
	GroupAssets       []GroupAsset      `json:"group_assets"`
	Nodes             []Node            `json:"nodes"`
	WebhookDescriptor WebhookDescriptor `json:"webhook_descriptor"`
	Safety
}

type PlanPage struct {
	Items   []PlanListItem `json:"items"`
	Total   int64          `json:"total"`
	Limit   int32          `json:"limit"`
	Offset  int32          `json:"offset"`
	HasMore bool           `json:"has_more"`
	Safety
}

type MemberPage struct {
	Items   []Member `json:"items"`
	Total   int64    `json:"total"`
	Limit   int32    `json:"limit"`
	Offset  int32    `json:"offset"`
	HasMore bool     `json:"has_more"`
	Safety
}

type GroupAssetPage struct {
	Items   []GroupAsset `json:"items"`
	Total   int64        `json:"total"`
	Limit   int32        `json:"limit"`
	Offset  int32        `json:"offset"`
	HasMore bool         `json:"has_more"`
	Safety
}

type NodePage struct {
	Items   []Node `json:"items"`
	Total   int64  `json:"total"`
	Limit   int32  `json:"limit"`
	Offset  int32  `json:"offset"`
	HasMore bool   `json:"has_more"`
	Safety
}

type ContentValidation struct {
	Valid           bool     `json:"valid"`
	IssueCodes      []string `json:"issue_codes"`
	PreviewLines    []string `json:"preview_lines"`
	NodeCount       int32    `json:"node_count"`
	GroupAssetCount int32    `json:"group_asset_count"`
	Safety
}

type CreatePlanCommand struct {
	Name           string
	Actor          int64
	IdempotencyKey string
}

type UpdatePlanCommand struct {
	PlanID           int64
	ExpectedRevision int64
	Name             string
	Actor            int64
	IdempotencyKey   string
}

type TransitionCommand struct {
	PlanID           int64
	ExpectedRevision int64
	Actor            int64
	IdempotencyKey   string
}

type MemberCommand struct {
	PlanID           int64
	ExpectedRevision int64
	StaffID          int64
	Actor            int64
	IdempotencyKey   string
}

type GroupAssetCommand struct {
	PlanID           int64
	ExpectedRevision int64
	AssetRef         string
	Actor            int64
	IdempotencyKey   string
}

type NodeCreateCommand struct {
	PlanID           int64
	ExpectedRevision int64
	Position         int32
	Kind             NodeKind
	MessageText      string
	DelayMinutes     int32
	MaterialRef      string
	MaterialPlan     MaterialPlan
	Actor            int64
	IdempotencyKey   string
}

type NodeUpdateCommand struct {
	PlanID           int64
	NodeID           int64
	ExpectedRevision int64
	Position         int32
	Kind             NodeKind
	MessageText      string
	DelayMinutes     int32
	MaterialRef      string
	MaterialPlan     MaterialPlan
	Actor            int64
	IdempotencyKey   string
}

type NodeDeleteCommand struct {
	PlanID           int64
	NodeID           int64
	ExpectedRevision int64
	Actor            int64
	IdempotencyKey   string
}

type WebhookDescriptorCommand struct {
	PlanID           int64
	ExpectedRevision int64
	Reference        string
	Actor            int64
	IdempotencyKey   string
}
