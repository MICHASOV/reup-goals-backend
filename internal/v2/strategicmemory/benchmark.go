package strategicmemory

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/v2/api"
)

const maxBenchmarkModels = 6

type modelBenchmarkRequest struct {
	Models    []string `json:"models"`
	Scenarios []string `json:"scenarios"`
}

type modelBenchmarkResponse struct {
	PromptVersion string                 `json:"prompt_version"`
	Models        []string               `json:"models"`
	Scenarios     []benchmarkScenario    `json:"scenarios"`
	Results       []modelBenchmarkResult `json:"results"`
}

type benchmarkScenario struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type modelBenchmarkResult struct {
	Model        string   `json:"model"`
	ScenarioID   string   `json:"scenario_id"`
	LatencyMS    int64    `json:"latency_ms"`
	InputTokens  int      `json:"input_tokens"`
	OutputTokens int      `json:"output_tokens"`
	TotalTokens  int      `json:"total_tokens"`
	EstimatedUSD float64  `json:"estimated_usd"`
	Status       string   `json:"status"`
	Error        string   `json:"error,omitempty"`
	Text         string   `json:"text,omitempty"`
	Notes        []string `json:"notes,omitempty"`
}

type benchmarkPricing struct {
	InputPerMTokens  float64
	OutputPerMTokens float64
}

var defaultBenchmarkModels = []string{
	"gpt-4.1",
	"gpt-5",
	"gpt-5.4-mini",
	"gpt-5.4",
	"gpt-5.5",
}

var allowedBenchmarkModels = map[string]bool{
	"gpt-4.1":      true,
	"gpt-4.1-mini": true,
	"gpt-5":        true,
	"gpt-5-mini":   true,
	"gpt-5.2":      true,
	"gpt-5.4-nano": true,
	"gpt-5.4-mini": true,
	"gpt-5.4":      true,
	"gpt-5.5":      true,
}

var benchmarkPrices = map[string]benchmarkPricing{
	"gpt-4.1":      {InputPerMTokens: 2.00, OutputPerMTokens: 8.00},
	"gpt-4.1-mini": {InputPerMTokens: 0.40, OutputPerMTokens: 1.60},
	"gpt-5":        {InputPerMTokens: 1.25, OutputPerMTokens: 10.00},
	"gpt-5-mini":   {InputPerMTokens: 0.25, OutputPerMTokens: 2.00},
	"gpt-5.2":      {InputPerMTokens: 1.75, OutputPerMTokens: 14.00},
	"gpt-5.4-nano": {InputPerMTokens: 0.20, OutputPerMTokens: 1.25},
	"gpt-5.4-mini": {InputPerMTokens: 0.75, OutputPerMTokens: 4.50},
	"gpt-5.4":      {InputPerMTokens: 2.50, OutputPerMTokens: 15.00},
	"gpt-5.5":      {InputPerMTokens: 5.00, OutputPerMTokens: 30.00},
}

var benchmarkScenarioOrder = []string{
	"first_contact_startup",
	"dense_startup_context",
	"established_b2b_constraints",
	"frustrated_founder",
}

var benchmarkScenarios = map[string]benchmarkScenario{
	"first_contact_startup": {
		ID:          "first_contact_startup",
		Title:       "Первый контакт, почти пустой контекст",
		Description: "Проверяем, умеет ли модель начать живой аудит бизнеса без анкеты.",
	},
	"dense_startup_context": {
		ID:          "dense_startup_context",
		Title:       "Много сырого контекста про стартап",
		Description: "Проверяем, умеет ли модель не пересказывать, а выбрать следующий полезный уточняющий ход.",
	},
	"established_b2b_constraints": {
		ID:          "established_b2b_constraints",
		Title:       "Действующий B2B-сервис с ограничениями",
		Description: "Проверяем работу с цифрами, узкими местами и текущей реальностью.",
	},
	"frustrated_founder": {
		ID:          "frustrated_founder",
		Title:       "Раздражённый пользователь",
		Description: "Проверяем тон, эмпатию и способность вернуть разговор к делу без шаблона.",
	},
}

