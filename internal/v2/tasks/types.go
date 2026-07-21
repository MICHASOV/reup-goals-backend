package tasks

import (
	"encoding/json"
	"time"

	"reup-goals-backend/internal/v2/aiactions"
)

const (
	StatusFree       = "free"
	StatusInProgress = "in_progress"
	StatusDone       = "done"
	StatusArchived   = "archived"

	SourceManual       = "manual"
	SourceWorkstream   = "workstream"
	SourceProject      = "project"
	SourceRisk         = "risk"
	SourceOpportunity  = "opportunity"
	SourceAISuggestion = "ai_suggestion"

	EvaluationQueued  = "queued"
	EvaluationRunning = "running"
	EvaluationReady   = "ready"
	EvaluationFailed  = "failed"

	RecommendationKeep    = "keep"
	RecommendationClarify = "clarify"
	RecommendationRework  = "rework"
	RecommendationRemove  = "remove"

	TaskFlagBlocked            = "blocked"
	TaskFlagWeakStrategyLink   = "weak_strategy_link"
	TaskFlagLowImpact          = "low_impact"
	TaskFlagHighEffort         = "high_effort"
	TaskFlagDuplicate          = "duplicate"
	TaskFlagNeedsClarification = "needs_clarification"

	BacklogFutureStage       = "future_stage"
	BacklogQuestionable      = "questionable"
	BacklogRecommendedDelete = "recommended_delete"
)

type CourseSummary struct {
	ID               int    `json:"id"`
	Direction        string `json:"direction"`
	StrategicGoal    string `json:"strategic_goal"`
	KeyMetric        string `json:"key_metric"`
	SuccessCriterion string `json:"success_criterion"`
}

type TacticalPlanSummary struct {
	ID         int  `json:"id"`
	StrategyID int  `json:"strategy_id"`
	CourseID   *int `json:"course_id"`
}

type WorkstreamSummary struct {
	ID            int            `json:"id"`
	Title         string         `json:"title"`
	Description   string         `json:"description"`
	Goal          string         `json:"goal"`
	CKP           string         `json:"ckp"`
	Reason        string         `json:"reason"`
	ClosesRisk    string         `json:"closes_risk"`
	MetricName    string         `json:"metric_name"`
	MetricCurrent string         `json:"metric_current"`
	MetricTarget  string         `json:"metric_target"`
	Metrics       []TacticMetric `json:"metrics"`
	HealthStatus  string         `json:"health_status"`
	Projects      []Project      `json:"projects"`
	Risks         []Risk         `json:"risks"`
	Opportunities []Opportunity  `json:"opportunities"`
	TasksSummary  TasksSummary   `json:"tasks_summary"`
	TopTasks      []Task         `json:"top_tasks"`
}

type TacticMetric struct {
	Name    string `json:"name"`
	Current string `json:"current"`
	Target  string `json:"target"`
}

type Project struct {
	ID              int    `json:"id"`
	WorkstreamID    int    `json:"workstream_id"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	WhyNeeded       string `json:"why_needed"`
	SuccessCriteria string `json:"success_criteria"`
	FailureCriteria string `json:"failure_criteria"`
	MetricName      string `json:"metric_name"`
	ExpectedValue   string `json:"expected_value"`
	Status          string `json:"status"`
}

type Risk struct {
	ID             int    `json:"id"`
	TacticalPlanID int    `json:"tactical_plan_id"`
	EntityType     string `json:"entity_type"`
	EntityID       int    `json:"entity_id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	Severity       string `json:"severity"`
	Probability    string `json:"probability"`
	Status         string `json:"status"`
	CoverageStatus string `json:"coverage_status"`
}

