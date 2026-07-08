package strategicmemory

import (
	"encoding/json"
	"time"
)

const (
	SourceTypeUserMessage      = "user_message"
	SourceTypeAssistantMessage = "assistant_message"

	ConversationStateCollectingContext = "collecting_context"

	ClaimStatusActive = "active"

	DefaultCommunicationTone       = "direct"
	DefaultAddressStyle            = "ты"
	DefaultDetailLevel             = "normal"
	DefaultStructurePreference     = "free_dialogue"
	DefaultFrustrationSensitivity  = "medium"
	StrategicMemoryPromptVersion   = "strategic_memory_v0_1_single_pass"
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
	SourceIDs     json.RawMessage `json:"source_ids_json,omitempty"`
	Status        string          `json:"status"`
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

type StateResponse struct {
	WorkspaceID          int                   `json:"workspace_id"`
	Snapshot             *MemorySnapshot       `json:"snapshot,omitempty"`
	Claims               []Claim               `json:"claims"`
	Agenda               []ResearchAgendaItem  `json:"agenda"`
	CommunicationProfile CommunicationProfile  `json:"communication_profile"`
	Documents            []StrategicDocument   `json:"documents"`
	RecentMessages       []ConversationMessage `json:"recent_messages"`
}

type ConversationMessage struct {
	ID        int       `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type MessageRequest struct {
	Message           string `json:"message"`
	ContextDocumentID *int   `json:"context_document_id,omitempty"`
}

type MessageResponse struct {
	WorkspaceID          int                  `json:"workspace_id"`
	AssistantMessage     string               `json:"assistant_message"`
	ConversationState    string               `json:"conversation_state"`
	MemoryUpdates        MemoryUpdates        `json:"memory_updates"`
	Snapshot             *MemorySnapshot      `json:"snapshot,omitempty"`
	Documents            []StrategicDocument  `json:"documents"`
	Agenda               []ResearchAgendaItem `json:"agenda"`
	Claims               []Claim              `json:"claims"`
	CommunicationProfile CommunicationProfile `json:"communication_profile"`
}

type MemoryUpdates struct {
	ClaimsAdded      int `json:"claims_added"`
	ClaimsSkipped    int `json:"claims_skipped"`
	AgendaUpdated    int `json:"agenda_updated"`
	DocumentsUpdated int `json:"documents_updated"`
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
	Snapshot       map[string]any `json:"snapshot"`
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