func (h *Handler) ModelBenchmark(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	var req modelBenchmarkRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	models := sanitizeBenchmarkModels(req.Models)
	scenarioIDs := sanitizeBenchmarkScenarios(req.Scenarios)

	results := make([]modelBenchmarkResult, 0, len(models)*len(scenarioIDs))
	for _, model := range models {
		for _, scenarioID := range scenarioIDs {
			result := h.runModelBenchmark(r.Context(), model, scenarioID)
			results = append(results, result)
		}
	}

	api.WriteJSON(w, http.StatusOK, modelBenchmarkResponse{
		PromptVersion: StrategicMemoryPromptVersion,
		Models:        models,
		Scenarios:     scenarioList(scenarioIDs),
		Results:       results,
	})
}

func (h *Handler) runModelBenchmark(ctx context.Context, model string, scenarioID string) modelBenchmarkResult {
	input := benchmarkInput(scenarioID)
	rawInput, _ := json.Marshal(input)
	client := h.service.ai.ForModel(model)

	started := time.Now()
	result, err := client.GenerateJSONNative(ctx, businessAuditorPrompt, string(rawInput), ai.ResponseContextOptions{MaxOutputTokens: 900})
	latencyMS := time.Since(started).Milliseconds()

	output := modelBenchmarkResult{
		Model:      model,
		ScenarioID: scenarioID,
		LatencyMS:  latencyMS,
		Status:     "success",
	}
	if err != nil {
		output.Status = "failed"
		output.Error = err.Error()
		return output
	}

	var turn auditorTurnOutput
	if err := json.Unmarshal([]byte(result.Text), &turn); err != nil || strings.TrimSpace(turn.Reply) == "" {
		output.Status = "failed"
		output.Error = "invalid structured auditor response"
		return output
	}
	output.Text = cleanAssistantMessage(turn.Reply)
	output.InputTokens = result.Usage.InputTokens
	output.OutputTokens = result.Usage.OutputTokens
	output.TotalTokens = result.Usage.TotalTokens
	output.EstimatedUSD = estimateBenchmarkCost(model, result.Usage)
	output.Notes = quickBenchmarkNotes(output.Text)
	return output
}

func sanitizeBenchmarkModels(models []string) []string {
	if len(models) == 0 {
		return append([]string(nil), defaultBenchmarkModels...)
	}

	seen := map[string]bool{}
	result := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] || !allowedBenchmarkModels[model] {
			continue
		}
		seen[model] = true
		result = append(result, model)
		if len(result) >= maxBenchmarkModels {
			break
		}
	}
	if len(result) == 0 {
		return append([]string(nil), defaultBenchmarkModels...)
	}
	return result
}

func sanitizeBenchmarkScenarios(ids []string) []string {
	if len(ids) == 0 {
		return append([]string(nil), benchmarkScenarioOrder...)
	}

	seen := map[string]bool{}
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if _, ok := benchmarkScenarios[id]; !ok {
			continue
		}
		seen[id] = true
		result = append(result, id)
	}
	if len(result) == 0 {
		return append([]string(nil), benchmarkScenarioOrder...)
	}
	return result
}

func scenarioList(ids []string) []benchmarkScenario {
	result := make([]benchmarkScenario, 0, len(ids))
	for _, id := range ids {
		result = append(result, benchmarkScenarios[id])
	}
	return result
}

func estimateBenchmarkCost(model string, usage ai.Usage) float64 {
	pricing, ok := benchmarkPrices[model]
	if !ok {
		return 0
	}
	inputCost := float64(usage.InputTokens) / 1_000_000 * pricing.InputPerMTokens
	outputCost := float64(usage.OutputTokens) / 1_000_000 * pricing.OutputPerMTokens
	return inputCost + outputCost
}

func quickBenchmarkNotes(text string) []string {
	notes := []string{}
	trimmed := strings.TrimSpace(text)
	if len([]rune(trimmed)) < 180 {
		notes = append(notes, "very_short")
	}
	if strings.Count(trimmed, "?") > 3 {
		notes = append(notes, "many_questions")
	}
	if strings.Contains(strings.ToLower(trimmed), "стратег") {
		notes = append(notes, "mentions_strategy")
	}
	if strings.Contains(trimmed, "1.") || strings.Contains(trimmed, "- ") {
		notes = append(notes, "structured")
	}
	sort.Strings(notes)
	return notes
}

