package strategicmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/v2/contextindex"
	"reup-goals-backend/internal/v2/jobs"
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
	ExistingClaimID    int      `json:"existing_claim_id"`
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

type materializationOptions struct {
	SourceType            string
	PreferredDocumentType string
	FactsOnly             bool
}

const (
	jobTypeMaterializeBusinessContext = "knowledge_base.materialize"
	jobTypeKnowledgeQualityAudit      = "knowledge_base.quality_audit"
)

type materializationJobPayload struct {
	SourceID         int                    `json:"source_id"`
	UserMessage      string                 `json:"user_message"`
	AssistantMessage string                 `json:"assistant_message"`
	Options          materializationOptions `json:"options"`
}

type qualityAuditJobPayload struct {
	ChangedDocumentTypes []string `json:"changed_document_types"`
	Trigger              string   `json:"trigger"`
}

func (s *Service) registerJobHandlers() {
	s.jobs.Register(jobTypeMaterializeBusinessContext, func(ctx context.Context, job jobs.Job) error {
		if job.WorkspaceID == nil {
			return fmt.Errorf("materialization job has no workspace")
		}
		var payload materializationJobPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return err
		}
		return s.materializeBusinessContextWithOptions(
			ctx, *job.WorkspaceID, payload.SourceID, payload.UserMessage, payload.AssistantMessage, payload.Options,
		)
	})
	s.jobs.Register(jobTypeKnowledgeQualityAudit, func(ctx context.Context, job jobs.Job) error {
		if job.WorkspaceID == nil {
			return fmt.Errorf("quality audit job has no workspace")
		}
		var payload qualityAuditJobPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return err
		}
		_, err := s.RunQualityAudit(ctx, *job.WorkspaceID, payload.ChangedDocumentTypes, payload.Trigger)
		return err
	})
}

func (s *Service) queueBusinessContextMaterialization(workspaceID int, sourceID int, userMessage string, assistantMessage string) {
	s.queueBusinessContextMaterializationWithOptions(workspaceID, sourceID, userMessage, assistantMessage, materializationOptions{
		SourceType: SourceTypeUserMessage,
	})
}

func (s *Service) queueDocumentContextMaterialization(
	workspaceID int,
	sourceID int,
	documentType string,
	userMessage string,
	assistantMessage string,
) {
	s.queueBusinessContextMaterializationWithOptions(workspaceID, sourceID, userMessage, assistantMessage, materializationOptions{
		SourceType:            SourceTypeDocumentMessage,
		PreferredDocumentType: documentType,
	})
}

func (s *Service) QueueFactsFromStrategy(
	ctx context.Context,
	workspaceID int,
	userID int,
	strategyMessageID int,
	userMessage string,
) error {
	sourceID, err := s.store.CreateRawSource(ctx, workspaceID, &userID, SourceTypeStrategyMessage, userMessage, map[string]any{
		"strategy_chat_message_id": strategyMessageID,
		"facts_only":               true,
	})
	if err != nil {
		return err
	}
	s.queueBusinessContextMaterializationWithOptions(workspaceID, sourceID, userMessage, "", materializationOptions{
		SourceType: SourceTypeStrategyMessage,
		FactsOnly:  true,
	})
	return nil
}

func (s *Service) queueBusinessContextMaterializationWithOptions(
	workspaceID int,
	sourceID int,
	userMessage string,
	assistantMessage string,
	options materializationOptions,
) {
	if s.jobs != nil {
		_, err := s.jobs.Enqueue(context.Background(), workspaceID, jobTypeMaterializeBusinessContext,
			fmt.Sprintf("source:%d", sourceID), materializationJobPayload{
				SourceID: sourceID, UserMessage: userMessage, AssistantMessage: assistantMessage, Options: options,
			}, 5, time.Now().UTC())
		if err != nil {
			log.Printf("[WARN] strategic memory materialization enqueue failed workspace_id=%d: %v", workspaceID, err)
		}
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), materializerTimeout)
		defer cancel()

		if err := s.materializeBusinessContextWithOptions(ctx, workspaceID, sourceID, userMessage, assistantMessage, options); err != nil {
			log.Printf("[WARN] strategic memory materialization failed workspace_id=%d: %v", workspaceID, err)
		}
	}()
}

func (s *Service) materializeBusinessContext(ctx context.Context, workspaceID int, sourceID int, userMessage string, assistantMessage string) error {
	return s.materializeBusinessContextWithOptions(ctx, workspaceID, sourceID, userMessage, assistantMessage, materializationOptions{
		SourceType: SourceTypeUserMessage,
	})
}

