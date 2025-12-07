package tasks

import "time"

type Task struct {
	ID        int       `json:"id"`
	Text      string    `json:"text"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`

	// AI-поле: нормализованная формулировка задачи
	NormalizedTask string `json:"normalized_task,omitempty"`

	// AI-флаг избегания
	Avoidance bool `json:"avoidance,omitempty"`

	// AI-объяснение (короткое)
	Explanation string `json:"explanation,omitempty"`

	// Вычисленный приоритет (не хранится в БД)
	Priority int `json:"priority"`
}
