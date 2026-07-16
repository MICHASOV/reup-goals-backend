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

	SourceManual       = "manual"
	SourceAISuggestion = "ai_suggestion"

	TacticsFacilitatorPromptVersion = "tactics_facilitator_openai_native_v0_1_1"
	TacticsReadinessPromptVersion   = "tactics_quality_readiness_auditor_v0_1_2"

	FacilitatorStatusInProgress     = "in_progress"
	FacilitatorStatusCandidateReady = "candidate_ready"
	FacilitatorStatusBlocked        = "blocked"
)

type TacticalPlan struct {
	ID                       int        `json:"id"`
	WorkspaceID              int        `json:"workspace_id"`
	StrategyID               int        `json:"strategy_id"`
	CourseID                 *int       `json:"course_id"`
	Status                   string     `json:"status"`
	Revision                 int        `json:"revision"`
	ActivatedRevision        *int       `json:"activated_revision,omitempty"`
	ActivationReadinessRunID *int       `json:"activation_readiness_run_id,omitempty"`
	Title                    string     `json:"title"`
	Summary                  string     `json:"summary"`
	Source                   string     `json:"source"`
	CreatedBy                *int       `json:"created_by,omitempty"`
	CreatedAt                time.Time  `json:"created_at"`
	UpdatedAt                time.Time  `json:"updated_at"`
	ActivatedAt              *time.Time `json:"activated_at"`
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
	ID               int            `json:"id"`
	WorkspaceID      int            `json:"-"`
	TacticalPlanID   int            `json:"tactical_plan_id"`
	StrategyID       int            `json:"strategy_id"`
	CourseID         *int           `json:"course_id"`
	Title            string         `json:"title"`
	Description      string         `json:"description"`
	Goal             string         `json:"goal"`
	CKP              string         `json:"ckp"`
	Reason           string         `json:"reason"`
	ClosesRisk       string         `json:"closes_risk"`
	MetricName       string         `json:"metric_name"`
	MetricCurrent    string         `json:"metric_current"`
	MetricTarget     string         `json:"metric_target"`
	Metrics          []TacticMetric `json:"metrics"`
	Status           string         `json:"status"`
	HealthStatus     string         `json:"health_status"`
	ContributionType string         `json:"contribution_type"`
	Confidence       *float64       `json:"confidence"`
	Source           string         `json:"source"`
	SortOrder        int            `json:"sort_order"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	Projects         []Project      `json:"projects"`
	Risks            []Risk         `json:"risks"`
	Opportunities    []Opportunity  `json:"opportunities"`
}

type TacticMetric struct {
	Name    string `json:"name"`
	Current string `json:"current"`
	Target  string `json:"target"`
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
	ExpectedValue   string    `json:"expected_value"`
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
	Probability    string    `json:"probability"`
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
	Urgency         string    `json:"urgency"`
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
	ID              int                  `json:"id"`
	Role            string               `json:"role"`
	Content         string               `json:"content"`
	ProposedChanges []TacticsDraftChange `json:"proposed_changes,omitempty"`
	AppliedIndices  []int                `json:"applied_indices,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
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
	Readiness      *TacticsReadinessRun                 `json:"readiness,omitempty"`
}

type TacticsFacilitatorHistoryState struct {
	WorkspaceID    int                  `json:"workspace_id"`
	RecentMessages []TacticsChatMessage `json:"recent_messages"`
	Session        TacticsSessionState  `json:"session"`
	Readiness      *TacticsReadinessRun `json:"readiness,omitempty"`
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
	WorkspaceID       int                    `json:"workspace_id"`
	AssistantMessage  string                 `json:"assistant_message"`
	RecentMessages    []TacticsChatMessage   `json:"recent_messages"`
	OpenAIResponseID  string                 `json:"openai_response_id,omitempty"`
	Session           TacticsSessionState    `json:"session"`
	ProposalMessageID int                    `json:"proposal_message_id,omitempty"`
	ProposedChanges   []TacticsDraftChange   `json:"proposed_changes"`
	AppliedChanges    []AppliedTacticsChange `json:"applied_changes"`
}

type ApplyTacticsChangesRequest struct {
	MessageID     int   `json:"message_id"`
	ActionIndices []int `json:"action_indices"`
}

type ApplyTacticsChangesResponse struct {
	WorkspaceID    int                    `json:"workspace_id"`
	AppliedIndices []int                  `json:"applied_indices"`
	AppliedChanges []AppliedTacticsChange `json:"applied_changes"`
}