func (s *Service) materializeBusinessContextWithOptions(
	ctx context.Context,
	workspaceID int,
	sourceID int,
	userMessage string,
	assistantMessage string,
	options materializationOptions,
) error {
	state, err := s.State(ctx, workspaceID)
	if err != nil {
		return err
	}
	session, err := s.store.OpenAISession(ctx, workspaceID, s.compactThreshold)
	if err != nil {
		return err
	}

	materialized, err := s.extractBusinessContext(ctx, workspaceID, sourceID, userMessage, assistantMessage, state, session, options)
	if err != nil {
		return err
	}
	if options.FactsOnly {
		materialized = factsOnlyMaterialization(materialized)
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
		if documentsUpdated > 0 {
			s.queueQualityAudit(workspaceID, documentTypesFromStrategicDocuments(updatedDocuments), "documents_updated")
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
	if s.contextIndex != nil {
		s.contextIndex.RefreshAsync(workspaceID)
	}
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
	options materializationOptions,
) (materializerOutput, error) {
	sourceType := defaultString(options.SourceType, SourceTypeUserMessage)
	input := map[string]any{
		"workspace_id": workspaceID,
		"new_source": map[string]any{
			"source_id": sourceID,
			"type":      sourceType,
			"content":   userMessage,
		},
		"preferred_document_type": strings.TrimSpace(options.PreferredDocumentType),
		"facts_only":              options.FactsOnly,
		"assistant_reply":         assistantMessage,
		"document_catalog":        strategicDocumentCatalog(),
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
	vectorStoreIDs, indexed := s.workspaceContextVectorStoreIDs(ctx, workspaceID, session)
	if indexed {
		delete(input, "current_memory")
		input["current_workspace_context"] = "Use file_search to compare the new source with the current knowledge base and avoid duplicates."
	}
	rawInput, _ := json.Marshal(input)

	aiCtx := ai.WithScenario(ctx, workspaceID, 0, "business_context_materializer", StrategicMemoryPromptVersion)
	started := time.Now()
	result, err := s.ai.GenerateJSONNative(aiCtx, businessContextMaterializerPrompt+contextindex.RetrievalInstructions, string(rawInput), ai.ResponseContextOptions{
		VectorStoreIDs:       vectorStoreIDs,
		CompactThreshold:     session.CompactThreshold,
		PromptCacheKey:       fmt.Sprintf("reupgoals-materializer-workspace-%d-v1", workspaceID),
		MaxFileSearchResults: 8,
		MaxOutputTokens:      materializerMaxOutputTokens,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil {
		s.store.LogAIRunWithUsage(ctx, workspaceID, "business_context_materializer", s.ai.ModelName(), StrategicMemoryPromptVersion, duration, 0, 0, "failed", err.Error())
		return materializerOutput{}, err
	}
	s.store.LogAIRunWithUsage(ctx, workspaceID, "business_context_materializer", s.ai.ModelName(), StrategicMemoryPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")

	var parsed materializerOutput
	if err := json.Unmarshal([]byte(result.Text), &parsed); err != nil {
		return materializerOutput{}, fmt.Errorf("materializer json decode error: %w", err)
	}
	return parsed, nil
}

func factsOnlyMaterialization(materialized materializerOutput) materializerOutput {
	allowedTypes := map[string]bool{
		"fact": true, "historical_fact": true, "process": true, "problem": true,
		"constraint": true, "metric": true, "result": true, "contradiction": true,
	}
	filtered := make([]materializerItem, 0, len(materialized.ExtractedItems))
	affected := map[string]bool{}
	for _, item := range materialized.ExtractedItems {
		if !allowedTypes[strings.TrimSpace(item.Type)] {
			continue
		}
		filtered = append(filtered, item)
		affected[normalizeDocumentType(item.PrimaryDocument)] = true
	}
	materialized.ExtractedItems = filtered
	materialized.OpenQuestions = nil

	brief := make([]materializerDocumentNote, 0, len(materialized.DocumentBrief))
	for _, note := range materialized.DocumentBrief {
		if !affected[normalizeDocumentType(note.DocumentType)] {
			continue
		}
		note.OpenQuestions = nil
		brief = append(brief, note)
	}
	materialized.DocumentBrief = brief
	return materialized
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
	vectorStoreIDs, indexed := s.workspaceContextVectorStoreIDs(ctx, workspaceID, session)
	if indexed {
		delete(input, "current_documents")
		input["current_workspace_context"] = "Use file_search to read the current versions of affected documents before updating them."
	}
	rawInput, _ := json.Marshal(input)

	aiCtx := ai.WithScenario(ctx, workspaceID, 0, "business_document_visual_designer", StrategicMemoryPromptVersion)
	started := time.Now()
	result, err := s.ai.GenerateJSONNative(aiCtx, documentVisualDesignerPrompt+contextindex.RetrievalInstructions, string(rawInput), ai.ResponseContextOptions{
		VectorStoreIDs:       vectorStoreIDs,
		CompactThreshold:     session.CompactThreshold,
		PromptCacheKey:       fmt.Sprintf("reupgoals-document-designer-workspace-%d-v1", workspaceID),
		MaxFileSearchResults: 8,
		MaxOutputTokens:      documentDesignerMaxOutputTokens,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil {
		s.store.LogAIRunWithUsage(ctx, workspaceID, "business_document_visual_designer", s.ai.ModelName(), StrategicMemoryPromptVersion, duration, 0, 0, "failed", err.Error())
		return nil, err
	}
	s.store.LogAIRunWithUsage(ctx, workspaceID, "business_document_visual_designer", s.ai.ModelName(), StrategicMemoryPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")

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
			Relation:      item.RelationToExisting,
			ExistingID:    item.ExistingClaimID,
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

func documentTypesFromStrategicDocuments(documents []StrategicDocument) []string {
	result := make([]string, 0, len(documents))
	for _, doc := range documents {
		result = append(result, doc.DocumentType)
	}
	return normalizeDocumentTypes(result)
}

func strategicDocumentCatalog() []map[string]string {
	definitions := strategicDocumentDefinitions()
	result := make([]map[string]string, 0, len(definitions))
	for _, definition := range definitions {
		result = append(result, map[string]string{
			"document_type": definition.DocumentType,
			"title":         definition.Title,
			"description":   definition.Description,
		})
	}
	return result
}

func strategicDocumentDefinitions() []DocumentCatalogItem {
	return []DocumentCatalogItem{
		{DocumentType: "company_governance", Title: "Компания и управление", Description: "Устройство компании, собственники, управление, роли и принятие решений.", SortOrder: 10},
		{DocumentType: "strategy_development", Title: "Стратегия и развитие", Description: "Цели, приоритеты, решения, ограничения и направления развития.", SortOrder: 20},
		{DocumentType: "product_value", Title: "Продукт и ценность", Description: "Продукты, услуги, ценностное предложение, функциональность и развитие продукта.", SortOrder: 30},
		{DocumentType: "customers_market_competition", Title: "Клиенты, рынок и конкуренты", Description: "Клиенты, спрос, рынок, конкуренты и реальные причины покупки или отказа.", SortOrder: 40},
		{DocumentType: "marketing_sales_relationships", Title: "Маркетинг, продажи и клиентские отношения", Description: "Позиционирование, привлечение, продажи, сопровождение и удержание клиентов.", SortOrder: 50},
		{DocumentType: "operations_execution", Title: "Операционная деятельность и исполнение", Description: "Ключевые процессы, поставка ценности, качество и сроки исполнения.", SortOrder: 60},
		{DocumentType: "team_organization", Title: "Команда и организация", Description: "Команда, подрядчики, роли, компетенции, найм и организационные зависимости.", SortOrder: 70},
		{DocumentType: "technology_data_assets", Title: "Технологии, данные и активы", Description: "IT-системы, данные, инфраструктура, автоматизация и другие активы.", SortOrder: 80},
		{DocumentType: "finance_economics", Title: "Финансы и экономика", Description: "Доходы, расходы, денежные потоки, обязательства и экономика бизнеса.", SortOrder: 90},
		{DocumentType: "legal_compliance", Title: "Право и соответствие требованиям", Description: "Договоры, права, лицензии, обязательства и регуляторные требования.", SortOrder: 100},
		{DocumentType: "hypotheses_assumptions", Title: "Гипотезы и непроверенные предположения", Description: "Важные предположения, которые пока не подтверждены фактами или данными.", SortOrder: 110},
		{DocumentType: "open_questions", Title: "Открытые вопросы", Description: "Неизвестные и слабые зоны, которые еще нужно исследовать или уточнить.", SortOrder: 120},
		{DocumentType: "contradictions_changes", Title: "Противоречия и изменения", Description: "Расхождения, изменения состояния и значимая история бизнеса.", SortOrder: 130},
	}
}

func validStrategicDocumentType(value string) bool {
	value = strings.TrimSpace(value)
	for _, definition := range strategicDocumentDefinitions() {
		if definition.DocumentType == value {
			return true
		}
	}
	return false
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
