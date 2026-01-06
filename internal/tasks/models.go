package tasks

import "time"

type Task struct {
	ID        int       `json:"id"`
	Text      string    `json:"text"`
	Title     string    `json:"title"`
	Description string  `json:"description"`

	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`

	// AI-поле: нормализованная формулировка задачи
	NormalizedTask string `json:"normalized_task"`

	// AI-флаг избегания (ВАЖНО: ключ как во Flutter)
	AvoidanceFlag bool `json:"avoidance_flag"`

	// AI-флаг ловушки / обманки
	TrapTask bool `json:"trap_task"`

	// Нужно ли уточнение от пользователя
	ClarificationNeeded bool `json:"clarification_needed"`

	// Какой вопрос задан пользователю
	ClarificationQuestion string `json:"clarification_question"`

	// AI-объяснение (короткое) (ВАЖНО: ключ как во Flutter)
	ExplanationShort string `json:"explanation_short"`

	// Вычисленный приоритет (не хранится в БД)
	Priority int `json:"priority"`
}