type TacticsDraftChange struct {
	Apply            bool           `json:"apply"`
	Operation        string         `json:"operation"`
	EntityType       string         `json:"entity_type"`
	EntityID         *int           `json:"entity_id,omitempty"`
	DraftKey         string         `json:"draft_key,omitempty"`
	ParentEntityType string         `json:"parent_entity_type,omitempty"`
	ParentEntityID   *int           `json:"parent_entity_id,omitempty"`
	ParentDraftKey   string         `json:"parent_draft_key,omitempty"`
	Title            string         `json:"title"`
	Description      string         `json:"description,omitempty"`
	Goal             string         `json:"goal,omitempty"`
	CKP              string         `json:"ckp,omitempty"`
	Reason           string         `json:"reason,omitempty"`
	ClosesRisk       string         `json:"closes_risk,omitempty"`
	MetricName       string         `json:"metric_name,omitempty"`
	MetricCurrent    string         `json:"metric_current,omitempty"`
	MetricTarget     string         `json:"metric_target,omitempty"`
	Metrics          []TacticMetric `json:"metrics,omitempty"`
	WhyNeeded        string         `json:"why_needed,omitempty"`
	SuccessCriteria  string         `json:"success_criteria,omitempty"`
	FailureCriteria  string         `json:"failure_criteria,omitempty"`
	ExpectedValue    string         `json:"expected_value,omitempty"`
	Severity         string         `json:"severity,omitempty"`
	Probability      string         `json:"probability,omitempty"`
	PotentialImpact  string         `json:"potential_impact,omitempty"`
	Urgency          string         `json:"urgency,omitempty"`
	CoverageStatus   string         `json:"coverage_status,omitempty"`
}

type AppliedTacticsChange struct {
	ID         int            `json:"id,omitempty"`
	Operation  string         `json:"operation"`
	EntityType string         `json:"entity_type"`
	EntityID   int            `json:"entity_id"`
	Title      string         `json:"title"`
	Status     string         `json:"status"`
	Fields     map[string]any `json:"fields,omitempty"`
}

type tacticsFacilitatorModelOutput struct {
	Message              string               `json:"message"`
	SessionStatus        string               `json:"session_status"`
	StatusReason         string               `json:"status_reason"`
	CurrentFocus         TacticsFocus         `json:"current_focus"`
	DecisionsDetected    []string             `json:"decisions_detected"`
	OpenQuestions        []string             `json:"open_questions"`
	NeedsStrategyReview  bool                 `json:"needs_strategy_review"`
	StrategyReviewReason string               `json:"strategy_review_reason"`
	DraftChanges         []TacticsDraftChange `json:"draft_changes"`
}

const (
	TacticsReadinessRunQueued     = "queued"
	TacticsReadinessRunRunning    = "running"
	TacticsReadinessRunCompleted  = "completed"
	TacticsReadinessRunFailed     = "failed"
	TacticsReadinessRunSuperseded = "superseded"

	TacticsReadinessVerdictReady              = "ready"
	TacticsReadinessVerdictConditionallyReady = "conditionally_ready"
	TacticsReadinessVerdictNotReady           = "not_ready"
)

type TacticsReadinessRun struct {
	ID                        int                     `json:"id"`
	WorkspaceID               int                     `json:"workspace_id"`
	TacticalPlanID            int                     `json:"tactical_plan_id"`
	StrategyID                int                     `json:"strategy_id"`
	CourseID                  *int                    `json:"course_id,omitempty"`
	SessionRevision           int                     `json:"session_revision"`
	TacticalPlanRevision      int                     `json:"tactical_plan_revision"`
	ValidatedThroughMessageID int                     `json:"validated_through_message_id"`
	Status                    string                  `json:"status"`
	Verdict                   string                  `json:"verdict"`
	CanActivate               bool                    `json:"can_activate"`
	OverallScore              int                     `json:"overall_score"`
	Confidence                string                  `json:"confidence"`
	Report                    *TacticsReadinessReport `json:"report,omitempty"`
	Model                     string                  `json:"model"`
	PromptVersion             string                  `json:"prompt_version"`
	InputTokens               int                     `json:"input_tokens"`
	OutputTokens              int                     `json:"output_tokens"`
	DurationMS                int64                   `json:"duration_ms"`
	Error                     string                  `json:"error,omitempty"`
	CreatedBy                 *int                    `json:"created_by,omitempty"`
	CreatedAt                 time.Time               `json:"created_at"`
	StartedAt                 *time.Time              `json:"started_at,omitempty"`
	CompletedAt               *time.Time              `json:"completed_at,omitempty"`
}

type TacticsReadinessReport struct {
	Verdict                    string                             `json:"verdict"`
	CanActivate                bool                               `json:"can_activate"`
	OverallScore               int                                `json:"overall_score"`
	Confidence                 string                             `json:"confidence"`
	ValidatedThroughMessageID  int                                `json:"validated_through_message_id"`
	SessionRevision            int                                `json:"session_revision"`
	TacticalPlanRevision       int                                `json:"tactical_plan_revision"`
	ExecutiveSummary           string                             `json:"executive_summary"`
	CriteriaAssessment         []TacticsReadinessCriterion        `json:"criteria_assessment"`
	CourseCoverage             []TacticsCourseCoverage            `json:"course_coverage"`
	EntityAssessments          []TacticsEntityAssessment          `json:"entity_assessments"`
	BlockingGaps               []TacticsReadinessIssue            `json:"blocking_gaps"`
	WeakZones                  []TacticsReadinessIssue            `json:"weak_zones"`
	Contradictions             []TacticsReadinessIssue            `json:"contradictions"`
	CriticalAssumptions        []TacticsReadinessAssumption       `json:"critical_assumptions"`
	RedundantOrMisalignedItems []TacticsMisalignedInitiative      `json:"redundant_or_misaligned_initiatives"`
	AdditionalPerspectives     []TacticsReadinessPerspective      `json:"additional_perspectives"`
	FacilitatorGuidance        []TacticsReadinessFacilitatorGuide `json:"facilitator_guidance"`
	ActivationGuidance         TacticsReadinessActivationGuidance `json:"activation_guidance"`
	NeedsStrategyReview        bool                               `json:"needs_strategy_review"`
	StrategyReviewReason       string                             `json:"strategy_review_reason"`
}

