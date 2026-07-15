package tactics

import (
	"encoding/json"
	"time"

	"reup-goals-backend/internal/v2/strategicmemory"
)

const (
	PlanStatusDraft    = "draft"
	PlanStatusActive   = "active"
	PlanStatusArchived = "archived"

	WorkstreamStatusActive    = "active"
	WorkstreamStatusArchived  = "archived"
	WorkstreamStatusDraft     = "draft"
	WorkstreamStatusPaused    = "paused"
	WorkstreamStatusCompleted = "completed"

	ProjectStatusActive     = "active"
	ProjectStatusArchived   = "archived"
	ProjectStatusDraft      = "draft"
	ProjectStatusValidating = "validating"
	ProjectStatusPaused     = "paused"
	ProjectStatusCompleted  = "completed"
	ProjectStatusFailed     = "failed"

	EntityPlan       = "tactical_plan"
	EntityWorkstream = "workstream"
	EntityProject    = "project"

	CoverageUncovered        = "uncovered"
	CoveragePartiallyCovered = "partially_covered"
	CoverageCovered          = "covered"
	CoverageAccepted         = "accepted"
	CoverageIgnored          = "ignored"

	SourceManual = "manual"

	TacticsFacilitatorPromptVersion = "tactics_facilitator_openai_native_v0_1_0"

	FacilitatorStatusInProgress     = "in_progress"
	FacilitatorStatusCandidateReady = "candidate_ready"
	FacilitatorStatusBlocked        = "blocked"
)

type TacticalPlan struct {
	ID          int        `json:"id"`
	WorkspaceID int        `json:"workspace_id"`
	StrategyID  int        `json:"strategy_id"`
	CourseID    *int       `json:"course_id"`
	Status      string     `json:"status"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary"`
	Source      string     `json:"source"`
	CreatedBy   *int       `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ActivatedAt *time.Time `json:"activated_at"`
}

type StrategySummary struct {
	ID        int    `json:"id"`
	Status    string `json:"status"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	Version   int    `json:"version"`
	UpdatedAt string `json:"updated_at"`
}

type CourseSummary struct {
	ID               int        `json:"id"`
	StrategyID       int        `json:"strategy_id"`
	Status           string     `json:"status"`
	Title            string     `json:"title"`
	Direction        string     `json:"direction"`
	StrategicGoal    string     `json:"strategic_goal"`
	Meaning          string     `json:"meaning"`
	Horizon          int        `json:"horizon"`
	HorizonUnit      string     `json:"horizon_unit"`
	StartDate        string     `json:"start_date"`
	EndDate          string     `json:"end_date"`
	KeyMetric        string     `json:"key_metric"`
	SuccessCriterion string     `json:"success_criterion"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ActivatedAt      *time.Time `json:"activated_at,omitempty"`
}

type Workstream struct {
	ID               int           `json:"id"`
	WorkspaceID      int           `json:"-"`
	TacticalPlanID   int           `json:"tactical_plan_id"`
	StrategyID       int           `json:"strategy_id"`
	CourseID         *int          `json:"course_id"`
	Title            string        `json:"title"`
	Description      string        `json:"description"`
	Goal             string        `json:"goal"`
	CKP              string        `json:"ckp"`
	Reason           string        `json:"reason"`
	ClosesRisk       string        `json:"closes_risk"`
	MetricName       string        `json:"metric_name"`
	MetricCurrent    string        `json:"metric_current"`
	MetricTarget     string        `json:"metric_target"`
	Status           string        `json:"status"`
	HealthStatus     string        `json:"health_status"`
	ContributionType string        `json:"contribution_type"`
	Confidence       *float64      `json:"confidence"`
	Source           string        `json:"source"`
	SortOrder        int           `json:"sort_order"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	Projects         []Project     `json:"projects"`
	Risks            []Risk        `json:"risks"`
	Opportunities    []Opportunity `json:"opportunities"`
}

