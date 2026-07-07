package knowledge

import "time"

type IntakeSession struct {
	ID          int       `json:"id"`
	WorkspaceID int       `json:"workspace_id"`
	UserID      int       `json:"user_id"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type IntakePreviewResponse struct {
	SessionID         int                      `json:"session_id"`
	Status            string                   `json:"status"`
	InputSummary      string                   `json:"input_summary"`
	UpdatedDocuments  []IntakeDocumentPreview  `json:"updated_documents"`
	Conflicts         []IntakeConflict         `json:"conflicts"`
	IgnoredItems      []IntakeIgnoredItem      `json:"ignored_items"`
	UnroutedFragments []RouterUnroutedFragment `json:"unrouted_fragments"`
}

type IntakeDocumentPreview struct {
	DocumentID   int           `json:"document_id"`
	DocumentType string        `json:"document_type"`
	Title        string        `json:"title"`
	Patches      []IntakePatch `json:"patches"`
}

type IntakePatch struct {
	ID            int      `json:"id"`
	DocumentID    int      `json:"document_id"`
	DocumentType  string   `json:"document_type"`
	PatchType     string   `json:"patch_type"`
	SourceItemIDs []string `json:"source_item_ids"`
	ExistingText  string   `json:"existing_text"`
	NewText       string   `json:"new_text"`
	Reason        string   `json:"reason"`
	Status        string   `json:"status"`
}

type IntakeConflict struct {
	ID             int      `json:"id"`
	DocumentID     int      `json:"document_id"`
	DocumentType   string   `json:"document_type"`
	DocumentTitle  string   `json:"document_title"`
	SourceItemIDs  []string `json:"source_item_ids"`
	ExistingText   string   `json:"existing_text"`
	NewText        string   `json:"new_text"`
	Question       string   `json:"question"`
	OptionAText    string   `json:"option_a_text"`
	OptionBText    string   `json:"option_b_text"`
	Status         string   `json:"status"`
	SelectedOption string   `json:"selected_option,omitempty"`
}

type IntakeIgnoredItem struct {
	DocumentID    int      `json:"document_id"`
	DocumentType  string   `json:"document_type"`
	DocumentTitle string   `json:"document_title"`
	SourceItemIDs []string `json:"source_item_ids"`
	CleanText     string   `json:"clean_text"`
	Reason        string   `json:"reason"`
}

type IntakeConfirmResponse struct {
	SessionID        int `json:"session_id"`
	UpdatedDocuments int `json:"updated_documents"`
	AppliedChanges   int `json:"applied_changes"`
}

type RouterResponse struct {
	InputSummary       string                   `json:"input_summary"`
	ConversationIntent ConversationIntent       `json:"conversation_intent"`
	Items              []RouterItem             `json:"items"`
	UnroutedFragments  []RouterUnroutedFragment `json:"unrouted_fragments"`
}

type RouterItem struct {
	ClientItemID   string `json:"client_item_id"`
	SourceQuote    string `json:"source_quote"`
	CleanText      string `json:"clean_text"`
	StatementType  string `json:"statement_type"`
	TargetDocument string `json:"target_document"`
	RoutingReason  string `json:"routing_reason"`
	Confidence     string `json:"confidence"`
}

type RouterUnroutedFragment struct {
	SourceQuote string `json:"source_quote"`
	Reason      string `json:"reason"`
}

type ReconcilerResponse struct {
	DocumentType          string                  `json:"document_type"`
	DocumentUpdateSummary string                  `json:"document_update_summary"`
	Patches               []ReconcilerPatch       `json:"patches"`
	Conflicts             []ReconcilerConflict    `json:"conflicts"`
	IgnoredItems          []ReconcilerIgnoredItem `json:"ignored_items"`
}

type ReconcilerPatch struct {
	ClientPatchID        string   `json:"client_patch_id"`
	PatchType            string   `json:"patch_type"`
	SourceItemIDs        []string `json:"source_item_ids"`
	TargetEntryID        string   `json:"target_entry_id"`
	ExistingText         string   `json:"existing_text"`
	NewText              string   `json:"new_text"`
	Reason               string   `json:"reason"`
	RequiresConfirmation bool     `json:"requires_confirmation"`
}

type ReconcilerConflict struct {
	ClientConflictID string   `json:"client_conflict_id"`
	SourceItemIDs    []string `json:"source_item_ids"`
	ExistingEntryID  string   `json:"existing_entry_id"`
	ExistingText     string   `json:"existing_text"`
	NewText          string   `json:"new_text"`
	Question         string   `json:"question"`
	OptionAText      string   `json:"option_a_text"`
	OptionBText      string   `json:"option_b_text"`
}

type ReconcilerIgnoredItem struct {
	SourceItemIDs []string `json:"source_item_ids"`
	CleanText     string   `json:"clean_text"`
	Reason        string   `json:"reason"`
}

type documentComposerResponse struct {
	DocumentType string `json:"document_type"`
	Title        string `json:"title"`
	RenderedText string `json:"rendered_text"`
	Sections     []struct {
		Title  string   `json:"title"`
		Points []string `json:"points"`
	} `json:"sections"`
	SourceEntryIDs []string `json:"source_entry_ids"`
	Confidence     string   `json:"confidence"`
}
