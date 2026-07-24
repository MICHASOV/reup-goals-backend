package metrics

import "time"

const (
	ScopeWorkspace  = "workspace"
	ScopeStrategy   = "strategy"
	ScopeWorkstream = "workstream"
	ScopeProject    = "project"

	RolePrimary    = "primary"
	RoleGuardrail  = "guardrail"
	RoleSupporting = "supporting"
)

type Template struct {
	Key             string   `json:"key"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Category        string   `json:"category"`
	Unit            string   `json:"unit"`
	ValueType       string   `json:"value_type"`
	BetterDirection string   `json:"better_direction"`
	Formula         string   `json:"formula"`
	Interpretation  string   `json:"interpretation"`
	Aliases         []string `json:"aliases"`
}

type Definition struct {
	ID              int64      `json:"id"`
	WorkspaceID     int        `json:"workspace_id"`
	TemplateKey     string     `json:"template_key"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	Category        string     `json:"category"`
	Unit            string     `json:"unit"`
	ValueType       string     `json:"value_type"`
	BetterDirection string     `json:"better_direction"`
	Formula         string     `json:"formula"`
	IsCustom        bool       `json:"is_custom"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	ArchivedAt      *time.Time `json:"archived_at,omitempty"`
}

type Observation struct {
	ID          int64     `json:"id"`
	WorkspaceID int       `json:"workspace_id"`
	MetricID    int64     `json:"metric_id"`
	TargetID    *int64    `json:"target_id,omitempty"`
	Value       float64   `json:"value"`
	MeasuredAt  string    `json:"measured_at"`
	SourceType  string    `json:"source_type"`
	SourceNote  string    `json:"source_note"`
	EvidenceURL string    `json:"evidence_url"`
	Confidence  int       `json:"confidence"`
	CreatedBy   *int      `json:"created_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type Target struct {
	ID            int64         `json:"id"`
	WorkspaceID   int           `json:"workspace_id"`
	MetricID      int64         `json:"metric_id"`
	Metric        Definition    `json:"metric"`
	ScopeType     string        `json:"scope_type"`
	ScopeID       int           `json:"scope_id"`
	Role          string        `json:"role"`
	BaselineValue *float64      `json:"baseline_value,omitempty"`
	TargetValue   *float64      `json:"target_value,omitempty"`
	TargetDate    string        `json:"target_date"`
	DisplayUnit   string        `json:"display_unit"`
	Cadence       string        `json:"cadence"`
	SourceNote    string        `json:"source_note"`
	OwnerUserID   *int          `json:"owner_user_id,omitempty"`
	LatestValue   *float64      `json:"latest_value,omitempty"`
	LatestAt      string        `json:"latest_at"`
	Observations  []Observation `json:"observations"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

type TargetInput struct {
	TemplateKey     string   `json:"template_key"`
	MetricID        int64    `json:"metric_id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Category        string   `json:"category"`
	Unit            string   `json:"unit"`
	ValueType       string   `json:"value_type"`
	BetterDirection string   `json:"better_direction"`
	Formula         string   `json:"formula"`
	ScopeType       string   `json:"scope_type"`
	ScopeID         int      `json:"scope_id"`
	Role            string   `json:"role"`
	BaselineValue   *float64 `json:"baseline_value"`
	TargetValue     *float64 `json:"target_value"`
	TargetDate      string   `json:"target_date"`
	DisplayUnit     string   `json:"display_unit"`
	Cadence         string   `json:"cadence"`
	SourceNote      string   `json:"source_note"`
	OwnerUserID     *int     `json:"owner_user_id"`
	ClearBaseline   bool     `json:"clear_baseline"`
	ClearTarget     bool     `json:"clear_target"`
	ClearTargetDate bool     `json:"clear_target_date"`
}

type ObservationInput struct {
	Value       float64 `json:"value"`
	MeasuredAt  string  `json:"measured_at"`
	SourceType  string  `json:"source_type"`
	SourceNote  string  `json:"source_note"`
	EvidenceURL string  `json:"evidence_url"`
	Confidence  int     `json:"confidence"`
}
