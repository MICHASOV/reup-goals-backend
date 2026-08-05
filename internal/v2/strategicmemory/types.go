package strategicmemory

import (
	"encoding/json"
	"time"
)

const (
	SourceTypeUserMessage      = "user_message"
	SourceTypeAssistantMessage = "assistant_message"
	SourceTypeFileUpload       = "file_upload"
	SourceTypeDocumentMessage  = "document_user_message"
	SourceTypeStrategyMessage  = "strategy_user_message"
	SourceTypeTacticsMessage   = "tactics_user_message"
	SourceTypeTaskDiscussion   = "task_discussion_user_message"
	SourceTypeWorkspaceDoc     = "workspace_document"
	SourceTypeTaskCompletion   = "task_completion"
	SourceTypeDepartment       = "department"
	SourceTypeTacticalPlan     = "tactical_plan"
	SourceTypeWorkstream       = "tactical_workstream"
	SourceTypeProject          = "tactical_project"
	SourceTypeRisk             = "tactical_risk"
	SourceTypeOpportunity      = "tactical_opportunity"
	SourceTypeHypothesis       = "tactical_hypothesis"
	SourceTypeResearchResult   = "strategy_research_result"

	ConversationStateCollectingContext    = "collecting_context"
	ConversationStateProcessingContext    = "processing_context"
	ConversationStateAwaitingConfirmation = "awaiting_confirmation"
	ConversationStateReadyForStrategy     = "ready_for_strategy"

	KnowledgePipelineCollecting       = "collecting"
	KnowledgePipelineAuditCandidate   = "audit_candidate"
	KnowledgePipelineExtracting       = "extracting"
	KnowledgePipelineReviewing        = "reviewing"
	KnowledgePipelineNeedsMoreContext = "needs_more_context"
	KnowledgePipelineCompiling        = "compiling_documents"
	KnowledgePipelineReady            = "ready"

	ClaimStatusSuggested  = "suggested"
	ClaimStatusConfirmed  = "confirmed"
	ClaimStatusRejected   = "rejected"
	ClaimStatusConflicted = "conflicted"
	ClaimStatusOutdated   = "outdated"

	DefaultCommunicationTone       = "direct"
	DefaultAddressStyle            = "ты"
	DefaultDetailLevel             = "normal"
	DefaultStructurePreference     = "free_dialogue"
	DefaultFrustrationSensitivity  = "medium"
	StrategicMemoryPromptVersion   = "business_auditor_openai_native_v0_6_1"
	DefaultStrategicDocumentStatus = "draft"
)

