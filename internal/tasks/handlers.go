package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"

	"reup-goals-backend/internal/ai"

	"github.com/lib/pq"
)

type TaskHandler struct {
	AI *ai.OpenAIClient
	DB *sql.DB
}

func New(aiClient *ai.OpenAIClient, db *sql.DB) *TaskHandler {
	return &TaskHandler{
		AI: aiClient,
		DB: db,
	}
}

type AIParsedResult struct {
	NormalizedTask string `json:"normalized_task"`

	Scores struct {
		Relevance float64 `json:"relevance"`
		Impact    float64 `json:"impact"`
		Urgency   float64 `json:"urgency"`
		Effort    float64 `json:"effort"`
	} `json:"scores"`

	Avoidance   bool   `json:"avoidance_flag"`
	Explanation string `json:"explanation_short"`
}

func (h *TaskHandler) Evaluate(w http.ResponseWriter, r *http.Request) {
	var req EvaluateRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if req.TaskID == 0 {
		http.Error(w, "task_id required", http.StatusBadRequest)
		return
	}

	ctx := context.Background()

	input := ai.BuildPromptInput(
		req.GoalSummary,
		req.TaskRaw,
		req.Deadline,
		req.Duration,
		req.Category,
		req.UserState,
	)

	rawResult, err := h.AI.EvaluateTask(ctx, input)
	if err != nil {
		http.Error(w, "ai error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var parsed AIParsedResult
	if err := json.Unmarshal(rawResult, &parsed); err != nil {
		http.Error(w, "invalid AI JSON: "+err.Error(), 500)
		return
	}

	rel := int(parsed.Scores.Relevance * 100)
	imp := int(parsed.Scores.Impact * 100)
	urg := int(parsed.Scores.Urgency * 100)
	eff := int(parsed.Scores.Effort * 100)

	_, err = h.DB.Exec(`
		INSERT INTO task_ai_state (
			task_id, model_version,
			relevance, impact, urgency, effort,
			normalized_task, avoidance_flag, explanation_short, tags
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`,
		req.TaskID,
		h.AI.Model,
		rel, imp, urg, eff,
		parsed.NormalizedTask,
		parsed.Avoidance,
		parsed.Explanation,
		pq.Array([]string{}),
	)

	if err != nil {
		http.Error(w, "db insert error: "+err.Error(), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(parsed)
}
