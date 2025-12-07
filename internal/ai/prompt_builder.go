package ai

import (
	"strings"
)

// BuildUserPrompt формирует user prompt из входных данных.
// Memory/summary_for_ai пока не используется — только goal + task.
func BuildUserPrompt(
	goalSummary string,
	taskRaw string,
	deadline *string,
	duration *string,
	category *string,
	userState *string,
) string {

	var b strings.Builder

	// Обязательные поля
	b.WriteString("goal_summary: ")
	b.WriteString(goalSummary)
	b.WriteString("\n")

	b.WriteString("task_raw: ")
	b.WriteString(taskRaw)
	b.WriteString("\n")

	// Опциональные поля — добавляем только если они существуют
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

	// history_metadata пока **игнорируем** в v1

	return b.String()
}

// BuildChatPrompt — формирует массив сообщений формата Responses API
func BuildChatPrompt(
	goalSummary string,
	taskRaw string,
	deadline *string,
	duration *string,
	category *string,
	userState *string,
) []map[string]string {

	userContent := BuildUserPrompt(goalSummary, taskRaw, deadline, duration, category, userState)

	return []map[string]string{
		{
			"role":    "system",
			"content": SystemPrompt,
		},
		{
			"role":    "user",
			"content": userContent,
		},
	}
}
