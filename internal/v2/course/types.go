package course

import "time"

const (
	StatusDraft    = "draft"
	StatusActive   = "active"
	StatusArchived = "archived"

	SourceFromStrategy = "from_strategy"
	SourceManual       = "manual"
)

type Course struct {
	ID               int        `json:"id"`
	WorkspaceID      int        `json:"workspace_id"`
	StrategyID       int        `json:"strategy_id"`
	Title            string     `json:"title"`
	Direction        string     `json:"direction"`
	StrategicGoal    string     `json:"strategic_goal"`
	Meaning          string     `json:"meaning"`
	Horizon          int        `json:"horizon"`
	HorizonUnit      string     `json:"horizon_unit"`
	StartDate        string     `json:"start_date"`
	EndDate          *string    `json:"end_date"`
	KeyMetric        string     `json:"key_metric"`
	SuccessCriterion string     `json:"success_criterion"`
	Status           string     `json:"status"`
	Source           string     `json:"source"`
	CreatedBy        *int       `json:"created_by,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ActivatedAt      *time.Time `json:"activated_at"`
}

type StrategySummary struct {
	ID      int    `json:"id"`
	Status  string `json:"status"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

type CurrentResponse struct {
	Course   *Course          `json:"course"`
	Strategy *StrategySummary `json:"strategy,omitempty"`
	Reason   string           `json:"reason,omitempty"`
	Message  string           `json:"message,omitempty"`
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
	case StatusDraft, StatusActive, StatusArchived:
		return true
	default:
		return false
	}
}
