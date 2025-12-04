package tasks

import "time"

type Task struct {
    ID        int       `json:"id"`
    Text      string    `json:"text"`
    Status    string    `json:"status"`
    CreatedAt time.Time `json:"created_at"`
}
