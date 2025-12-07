package ai

// BuildPromptInput — создаёт JSON input для Path B
func BuildPromptInput(
	goalSummary string,
	taskRaw string,
	deadline *string,
	duration *string,
	category *string,
	userState *string,
) map[string]interface{} {

	return map[string]interface{}{
		"goal_summary":                goalSummary,
		"task_raw":                    taskRaw,
		"optional_deadline":           deadline,
		"optional_estimated_duration": duration,
		"optional_category":           category,
		"optional_user_state":         userState,
		"history_metadata":            nil,
	}
}
