package strategicmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"reup-goals-backend/internal/v2/jobs"
)

const materializerTimeout = 150 * time.Second

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
	SourceIDs          []int    `json:"source_ids"`
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
	DocumentType   string `json:"document_type"`
	Title          string `json:"title"`
	Markdown       string `json:"markdown"`
	Status         string `json:"status"`
	SourceClaimIDs []int  `json:"source_claim_ids"`
}

const (
	jobTypeKnowledgeQualityAudit   = "knowledge_base.quality_audit"
	jobTypeKnowledgeCandidate      = "knowledge_base.audit_candidate"
	jobTypeKnowledgeContextRefresh = "knowledge_base.context_refresh"
	knowledgeExtractionModel       = "gpt-5.6-luna"
	knowledgeDocumentModel         = "gpt-5.6-luna"
)

type qualityAuditJobPayload struct {
	ChangedDocumentTypes []string `json:"changed_document_types"`
	Trigger              string   `json:"trigger"`
}

func (s *Service) registerJobHandlers() {
	s.jobs.Register(jobTypeKnowledgeContextRefresh, func(ctx context.Context, job jobs.Job) error {
		if job.WorkspaceID == nil {
			return fmt.Errorf("knowledge context refresh job has no workspace")
		}
		return s.runKnowledgeContextRefresh(ctx, *job.WorkspaceID)
	})
	s.jobs.Register(jobTypeKnowledgeCandidate, func(ctx context.Context, job jobs.Job) error {
		if job.WorkspaceID == nil {
			return fmt.Errorf("knowledge candidate job has no workspace")
		}
		var payload knowledgeCandidateJobPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return err
		}
		err := s.runKnowledgeCandidate(ctx, *job.WorkspaceID, payload)
		if err != nil && job.Attempts >= job.MaxAttempts {
			_ = s.store.FailKnowledgeCandidate(context.WithoutCancel(ctx), *job.WorkspaceID, payload.Revision)
		}
		return err
	})
	s.jobs.Register(jobTypeKnowledgeQualityAudit, func(ctx context.Context, job jobs.Job) error {
		if job.WorkspaceID == nil {
			return fmt.Errorf("quality audit job has no workspace")
		}
		var payload qualityAuditJobPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return err
		}
		if payload.Trigger == "documents_updated" && !s.reserveAutoQualityAudit(*job.WorkspaceID) {
			log.Printf("[INFO] strategic quality audit job skipped by throttle workspace_id=%d", *job.WorkspaceID)
			return nil
		}
		_, err := s.RunQualityAudit(ctx, *job.WorkspaceID, payload.ChangedDocumentTypes, payload.Trigger)
		return err
	})
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
	// The snapshot is an untyped aggregate and cannot be mechanically filtered.
	// Facts-only strategy sources update claims instead of risking future plans
	// being promoted into the current business snapshot.
	materialized.Snapshot = nil
	return materialized
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
			Importance:    item.Importance,
			Relation:      item.RelationToExisting,
			ExistingID:    item.ExistingClaimID,
			SourceIDs:     item.SourceIDs,
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

// DocumentDefinitions exposes the canonical knowledge-base catalog to compact
// read models without duplicating document names or ordering.
func DocumentDefinitions() []DocumentCatalogItem {
	return strategicDocumentDefinitions()
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
