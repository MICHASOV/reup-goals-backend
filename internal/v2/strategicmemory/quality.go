package strategicmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/v2/contextindex"
)

const (
	qualityAuditMaxOutputTokens = 12000
	qualityAuditTimeout         = 180 * time.Second
)

type qualityAuditInputDocument struct {
	DocumentType string `json:"document_type"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	Version      int    `json:"version"`
	Markdown     string `json:"markdown"`
	MarkdownSize int    `json:"markdown_size"`
}

func (s *Service) queueQualityAudit(workspaceID int, changedDocumentTypes []string, trigger string) {
	if s.jobs != nil {
		notBefore := time.Now().UTC()
		dedupeKey := ""
		if trigger == "documents_updated" {
			dedupeKey = "automatic"
			notBefore = notBefore.Add(90 * time.Second)
		}
		_, err := s.jobs.Enqueue(context.Background(), workspaceID, jobTypeKnowledgeQualityAudit, dedupeKey,
			qualityAuditJobPayload{ChangedDocumentTypes: normalizeDocumentTypes(changedDocumentTypes), Trigger: trigger},
			4, notBefore)
		if err != nil {
			log.Printf("[WARN] strategic quality audit enqueue failed workspace_id=%d trigger=%s: %v", workspaceID, trigger, err)
		}
		return
	}
	if trigger == "documents_updated" && !s.reserveAutoQualityAudit(workspaceID) {
		log.Printf("[INFO] strategic quality audit skipped by throttle workspace_id=%d trigger=%s", workspaceID, trigger)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), qualityAuditTimeout)
		defer cancel()

		if _, err := s.RunQualityAudit(ctx, workspaceID, changedDocumentTypes, trigger); err != nil {
			log.Printf("[WARN] strategic quality audit failed workspace_id=%d trigger=%s: %v", workspaceID, trigger, err)
		}
	}()
}

func (s *Service) reserveAutoQualityAudit(workspaceID int) bool {
	now := time.Now()
	s.qualityAuditMu.Lock()
	defer s.qualityAuditMu.Unlock()

	last, ok := s.qualityAuditReservedAt[workspaceID]
	if ok && now.Sub(last) < autoQualityAuditThrottle {
		return false
	}
	s.qualityAuditReservedAt[workspaceID] = now
	return true
}

func (s *Service) markQualityAuditCompleted(workspaceID int) {
	s.qualityAuditMu.Lock()
	s.qualityAuditReservedAt[workspaceID] = time.Now()
	s.qualityAuditMu.Unlock()
}

func (s *Service) RunQualityAudit(ctx context.Context, workspaceID int, changedDocumentTypes []string, trigger string) (QualityReport, error) {
	state, err := s.State(ctx, workspaceID)
	if err != nil {
		return QualityReport{}, err
	}
	if len(state.Documents) == 0 {
		return QualityReport{}, fmt.Errorf("quality_audit_no_documents")
	}
	session, err := s.store.OpenAISession(ctx, workspaceID, s.compactThreshold)
	if err != nil {
		return QualityReport{}, err
	}

	changedDocumentTypes = normalizeDocumentTypes(changedDocumentTypes)
	input := map[string]any{
		"workspace_id":             workspaceID,
		"trigger":                  defaultString(trigger, "manual"),
		"changed_document_types":   changedDocumentTypes,
		"document_catalog":         strategicDocumentCatalog(),
		"documents":                documentsForQualityAuditContext(state.Documents),
		"claims":                   limitClaimsForContext(state.Claims, 160),
		"research_agenda":          limitAgendaForContext(state.Agenda, 80),
		"snapshot":                 state.Snapshot,
		"quality_formula_contract": qualityFormulaContract(),
	}
	vectorStoreIDs, indexed := s.workspaceContextVectorStoreIDs(ctx, workspaceID, session)
	if indexed {
		delete(input, "documents")
		delete(input, "claims")
		delete(input, "research_agenda")
		delete(input, "snapshot")
		input["current_workspace_context"] = "Use file_search to inspect all current knowledge documents, claims, open questions, and contradictions."
	}
	rawInput, _ := json.Marshal(input)

	aiCtx := ai.WithScenario(ctx, workspaceID, 0, "knowledge_base_quality_auditor", StrategicMemoryPromptVersion)
	workerAI := s.ai.ForModel(knowledgeWorkerModel)
	started := time.Now()
	result, err := workerAI.GenerateJSONNative(aiCtx, knowledgeBaseQualityAuditorPrompt+contextindex.RetrievalInstructions, string(rawInput), ai.ResponseContextOptions{
		VectorStoreIDs:       vectorStoreIDs,
		CompactThreshold:     session.CompactThreshold,
		PromptCacheKey:       fmt.Sprintf("reupgoals-quality-auditor-workspace-%d-v1", workspaceID),
		MaxFileSearchResults: 8,
		MaxOutputTokens:      qualityAuditMaxOutputTokens,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil {
		s.store.LogAIRunWithUsage(ctx, workspaceID, "knowledge_base_quality_auditor", workerAI.ModelName(), StrategicMemoryPromptVersion, duration, 0, 0, "failed", err.Error())
		return QualityReport{}, err
	}
	s.store.LogAIRunWithUsage(ctx, workspaceID, "knowledge_base_quality_auditor", workerAI.ModelName(), StrategicMemoryPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")

	var report QualityReport
	if err := json.Unmarshal([]byte(result.Text), &report); err != nil {
		return QualityReport{}, fmt.Errorf("quality auditor json decode error: %w", err)
	}
	report.WorkspaceID = workspaceID
	report.ChangedDocumentTypes = changedDocumentTypes
	report = finalizeQualityReport(report)

	saved, err := s.store.SaveQualityReport(ctx, workspaceID, report)
	if err == nil {
		s.markQualityAuditCompleted(workspaceID)
		if s.contextIndex != nil {
			s.contextIndex.RefreshAsync(workspaceID)
		}
	}
	return saved, err
}

func finalizeQualityReport(report QualityReport) QualityReport {
	for index := range report.Documents {
		doc := &report.Documents[index]
		doc.DocumentType = normalizeDocumentType(doc.DocumentType)
		doc.Relevance = normalizeDocumentRelevance(doc.Relevance)
		doc.Scores = clampCriterionScores(doc.Scores)
		doc.DocumentScore = scoreDocument(*doc)
		doc.Status = normalizeDocumentQualityStatus(defaultString(doc.Status, statusFromDocumentScore(doc.DocumentScore)))
	}

	crossScore := clampScore(report.Overall.CrossDocumentQualityScore)
	if crossScore == 0 {
		crossScore = averageConsistencyScore(report.Documents)
	}
	weightedDocs := weightedDocumentsScore(report.Documents)
	readinessScore := int(math.Round(float64(weightedDocs)*0.75 + float64(crossScore)*0.25))
	readinessScore = clampScore(readinessScore)
	report.StrategyGate = finalizeStrategyGate(report.StrategyGate, readinessScore, report.Overall.CriticalBlockers)
	status := readinessStatusFromScore(readinessScore, len(report.Overall.CriticalBlockers) > 0 || !report.StrategyGate.BasicProfileComplete)

	report.ReadinessScore = readinessScore
	report.ReadinessStatus = status
	report.Overall.ReadinessScore = readinessScore
	report.Overall.ReadinessStatus = status
	report.Overall.CrossDocumentQualityScore = crossScore
	return report
}

func finalizeStrategyGate(gate QualityStrategyGate, readinessScore int, criticalBlockers []string) QualityStrategyGate {
	gate.MinimumScoreMet = readinessScore >= 60
	gate.NoCriticalBlockers = len(criticalBlockers) == 0
	gate.BasicProfileComplete = qualityStrategyGateItemsComplete(gate.GateItems) && len(gate.MissingGateItems) == 0
	gate.CanStartStrategy = gate.MinimumScoreMet && gate.NoCriticalBlockers && gate.BasicProfileComplete
	if gate.Recommendation == "" {
		switch {
		case gate.CanStartStrategy:
			gate.Recommendation = "База знаний достаточна, чтобы переходить к первичной стратегической работе."
		case !gate.MinimumScoreMet:
			gate.Recommendation = "Перед стратегией нужно добрать контекст до минимального уровня готовности 60%."
		case !gate.NoCriticalBlockers:
			gate.Recommendation = "Перед стратегией нужно снять критические блокеры в базе знаний."
		default:
			gate.Recommendation = "Перед стратегией нужно закрыть базовый профиль бизнеса."
		}
	}
	return gate
}

func qualityStrategyGateItemsComplete(items QualityStrategyGateItems) bool {
	return items.ProductOrService &&
		items.CustomerOrSegment &&
		items.BusinessStage &&
		items.EvidenceStatus &&
		items.MainProblem &&
		items.KeyConstraints
}

func scoreDocument(doc QualityDocumentAssessment) int {
	scores := doc.Scores
	score := float64(clampScore(scores.Completeness))*0.20 +
		float64(clampScore(scores.Specificity))*0.15 +
		float64(clampScore(scores.EvidenceQuality))*0.15 +
		float64(clampScore(scores.Freshness))*0.10 +
		float64(clampScore(scores.StrategicValue))*0.20 +
		float64(clampScore(scores.Consistency))*0.10 +
		float64(clampScore(scores.Actionability))*0.10
	result := clampScore(int(math.Round(score)))

	if doc.Relevance != "not_relevant_now" && len(doc.MissingInformation) > 0 && scores.Completeness < 35 {
		result = minInt(result, 35)
	}
	if len(doc.Inconsistencies) > 0 && scores.Consistency < 65 {
		result = minInt(result, 65)
	}
	if scores.EvidenceQuality < 55 && len(doc.ProblemAreas) > 0 {
		result = minInt(result, 70)
	}
	if doc.Relevance == "critical" && scores.Specificity < 60 {
		result = minInt(result, 75)
	}
	return result
}

func weightedDocumentsScore(documents []QualityDocumentAssessment) int {
	totalWeight := 0.0
	totalScore := 0.0
	for _, doc := range documents {
		weight := relevanceWeight(doc.Relevance)
		if weight <= 0 {
			continue
		}
		totalWeight += weight
		totalScore += float64(clampScore(doc.DocumentScore)) * weight
	}
	if totalWeight <= 0 {
		return 0
	}
	return clampScore(int(math.Round(totalScore / totalWeight)))
}

func averageConsistencyScore(documents []QualityDocumentAssessment) int {
	if len(documents) == 0 {
		return 0
	}
	sum := 0
	count := 0
	for _, doc := range documents {
		if doc.Relevance == "not_relevant_now" {
			continue
		}
		sum += clampScore(doc.Scores.Consistency)
		count += 1
	}
	if count == 0 {
		return 0
	}
	return clampScore(int(math.Round(float64(sum) / float64(count))))
}

func relevanceWeight(value string) float64 {
	switch normalizeDocumentRelevance(value) {
	case "critical":
		return 1.2
	case "important":
		return 1.0
	case "supporting":
		return 0.7
	case "optional":
		return 0.4
	default:
		return 0
	}
}

func readinessStatusFromScore(score int, hasCriticalBlockers bool) string {
	switch {
	case score >= 80 && !hasCriticalBlockers:
		return "ready"
	case score >= 60:
		return "ready_with_limitations"
	default:
		return "not_ready"
	}
}

func statusFromDocumentScore(score int) string {
	switch {
	case score >= 80:
		return "strategically_sufficient"
	case score >= 55:
		return "partially_sufficient"
	default:
		return "insufficient"
	}
}

func normalizeDocumentQualityStatus(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "strategically_sufficient", "partially_sufficient", "insufficient":
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return "insufficient"
	}
}

func normalizeDocumentRelevance(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "critical", "important", "supporting", "optional", "not_relevant_now":
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return "important"
	}
}

func clampCriterionScores(scores QualityCriterionScores) QualityCriterionScores {
	return QualityCriterionScores{
		Completeness:    clampScore(scores.Completeness),
		Specificity:     clampScore(scores.Specificity),
		EvidenceQuality: clampScore(scores.EvidenceQuality),
		Freshness:       clampScore(scores.Freshness),
		StrategicValue:  clampScore(scores.StrategicValue),
		Consistency:     clampScore(scores.Consistency),
		Actionability:   clampScore(scores.Actionability),
	}
}

func documentsForQualityAuditContext(documents []StrategicDocument) []qualityAuditInputDocument {
	result := make([]qualityAuditInputDocument, 0, len(documents))
	for _, doc := range documents {
		markdown := strings.TrimSpace(doc.Markdown)
		result = append(result, qualityAuditInputDocument{
			DocumentType: normalizeDocumentType(doc.DocumentType),
			Title:        doc.Title,
			Status:       doc.Status,
			Version:      doc.Version,
			Markdown:     markdown,
			MarkdownSize: len([]rune(markdown)),
		})
	}
	return result
}

func normalizeDocumentTypes(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		docType := normalizeDocumentType(value)
		if docType == "" || seen[docType] {
			continue
		}
		seen[docType] = true
		result = append(result, docType)
	}
	return result
}

func qualityFormulaContract() map[string]any {
	return map[string]any{
		"document_score_formula": map[string]float64{
			"completeness":     0.20,
			"specificity":      0.15,
			"evidence_quality": 0.15,
			"freshness":        0.10,
			"strategic_value":  0.20,
			"consistency":      0.10,
			"actionability":    0.10,
		},
		"relevance_weights": map[string]float64{
			"critical":         1.2,
			"important":        1.0,
			"supporting":       0.7,
			"optional":         0.4,
			"not_relevant_now": 0,
		},
		"knowledge_base_score_formula": "weighted_document_score * 0.75 + cross_document_quality_score * 0.25",
		"readiness_status_rules": map[string]string{
			"0_59":                             "not_ready",
			"60_79":                            "ready_with_limitations",
			"80_100_without_critical_blockers": "ready",
		},
	}
}

func compactQualityReportForContext(report *QualityReport) map[string]any {
	if report == nil {
		return nil
	}
	return map[string]any{
		"readiness_score":        report.ReadinessScore,
		"readiness_status":       report.ReadinessStatus,
		"summary":                report.Overall.Summary,
		"critical_blockers":      report.Overall.CriticalBlockers,
		"weakest_documents":      report.Overall.WeakestDocuments,
		"blind_spots":            report.ChatGuidance.BlindSpots,
		"next_best_topic":        report.ChatGuidance.NextBestTopic,
		"next_best_questions":    report.ChatGuidance.NextBestQuestions,
		"avoid_repeating":        report.ChatGuidance.AvoidRepeating,
		"why_this_next":          report.ChatGuidance.WhyThisNext,
		"strategy_gate":          report.StrategyGate,
		"highest_priority_gaps":  report.Overall.MostImportantMissingInfo,
		"highest_priority_tasks": report.Overall.HighestPriorityImprovements,
	}
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
