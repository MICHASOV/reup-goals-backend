package strategicmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"reup-goals-backend/internal/ai"
)

const (
	materializerMaxOutputTokens     = 4500
	documentDesignerMaxOutputTokens = 12000
	materializerTimeout             = 150 * time.Second
)

type materializerOutput struct {
	BusinessStage  string                      `json:"business_stage"`
	ExtractedItems []materializerItem          `json:"extracted_items"`
	DocumentBrief  []materializerDocumentNote  `json:"document_brief"`
	OpenQuestions  []materializerQuestion      `json:"open_questions"`
	Contradictions []materializerContradiction `json:"contradictions"`
	Snapshot       map[string]any              `json:"snapshot"`
}

type materializerItem struct {
	Text               string   `json:"text"`
	Type               string   `json:"type"`
	EvidenceLevel      string   `json:"evidence_level"`
	Confidence         string   `json:"confidence"`
	PrimaryDocument    string   `json:"primary_document"`
	RelatedDocuments   []string `json:"related_documents"`
	TimeContext        string   `json:"time_context"`
	Importance         string   `json:"importance"`
	RelationToExisting string   `json:"relation_to_existing"`
}

type materializerDocumentNote struct {
	DocumentType  string   `json:"document_type"`
	UpdateGoal    string   `json:"update_goal"`
	KeyPoints     []string `json:"key_points"`
	OpenQuestions []string `json:"open_questions"`
}

type materializerQuestion struct {
	TopicKey     string `json:"topic_key"`
	QuestionGoal string `json:"question_goal"`
	WhyItMatters string `json:"why_it_matters"`
	Priority     string `json:"priority"`
}

type materializerContradiction struct {
	TopicKey        string `json:"topic_key"`
	Summary         string `json:"summary"`
	FirstStatement  string `json:"first_statement"`
	SecondStatement string `json:"second_statement"`
	Status          string `json:"status"`
}

type documentDesignerOutput struct {
	Documents []documentDesignerDocument `json:"documents"`
}

type documentDesignerDocument struct {
	DocumentType string `json:"document_type"`
	Title        string `json:"title"`
	Markdown     string `json:"markdown"`
	Status       string `json:"status"`
}

func (s *Service) queueBusinessContextMaterialization(workspaceID int, sourceID int, userMessage string, assistantMessage string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), materializerTimeout)
		defer cancel()

		if err := s.materializeBusinessContext(ctx, workspaceID, sourceID, userMessage, assistantMessage); err != nil {
			log.Printf("[WARN] strategic memory materialization failed workspace_id=%d: %v", workspaceID, err)
		}
	}()
}

func (s *Service) materializeBusinessContext(ctx context.Context, workspaceID int, sourceID int, userMessage string, assistantMessage string) error {
	state, err := s.State(ctx, workspaceID)
	if err != nil {
		return err
	}
	session, err := s.store.OpenAISession(ctx, workspaceID, s.compactThreshold)
	if err != nil {
		return err
	}

	materialized, err := s.extractBusinessContext(ctx, workspaceID, sourceID, userMessage, assistantMessage, state, session)
	if err != nil {
		return err
	}

	claimsAdded, claimsSkipped, err := s.store.InsertClaims(ctx, workspaceID, sourceID, materializerItemsToClaims(materialized.ExtractedItems))
	if err != nil {
		return err
	}
	agendaUpdated, err := s.store.UpsertAgenda(ctx, workspaceID, materializerQuestionsToAgenda(materialized.OpenQuestions, materialized.Contradictions))
	if err != nil {
		return err
	}
	if len(materialized.Snapshot) > 0 {
		if _, err := s.store.SaveSnapshot(ctx, workspaceID, materialized.BusinessStage, materialized.Snapshot); err != nil {
			return err
		}
	}

	documentsUpdated := 0
	if len(materialized.ExtractedItems) > 0 || len(materialized.DocumentBrief) > 0 || len(materialized.Contradictions) > 0 {
		updatedState, err := s.State(ctx, workspaceID)
		if err != nil {
			return err
		}

		updatedDocuments, err := s.designDocuments(ctx, workspaceID, materialized, updatedState, session)
		if err != nil {
			log.Printf("[WARN] strategic document designer failed workspace_id=%d: %v", workspaceID, err)
			updatedDocuments = fallbackDocumentsFromMaterialized(workspaceID, materialized, updatedState)
		}
		if len(updatedDocuments) == 0 {
			log.Printf("[WARN] strategic document designer returned no documents workspace_id=%d", workspaceID)
			updatedDocuments = fallbackDocumentsFromMaterialized(workspaceID, materialized, updatedState)
		}
		documentsUpdated, err = s.store.UpsertDocuments(ctx, workspaceID, updatedDocuments)
		if err != nil {
			return err
		}
	}

	log.Printf(
		"[INFO] strategic memory materialized workspace_id=%d claims_added=%d claims_skipped=%d agenda_updated=%d documents_updated=%d",
		workspaceID,
		claimsAdded,
		claimsSkipped,
		agendaUpdated,
		documentsUpdated,
	)
	return nil
}

