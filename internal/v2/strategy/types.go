package strategy

import (
	"encoding/json"
	"time"

	"reup-goals-backend/internal/v2/strategicmemory"
)

const (
	StatusDraft          = "draft"
	StatusReadyForReview = "ready_for_review"
	StatusActive         = "active"
	StatusArchived       = "archived"

	ArtifactStatusEmpty       = "empty"
	ArtifactStatusDraft       = "draft"
	ArtifactStatusFilled      = "filled"
	ArtifactStatusNeedsReview = "needs_review"
	ArtifactStatusApproved    = "approved"

	SourceManual = "manual"

	StrategyFacilitatorPromptVersion = "strategy_facilitator_openai_native_v0_2_0"
	StrategySynthesizerPromptVersion = "strategy_synthesizer_v0_1_0"
)

type Strategy struct {
	ID          int        `json:"id"`
	WorkspaceID int        `json:"workspace_id"`
	Status      string     `json:"status"`
	Version     int        `json:"version"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary"`
	SourceType  string     `json:"source_type"`
	CreatedBy   *int       `json:"created_by,omitempty"`
	ApprovedBy  *int       `json:"approved_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ApprovedAt  *time.Time `json:"approved_at"`
	ActivatedAt *time.Time `json:"activated_at"`
}

type Artifact struct {
	ID          int       `json:"id"`
	StrategyID  int       `json:"strategy_id"`
	WorkspaceID int       `json:"-"`
	Type        string    `json:"type"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Content     string    `json:"content"`
	Status      string    `json:"status"`
	SortOrder   int       `json:"sort_order"`
	Confidence  *float64  `json:"confidence"`
	Source      string    `json:"source"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type KnowledgeBaseSummary struct {
	BlocksTotal  int `json:"blocks_total"`
	BlocksReady  int `json:"blocks_ready"`
	BlocksFilled int `json:"blocks_filled"`
	BlocksEmpty  int `json:"blocks_empty"`
}

type StrategyChatMessage struct {
	ID        int       `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type StrategyKnowledgeContext struct {
	Summary       KnowledgeBaseSummary                `json:"summary"`
	Documents     []strategicmemory.StrategicDocument `json:"documents"`
	QualityReport *strategicmemory.QualityReport      `json:"quality_report,omitempty"`
	Files         []strategicmemory.StrategicFile     `json:"files,omitempty"`
}

type StrategyFacilitatorState struct {
	WorkspaceID    int                      `json:"workspace_id"`
	Strategy       Strategy                 `json:"strategy"`
	Artifacts      []Artifact               `json:"artifacts"`
	KnowledgeBase  StrategyKnowledgeContext `json:"knowledge_base"`
	RecentMessages []StrategyChatMessage    `json:"recent_messages"`
	Session        StrategySessionState     `json:"session"`
	Readiness      *StrategyReadinessRun    `json:"readiness,omitempty"`
}

type StrategyFacilitatorMessageRequest struct {
	Message string `json:"message"`
}

type StrategyFacilitatorMessageResponse struct {
	WorkspaceID      int                   `json:"workspace_id"`
	AssistantMessage string                `json:"assistant_message"`
	RecentMessages   []StrategyChatMessage `json:"recent_messages"`
	OpenAIResponseID string                `json:"openai_response_id,omitempty"`
	SessionStatus    string                `json:"session_status"`
	SessionRevision  int                   `json:"session_revision"`
}

const (
	FacilitatorStatusContinue       = "continue"
	FacilitatorStatusCandidateReady = "candidate_ready"
	FacilitatorStatusNeedsResearch  = "needs_research"
)

type strategyFacilitatorModelOutput struct {
	Message                string   `json:"message"`
	SessionStatus          string   `json:"session_status"`
	StatusReason           string   `json:"status_reason"`
	RemainingUncertainties []string `json:"remaining_uncertainties"`
}

type StrategySessionState struct {
	WorkspaceID            int       `json:"workspace_id"`
	Revision               int       `json:"revision"`
	LastUserMessageID      int       `json:"last_user_message_id"`
	LastUserID             *int      `json:"last_user_id,omitempty"`
	FacilitatorStatus      string    `json:"facilitator_status"`
	StatusReason           string    `json:"status_reason"`
	RemainingUncertainties []string  `json:"remaining_uncertainties"`
	LastAuditedRevision    int       `json:"last_audited_revision"`
	LastReadinessRunID     *int      `json:"last_readiness_run_id,omitempty"`
	LastSynthesisRunID     *int      `json:"last_synthesis_run_id,omitempty"`
	UpdatedAt              time.Time `json:"updated_at"`
}

const (
	ReadinessRunQueued     = "queued"
	ReadinessRunRunning    = "running"
	ReadinessRunCompleted  = "completed"
	ReadinessRunFailed     = "failed"
	ReadinessRunSuperseded = "superseded"

	ReadinessVerdictReady              = "ready"
	ReadinessVerdictConditionallyReady = "conditionally_ready"
	ReadinessVerdictNotReady           = "not_ready"
)

type StrategyReadinessRun struct {
	ID                        int                      `json:"id"`
	WorkspaceID               int                      `json:"workspace_id"`
	StrategyID                int                      `json:"strategy_id"`
	SessionRevision           int                      `json:"session_revision"`
	ValidatedThroughMessageID int                      `json:"validated_through_message_id"`
	Status                    string                   `json:"status"`
	Verdict                   string                   `json:"verdict"`
	CanSynthesize             bool                     `json:"can_synthesize"`
	Confidence                string                   `json:"confidence"`
	Report                    *StrategyReadinessReport `json:"report,omitempty"`
	Model                     string                   `json:"model"`
	PromptVersion             string                   `json:"prompt_version"`
	InputTokens               int                      `json:"input_tokens"`
	OutputTokens              int                      `json:"output_tokens"`
	DurationMS                int64                    `json:"duration_ms"`
	Error                     string                   `json:"error,omitempty"`
	CreatedBy                 *int                     `json:"created_by,omitempty"`
	CreatedAt                 time.Time                `json:"created_at"`
	StartedAt                 *time.Time               `json:"started_at,omitempty"`
	CompletedAt               *time.Time               `json:"completed_at,omitempty"`
}

type StrategyReadinessReport struct {
	Verdict                   string                              `json:"verdict"`
	CanSynthesize             bool                                `json:"can_synthesize"`
	ValidatedThroughMessageID int                                 `json:"validated_through_message_id"`
	SessionRevision           int                                 `json:"session_revision"`
	Confidence                string                              `json:"confidence"`
	ExecutiveSummary          string                              `json:"executive_summary"`
	CriteriaAssessment        []StrategyReadinessCriterion        `json:"criteria_assessment"`
	BlockingGaps              []StrategyReadinessIssue            `json:"blocking_gaps"`
	WeakZones                 []StrategyReadinessWeakZone         `json:"weak_zones"`
	Contradictions            []StrategyReadinessContradiction    `json:"contradictions"`
	CriticalAssumptions       []StrategyReadinessAssumption       `json:"critical_assumptions"`
	AdditionalPerspectives    []StrategyReadinessPerspective      `json:"additional_perspectives"`
	FacilitatorGuidance       []StrategyReadinessFacilitatorGuide `json:"facilitator_guidance"`
	SynthesisGuidance         StrategyReadinessSynthesisGuidance  `json:"synthesis_guidance"`
}

type StrategyReadinessCriterion struct {
	Area       string   `json:"area"`
	Status     string   `json:"status"`
	Assessment string   `json:"assessment"`
	SourceKeys []string `json:"source_keys"`
}

type StrategyReadinessIssue struct {
	Area        string   `json:"area"`
	Issue       string   `json:"issue"`
	WhyItBlocks string   `json:"why_it_blocks"`
	SourceKeys  []string `json:"source_keys"`
}

type StrategyReadinessWeakZone struct {
	Area       string   `json:"area"`
	Issue      string   `json:"issue"`
	Impact     string   `json:"impact"`
	SourceKeys []string `json:"source_keys"`
}

type StrategyReadinessContradiction struct {
	Issue        string   `json:"issue"`
	WhyItMatters string   `json:"why_it_matters"`
	SourceKeys   []string `json:"source_keys"`
}

type StrategyReadinessAssumption struct {
	Assumption      string   `json:"assumption"`
	EvidenceStatus  string   `json:"evidence_status"`
	StrategicImpact string   `json:"strategic_impact"`
	SourceKeys      []string `json:"source_keys"`
}

type StrategyReadinessPerspective struct {
	Perspective  string   `json:"perspective"`
	WhyItMatters string   `json:"why_it_matters"`
	IsBlocking   bool     `json:"is_blocking"`
	SourceKeys   []string `json:"source_keys"`
}

type StrategyReadinessFacilitatorGuide struct {
	Priority       string `json:"priority"`
	Area           string `json:"area"`
	ResearchGoal   string `json:"research_goal"`
	WhyItMatters   string `json:"why_it_matters"`
	ContextToCarry string `json:"context_to_carry"`
	Blocking       bool   `json:"blocking"`
}

type StrategyReadinessSynthesisGuidance struct {
	WarningsToPreserve    []string `json:"warnings_to_preserve"`
	AssumptionsToPreserve []string `json:"assumptions_to_preserve"`
	ResearchToInclude     []string `json:"research_to_include"`
	ImportantSourceKeys   []string `json:"important_source_keys"`
}

type StrategyReadinessQueueItem struct {
	WorkspaceID      int       `json:"workspace_id"`
	StrategyID       int       `json:"strategy_id"`
	SessionRevision  int       `json:"session_revision"`
	ThroughMessageID int       `json:"through_message_id"`
	RequestedBy      *int      `json:"requested_by,omitempty"`
	NotBefore        time.Time `json:"not_before"`
	UpdatedAt        time.Time `json:"updated_at"`
}

const (
	SynthesisStatusQueued     = "queued"
	SynthesisStatusRunning    = "running"
	SynthesisStatusCompleted  = "completed"
	SynthesisStatusFailed     = "failed"
	SynthesisStatusSuperseded = "superseded"

	SynthesisDocumentFilled           = "filled"
	SynthesisDocumentInsufficientData = "insufficient_data"
	SynthesisDocumentNotApplicable    = "not_applicable"
)

type StrategySynthesisRun struct {
	ID               int        `json:"id"`
	WorkspaceID      int        `json:"workspace_id"`
	StrategyID       int        `json:"strategy_id"`
	Version          int        `json:"version"`
	SessionRevision  int        `json:"session_revision"`
	ThroughMessageID int        `json:"through_message_id"`
	Status           string     `json:"status"`
	Model            string     `json:"model"`
	PromptVersion    string     `json:"prompt_version"`
	Summary          string     `json:"summary"`
	OpenAIResponseID string     `json:"openai_response_id,omitempty"`
	InputTokens      int        `json:"input_tokens"`
	OutputTokens     int        `json:"output_tokens"`
	DurationMS       int64      `json:"duration_ms"`
	Error            string     `json:"error,omitempty"`
	CreatedBy        *int       `json:"created_by,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
}

type StrategySynthesisContentBlock struct {
	Text       string   `json:"text"`
	SourceKeys []string `json:"source_keys"`
	SourceNote string   `json:"source_note,omitempty"`
}

type StrategySynthesisSourceRef struct {
	Key        string `json:"key"`
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id"`
	Label      string `json:"label"`
	Href       string `json:"href"`
	Supports   string `json:"supports,omitempty"`
}

type StrategySynthesisDocument struct {
	ID            int                             `json:"id"`
	RunID         int                             `json:"run_id"`
	WorkspaceID   int                             `json:"workspace_id"`
	DocumentType  string                          `json:"document_type"`
	Title         string                          `json:"title"`
	Status        string                          `json:"status"`
	ContentBlocks []StrategySynthesisContentBlock `json:"content_blocks"`
	SourceRefs    []StrategySynthesisSourceRef    `json:"source_refs"`
	SortOrder     int                             `json:"sort_order"`
	CreatedAt     time.Time                       `json:"created_at"`
}

type StrategySynthesisResponse struct {
	Run       *StrategySynthesisRun       `json:"run"`
	Documents []StrategySynthesisDocument `json:"documents"`
}

type strategySynthesisModelOutput struct {
	Summary   string                           `json:"summary"`
	Documents []strategySynthesisModelDocument `json:"documents"`
}

type strategySynthesisModelDocument struct {
	DocumentType  string                          `json:"document_type"`
	Title         string                          `json:"title"`
	Status        string                          `json:"status"`
	ContentBlocks []StrategySynthesisContentBlock `json:"content_blocks"`
}

type strategySynthesisSourceCatalogItem struct {
	Key        string `json:"key"`
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id"`
	Label      string `json:"label"`
	Href       string `json:"href"`
}

type strategySynthesisDocumentDefinition struct {
	Type        string
	Title       string
	Description string
	SortOrder   int
}

var strategySynthesisDocumentDefinitions = []strategySynthesisDocumentDefinition{
	{Type: "strategic_diagnosis", Title: "Стратегический диагноз", Description: "Текущее положение компании и обстоятельства, определяющие стратегию.", SortOrder: 1},
	{Type: "key_challenge", Title: "Ключевой вызов компании", Description: "Центральная проблема, противоречие или ограничение компании.", SortOrder: 2},
	{Type: "chosen_direction_and_refusals", Title: "Выбранное направление и сознательные отказы", Description: "Стратегический фокус, причины выбора и отвергнутые альтернативы.", SortOrder: 3},
	{Type: "causal_map", Title: "Карта причинно-следственных связей", Description: "Логика перехода от текущей ситуации и решений к ожидаемому результату.", SortOrder: 4},
	{Type: "goals_and_metrics", Title: "Цели и ключевые метрики", Description: "Цели, сроки, измеримые ориентиры и критерии успеха.", SortOrder: 5},
	{Type: "strategy_economics", Title: "Экономика стратегии", Description: "Финансовая и экономическая логика выбранного направления.", SortOrder: 6},
	{Type: "hypotheses_risks_confidence", Title: "Гипотезы, риски и степень уверенности", Description: "Предположения, риски, подтверждения и уровень неопределённости.", SortOrder: 7},
	{Type: "research_plan", Title: "План необходимых исследований", Description: "Данные и проверки, необходимые для продолжения стратегической работы.", SortOrder: 8},
	{Type: "ninety_day_course", Title: "Курс на ближайшие 90 дней", Description: "Согласованный ближайший курс, приоритеты и ожидаемые результаты периода.", SortOrder: 9},
	{Type: "decision_history", Title: "История принятых решений", Description: "Решения, альтернативы, причины выбора и изменения позиции.", SortOrder: 10},
}

func synthesisDocumentCatalogJSON() json.RawMessage {
	items := make([]map[string]any, 0, len(strategySynthesisDocumentDefinitions))
	for _, definition := range strategySynthesisDocumentDefinitions {
		items = append(items, map[string]any{
			"document_type": definition.Type,
			"title":         definition.Title,
			"description":   definition.Description,
			"sort_order":    definition.SortOrder,
		})
	}
	raw, _ := json.Marshal(items)
	return raw
}

type StrategyOpenAISession struct {
	ID                 int       `json:"id"`
	WorkspaceID        int       `json:"workspace_id"`
	PreviousResponseID string    `json:"previous_response_id,omitempty"`
	CompactThreshold   int       `json:"compact_threshold"`
	PromptCacheKey     string    `json:"prompt_cache_key,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type ArtifactDefinition struct {
	Type        string
	Title       string
	Description string
	SortOrder   int
}

var artifactDefinitions = []ArtifactDefinition{
	{
		Type:        "business_stage",
		Title:       "Стадия бизнеса",
		Description: "Где компания находится сейчас с точки зрения развития и главного ограничения.",
		SortOrder:   1,
	},
	{
		Type:        "global_goal",
		Title:       "Глобальная цель",
		Description: "Долгосрочный желаемый результат компании.",
		SortOrder:   2,
	},
	{
		Type:        "current_challenge",
		Title:       "Вызов текущего этапа",
		Description: "Главный узел, который нужно решить сейчас.",
		SortOrder:   3,
	},
	{
		Type:        "strategic_direction",
		Title:       "Направление",
		Description: "Зона концентрации ресурса компании.",
		SortOrder:   4,
	},
	{
		Type:        "economic_engine",
		Title:       "Экономический двигатель",
		Description: "Механизм, за счёт которого компания зарабатывает и растёт.",
		SortOrder:   5,
	},
	{
		Type:        "key_metric",
		Title:       "Ключевая метрика",
		Description: "Главный показатель, по которому можно понять, движется ли компания правильно.",
		SortOrder:   6,
	},
	{
		Type:        "local_goal",
		Title:       "Локальная цель",
		Description: "Ближайшая измеримая цель внутри текущего горизонта.",
		SortOrder:   7,
	},
	{
		Type:        "tactical_focuses",
		Title:       "Тактические фокусы",
		Description: "Направления работы, через которые реализуется стратегия.",
		SortOrder:   8,
	},
	{
		Type:        "risks_and_hypotheses",
		Title:       "Риски и гипотезы",
		Description: "Что может не сработать и что нужно проверить.",
		SortOrder:   9,
	},
	{
		Type:        "strategy_verdict",
		Title:       "Вердикт стратегии",
		Description: "Текущий вывод: готова ли стратегия, требует ли проверки, чего не хватает.",
		SortOrder:   10,
	},
	{
		Type:        "validation_plan",
		Title:       "План проверки",
		Description: "Как компания будет проверять стратегические предположения.",
		SortOrder:   11,
	},
}

func ValidStrategyStatus(status string) bool {
	switch status {
	case StatusDraft, StatusReadyForReview, StatusActive, StatusArchived:
		return true
	default:
		return false
	}
}

func ValidArtifactStatus(status string) bool {
	switch status {
	case ArtifactStatusEmpty, ArtifactStatusDraft, ArtifactStatusFilled, ArtifactStatusNeedsReview, ArtifactStatusApproved:
		return true
	default:
		return false
	}
}
