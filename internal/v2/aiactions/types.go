package aiactions

import (
	"encoding/json"
	"time"
)

const (
	ScenarioTacticsFacilitator = "tactics_facilitator"
	ScenarioTaskBrainstorm     = "task_brainstorm"

	StatusProposed  = "proposed"
	StatusConfirmed = "confirmed"
	StatusApplied   = "applied"
	StatusRejected  = "rejected"
	StatusEdited    = "edited"
	StatusExpired   = "expired"
	StatusFailed    = "failed"
)

type Proposal struct {
	ActionType string
	Payload    any
}

type Action struct {
	ID          int64           `json:"id"`
	WorkspaceID int             `json:"workspace_id"`
	Scenario    string          `json:"scenario"`
	ScopeType   string          `json:"scope_type"`
	ScopeID     int             `json:"scope_id"`
	MessageID   int             `json:"message_id"`
	ActionIndex int             `json:"action_index"`
	ActionType  string          `json:"action_type"`
	Payload     json.RawMessage `json:"payload_json"`
	Status      string          `json:"status"`
	EntityType  string          `json:"entity_type,omitempty"`
	EntityID    *int            `json:"entity_id,omitempty"`
	ProposedBy  *int            `json:"proposed_by,omitempty"`
	ConfirmedBy *int            `json:"confirmed_by,omitempty"`
	EditedBy    *int            `json:"edited_by,omitempty"`
	RejectedBy  *int            `json:"rejected_by,omitempty"`
	Error       string          `json:"error,omitempty"`
	ExpiresAt   *time.Time      `json:"expires_at,omitempty"`
	ConfirmedAt *time.Time      `json:"confirmed_at,omitempty"`
	AppliedAt   *time.Time      `json:"applied_at,omitempty"`
	RejectedAt  *time.Time      `json:"rejected_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type UpdateRequest struct {
	Status     string          `json:"status"`
	Payload    json.RawMessage `json:"payload_json,omitempty"`
	ActionType string          `json:"action_type,omitempty"`
}
