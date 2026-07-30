package agent

import (
	"encoding/json"
	"time"

	"reup-goals-backend/internal/v2/tactics"
)

const (
	StatusQueued          = "queued"
	StatusRunning         = "running"
	StatusWaitingApproval = "waiting_approval"
	StatusCompleted       = "completed"
	StatusFailed          = "failed"
	StatusCanceled        = "canceled"

	JobTypeExecute = "executive_agent.execute"
	JobTypeResume  = "executive_agent.resume"

	PromptVersion  = "executive_advisor_v2"
	DefaultRelease = "executive_advisor_2026_07_31_v2"
)

type Scope struct {
	Type  string `json:"type"`
	ID    int    `json:"id"`
	Label string `json:"label"`
}

type Run struct {
	ID                 int64                        `json:"-"`
	PublicID           string                       `json:"id"`
	WorkspaceID        int                          `json:"workspace_id"`
	UserID             int                          `json:"user_id"`
	ThreadID           int                          `json:"thread_id"`
	UserMessageID      int                          `json:"user_message_id,omitempty"`
	AssistantMessageID int                          `json:"assistant_message_id,omitempty"`
	Scope              Scope                        `json:"scope"`
	Status             string                       `json:"status"`
	Model              string                       `json:"model"`
	PromptVersion      string                       `json:"prompt_version"`
	AgentReleaseID     string                       `json:"agent_release_id"`
	SessionGeneration  int                          `json:"session_generation"`
	MigratedFrom       string                       `json:"migrated_from_release_id,omitempty"`
	ContinuityContext  string                       `json:"-"`
	InputText          string                       `json:"-"`
	OutputText         string                       `json:"output_text"`
	PartialOutput      string                       `json:"partial_output"`
	PreviousResponseID string                       `json:"-"`
	ConversationID     string                       `json:"-"`
	VectorStoreID      string                       `json:"-"`
	StateCiphertext    string                       `json:"-"`
	ErrorText          string                       `json:"error,omitempty"`
	ReservationID      string                       `json:"-"`
	UsageRequests      int                          `json:"usage_requests"`
	UsageInputTokens   int                          `json:"usage_input_tokens"`
	UsageOutputTokens  int                          `json:"usage_output_tokens"`
	UsageTotalTokens   int                          `json:"usage_total_tokens"`
	StartedAt          *time.Time                   `json:"started_at,omitempty"`
	CompletedAt        *time.Time                   `json:"completed_at,omitempty"`
	CreatedAt          time.Time                    `json:"created_at"`
	UpdatedAt          time.Time                    `json:"updated_at"`
	Events             []Event                      `json:"events"`
	Approvals          []Approval                   `json:"approvals"`
	ProposalMessageID  int                          `json:"proposal_message_id,omitempty"`
	ProposedChanges    []tactics.TacticsDraftChange `json:"proposed_changes"`
}

type Event struct {
	ID         int64           `json:"id"`
	Type       string          `json:"type"`
	Stage      string          `json:"stage"`
	Title      string          `json:"title"`
	Detail     string          `json:"detail,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

type Approval struct {
	ID          int64           `json:"id"`
	CallID      string          `json:"call_id"`
	ToolName    string          `json:"tool_name"`
	Arguments   json.RawMessage `json:"arguments"`
	Status      string          `json:"status"`
	ActionIndex int             `json:"action_index"`
	Result      json.RawMessage `json:"result,omitempty"`
	ErrorText   string          `json:"error,omitempty"`
	DecidedAt   *time.Time      `json:"decided_at,omitempty"`
	AppliedAt   *time.Time      `json:"applied_at,omitempty"`
}

type CreateRunRequest struct {
	ThreadID    int          `json:"thread_id"`
	Message     string       `json:"message"`
	Scope       Scope        `json:"scope"`
	Attachments []Attachment `json:"attachments"`
}

type Attachment struct {
	Type  string `json:"type"`
	ID    int    `json:"id,omitempty"`
	Key   string `json:"key,omitempty"`
	Label string `json:"label"`
}

type Decision struct {
	CallID   string `json:"call_id"`
	Approved bool   `json:"approved"`
}

type DecisionRequest struct {
	Decisions []Decision `json:"decisions"`
}

type RuntimeEvent struct {
	Type       string `json:"type"`
	Stage      string `json:"stage"`
	Title      string `json:"title"`
	Detail     string `json:"detail,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
}

type RuntimeInterruption struct {
	CallID    string         `json:"call_id"`
	ToolName  string         `json:"tool_name"`
	Arguments map[string]any `json:"arguments"`
}

type RuntimeUsage struct {
	Requests          int `json:"requests"`
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	TotalTokens       int `json:"total_tokens"`
}

type ThreadSession struct {
	PreviousResponseID string
	ConversationID     string
	AgentReleaseID     string
	Model              string
	PromptVersion      string
	SessionGeneration  int
	Found              bool
}

type RuntimeResult struct {
	Status             string                `json:"status"`
	Output             string                `json:"output"`
	PartialOutput      string                `json:"partial_output"`
	PreviousResponseID string                `json:"previous_response_id,omitempty"`
	State              string                `json:"state,omitempty"`
	Interruptions      []RuntimeInterruption `json:"interruptions"`
	Events             []RuntimeEvent        `json:"events"`
	Usage              RuntimeUsage          `json:"usage"`
}

type ToolRequest struct {
	RunID      string         `json:"run_id"`
	ToolCallID string         `json:"tool_call_id"`
	Input      map[string]any `json:"input"`
}
