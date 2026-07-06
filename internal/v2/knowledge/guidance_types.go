package knowledge

import "encoding/json"

type ConversationIntent struct {
	HasIntent    bool   `json:"has_intent"`
	IntentType   string `json:"intent_type"`
	RawText      string `json:"raw_text"`
	CleanText    string `json:"clean_text"`
	HandlingNote string `json:"handling_note,omitempty"`
}

func defaultConversationIntent(intent ConversationIntent) ConversationIntent {
	if intent.IntentType == "" {
		intent.IntentType = "business_context"
	}
	if intent.IntentType == "business_context" {
		intent.HasIntent = false
	}
	return intent
}

type CompanyProfile struct {
	Status           string          `json:"status"`
	ProfileText      string          `json:"profile_text"`
	BaselineCoverage json.RawMessage `json:"baseline_coverage"`
	Raw              json.RawMessage `json:"-"`
}

type KnowledgeBaseReadiness struct {
	OverallStatus             string `json:"overall_status"`
	OverallScore              int    `json:"overall_score"`
	StrategyTransitionAllowed bool   `json:"strategy_transition_allowed"`
}

type DocumentReadiness struct {
	DocumentID             int             `json:"-"`
	DocumentType           string          `json:"document_type"`
	Title                  string          `json:"title,omitempty"`
	ReadinessStatus        string          `json:"readiness_status"`
	ReadinessReason        string          `json:"readiness_reason,omitempty"`
	MainMissingAreas       json.RawMessage `json:"main_missing_areas,omitempty"`
	ShouldRunDeepEvaluator bool            `json:"should_run_deep_evaluator"`
	Confidence             string          `json:"confidence"`
	EntriesCount           int             `json:"entries_count,omitempty"`
}

type GuidanceQuestionBlock struct {
	ID                      int             `json:"id"`
	Source                  string          `json:"source,omitempty"`
	GuidanceStatus          string          `json:"guidance_status"`
	QuestionType            string          `json:"question_type"`
	IntendedFocusSummary    string          `json:"intended_focus_summary,omitempty"`
	IntendedDocuments       json.RawMessage `json:"intended_documents,omitempty"`
	SelectionReasonInternal string          `json:"-"`
	Title                   string          `json:"title"`
	Intro                   string          `json:"intro"`
	Questions               []string        `json:"questions"`
	HandledUserIntent       json.RawMessage `json:"handled_user_intent,omitempty"`
	Confidence              string          `json:"confidence,omitempty"`
}

type GuidanceBootstrapResponse struct {
	WorkspaceID            int                    `json:"workspace_id"`
	Mode                   string                 `json:"mode"`
	CompanyProfile         CompanyProfile         `json:"company_profile"`
	KnowledgeBaseReadiness KnowledgeBaseReadiness `json:"knowledge_base_readiness"`
	Documents              []DocumentReadiness    `json:"documents"`
	ActiveQuestionBlock    GuidanceQuestionBlock  `json:"active_question_block"`
}

type GuidancePreviewResponse struct {
	SessionID          int                      `json:"session_id"`
	Status             string                   `json:"status"`
	ConversationIntent ConversationIntent       `json:"conversation_intent"`
	UpdatedDocuments   []IntakeDocumentPreview  `json:"updated_documents"`
	Conflicts          []IntakeConflict         `json:"conflicts"`
	IgnoredItems       []IntakeIgnoredItem      `json:"ignored_items"`
	UnroutedFragments  []RouterUnroutedFragment `json:"unrouted_fragments,omitempty"`
	NextQuestionBlock  *GuidanceQuestionBlock   `json:"next_question_block,omitempty"`
}

type GuidanceConfirmResponse struct {
	Status                   string                 `json:"status"`
	Mode                     string                 `json:"mode"`
	CompanyProfile           CompanyProfile         `json:"company_profile"`
	KnowledgeBaseReadiness   KnowledgeBaseReadiness `json:"knowledge_base_readiness"`
	DocumentReadinessUpdates []DocumentReadiness    `json:"document_readiness_updates"`
	NextQuestionBlock        GuidanceQuestionBlock  `json:"next_question_block"`
	AppliedChanges           IntakeConfirmResponse  `json:"applied_changes"`
}

type companyProfileCollectorResponse struct {
	CompanyGateSignal             string          `json:"company_gate_signal"`
	CanContinueToAdaptiveGuidance bool            `json:"can_continue_to_adaptive_guidance"`
	ProfileText                   string          `json:"profile_text"`
	BaselineCoverage              json.RawMessage `json:"baseline_coverage"`
	BusinessProfileNotes          json.RawMessage `json:"business_profile_notes"`
	MissingPoints                 json.RawMessage `json:"missing_points"`
	ClarificationQuestionBlock    struct {
		Title     string   `json:"title"`
		Intro     string   `json:"intro"`
		Questions []string `json:"questions"`
	} `json:"clarification_question_block"`
}

type readinessPreflightResponse struct {
	DocumentType           string   `json:"document_type"`
	ReadinessStatus        string   `json:"readiness_status"`
	ReadinessReason        string   `json:"readiness_reason"`
	MainMissingAreas       []string `json:"main_missing_areas"`
	ShouldRunDeepEvaluator bool     `json:"should_run_deep_evaluator"`
	Confidence             string   `json:"confidence"`
}

type guidancePlannerResponse struct {
	GuidanceStatus string `json:"guidance_status"`
	QuestionType   string `json:"question_type"`
	IntendedFocus  struct {
		FocusSummary            string   `json:"focus_summary"`
		IntendedDocuments       []string `json:"intended_documents"`
		SelectionReasonInternal string   `json:"selection_reason_internal"`
	} `json:"intended_focus"`
	QuestionBlock struct {
		Title     string   `json:"title"`
		Intro     string   `json:"intro"`
		Questions []string `json:"questions"`
	} `json:"question_block"`
	HandledUserIntent struct {
		IntentType      string `json:"intent_type"`
		HandlingSummary string `json:"handling_summary"`
	} `json:"handled_user_intent"`
	Confidence string `json:"confidence"`
}