type Project struct {
	ID              int       `json:"id"`
	WorkspaceID     int       `json:"-"`
	WorkstreamID    int       `json:"workstream_id"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	WhyNeeded       string    `json:"why_needed"`
	SuccessCriteria string    `json:"success_criteria"`
	FailureCriteria string    `json:"failure_criteria"`
	MetricName      string    `json:"metric_name"`
	Status          string    `json:"status"`
	Confidence      *float64  `json:"confidence"`
	Source          string    `json:"source"`
	SortOrder       int       `json:"sort_order"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Risk struct {
	ID             int       `json:"id"`
	WorkspaceID    int       `json:"-"`
	TacticalPlanID int       `json:"tactical_plan_id"`
	EntityType     string    `json:"entity_type"`
	EntityID       int       `json:"entity_id"`
	Title          string    `json:"title"`
	Description    string    `json:"description"`
	Severity       string    `json:"severity"`
	Status         string    `json:"status"`
	CoverageStatus string    `json:"coverage_status"`
	Source         string    `json:"source"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Opportunity struct {
	ID              int       `json:"id"`
	WorkspaceID     int       `json:"-"`
	TacticalPlanID  int       `json:"tactical_plan_id"`
	EntityType      string    `json:"entity_type"`
	EntityID        int       `json:"entity_id"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	PotentialImpact string    `json:"potential_impact"`
	Status          string    `json:"status"`
	CoverageStatus  string    `json:"coverage_status"`
	Source          string    `json:"source"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type CurrentResponse struct {
	TacticalPlan *TacticalPlan    `json:"tactical_plan"`
	Strategy     *StrategySummary `json:"strategy"`
	Course       *CourseSummary   `json:"course,omitempty"`
	Workstreams  []Workstream     `json:"workstreams"`
	Uncovered    Uncovered        `json:"uncovered"`
	Reason       string           `json:"reason,omitempty"`
	Message      string           `json:"message,omitempty"`
}

type TacticsChatMessage struct {
	ID        int       `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type TacticsFocus struct {
	EntityType   string `json:"entity_type"`
	EntityID     *int   `json:"entity_id,omitempty"`
	Title        string `json:"title"`
	ResearchGoal string `json:"research_goal"`
}

type TacticsSessionState struct {
	WorkspaceID          int          `json:"workspace_id"`
	Revision             int          `json:"revision"`
	LastUserMessageID    int          `json:"last_user_message_id"`
	LastUserID           *int         `json:"last_user_id,omitempty"`
	FacilitatorStatus    string       `json:"facilitator_status"`
	StatusReason         string       `json:"status_reason"`
	CurrentFocus         TacticsFocus `json:"current_focus"`
	Decisions            []string     `json:"decisions"`
	OpenQuestions        []string     `json:"open_questions"`
	NeedsStrategyReview  bool         `json:"needs_strategy_review"`
	StrategyReviewReason string       `json:"strategy_review_reason"`
	UpdatedAt            time.Time    `json:"updated_at"`
}

type TacticsOpenAISession struct {
	ID                 int       `json:"id"`
	WorkspaceID        int       `json:"workspace_id"`
	PreviousResponseID string    `json:"previous_response_id,omitempty"`
	CompactThreshold   int       `json:"compact_threshold"`
	PromptCacheKey     string    `json:"prompt_cache_key,omitempty"`
	ContextFingerprint string    `json:"context_fingerprint,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type TacticsStrategyDocument struct {
	RunID         int             `json:"run_id"`
	DocumentType  string          `json:"document_type"`
	Title         string          `json:"title"`
	Status        string          `json:"status"`
	Content       string          `json:"content"`
	SourceRefs    json.RawMessage `json:"source_refs,omitempty"`
	OpenQuestions json.RawMessage `json:"open_questions,omitempty"`
}

type TacticsFacilitatorState struct {
	WorkspaceID    int                                  `json:"workspace_id"`
	Current        CurrentResponse                      `json:"current"`
	StrategyDocs   []TacticsStrategyDocument            `json:"strategy_documents"`
	KnowledgeDocs  []strategicmemory.StrategicDocument  `json:"knowledge_documents"`
	KnowledgeAudit *strategicmemory.QualityReport       `json:"knowledge_quality,omitempty"`
	Files          []strategicmemory.StrategicFile      `json:"files,omitempty"`
	Communication  strategicmemory.CommunicationProfile `json:"communication_profile"`
	RecentMessages []TacticsChatMessage                 `json:"recent_messages"`
	Session        TacticsSessionState                  `json:"session"`
}

type TacticsFacilitatorHistoryState struct {
	WorkspaceID    int                  `json:"workspace_id"`
	RecentMessages []TacticsChatMessage `json:"recent_messages"`
	Session        TacticsSessionState  `json:"session"`
}

type TacticsMessageScope struct {
	EntityType string `json:"entity_type"`
	EntityID   int    `json:"entity_id"`
	Label      string `json:"label"`
}

type TacticsFacilitatorMessageRequest struct {
	Message         string               `json:"message"`
	ParticipantRole string               `json:"participant_role,omitempty"`
	Scope           *TacticsMessageScope `json:"scope,omitempty"`
}

type TacticsFacilitatorMessageResponse struct {
	WorkspaceID      int                  `json:"workspace_id"`
	AssistantMessage string               `json:"assistant_message"`
	RecentMessages   []TacticsChatMessage `json:"recent_messages"`
	OpenAIResponseID string               `json:"openai_response_id,omitempty"`
	Session          TacticsSessionState  `json:"session"`
}

type tacticsFacilitatorModelOutput struct {
	Message              string       `json:"message"`
	SessionStatus        string       `json:"session_status"`
	StatusReason         string       `json:"status_reason"`
	CurrentFocus         TacticsFocus `json:"current_focus"`
	DecisionsDetected    []string     `json:"decisions_detected"`
	OpenQuestions        []string     `json:"open_questions"`
	NeedsStrategyReview  bool         `json:"needs_strategy_review"`
	StrategyReviewReason string       `json:"strategy_review_reason"`
}

type Uncovered struct {
	Risks         []Risk        `json:"risks"`
	Opportunities []Opportunity `json:"opportunities"`
}

func ValidPlanStatus(status string) bool {
	switch status {
	case PlanStatusDraft, PlanStatusActive, PlanStatusArchived:
		return true
	default:
		return false
	}
}

func ValidWorkstreamStatus(status string) bool {
	switch status {
	case WorkstreamStatusActive, WorkstreamStatusArchived, WorkstreamStatusDraft, WorkstreamStatusPaused, WorkstreamStatusCompleted:
		return true
	default:
		return false
	}
}

func ValidProjectStatus(status string) bool {
	switch status {
	case ProjectStatusActive, ProjectStatusArchived, ProjectStatusDraft, ProjectStatusValidating, ProjectStatusPaused, ProjectStatusCompleted, ProjectStatusFailed:
		return true
	default:
		return false
	}
}

func ValidEntityType(entityType string) bool {
	switch entityType {
	case EntityPlan, EntityWorkstream, EntityProject:
		return true
	default:
		return false
	}
}

func ValidCoverageStatus(status string) bool {
	switch status {
	case CoverageUncovered, CoveragePartiallyCovered, CoverageCovered, CoverageAccepted, CoverageIgnored:
		return true
	default:
		return false
	}
}
