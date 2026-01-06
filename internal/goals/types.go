package goals

import "time"

type Goal struct {
    ID          int       `json:"id"`
    Title       string    `json:"title"`
    Description string    `json:"description"`
    Context     string    `json:"context"`
    IsActive    bool      `json:"is_active"`
    CreatedAt   time.Time `json:"created_at"`
}