func benchmarkInput(scenarioID string) map[string]any {
	base := map[string]any{
		"workspace_id": 999,
		"known_business_context": map[string]any{
			"compact_snapshot":       nil,
			"existing_claims":        []any{},
			"current_documents":      []any{},
			"open_research_agenda":   []any{},
			"current_dialogue_focus": map[string]any{},
		},
		"communication_style": map[string]any{
			"tone":                    "direct",
			"address_style":           "ты",
			"detail_level":            "normal",
			"structure_preference":    "free_dialogue",
			"frustration_sensitivity": "medium",
		},
		"answer_goal": "Reply to the latest user message as the AI business auditor responsible for collecting, clarifying, checking, and updating business context. Use the known context as memory, not as a questionnaire.",
	}

	switch scenarioID {
	case "dense_startup_context":
		base["latest_user_message"] = "Мы делаем REUP.goals — AI-native SaaS для предпринимателей и небольших команд. Продукт помогает собрать бизнес-контекст, потом на его основе сформировать стратегию, курс, тактику и задачи. Сейчас всё на стадии запуска: есть прототип веб-кабинета, backend, интеграции auth/email/payments, но платящих клиентов пока нет. ЦА пока гипотеза: предприниматели, малый бизнес, команды развития, возможно консалтинг. Боль: люди тонут в задачах, не понимают, что реально двигает бизнес, и хотят стратегического директора, но не могут его нанять. Монетизация предполагается подпиской около 199 рублей в месяц на старте, но это тоже гипотеза. Хочу понять, какой вопрос ты задашь дальше, чтобы лучше понять бизнес, а не ушел в стратегию."
		base["recent_dialogue"] = []map[string]string{
			{"role": "assistant", "content": "Давай соберём реальность бизнеса, не стратегию."},
			{"role": "user", "content": "Ок, у нас пока много гипотез и мало фактов."},
		}
		base["known_business_context"].(map[string]any)["current_documents"] = []map[string]any{
			{"document_type": "company_snapshot", "title": "О компании", "markdown": "REUP.goals — AI-native SaaS на стадии запуска. Платящих клиентов пока нет.", "status": "draft"},
			{"document_type": "customer_reality", "title": "Клиент и спрос", "markdown": "ЦА пока гипотетическая: предприниматели, малый бизнес, команды развития.", "status": "draft"},
		}
	case "established_b2b_constraints":
		base["latest_user_message"] = "У нас студия внедрения CRM для локального бизнеса: клиники, школы, сервисные компании. 24 активных клиента, средний чек внедрения 180 тысяч, поддержка 18 тысяч в месяц. Основная проблема — проекты постоянно расползаются по срокам, маржинальность проседает, основатель лично участвует почти во всех пресейлах и спорных внедрениях. Лидов достаточно, но продажи нестабильные: часть клиентов не понимает ценность аналитики и хочет просто 'настроить Битрикс'."
		base["known_business_context"].(map[string]any)["compact_snapshot"] = "B2B CRM implementation studio, active clients and revenue exist, bottleneck around delivery margins and founder dependency."
		base["known_business_context"].(map[string]any)["existing_claims"] = []map[string]any{
			{"claim": "24 active clients", "confidence": "confirmed"},
			{"claim": "Average implementation check is 180k RUB", "confidence": "confirmed"},
			{"claim": "Founder dependency in sales and escalations", "confidence": "confirmed"},
		}
	case "frustrated_founder":
		base["latest_user_message"] = "Слушай, меня уже бесит отвечать на одни и те же вопросы. Я уже писал, что у нас сервис для HoReCa, мы автоматизируем закупки и склад, но ты опять спрашиваешь 'что за бизнес'. Давай без анкеты, спроси нормально то, что реально важно."
		base["recent_dialogue"] = []map[string]string{
			{"role": "assistant", "content": "Расскажите, что это за бизнес и кому вы продаете?"},
			{"role": "user", "content": "Я же уже писал: HoReCa, закупки, склад, рестораны."},
		}
		base["known_business_context"].(map[string]any)["compact_snapshot"] = "Service for HoReCa: procurement and inventory automation for restaurants. User is frustrated by repeated generic questions."
	default:
		base["latest_user_message"] = "Привет. Давай начнём. У меня есть бизнес, но я пока не хочу заполнять анкету, хочу просто поговорить и чтобы ты понял, что важно."
		base["recent_dialogue"] = []any{}
	}

	if _, ok := base["recent_dialogue"]; !ok {
		base["recent_dialogue"] = []any{}
	}
	base["relevant_older_dialogue"] = []any{}
	return base
}
