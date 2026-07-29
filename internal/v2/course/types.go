package course

import "time"

const (
	StatusDraft       = "draft"
	StatusNeedsReview = "needs_review"
	StatusActive      = "active"
	StatusCompleted   = "completed"
	StatusArchived    = "archived"

	SourceFromStrategy = "from_strategy"
	SourceManual       = "manual"
)

type Course struct {
	ID                    int        `json:"id"`
	WorkspaceID           int        `json:"workspace_id"`
	StrategyID            int        `json:"strategy_id"`
	SourceSynthesisRunID  *int       `json:"source_synthesis_run_id,omitempty"`
	SourceSessionRevision int        `json:"source_session_revision"`
	Title                 string     `json:"title"`
	Direction             string     `json:"direction"`
	StrategicGoal         string     `json:"strategic_goal"`
	Meaning               string     `json:"meaning"`
	Horizon               int        `json:"horizon"`
	HorizonUnit           string     `json:"horizon_unit"`
	StartDate             string     `json:"start_date"`
	EndDate               *string    `json:"end_date"`
	KeyMetric             string     `json:"key_metric"`
	SuccessCriterion      string     `json:"success_criterion"`
	Status                string     `json:"status"`
	Source                string     `json:"source"`
	CreatedBy             *int       `json:"created_by,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	ActivatedAt           *time.Time `json:"activated_at"`
}

type StrategySummary struct {
	ID      int    `json:"id"`
	Status  string `json:"status"`
	Version int    `json:"version"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

type KnowledgeBaseSummary struct {
	DocumentsTotal  int    `json:"documents_total"`
	DocumentsFilled int    `json:"documents_filled"`
	ReadinessScore  int    `json:"readiness_score"`
	ReadinessStatus string `json:"readiness_status"`
	UpdatedAt       string `json:"updated_at,omitempty"`
}

type CourseSource struct {
	ArtifactType string   `json:"artifact_type"`
	Title        string   `json:"title"`
	Summary      string   `json:"summary"`
	Fields       []string `json:"fields"`
}

const (
	SyncCurrent         = "current"
	SyncDraft           = "draft"
	SyncStrategyUpdated = "strategy_updated"
	SyncLegacy          = "legacy"
	SyncUnavailable     = "unavailable"
)

type CourseSync struct {
	State                     string `json:"state"`
	NeedsReview               bool   `json:"needs_review"`
	Message                   string `json:"message"`
	SourceSessionRevision     int    `json:"source_session_revision"`
	CurrentSessionRevision    int    `json:"current_session_revision"`
	SourceSynthesisRunID      *int   `json:"source_synthesis_run_id,omitempty"`
	CurrentSynthesisRunID     *int   `json:"current_synthesis_run_id,omitempty"`
	CurrentSynthesisIsCurrent bool   `json:"current_synthesis_is_current"`
}

type CurrentResponse struct {
	Course        *Course              `json:"course"`
	Strategy      *StrategySummary     `json:"strategy,omitempty"`
	Sync          *CourseSync          `json:"sync,omitempty"`
	LatestReview  *CourseReview        `json:"latest_review,omitempty"`
	Sources       []CourseSource       `json:"sources"`
	KnowledgeBase KnowledgeBaseSummary `json:"knowledge_base"`
	Reason        string               `json:"reason,omitempty"`
	Message       string               `json:"message,omitempty"`
}

type CourseReview struct {
	ID           int64     `json:"id"`
	WorkspaceID  int       `json:"workspace_id"`
	CourseID     int       `json:"course_id"`
	Result       string    `json:"result"`
	MetricResult string    `json:"metric_result"`
	Outcome      string    `json:"outcome"`
	Decision     string    `json:"decision"`
	CreatedBy    *int      `json:"created_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type CourseReviewInput struct {
	Result       string `json:"result"`
	MetricResult string `json:"metric_result"`
	Outcome      string `json:"outcome"`
	Decision     string `json:"decision"`
}

type CourseInput struct {
	Title            string  `json:"title"`
	Direction        string  `json:"direction"`
	StrategicGoal    string  `json:"strategic_goal"`
	Meaning          string  `json:"meaning"`
	Horizon          *int    `json:"horizon"`
	HorizonUnit      string  `json:"horizon_unit"`
	StartDate        string  `json:"start_date"`
	EndDate          *string `json:"end_date"`
	KeyMetric        string  `json:"key_metric"`
	SuccessCriterion string  `json:"success_criterion"`
	Status           string  `json:"status"`
}

func ValidStatus(status string) bool {
	switch status {
	case StatusDraft, StatusNeedsReview, StatusActive, StatusCompleted, StatusArchived:
		return true
	default:
		return false
	}
}

func ValidReviewOutcome(outcome string) bool {
	switch outcome {
	case "achieved", "partially_achieved", "not_achieved", "changed":
		return true
	default:
		return false
	}
}

func ValidReviewDecision(decision string) bool {
	switch decision {
	case "continue", "revise", "complete":
		return true
	default:
		return false
	}
}

var requiredCourseFields = []string{
	"title",
	"direction",
	"strategic_goal",
	"key_metric",
	"success_criterion",
}
