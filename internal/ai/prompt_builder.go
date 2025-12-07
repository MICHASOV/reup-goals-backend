package ai

import "strings"

// BuildUserPrompt — формирует строковый input для Responses API
func BuildUserPrompt(
	goalSummary string,
	taskRaw string,
	deadline *string,
	duration *string,
	category *string,
	userState *string,
) string {

	var b strings.Builder

	b.WriteString("goal_summary: ")
	b.WriteString(goalSummary)
	b.WriteString("\n")

	b.WriteString("task_raw: ")
	b.WriteString(taskRaw)
	b.WriteString("\n")

	if deadline != nil && *deadline != "" {
		b.WriteString("optional_deadline: ")
		b.WriteString(*deadline)
		b.WriteString("\n")
	}

	if duration != nil && *duration != "" {
		b.WriteString("optional_estimated_duration: ")
		b.WriteString(*duration)
		b.WriteString("\n")
	}

	if category != nil && *category != "" {
		b.WriteString("optional_category: ")
		b.WriteString(*category)
		b.WriteString("\n")
	}

	if userState != nil && *userState != "" {
		b.WriteString("optional_user_state: ")
		b.WriteString(*userState)
		b.WriteString("\n")
	}

	return b.String()
}