type RawSource struct {
	ID          int             `json:"id"`
	WorkspaceID int             `json:"workspace_id"`
	UserID      *int            `json:"user_id,omitempty"`
	SourceType  string          `json:"source_type"`
	Content     string          `json:"content"`
	Metadata    json.RawMessage `json:"metadata_json,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

type Claim struct {
	ID            int             `json:"id"`
	WorkspaceID   int             `json:"workspace_id"`
	ClaimText     string          `json:"claim_text"`
	ClaimType     string          `json:"claim_type"`
	TopicKey      string          `json:"topic_key"`
	EvidenceLevel string          `json:"evidence_level"`
	Confidence    string          `json:"confidence"`
	Importance    string          `json:"importance"`
	SourceIDs     json.RawMessage `json:"source_ids_json,omitempty"`
	Status        string          `json:"status"`
	StatusReason  string          `json:"status_reason,omitempty"`
	SupersededBy  *int            `json:"superseded_by_claim_id,omitempty"`
	ReviewedBy    *int            `json:"reviewed_by,omitempty"`
	ReviewedAt    *time.Time      `json:"reviewed_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type ResearchAgendaItem struct {
	ID             int             `json:"id"`
	WorkspaceID    int             `json:"workspace_id"`
	TopicKey       string          `json:"topic_key"`
	QuestionGoal   string          `json:"question_goal"`
	WhyItMatters   string          `json:"why_it_matters"`
	Status         string          `json:"status"`
	Priority       string          `json:"priority"`
	LinkedClaimIDs json.RawMessage `json:"linked_claim_ids_json,omitempty"`
	LastAskedAt    *time.Time      `json:"last_asked_at,omitempty"`
	TimesAsked     int             `json:"times_asked"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type CommunicationProfile struct {
	ID                     int             `json:"id"`
	WorkspaceID            int             `json:"workspace_id"`
	Tone                   string          `json:"tone"`
	AddressStyle           string          `json:"address_style"`
	DetailLevel            string          `json:"detail_level"`
	StructurePreference    string          `json:"structure_preference"`
	FrustrationSensitivity string          `json:"frustration_sensitivity"`
	KnownPreferences       json.RawMessage `json:"known_preferences_json,omitempty"`
	UpdatedAt              time.Time       `json:"updated_at"`
}

type DialogueFocus struct {
	ID                 int             `json:"id"`
	WorkspaceID        int             `json:"workspace_id"`
	CurrentTopic       string          `json:"current_topic"`
	ResearchGoal       string          `json:"research_goal"`
	LastQuestion       string          `json:"last_question"`
	ExpectedAnswerType string          `json:"expected_answer_type"`
	AnswerStatus       string          `json:"answer_status"`
	DoNotRepeat        json.RawMessage `json:"do_not_repeat_json,omitempty"`
	NextAngles         json.RawMessage `json:"next_angles_json,omitempty"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type MemorySnapshot struct {
	ID            int             `json:"id"`
	WorkspaceID   int             `json:"workspace_id"`
	Snapshot      json.RawMessage `json:"snapshot_json"`
	BusinessStage string          `json:"business_stage"`
	Version       int             `json:"version"`
	CreatedAt     time.Time       `json:"created_at"`
}

type StrategicDocument struct {
	ID             int             `json:"id"`
	WorkspaceID    int             `json:"workspace_id"`
	DocumentType   string          `json:"document_type"`
	Title          string          `json:"title"`
	Markdown       string          `json:"markdown"`
	SourceClaimIDs json.RawMessage `json:"source_claim_ids_json,omitempty"`
	Status         string          `json:"status"`
	Version        int             `json:"version"`
	GeneratedAt    time.Time       `json:"generated_at"`
}

type OpenAISession struct {
	ID                 int       `json:"id"`
	WorkspaceID        int       `json:"workspace_id"`
	ConversationID     string    `json:"conversation_id,omitempty"`
	PreviousResponseID string    `json:"previous_response_id,omitempty"`
	VectorStoreID      string    `json:"vector_store_id,omitempty"`
	CompactThreshold   int       `json:"compact_threshold"`
	PromptCacheKey     string    `json:"prompt_cache_key,omitempty"`
	PromptVersion      string    `json:"prompt_version,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type KnowledgePipelineState struct {
	WorkspaceID               int             `json:"workspace_id"`
	Status                    string          `json:"status"`
	ConversationRevision      int             `json:"conversation_revision"`
	LastUserSourceID          int             `json:"last_user_source_id"`
	LastExtractedSourceID     int             `json:"last_extracted_source_id"`
	LastAuditedSourceID       int             `json:"last_audited_source_id"`
	CandidateRevision         int             `json:"candidate_revision"`
	CandidateSourceID         int             `json:"candidate_source_id"`
	ReadyRevision             int             `json:"ready_revision"`
	CompiledRevision          int             `json:"compiled_revision"`
	CandidateReason           string          `json:"candidate_reason,omitempty"`
	AuditFeedback             json.RawMessage `json:"audit_feedback_json,omitempty"`
	CandidateReport           json.RawMessage `json:"-"`
	FeedbackDeliveredRevision int             `json:"feedback_delivered_revision"`
	OnboardingConfirmedBy     *int            `json:"onboarding_confirmed_by,omitempty"`
	OnboardingConfirmedAt     *time.Time      `json:"onboarding_confirmed_at,omitempty"`
	UpdatedAt                 time.Time       `json:"updated_at"`
}

type OnboardingSummary struct {
	WorkspaceID    int       `json:"workspace_id"`
	SourceRevision int       `json:"source_revision"`
	SourceID       int       `json:"source_id"`
	Status         string    `json:"status"`
	Markdown       string    `json:"markdown"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type StrategicFile struct {
	ID            int       `json:"id"`
	WorkspaceID   int       `json:"workspace_id"`
	RawSourceID   *int      `json:"raw_source_id,omitempty"`
	OpenAIFileID  string    `json:"openai_file_id"`
	VectorStoreID string    `json:"vector_store_id"`
	Filename      string    `json:"filename"`
	ContentType   string    `json:"content_type"`
	SizeBytes     int64     `json:"size_bytes"`
	Status        string    `json:"status"`
	Error         string    `json:"error,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type StateResponse struct {
	WorkspaceID          int                    `json:"workspace_id"`
	DocumentCatalog      []DocumentCatalogItem  `json:"document_catalog"`
	Snapshot             *MemorySnapshot        `json:"snapshot,omitempty"`
	Claims               []Claim                `json:"claims"`
	Agenda               []ResearchAgendaItem   `json:"agenda"`
	QualityReport        *QualityReport         `json:"quality_report,omitempty"`
	CommunicationProfile CommunicationProfile   `json:"communication_profile"`
	DialogueFocus        DialogueFocus          `json:"dialogue_focus"`
	Documents            []StrategicDocument    `json:"documents"`
	RecentMessages       []ConversationMessage  `json:"recent_messages"`
	Files                []StrategicFile        `json:"files,omitempty"`
	Pipeline             KnowledgePipelineState `json:"pipeline"`
	OnboardingSummary    *OnboardingSummary     `json:"onboarding_summary,omitempty"`
}

type DocumentCatalogItem struct {
	DocumentType string `json:"document_type"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	SortOrder    int    `json:"sort_order"`
}

type ConversationMessage struct {
	ID        int       `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type DocumentChatMessage struct {
	ID        int       `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type DocumentChatSession struct {
	ConversationID     string
	PreviousResponseID string
	CompactThreshold   int
	PromptCacheKey     string
	ContextFingerprint string
}

type DocumentChatHistoryResponse struct {
	WorkspaceID int                   `json:"workspace_id"`
	Document    StrategicDocument     `json:"document"`
	Messages    []DocumentChatMessage `json:"messages"`
}

type DocumentChatMessageRequest struct {
	Message string `json:"message"`
}

type DocumentChatMessageResponse struct {
	WorkspaceID      int                 `json:"workspace_id"`
	Document         StrategicDocument   `json:"document"`
	UserMessage      DocumentChatMessage `json:"user_message"`
	AssistantMessage DocumentChatMessage `json:"assistant_message"`
	InputTokens      int                 `json:"input_tokens"`
	OutputTokens     int                 `json:"output_tokens"`
}

type MessageRequest struct {
	Message           string `json:"message"`
	ContextDocumentID *int   `json:"context_document_id,omitempty"`
}

type MessageResponse struct {
	WorkspaceID          int                    `json:"workspace_id"`
	AssistantMessage     string                 `json:"assistant_message"`
	ConversationState    string                 `json:"conversation_state"`
	MemoryUpdates        MemoryUpdates          `json:"memory_updates"`
	Snapshot             *MemorySnapshot        `json:"snapshot,omitempty"`
	Documents            []StrategicDocument    `json:"documents"`
	Agenda               []ResearchAgendaItem   `json:"agenda"`
	Claims               []Claim                `json:"claims"`
	CommunicationProfile CommunicationProfile   `json:"communication_profile"`
	DialogueFocus        DialogueFocus          `json:"dialogue_focus"`
	OpenAIResponseID     string                 `json:"openai_response_id,omitempty"`
	Pipeline             KnowledgePipelineState `json:"pipeline"`
	OnboardingSummary    *OnboardingSummary     `json:"onboarding_summary,omitempty"`
}

type auditorTurnOutput struct {
	Reply                 string `json:"reply"`
	ContextReady          bool   `json:"context_ready"`
	ReadinessReason       string `json:"readiness_reason"`
	LegacyAuditCandidate  bool   `json:"audit_candidate,omitempty"`
	LegacyCandidateReason string `json:"candidate_reason,omitempty"`
}

type onboardingSummaryOutput struct {
	SummaryMarkdown string `json:"summary_markdown"`
}

type FileUploadResponse struct {
	WorkspaceID int           `json:"workspace_id"`
	File        StrategicFile `json:"file"`
}

type MemoryUpdates struct {
	ClaimsAdded      int `json:"claims_added"`
	ClaimsSkipped    int `json:"claims_skipped"`
	AgendaUpdated    int `json:"agenda_updated"`
	DocumentsUpdated int `json:"documents_updated"`
}

type QualityReport struct {
	ID                   int                         `json:"id"`
	WorkspaceID          int                         `json:"workspace_id"`
	ReadinessScore       int                         `json:"readiness_score"`
	ReadinessStatus      string                      `json:"readiness_status"`
	ChangedDocumentTypes []string                    `json:"changed_document_types"`
	Overall              QualityOverallAssessment    `json:"overall"`
	Documents            []QualityDocumentAssessment `json:"documents"`
	ChatGuidance         QualityChatGuidance         `json:"chat_guidance"`
	StrategyGate         QualityStrategyGate         `json:"strategy_gate"`
	CreatedAt            time.Time                   `json:"created_at"`
}

type QualityOverallAssessment struct {
	ReadinessScore              int      `json:"readiness_score"`
	ReadinessStatus             string   `json:"readiness_status"`
	Summary                     string   `json:"summary"`
	CriticalBlockers            []string `json:"critical_blockers"`
	StrongestDocuments          []string `json:"strongest_documents"`
	WeakestDocuments            []string `json:"weakest_documents"`
	MostImportantMissingInfo    []string `json:"most_important_missing_information"`
	MajorInconsistencies        []string `json:"major_inconsistencies"`
	MissingConnections          []string `json:"important_missing_connections"`
	RecurringWeaknesses         []string `json:"recurring_weaknesses"`
	HighestPriorityImprovements []string `json:"highest_priority_improvements"`
	HighestPriorityQuestions    []string `json:"highest_priority_clarifications"`
	CrossDocumentQualityScore   int      `json:"cross_document_quality_score"`
}

type QualityCriterionScores struct {
	Completeness    int `json:"completeness"`
	Specificity     int `json:"specificity"`
	EvidenceQuality int `json:"evidence_quality"`
	Freshness       int `json:"freshness"`
	StrategicValue  int `json:"strategic_value"`
	Consistency     int `json:"consistency"`
	Actionability   int `json:"actionability"`
}

type QualityDocumentAssessment struct {
	DocumentType           string                 `json:"document_type"`
	Title                  string                 `json:"title"`
	Relevance              string                 `json:"relevance"`
	RelevanceReason        string                 `json:"relevance_reason"`
	Scores                 QualityCriterionScores `json:"scores"`
	DocumentScore          int                    `json:"document_score"`
	Status                 string                 `json:"status"`
	WhatIsGood             []string               `json:"what_is_good"`
	ProblemAreas           []string               `json:"problem_areas"`
	MissingInformation     []string               `json:"missing_information"`
	Inconsistencies        []string               `json:"inconsistencies"`
	RequiredClarifications []string               `json:"required_clarifications"`
}

type QualityChatGuidance struct {
	NextBestTopic     string   `json:"next_best_topic"`
	NextBestQuestions []string `json:"next_best_questions"`
	AvoidRepeating    []string `json:"avoid_repeating"`
	BlindSpots        []string `json:"blind_spots"`
	WhyThisNext       string   `json:"why_this_next"`
}

type QualityStrategyGate struct {
	CanStartStrategy     bool                     `json:"can_start_strategy"`
	MinimumScoreMet      bool                     `json:"minimum_score_met"`
	NoCriticalBlockers   bool                     `json:"no_critical_blockers"`
	BasicProfileComplete bool                     `json:"basic_profile_complete"`
	GateItems            QualityStrategyGateItems `json:"gate_items"`
	MissingGateItems     []string                 `json:"missing_gate_items"`
	Recommendation       string                   `json:"recommendation"`
}

type QualityStrategyGateItems struct {
	ProductOrService  bool `json:"product_or_service"`
	CustomerOrSegment bool `json:"customer_or_segment"`
	BusinessStage     bool `json:"business_stage"`
	EvidenceStatus    bool `json:"evidence_status"`
	MainProblem       bool `json:"main_problem"`
	KeyConstraints    bool `json:"key_constraints"`
}

type AIRun struct {
	ID            int       `json:"id"`
	WorkspaceID   int       `json:"workspace_id"`
	Scenario      string    `json:"scenario"`
	Model         string    `json:"model"`
	PromptVersion string    `json:"prompt_version"`
	InputTokens   int       `json:"input_tokens"`
	OutputTokens  int       `json:"output_tokens"`
	DurationMs    int       `json:"duration_ms"`
	Status        string    `json:"status"`
	Error         string    `json:"error,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type aiMemoryResponse struct {
	AssistantMessage  string `json:"assistant_message"`
	ConversationState string `json:"conversation_state"`
	BusinessStage     string `json:"business_stage"`
	Claims            []struct {
		ClaimText     string `json:"claim_text"`
		ClaimType     string `json:"claim_type"`
		TopicKey      string `json:"topic_key"`
		EvidenceLevel string `json:"evidence_level"`
		Confidence    string `json:"confidence"`
	} `json:"claims"`
	Snapshot      map[string]any `json:"snapshot"`
	DialogueFocus struct {
		CurrentTopic       string   `json:"current_topic"`
		ResearchGoal       string   `json:"research_goal"`
		LastQuestion       string   `json:"last_question"`
		ExpectedAnswerType string   `json:"expected_answer_type"`
		AnswerStatus       string   `json:"answer_status"`
		DoNotRepeat        []string `json:"do_not_repeat"`
		NextAngles         []string `json:"next_angles"`
	} `json:"dialogue_focus"`
	ResearchAgenda []struct {
		TopicKey     string `json:"topic_key"`
		QuestionGoal string `json:"question_goal"`
		WhyItMatters string `json:"why_it_matters"`
		Status       string `json:"status"`
		Priority     string `json:"priority"`
	} `json:"research_agenda"`
	CommunicationProfile struct {
		Tone                   string         `json:"tone"`
		AddressStyle           string         `json:"address_style"`
		DetailLevel            string         `json:"detail_level"`
		StructurePreference    string         `json:"structure_preference"`
		FrustrationSensitivity string         `json:"frustration_sensitivity"`
		KnownPreferences       map[string]any `json:"known_preferences"`
	} `json:"communication_profile"`
	Documents []struct {
		DocumentType string `json:"document_type"`
		Title        string `json:"title"`
		Markdown     string `json:"markdown"`
		Status       string `json:"status"`
	} `json:"documents"`
}