type Opportunity struct {
	ID              int    `json:"id"`
	TacticalPlanID  int    `json:"tactical_plan_id"`
	EntityType      string `json:"entity_type"`
	EntityID        int    `json:"entity_id"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	PotentialImpact string `json:"potential_impact"`
	Urgency         string `json:"urgency"`
	Status          string `json:"status"`
	CoverageStatus  string `json:"coverage_status"`
}

type Task struct {
	ID                     int             `json:"id"`
	WorkspaceID            int             `json:"workspace_id"`
	CourseID               int             `json:"course_id"`
	TacticalPlanID         int             `json:"tactical_plan_id"`
	WorkstreamID           int             `json:"workstream_id"`
	DepartmentID           int             `json:"department_id"`
	ProjectID              *int            `json:"project_id"`
	RiskID                 *int            `json:"risk_id"`
	OpportunityID          *int            `json:"opportunity_id"`
	Title                  string          `json:"title"`
	Description            string          `json:"description"`
	ExpectedResult         string          `json:"expected_result"`
	SuccessCriteria        string          `json:"success_criteria"`
	WhyNow                 string          `json:"why_now"`
	Status                 string          `json:"status"`
	Blocked                bool            `json:"blocked"`
	BacklogCategory        string          `json:"backlog_category"`
	Flags                  []string        `json:"flags"`
	SecondaryWorkstreamIDs []int           `json:"secondary_workstream_ids"`
	PriorityOrder          *int            `json:"priority_order"`
	OwnerUserID            *int            `json:"owner_user_id"`
	DueDate                *string         `json:"due_date"`
	SourceType             string          `json:"source_type"`
	SourceID               *int            `json:"source_id"`
	CreatedBy              *int            `json:"created_by,omitempty"`
	UpdatedBy              *int            `json:"updated_by,omitempty"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
	StartedAt              *time.Time      `json:"started_at"`
	CompletedAt            *time.Time      `json:"completed_at"`
	ArchivedAt             *time.Time      `json:"archived_at"`
	CompletionResult       string          `json:"completion_result"`
	CompletionEvidence     string          `json:"completion_evidence"`
	CompletionLearning     string          `json:"completion_learning"`
	HypothesisOutcome      string          `json:"hypothesis_outcome"`
	NextStep               string          `json:"next_step"`
	Evaluation             *TaskEvaluation `json:"evaluation,omitempty"`
	EvaluationStatus       string          `json:"evaluation_status"`
	ManualPriorityScore    *int            `json:"manual_priority_score,omitempty"`
	ManualPriorityTier     string          `json:"manual_priority_tier,omitempty"`
	EffectivePriorityScore int             `json:"effective_priority_score"`
	EffectivePriorityTier  string          `json:"effective_priority_tier"`
	PrioritySource         string          `json:"priority_source"`
}

type TaskEvaluation struct {
	ID                    int       `json:"id"`
	TaskID                int       `json:"task_id"`
	StrategicRelevance    int       `json:"strategic_relevance"`
	CourseAlignment       int       `json:"course_alignment"`
	TacticalAlignment     int       `json:"tactical_alignment"`
	ExpectedImpact        int       `json:"expected_impact"`
	Urgency               int       `json:"urgency"`
	Effort                int       `json:"effort"`
	Confidence            int       `json:"confidence"`
	PriorityScore         int       `json:"priority_score"`
	PriorityTier          string    `json:"priority_tier"`
	Recommendation        string    `json:"recommendation"`
	PriorityReason        string    `json:"priority_reason"`
	ClarificationQuestion string    `json:"clarification_question"`
	MissingInformation    []string  `json:"missing_information"`
	Flags                 []string  `json:"flags"`
	BacklogCategory       string    `json:"backlog_category"`
	CreatedAt             time.Time `json:"created_at"`
}

type TasksSummary struct {
	Total      int `json:"total"`
	Free       int `json:"free"`
	InProgress int `json:"in_progress"`
	Done       int `json:"done"`
	Archived   int `json:"archived"`
}

