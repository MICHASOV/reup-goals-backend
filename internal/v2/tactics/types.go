package tactics

import "time"

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
	Workstreams  []Workstream     `json:"workstreams"`
	Uncovered    Uncovered        `json:"uncovered"`
	Reason       string           `json:"reason,omitempty"`
	Message      string           `json:"message,omitempty"`
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
