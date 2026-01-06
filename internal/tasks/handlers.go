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

// --- NEW STRUCT FOR SUBFACTORS ---

type AIParsedResult struct {
	NormalizedTask string `json:"normalized_task"`

	Scores struct {
		RelevanceSub struct {
			DirectFit   int `json:"direct_fit"`
			Bottleneck  int `json:"bottleneck"`
			CoreSupport int `json:"core_support"`
			Decoy       int `json:"decoy"`
		} `json:"relevance_sub"`

		ImpactSub struct {
			Depth         int `json:"depth"`
			Breadth       int `json:"breadth"`
			Compound      int `json:"compound"`
			RiskReduction int `json:"risk_reduction"`
		} `json:"impact_sub"`

		UrgencySub struct {
			DeadlinePressure int `json:"deadline_pressure"`
			EffortVsTime     int `json:"effort_vs_time"`
			CostOfDelay      int `json:"cost_of_delay"`
			Interdependence  int `json:"interdependence"`
		} `json:"urgency_sub"`

		EffortSub struct {
			Complexity  int `json:"complexity"`
			Emotion     int `json:"emotion"`
			Uncertainty int `json:"uncertainty"`
		} `json:"effort_sub"`
	} `json:"scores"`

	AvoidanceFlag       bool   `json:"avoidance_flag"`
	TrapTask            bool   `json:"trap_task"`
	ClarificationNeeded bool   `json:"clarification_needed"`
	ClarificationQ      string `json:"clarification_question"`
	Explanation         string `json:"explanation_short"`
}

// EvaluateRequest у тебя уже есть в проекте.
// Если он лежит в другом файле пакета tasks — оставь там.
// Здесь мы его НЕ дублируем.

// ✅ НОВОЕ: общая функция, чтобы create/update могли запускать AI напрямую (без HTTP вызова /task/evaluate)
func (h *TaskHandler) EvaluateAndStore(
	ctx context.Context,
	taskID int,
	goalSummary string,
	taskRaw string,
) (AIParsedResult, error) {

	var deadline *string
var duration *string
var category *string
var userState *string

input := ai.BuildUserPrompt(
  goalSummary,
  taskRaw,
  deadline,
  duration,
  category,
  userState,
)


	// Call OpenAI
	rawResult, err := h.AI.EvaluateTask(ctx, input)
	if err != nil {
		return AIParsedResult{}, err
	}

	// Parse JSON result
	var parsed AIParsedResult
	if err := json.Unmarshal(rawResult, &parsed); err != nil {
		return AIParsedResult{}, err
	}

	// --- CALCULATE FINAL SCORES ---
	relevance := int(
		float64(parsed.Scores.RelevanceSub.DirectFit)*0.4 +
			float64(parsed.Scores.RelevanceSub.Bottleneck)*0.3 +
			float64(parsed.Scores.RelevanceSub.CoreSupport)*0.2 +
			float64(parsed.Scores.RelevanceSub.Decoy)*0.1,
	)

	impact := int(
		float64(parsed.Scores.ImpactSub.Depth)*0.4 +
			float64(parsed.Scores.ImpactSub.Breadth)*0.3 +
			float64(parsed.Scores.ImpactSub.Compound)*0.2 +
			float64(parsed.Scores.ImpactSub.RiskReduction)*0.1,
	)

	urgency := int(
		float64(parsed.Scores.UrgencySub.DeadlinePressure)*0.4 +
			float64(parsed.Scores.UrgencySub.EffortVsTime)*0.3 +
			float64(parsed.Scores.UrgencySub.CostOfDelay)*0.2 +
			float64(parsed.Scores.UrgencySub.Interdependence)*0.1,
	)

	effort := int(
		(float64(parsed.Scores.EffortSub.Complexity)*1.0+
			float64(parsed.Scores.EffortSub.Emotion)*0.5+
			float64(parsed.Scores.EffortSub.Uncertainty)*0.5)/2,
	)

	// --- UPSERT AI RESULT INTO DATABASE (1 row per task) ---
_, err = h.DB.Exec(`
	INSERT INTO task_ai_state (
		task_id, model_version,
		relevance, impact, urgency, effort,
		normalized_task, avoidance_flag, trap_task,
		clarification_needed, clarification_question,
		explanation_short,
		tags,
		updated_at
	)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13, now())
	ON CONFLICT (task_id) DO UPDATE SET
		model_version = EXCLUDED.model_version,
		relevance = EXCLUDED.relevance,
		impact = EXCLUDED.impact,
		urgency = EXCLUDED.urgency,
		effort = EXCLUDED.effort,
		normalized_task = EXCLUDED.normalized_task,
		avoidance_flag = EXCLUDED.avoidance_flag,
		trap_task = EXCLUDED.trap_task,
		clarification_needed = EXCLUDED.clarification_needed,
		clarification_question = EXCLUDED.clarification_question,
		explanation_short = EXCLUDED.explanation_short,
		tags = EXCLUDED.tags,
		updated_at = now()
`,
	taskID,
	h.AI.Model,

	relevance,
	impact,
	urgency,
	effort,

	parsed.NormalizedTask,
	parsed.AvoidanceFlag,
	parsed.TrapTask,
	parsed.ClarificationNeeded,
	parsed.ClarificationQ,
	parsed.Explanation,

	pq.Array([]string{}),
)
	if err != nil {
		return AIParsedResult{}, err
	}

	return parsed, nil
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

	parsed, err := h.EvaluateAndStore(ctx, req.TaskID, req.GoalSummary, req.TaskRaw)
	if err != nil {
		http.Error(w, "ai/db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Return parsed LLM output to client
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(parsed)
}
