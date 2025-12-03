package tasks

type EvaluateRequest struct {
	GoalSummary            string      `json:"goal_summary"`
	TaskRaw                string      `json:"task_raw"`
	Deadline               *string     `json:"optional_deadline"`
	Duration               *string     `json:"optional_estimated_duration"`
	Category               *string     `json:"optional_category"`
	UserState              *string     `json:"optional_user_state"`
	HistoryMetadata        interface{} `json:"history_metadata"`
}

type EvaluateResponse struct {
	AIResult interface{} `json:"result"`
}