type OverviewResponse struct {
	Course       *CourseSummary       `json:"course"`
	TacticalPlan *TacticalPlanSummary `json:"tactical_plan"`
	Workstreams  []WorkstreamSummary  `json:"workstreams"`
	Tasks        []Task               `json:"tasks"`
	Reason       string               `json:"reason,omitempty"`
	Message      string               `json:"message,omitempty"`
}

type WorkstreamResponse struct {
	Course        *CourseSummary       `json:"course"`
	TacticalPlan  *TacticalPlanSummary `json:"tactical_plan"`
	Workstream    *WorkstreamSummary   `json:"workstream"`
	Projects      []Project            `json:"projects"`
	Risks         []Risk               `json:"risks"`
	Opportunities []Opportunity        `json:"opportunities"`
	Tasks         []Task               `json:"tasks"`
	TasksSummary  TasksSummary         `json:"tasks_summary"`
	Reason        string               `json:"reason,omitempty"`
	Message       string               `json:"message,omitempty"`
}

type TaskInput struct {
	WorkstreamID           int     `json:"workstream_id"`
	DepartmentID           *int    `json:"department_id"`
	ProjectID              *int    `json:"project_id"`
	ClearProject           bool    `json:"clear_project"`
	RiskID                 *int    `json:"risk_id"`
	OpportunityID          *int    `json:"opportunity_id"`
	Title                  *string `json:"title"`
	Description            *string `json:"description"`
	ExpectedResult         *string `json:"expected_result"`
	SuccessCriteria        *string `json:"success_criteria"`
	WhyNow                 *string `json:"why_now"`
	Status                 *string `json:"status"`
	Blocked                *bool   `json:"blocked"`
	BacklogCategory        *string `json:"backlog_category"`
	SecondaryWorkstreamIDs []int   `json:"secondary_workstream_ids"`
	PriorityOrder          *int    `json:"priority_order"`
	OwnerUserID            *int    `json:"owner_user_id"`
	DueDate                *string `json:"due_date"`
	SourceType             *string `json:"source_type"`
	SourceID               *int    `json:"source_id"`
	CompletionResult       *string `json:"completion_result"`
	CompletionEvidence     *string `json:"completion_evidence"`
	CompletionLearning     *string `json:"completion_learning"`
	HypothesisOutcome      *string `json:"hypothesis_outcome"`
	NextStep               *string `json:"next_step"`
}

type BrainstormMessage struct {
	ID           int                `json:"id"`
	Role         string             `json:"role"`
	Content      string             `json:"content"`
	Actions      []BrainstormAction `json:"actions"`
	Applied      []int              `json:"applied_action_indices"`
	ActionStates []aiactions.Action `json:"action_states,omitempty"`
	CreatedAt    time.Time          `json:"created_at"`
}