type TacticsReadinessCriterion struct {
	CriterionCode string   `json:"criterion_code"`
	Score         int      `json:"score"`
	Assessment    string   `json:"assessment"`
	Strengths     []string `json:"strengths"`
	Gaps          []string `json:"gaps"`
	SourceKeys    []string `json:"source_keys"`
}

type TacticsCourseCoverage struct {
	CourseElement string   `json:"course_element"`
	Coverage      string   `json:"coverage"`
	Assessment    string   `json:"assessment"`
	SourceKeys    []string `json:"source_keys"`
}

type TacticsEntityAssessment struct {
	EntityType string   `json:"entity_type"`
	EntityID   int      `json:"entity_id"`
	Title      string   `json:"title"`
	Status     string   `json:"status"`
	Assessment string   `json:"assessment"`
	SourceKeys []string `json:"source_keys"`
}

type TacticsReadinessIssue struct {
	Area         string   `json:"area"`
	Issue        string   `json:"issue"`
	Impact       string   `json:"impact"`
	NextEvidence string   `json:"next_evidence"`
	SourceKeys   []string `json:"source_keys"`
}

type TacticsReadinessAssumption struct {
	Assumption     string   `json:"assumption"`
	EvidenceStatus string   `json:"evidence_status"`
	TacticalImpact string   `json:"tactical_impact"`
	SourceKeys     []string `json:"source_keys"`
}

type TacticsMisalignedInitiative struct {
	EntityType        string   `json:"entity_type"`
	EntityID          int      `json:"entity_id"`
	Title             string   `json:"title"`
	Reason            string   `json:"reason"`
	RecommendedAction string   `json:"recommended_action"`
	SourceKeys        []string `json:"source_keys"`
}

type TacticsReadinessPerspective struct {
	Perspective  string   `json:"perspective"`
	WhyItMatters string   `json:"why_it_matters"`
	IsBlocking   bool     `json:"is_blocking"`
	SourceKeys   []string `json:"source_keys"`
}

type TacticsReadinessFacilitatorGuide struct {
	Priority       string `json:"priority"`
	Area           string `json:"area"`
	ResearchGoal   string `json:"research_goal"`
	WhyItMatters   string `json:"why_it_matters"`
	ContextToCarry string `json:"context_to_carry"`
	Blocking       bool   `json:"blocking"`
}

type TacticsReadinessActivationGuidance struct {
	ConditionsToActivate []string `json:"conditions_to_activate"`
	WarningsToPreserve   []string `json:"warnings_to_preserve"`
	FirstReviewSignals   []string `json:"first_review_signals"`
}

type TacticsReadinessQueueItem struct {
	WorkspaceID          int       `json:"workspace_id"`
	TacticalPlanID       int       `json:"tactical_plan_id"`
	StrategyID           int       `json:"strategy_id"`
	CourseID             *int      `json:"course_id,omitempty"`
	SessionRevision      int       `json:"session_revision"`
	TacticalPlanRevision int       `json:"tactical_plan_revision"`
	ThroughMessageID     int       `json:"through_message_id"`
	RequestedBy          *int      `json:"requested_by,omitempty"`
	NotBefore            time.Time `json:"not_before"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type TacticsReadinessResponse struct {
	Run       *TacticsReadinessRun `json:"run"`
	IsCurrent bool                 `json:"is_current"`
}

type Uncovered struct {
	Risks                   []Risk               `json:"risks"`
	Opportunities           []Opportunity        `json:"opportunities"`
	WorkstreamsWithoutTasks []TacticsCoverageGap `json:"workstreams_without_tasks"`
	ProjectsWithoutTasks    []TacticsCoverageGap `json:"projects_without_tasks"`
	MissingMetrics          []TacticsCoverageGap `json:"missing_metrics"`
	MissingCKP              []TacticsCoverageGap `json:"missing_ckp"`
	MissingSuccessCriteria  []TacticsCoverageGap `json:"missing_success_criteria"`
}

type TacticsCoverageGap struct {
	EntityType string `json:"entity_type"`
	EntityID   int    `json:"entity_id"`
	Title      string `json:"title"`
	Reason     string `json:"reason"`
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
