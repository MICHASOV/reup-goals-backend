package tasks

import (
	"context"
	"encoding/json"
	"net/http"

	"reup-goals-backend/internal/ai"
)

type TaskHandler struct {
	AI *ai.OpenAIClient
}

func New(aiClient *ai.OpenAIClient) *TaskHandler {
	return &TaskHandler{AI: aiClient}
}

func (h *TaskHandler) Evaluate(w http.ResponseWriter, r *http.Request) {
	var req EvaluateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	input := map[string]interface{}{
		"goal_summary":                  req.GoalSummary,
		"task_raw":                      req.TaskRaw,
		"optional_deadline":             req.Deadline,
		"optional_estimated_duration":   req.Duration,
		"optional_category":             req.Category,
		"optional_user_state":           req.UserState,
		"history_metadata":              nil,
	}

	ctx := context.Background()

	result, err := h.AI.EvaluateTask(ctx, input)
	if err != nil {
		http.Error(w, "ai error: "+err.Error(), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(EvaluateResponse{
		AIResult: json.RawMessage(result),
	})
}