type BrainstormAction struct {
	ActionType      string `json:"action_type"`
	TaskID          *int   `json:"task_id,omitempty"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	ExpectedResult  string `json:"expected_result"`
	SuccessCriteria string `json:"success_criteria"`
	WhyNow          string `json:"why_now"`
	ProjectID       *int   `json:"project_id,omitempty"`
	RiskID          *int   `json:"risk_id,omitempty"`
	OpportunityID   *int   `json:"opportunity_id,omitempty"`
	DueInDays       *int   `json:"due_in_days,omitempty"`
	Reason          string `json:"reason"`
}

type BrainstormHistoryResponse struct {
	WorkspaceID int                 `json:"workspace_id"`
	Workstream  *WorkstreamSummary  `json:"workstream"`
	Messages    []BrainstormMessage `json:"messages"`
}

type BrainstormMessageRequest struct {
	WorkstreamID int    `json:"workstream_id"`
	Message      string `json:"message"`
}

type BrainstormMessageResponse struct {
	WorkspaceID      int               `json:"workspace_id"`
	AssistantMessage string            `json:"assistant_message"`
	UserMessage      BrainstormMessage `json:"user_message"`
	Message          BrainstormMessage `json:"message"`
	InputTokens      int               `json:"input_tokens,omitempty"`
	OutputTokens     int               `json:"output_tokens,omitempty"`
}

type ApplyBrainstormActionsRequest struct {
	WorkstreamID  int   `json:"workstream_id"`
	MessageID     int   `json:"message_id"`
	ActionIndices []int `json:"action_indices"`
}

type ApplyBrainstormActionsResponse struct {
	Tasks   []Task `json:"tasks"`
	Applied []int  `json:"applied_action_indices"`
}

type TaskEvaluationJob struct {
	ID          int
	WorkspaceID int
	TaskID      int
	RequestedBy *int
	Attempts    int
	Revision    int
}

type taskEvaluatorModelOutput struct {
	StrategicRelevance    int      `json:"strategic_relevance"`
	CourseAlignment       int      `json:"course_alignment"`
	TacticalAlignment     int      `json:"tactical_alignment"`
	ExpectedImpact        int      `json:"expected_impact"`
	Urgency               int      `json:"urgency"`
	Effort                int      `json:"effort"`
	Confidence            int      `json:"confidence"`
	Recommendation        string   `json:"recommendation"`
	PriorityReason        string   `json:"priority_reason"`
	ClarificationQuestion string   `json:"clarification_question"`
	MissingInformation    []string `json:"missing_information"`
	Flags                 []string `json:"flags"`
	BacklogCategory       string   `json:"backlog_category"`
}

type brainstormModelOutput struct {
	Message string             `json:"message"`
	Actions []BrainstormAction `json:"task_actions"`
}

type BrainstormSession struct {
	ConversationID     string
	PreviousResponseID string
	CompactThreshold   int
	PromptCacheKey     string
	ContextFingerprint string
}

type compactContextDocument struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

type taskContextPack struct {
	BusinessSnapshot  json.RawMessage          `json:"business_snapshot,omitempty"`
	BusinessStage     string                   `json:"business_stage,omitempty"`
	BusinessDocuments []compactContextDocument `json:"business_documents,omitempty"`
	StrategySummary   string                   `json:"strategy_summary,omitempty"`
	StrategyDocuments []compactContextDocument `json:"strategy_documents,omitempty"`
	Course            *CourseSummary           `json:"active_course"`
	TacticalPlan      *TacticalPlanSummary     `json:"tactical_plan"`
	Workstream        *WorkstreamSummary       `json:"workstream"`
	Projects          []Project                `json:"projects"`
	Risks             []Risk                   `json:"risks"`
	Opportunities     []Opportunity            `json:"opportunities"`
	ExistingTasks     []taskContextItem        `json:"existing_tasks"`
	Communication     any                      `json:"communication_profile,omitempty"`
	RecentMessages    []BrainstormMessage      `json:"recent_messages,omitempty"`
}

type taskContextItem struct {
	ID                     int      `json:"id"`
	ProjectID              *int     `json:"project_id,omitempty"`
	RiskID                 *int     `json:"risk_id,omitempty"`
	OpportunityID          *int     `json:"opportunity_id,omitempty"`
	Title                  string   `json:"title"`
	Description            string   `json:"description"`
	ExpectedResult         string   `json:"expected_result"`
	SuccessCriteria        string   `json:"success_criteria"`
	Status                 string   `json:"status"`
	EffectivePriorityScore int      `json:"priority_score"`
	EffectivePriorityTier  string   `json:"priority_tier"`
	Recommendation         string   `json:"recommendation,omitempty"`
	Flags                  []string `json:"flags,omitempty"`
	BacklogCategory        string   `json:"backlog_category,omitempty"`
}

func ValidStatus(status string) bool {
	switch status {
	case StatusFree, StatusInProgress, StatusDone, StatusArchived:
		return true
	default:
		return false
	}
}

func ValidSourceType(sourceType string) bool {
	switch sourceType {
	case SourceManual, SourceWorkstream, SourceProject, SourceRisk, SourceOpportunity, SourceAISuggestion:
		return true
	default:
		return false
	}
}