func (s *Service) extractBusinessContext(
	ctx context.Context,
	workspaceID int,
	sourceID int,
	userMessage string,
	assistantMessage string,
	state StateResponse,
	session OpenAISession,
) (materializerOutput, error) {
	input := map[string]any{
		"workspace_id": workspaceID,
		"new_source": map[string]any{
			"source_id": sourceID,
			"type":      SourceTypeUserMessage,
			"content":   userMessage,
		},
		"assistant_reply":  assistantMessage,
		"document_catalog": strategicDocumentCatalog(),
		"current_memory": map[string]any{
			"snapshot":              state.Snapshot,
			"claims":                limitClaimsForContext(state.Claims, 120),
			"documents":             limitDocumentsForContext(state.Documents, 13),
			"research_agenda":       limitAgendaForContext(state.Agenda, 40),
			"dialogue_focus":        state.DialogueFocus,
			"communication_profile": state.CommunicationProfile,
			"recent_dialogue":       state.RecentMessages,
			"files":                 state.Files,
		},
	}
	rawInput, _ := json.Marshal(input)

	started := time.Now()
	result, err := s.ai.GenerateJSONNative(ctx, businessContextMaterializerPrompt, string(rawInput), ai.ResponseContextOptions{
		VectorStoreIDs:       vectorStoreIDsFromSession(session),
		CompactThreshold:     session.CompactThreshold,
		PromptCacheKey:       fmt.Sprintf("reupgoals-materializer-workspace-%d-v1", workspaceID),
		MaxFileSearchResults: 8,
		MaxOutputTokens:      materializerMaxOutputTokens,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil {
		s.store.LogAIRunWithUsage(ctx, workspaceID, "business_context_materializer", s.ai.Model, StrategicMemoryPromptVersion, duration, 0, 0, "failed", err.Error())
		return materializerOutput{}, err
	}
	s.store.LogAIRunWithUsage(ctx, workspaceID, "business_context_materializer", s.ai.Model, StrategicMemoryPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")

	var parsed materializerOutput
	if err := json.Unmarshal([]byte(result.Text), &parsed); err != nil {
		return materializerOutput{}, fmt.Errorf("materializer json decode error: %w", err)
	}
	return parsed, nil
}

func (s *Service) designDocuments(
	ctx context.Context,
	workspaceID int,
	materialized materializerOutput,
	state StateResponse,
	session OpenAISession,
) ([]StrategicDocument, error) {
	input := map[string]any{
		"workspace_id":            workspaceID,
		"document_catalog":        strategicDocumentCatalog(),
		"affected_document_types": affectedDocumentTypes(materialized),
		"current_documents":       limitDocumentsForContext(state.Documents, 13),
		"new_extracted_items":     materialized.ExtractedItems,
		"document_brief":          materialized.DocumentBrief,
		"open_questions":          materialized.OpenQuestions,
		"contradictions":          materialized.Contradictions,
	}
	rawInput, _ := json.Marshal(input)

	started := time.Now()
	result, err := s.ai.GenerateJSONNative(ctx, documentVisualDesignerPrompt, string(rawInput), ai.ResponseContextOptions{
		VectorStoreIDs:       vectorStoreIDsFromSession(session),
		CompactThreshold:     session.CompactThreshold,
		PromptCacheKey:       fmt.Sprintf("reupgoals-document-designer-workspace-%d-v1", workspaceID),
		MaxFileSearchResults: 8,
		MaxOutputTokens:      documentDesignerMaxOutputTokens,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil {
		s.store.LogAIRunWithUsage(ctx, workspaceID, "business_document_visual_designer", s.ai.Model, StrategicMemoryPromptVersion, duration, 0, 0, "failed", err.Error())
		return nil, err
	}
	s.store.LogAIRunWithUsage(ctx, workspaceID, "business_document_visual_designer", s.ai.Model, StrategicMemoryPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")

	var parsed documentDesignerOutput
	if err := json.Unmarshal([]byte(result.Text), &parsed); err != nil {
		return nil, fmt.Errorf("document designer json decode error: %w", err)
	}

	documents := make([]StrategicDocument, 0, len(parsed.Documents))
	for _, doc := range parsed.Documents {
		markdown := strings.TrimSpace(doc.Markdown)
		if markdown == "" {
			continue
		}
		docType := normalizeDocumentType(doc.DocumentType)
		documents = append(documents, StrategicDocument{
			WorkspaceID:  workspaceID,
			DocumentType: docType,
			Title:        defaultString(doc.Title, documentTitle(docType)),
			Markdown:     markdown,
			Status:       normalizeDocumentStatus(doc.Status),
		})
	}
	return documents, nil
}

func materializerItemsToClaims(items []materializerItem) []aiMemoryResponseClaim {
	claims := make([]aiMemoryResponseClaim, 0, len(items))
	for _, item := range items {
		text := cleanText(item.Text)
		if text == "" {
			continue
		}
		claims = append(claims, aiMemoryResponseClaim{
			ClaimText:     text,
			ClaimType:     item.Type,
			TopicKey:      normalizeDocumentType(item.PrimaryDocument),
			EvidenceLevel: item.EvidenceLevel,
			Confidence:    item.Confidence,
		})
	}
	return claims
}

func materializerQuestionsToAgenda(questions []materializerQuestion, contradictions []materializerContradiction) []ResearchAgendaItem {
	items := make([]ResearchAgendaItem, 0, len(questions)+len(contradictions))
	for _, question := range questions {
		goal := cleanText(question.QuestionGoal)
		if goal == "" {
			continue
		}
		items = append(items, ResearchAgendaItem{
			TopicKey:     normalizeDocumentType(question.TopicKey),
			QuestionGoal: goal,
			WhyItMatters: cleanText(question.WhyItMatters),
			Status:       "open",
			Priority:     normalizePriority(question.Priority),
		})
	}
	for _, contradiction := range contradictions {
		summary := cleanText(contradiction.Summary)
		if summary == "" {
			continue
		}
		items = append(items, ResearchAgendaItem{
			TopicKey:     normalizeDocumentType(defaultString(contradiction.TopicKey, "contradictions_changes")),
			QuestionGoal: "Уточнить противоречие: " + summary,
			WhyItMatters: cleanText(strings.Join([]string{contradiction.FirstStatement, contradiction.SecondStatement}, " / ")),
			Status:       "open",
			Priority:     "high",
		})
	}
	return items
}

func affectedDocumentTypes(materialized materializerOutput) []string {
	seen := map[string]bool{}
	result := []string{}
	add := func(value string) {
		docType := normalizeDocumentType(value)
		if docType == "" || seen[docType] {
			return
		}
		seen[docType] = true
		result = append(result, docType)
	}
	for _, item := range materialized.ExtractedItems {
		add(item.PrimaryDocument)
		for _, related := range item.RelatedDocuments {
			add(related)
		}
	}
	for _, note := range materialized.DocumentBrief {
		add(note.DocumentType)
	}
	for _, question := range materialized.OpenQuestions {
		add(question.TopicKey)
	}
	for _, contradiction := range materialized.Contradictions {
		add(defaultString(contradiction.TopicKey, "contradictions_changes"))
		add("contradictions_changes")
	}
	return result
}

func strategicDocumentCatalog() []map[string]string {
	return []map[string]string{
		{"document_type": "company_governance", "title": "Компания и управление"},
		{"document_type": "strategy_development", "title": "Стратегия и развитие"},
		{"document_type": "product_value", "title": "Продукт и ценность"},
		{"document_type": "customers_market_competition", "title": "Клиенты, рынок и конкуренты"},
		{"document_type": "marketing_sales_relationships", "title": "Маркетинг, продажи и клиентские отношения"},
		{"document_type": "operations_execution", "title": "Операционная деятельность и исполнение"},
		{"document_type": "team_organization", "title": "Команда и организация"},
		{"document_type": "technology_data_assets", "title": "Технологии, данные и активы"},
		{"document_type": "finance_economics", "title": "Финансы и экономика"},
		{"document_type": "legal_compliance", "title": "Право и соответствие требованиям"},
		{"document_type": "hypotheses_assumptions", "title": "Гипотезы и непроверенные предположения"},
		{"document_type": "open_questions", "title": "Открытые вопросы"},
		{"document_type": "contradictions_changes", "title": "Противоречия и изменения"},
	}
}

func normalizeDocumentType(value string) string {
	switch normalizeTopicKey(value) {
	case "company", "company_profile", "company_card", "company_snapshot", "company_governance":
		return "company_governance"
	case "strategy", "strategy_development":
		return "strategy_development"
	case "product", "product_value":
		return "product_value"
	case "customers", "customer", "demand", "customers_and_demand", "customer_reality", "market", "market_arena", "customers_market_competition":
		return "customers_market_competition"
	case "marketing", "sales", "marketing_sales", "marketing_sales_relationships":
		return "marketing_sales_relationships"
	case "operations", "execution", "operations_execution":
		return "operations_execution"
	case "team", "team_organization", "resources_capabilities":
		return "team_organization"
	case "technology", "data", "assets", "technology_data_assets":
		return "technology_data_assets"
	case "finance", "economics", "economic_engine", "finance_economics":
		return "finance_economics"
	case "legal", "compliance", "legal_compliance":
		return "legal_compliance"
	case "hypothesis", "hypotheses", "assumptions", "hypotheses_assumptions":
		return "hypotheses_assumptions"
	case "open_question", "open_questions", "unknowns", "evidence_and_unknowns":
		return "open_questions"
	case "contradiction", "contradictions", "changes", "contradictions_changes":
		return "contradictions_changes"
	default:
		return normalizeTopicKey(value)
	}
}

func normalizeDocumentStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "draft", "useful", "strong":
		return value
	case "ready":
		return "strong"
	case "partially_filled":
		return "useful"
	default:
		return DefaultStrategicDocumentStatus
	}
}

func fallbackDocumentsFromMaterialized(workspaceID int, materialized materializerOutput, state StateResponse) []StrategicDocument {
	affected := affectedDocumentTypes(materialized)
	if len(affected) == 0 {
		return nil
	}

	existing := map[string]StrategicDocument{}
	for _, doc := range state.Documents {
		existing[normalizeDocumentType(doc.DocumentType)] = doc
	}

	itemsByDoc := map[string][]materializerItem{}
	for _, item := range materialized.ExtractedItems {
		docType := normalizeDocumentType(item.PrimaryDocument)
		if docType == "" {
			continue
		}
		itemsByDoc[docType] = append(itemsByDoc[docType], item)
	}

	questionsByDoc := map[string][]materializerQuestion{}
	for _, question := range materialized.OpenQuestions {
		docType := normalizeDocumentType(question.TopicKey)
		if docType == "" {
			continue
		}
		questionsByDoc[docType] = append(questionsByDoc[docType], question)
	}

	docs := make([]StrategicDocument, 0, len(affected))
	for _, docType := range affected {
		current := existing[docType]
		var builder strings.Builder
		if strings.TrimSpace(current.Markdown) != "" {
			builder.WriteString(strings.TrimSpace(current.Markdown))
			builder.WriteString("\n\n")
		} else {
			builder.WriteString("# ")
			builder.WriteString(documentTitle(docType))
			builder.WriteString("\n\n")
		}

		if items := itemsByDoc[docType]; len(items) > 0 {
			builder.WriteString("## Последние уточнения\n\n")
			for _, item := range items {
				text := cleanText(item.Text)
				if text == "" {
					continue
				}
				builder.WriteString("- ")
				builder.WriteString(text)
				if item.Type != "" || item.EvidenceLevel != "" || item.Confidence != "" {
					builder.WriteString("  \n  _")
					builder.WriteString(strings.Trim(strings.Join([]string{
						cleanText(item.Type),
						cleanText(item.EvidenceLevel),
						cleanText(item.Confidence),
					}, " / "), " /"))
					builder.WriteString("_")
				}
				builder.WriteString("\n")
			}
			builder.WriteString("\n")
		}

		if questions := questionsByDoc[docType]; len(questions) > 0 {
			builder.WriteString("## Открытые вопросы\n\n")
			for _, question := range questions {
				goal := cleanText(question.QuestionGoal)
				if goal == "" {
					continue
				}
				builder.WriteString("- ")
				builder.WriteString(goal)
				if why := cleanText(question.WhyItMatters); why != "" {
					builder.WriteString("  \n  _")
					builder.WriteString(why)
					builder.WriteString("_")
				}
				builder.WriteString("\n")
			}
			builder.WriteString("\n")
		}

		status := current.Status
		if status == "" {
			status = DefaultStrategicDocumentStatus
		}
		docs = append(docs, StrategicDocument{
			WorkspaceID:  workspaceID,
			DocumentType: docType,
			Title:        defaultString(current.Title, documentTitle(docType)),
			Markdown:     strings.TrimSpace(builder.String()),
			Status:       normalizeDocumentStatus(status),
		})
	}
	return docs
}
