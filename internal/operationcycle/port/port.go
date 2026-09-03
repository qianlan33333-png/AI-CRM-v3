// Package port exposes only local operation-cycle facts.  It cannot send a
// message, invoke a provider, or create an external-effect retry.
package port

import (
	"encoding/json"
	"time"
)

const (
	StatusQueued      = "queued"
	StatusClaimed     = "claimed"
	StatusThreadBound = "thread_bound"
	StatusTurnStarted = "turn_started"
	StatusCompleted   = "completed"
	StatusFailed      = "failed"
)

type Strategy struct {
	Key        string
	Title      string
	Status     string
	Version    int
	Definition json.RawMessage
	Snapshot   json.RawMessage
	UpdatedAt  time.Time
}

type Run struct {
	Key         string
	StrategyKey string
	Revision    int
	Snapshot    json.RawMessage
	ReceivedAt  time.Time
}

type Runner struct {
	ID                  string
	PrincipalID         string
	ConnectorVersion    string
	CodexVersion        string
	CompatibilityStatus string
	BindingKeys         []string
	LastHeartbeatAt     time.Time
}

type ActionRequest struct {
	ID              string
	StrategyKey     string
	RunKey          string
	ActionKey       string
	ActionTitle     string
	StrategyVersion int
	RunnerID        string
	Status          string
	ParentRequestID string
	ThreadID        string
	TurnID          string
	FinalResult     json.RawMessage
	FailureCode     string
	CreatedBy       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CompletedAt     *time.Time
}

type Proposal struct {
	ID                  string
	StrategyKey         string
	BaseStrategyVersion int
	Status              string
	Payload             json.RawMessage
	CreatedBy           string
	DecidedBy           string
	CreatedAt           time.Time
	DecidedAt           *time.Time
}
