package strategy

import (
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

	StrategyFacilitatorPromptVersion = "strategy_facilitator_openai_native_v0_1_0"
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
}

type StrategyFacilitatorMessageRequest struct {
	Message string `json:"message"`
}

type StrategyFacilitatorMessageResponse struct {
	WorkspaceID      int                   `json:"workspace_id"`
	AssistantMessage string                `json:"assistant_message"`
	RecentMessages   []StrategyChatMessage `json:"recent_messages"`
	OpenAIResponseID string                `json:"openai_response_id,omitempty"`
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
