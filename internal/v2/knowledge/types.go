package knowledge

import "time"

const (
	StatusEmpty           = "empty"
	StatusDraft           = "draft"
	StatusPartiallyFilled = "partially_filled"
	StatusReady           = "ready"

	StatementTypeStatement  = "statement"
	StatementTypeHypothesis = "hypothesis"
	StatementTypeUnknown    = "unknown"

	ConfidenceLow    = "low"
	ConfidenceMedium = "medium"
	ConfidenceHigh   = "high"

	SessionProcessing   = "processing"
	SessionPreviewReady = "preview_ready"
	SessionConfirmed    = "confirmed"
	SessionRejected     = "rejected"
	SessionFailed       = "failed"

	PatchTypeAdd    = "add"
	PatchTypeUpdate = "update"

	PatchStatusSuggested = "suggested"
	PatchStatusApplied   = "applied"
	PatchStatusRejected  = "rejected"

	ConflictStatusActive    = "active"
	ConflictStatusResolved  = "resolved"
	ConflictStatusDismissed = "dismissed"

	ConflictOptionExisting = "existing"
	ConflictOptionNew      = "new"

	RouterPromptVersion     = "knowledge_intake_router_v1_7_pipeline_modes"
	ReconcilerPromptVersion = "knowledge_document_reconciler_v1"
	DocumentComposerVersion = "knowledge_document_composer_v1"

	CompanyProfileCollectorVersion = "company_profile_collector_v2_compact"
	DocumentReadinessVersion       = "document_readiness_preflight_v1"
	GuidancePlannerVersion         = "strategic_guidance_question_planner_v3_4_full_checklist_intent"

	ProfileStatusRed    = "red"
	ProfileStatusOrange = "orange"
	ProfileStatusGreen  = "green"

	ReadinessRed    = "red"
	ReadinessYellow = "yellow"
	ReadinessGreen  = "green"

	KnowledgeReadinessNotReady      = "not_ready"
	KnowledgeReadinessAlmostReady   = "almost_ready"
	KnowledgeReadinessStrategyReady = "strategy_ready"

	QuestionSourceFirstGate = "first_gate"
	QuestionSourcePlanner   = "planner"

	QuestionStatusActive   = "active"
	QuestionStatusAnswered = "answered"

	GuidanceStatusAskNextQuestion           = "ask_next_question"
	GuidanceStatusSuggestStrategyTransition = "suggest_strategy_transition"
)

type Block struct {
	ID          int       `json:"id"`
	WorkspaceID int       `json:"-"`
	Type        string    `json:"type"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Content     string    `json:"content"`
	Status      string    `json:"status"`
	SortOrder   int       `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type BlockDefinition struct {
	Type        string
	Title       string
	Description string
	SortOrder   int
}

type DocumentDefinition struct {
	Type      string
	Title     string
	BlockType string
}

var documentDefinitions = []DocumentDefinition{
	{Type: "company_card", Title: "Карточка компании", BlockType: "company_snapshot"},
	{Type: "current_business_model", Title: "Текущая бизнес-модель", BlockType: "business_model"},
	{Type: "clients_and_demand", Title: "Клиенты и спрос", BlockType: "customer_reality"},
	{Type: "market_and_competition", Title: "Рынок и конкурентная среда", BlockType: "market_arena"},
	{Type: "business_economics", Title: "Экономика бизнеса", BlockType: "economic_engine"},
	{Type: "resources_and_competencies", Title: "Ресурсы и компетенции", BlockType: "resources_capabilities"},
	{Type: "past_experience_and_evidence", Title: "Прошлый опыт и доказательства", BlockType: "past_evidence"},
	{Type: "strategic_challenge", Title: "Главный стратегический вызов", BlockType: "strategic_problem"},
	{Type: "opportunities_and_distractions", Title: "Возможности и отвлечения", BlockType: "opportunities_distractions"},
	{Type: "constraints_and_non_negotiables", Title: "Ограничения и неизменные условия", BlockType: "constraints"},
	{Type: "strategic_refusals", Title: "Стратегические отказы", BlockType: "trade_offs"},
	{Type: "leader_intent_and_risk_profile", Title: "Намерения руководителя и риск-профиль", BlockType: "ceo_intent"},
}

var blockDefinitions = []BlockDefinition{
	{
		Type:        "company_snapshot",
		Title:       "О компании",
		Description: "Базовое описание бизнеса, продукта, команды и текущей ситуации.",
		SortOrder:   1,
	},
	{
		Type:        "business_model",
		Title:       "Бизнес-модель",
		Description: "Как компания создаёт, доставляет и монетизирует ценность.",
		SortOrder:   2,
	},
	{
		Type:        "customer_reality",
		Title:       "Клиент и спрос",
		Description: "Кто клиент, какая у него боль, почему он покупает или не покупает.",
		SortOrder:   3,
	},
	{
		Type:        "market_arena",
		Title:       "Рынок и арена",
		Description: "Где компания конкурирует, с кем сравнивается, какие есть альтернативы.",
		SortOrder:   4,
	},
	{
		Type:        "economic_engine",
		Title:       "Экономика",
		Description: "Деньги, маржинальность, стоимость привлечения, LTV, расходы, runway.",
		SortOrder:   5,
	},
	{
		Type:        "resources_capabilities",
		Title:       "Ресурсы и возможности",
		Description: "Команда, компетенции, активы, каналы, связи, ограничения по ресурсу.",
		SortOrder:   6,
	},
	{
		Type:        "past_evidence",
		Title:       "Что уже доказано",
		Description: "Факты, подтверждения, эксперименты, результаты, которые уже есть.",
		SortOrder:   7,
	},
	{
		Type:        "strategic_problem",
		Title:       "Главная проблема / crux",
		Description: "Ключевой узел, который мешает росту или достижению цели.",
		SortOrder:   8,
	},
	{
		Type:        "opportunities_distractions",
		Title:       "Возможности и отвлечения",
		Description: "Потенциальные точки роста и идеи, которые могут как помочь, так и распылить фокус.",
		SortOrder:   9,
	},
	{
		Type:        "constraints",
		Title:       "Ограничения",
		Description: "Ресурсы, сроки, деньги, юридические, технические и операционные ограничения.",
		SortOrder:   10,
	},
	{
		Type:        "trade_offs",
		Title:       "Осознанные отказы",
		Description: "Что компания сейчас не делает, чтобы сохранить фокус.",
		SortOrder:   11,
	},
	{
		Type:        "ceo_intent",
		Title:       "Намерение CEO / ценности / риск-аппетит",
		Description: "Видение, ценности, риск-аппетит и личное намерение основателя или руководителя.",
		SortOrder:   12,
	},
}

func ValidStatus(status string) bool {
	switch status {
	case StatusEmpty, StatusDraft, StatusPartiallyFilled, StatusReady:
		return true
	default:
		return false
	}
}

func ValidStatementType(value string) bool {
	switch value {
	case StatementTypeStatement, StatementTypeHypothesis, StatementTypeUnknown:
		return true
	default:
		return false
	}
}

func ValidConfidence(value string) bool {
	switch value {
	case ConfidenceLow, ConfidenceMedium, ConfidenceHigh:
		return true
	default:
		return false
	}
}

func ValidDocumentType(value string) bool {
	_, ok := documentDefinitionByType(value)
	return ok
}

func documentDefinitionByType(documentType string) (DocumentDefinition, bool) {
	for _, definition := range documentDefinitions {
		if definition.Type == documentType {
			return definition, true
		}
	}

	return DocumentDefinition{}, false
}
