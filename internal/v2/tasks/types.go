package tasks

import "time"

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
	ID            int           `json:"id"`
	Title         string        `json:"title"`
	Description   string        `json:"description"`
	Goal          string        `json:"goal"`
	CKP           string        `json:"ckp"`
	Reason        string        `json:"reason"`
	ClosesRisk    string        `json:"closes_risk"`
	MetricName    string        `json:"metric_name"`
	MetricCurrent string        `json:"metric_current"`
	MetricTarget  string        `json:"metric_target"`
	HealthStatus  string        `json:"health_status"`
	Projects      []Project     `json:"projects"`
	Risks         []Risk        `json:"risks"`
	Opportunities []Opportunity `json:"opportunities"`
	TasksSummary  TasksSummary  `json:"tasks_summary"`
	TopTasks      []Task        `json:"top_tasks"`
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
	Status          string `json:"status"`
	CoverageStatus  string `json:"coverage_status"`
}

type Task struct {
	ID             int        `json:"id"`
	WorkspaceID    int        `json:"workspace_id"`
	CourseID       int        `json:"course_id"`
	TacticalPlanID int        `json:"tactical_plan_id"`
	WorkstreamID   int        `json:"workstream_id"`
	ProjectID      *int       `json:"project_id"`
	RiskID         *int       `json:"risk_id"`
	OpportunityID  *int       `json:"opportunity_id"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	Status         string     `json:"status"`
	PriorityOrder  *int       `json:"priority_order"`
	OwnerUserID    *int       `json:"owner_user_id"`
	DueDate        *string    `json:"due_date"`
	SourceType     string     `json:"source_type"`
	SourceID       *int       `json:"source_id"`
	CreatedBy      *int       `json:"created_by,omitempty"`
	UpdatedBy      *int       `json:"updated_by,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	StartedAt      *time.Time `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	ArchivedAt     *time.Time `json:"archived_at"`
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
	WorkstreamID  int     `json:"workstream_id"`
	ProjectID     *int    `json:"project_id"`
	RiskID        *int    `json:"risk_id"`
	OpportunityID *int    `json:"opportunity_id"`
	Title         *string `json:"title"`
	Description   *string `json:"description"`
	Status        *string `json:"status"`
	PriorityOrder *int    `json:"priority_order"`
	OwnerUserID   *int    `json:"owner_user_id"`
	DueDate       *string `json:"due_date"`
	SourceType    *string `json:"source_type"`
	SourceID      *int    `json:"source_id"`
}

type TaskSuggestionRequest struct {
	WorkstreamID  int    `json:"workstream_id"`
	ProjectID     *int   `json:"project_id,omitempty"`
	RiskID        *int   `json:"risk_id,omitempty"`
	OpportunityID *int   `json:"opportunity_id,omitempty"`
	Instruction   string `json:"instruction,omitempty"`
}

type TaskSuggestion struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	WhyNow      string `json:"why_now"`
	Priority    int    `json:"priority"`
	DueInDays   *int   `json:"due_in_days,omitempty"`
}

type TaskSuggestionResponse struct {
	Summary      string           `json:"summary"`
	Suggestions  []TaskSuggestion `json:"suggestions"`
	InputTokens  int              `json:"input_tokens,omitempty"`
	OutputTokens int              `json:"output_tokens,omitempty"`
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
