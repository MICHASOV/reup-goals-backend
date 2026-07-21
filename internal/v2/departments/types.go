package departments

import "time"

const (
	StatusActive   = "active"
	StatusArchived = "archived"

	ResponsibilityLead        = "lead"
	ResponsibilityParticipant = "participant"

	EntityWorkstream = "workstream"
	EntityProject    = "project"
)

type KPI struct {
	Name    string `json:"name"`
	Current string `json:"current,omitempty"`
	Target  string `json:"target,omitempty"`
}

type Department struct {
	ID              int       `json:"id"`
	WorkspaceID     int       `json:"workspace_id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	Responsibility  string    `json:"responsibility"`
	ManagerUserID   *int      `json:"manager_user_id,omitempty"`
	KPIs            []KPI     `json:"kpis"`
	Status          string    `json:"status"`
	SortOrder       int       `json:"sort_order"`
	MemberCount     int       `json:"member_count"`
	InitiativeCount int       `json:"initiative_count"`
	ProjectCount    int       `json:"project_count"`
	ActiveTaskCount int       `json:"active_task_count"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Member struct {
	UserID    int    `json:"user_id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url,omitempty"`
	Role      string `json:"role"`
}

type EntitySummary struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Role        string `json:"role"`
}

type DocumentLink struct {
	ID           int       `json:"id"`
	DocumentID   int       `json:"document_id"`
	DocumentType string    `json:"document_type"`
	Title        string    `json:"title"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Detail struct {
	Department  Department      `json:"department"`
	Members     []Member        `json:"members"`
	Initiatives []EntitySummary `json:"initiatives"`
	Projects    []EntitySummary `json:"projects"`
	Documents   []DocumentLink  `json:"documents"`
}

type Input struct {
	Name           *string `json:"name"`
	Description    *string `json:"description"`
	Responsibility *string `json:"responsibility"`
	ManagerUserID  *int    `json:"manager_user_id"`
	ClearManager   bool    `json:"clear_manager"`
	MemberUserIDs  []int   `json:"member_user_ids"`
	KPIs           []KPI   `json:"kpis"`
	SortOrder      *int    `json:"sort_order"`
}

type Responsibility struct {
	EntityType               string `json:"entity_type"`
	EntityID                 int    `json:"entity_id"`
	LeadDepartmentID         int    `json:"lead_department_id"`
	ParticipantDepartmentIDs []int  `json:"participant_department_ids"`
}

type ResponsibilityView struct {
	EntityType   string       `json:"entity_type"`
	EntityID     int          `json:"entity_id"`
	Lead         *Department  `json:"lead,omitempty"`
	Participants []Department `json:"participants"`
}
