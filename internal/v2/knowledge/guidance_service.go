package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"reup-goals-backend/internal/ai"
)

type GuidanceService struct {
	store  *Store
	intake *IntakeService
	ai     *ai.OpenAIClient
}

func NewGuidanceService(store *Store, intake *IntakeService, aiClient *ai.OpenAIClient) *GuidanceService {
	return &GuidanceService{store: store, intake: intake, ai: aiClient}
}

func (s *GuidanceService) Bootstrap(ctx context.Context, workspaceID int, userID int) (GuidanceBootstrapResponse, error) {
	if err := s.store.EnsureGuidanceBootstrap(ctx, workspaceID); err != nil {
		return GuidanceBootstrapResponse{}, err
	}
	profile, err := s.store.CompanyProfile(ctx, workspaceID)
	if err != nil {
		return GuidanceBootstrapResponse{}, err
	}
	readiness, err := s.store.KnowledgeBaseReadiness(ctx, workspaceID)
	if err != nil {
		return GuidanceBootstrapResponse{}, err
	}
	documents, err := s.store.DocumentReadinessList(ctx, workspaceID)
	if err != nil {
		return GuidanceBootstrapResponse{}, err
	}
	question, err := s.store.ActiveQuestionBlock(ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		question, err = s.createNextQuestion(ctx, workspaceID, userID, profile, readiness, defaultConversationIntent(ConversationIntent{}), "")
	}
	if err != nil {
		return GuidanceBootstrapResponse{}, err
	}

	return GuidanceBootstrapResponse{
		WorkspaceID:            workspaceID,
		Mode:                   guidanceMode(profile),
		CompanyProfile:         profile,
		KnowledgeBaseReadiness: readiness,
		Documents:              documents,
		ActiveQuestionBlock:    question,
	}, nil
}

func (s *GuidanceService) PreviewAnswer(ctx context.Context, workspaceID int, userID int, questionBlockID int, rawText string) (GuidancePreviewResponse, error) {
	response, err := s.intake.BuildGuidancePreview(ctx, workspaceID, userID, questionBlockID, rawText)
	if err != nil {
		return GuidancePreviewResponse{}, err
	}
	if response.Status != "no_preview_needed" {
		result, err := s.store.AutoApplyIntake(ctx, workspaceID, userID, response.SessionID)
		if err != nil {
			return GuidancePreviewResponse{}, err
		}
		if err := s.store.MarkQuestionAnsweredForSession(ctx, workspaceID, response.SessionID); err != nil {
			return GuidancePreviewResponse{}, err
		}

		affected, err := s.store.SessionAffectedDocuments(ctx, workspaceID, response.SessionID)
		if err != nil {
			return GuidancePreviewResponse{}, err
		}
		companyCardChanged, err := s.store.SessionChangedCompanyCard(ctx, workspaceID, response.SessionID)
		if err != nil {
			return GuidancePreviewResponse{}, err
		}
		profile, err := s.refreshFirstGateProfileFromConfirmedAnswer(ctx, workspaceID, rawText, companyCardChanged)
		if err != nil {
			return GuidancePreviewResponse{}, err
		}
		readiness, err := s.store.RecalculateKnowledgeBaseReadiness(ctx, workspaceID)
		if err != nil {
			return GuidancePreviewResponse{}, err
		}
		next, err := s.nextQuestionAfterAutoApply(ctx, workspaceID, userID, profile, readiness, response.ConversationIntent, rawText, response.Conflicts)
		if err != nil {
			return GuidancePreviewResponse{}, err
		}
		response.Status = SessionConfirmed
		response.NextQuestionBlock = &next
		response.AppliedChanges = &result
		s.refreshKnowledgeAsync(workspaceID, userID, rawText, response.ConversationIntent, affected, companyCardChanged)
		return response, nil
	}

	profile, err := s.store.CompanyProfile(ctx, workspaceID)
	if err != nil {
		return GuidancePreviewResponse{}, err
	}
	readiness, err := s.store.RecalculateKnowledgeBaseReadiness(ctx, workspaceID)
	if err != nil {
		return GuidancePreviewResponse{}, err
	}
	next, err := s.createNextQuestion(ctx, workspaceID, userID, profile, readiness, response.ConversationIntent, rawText)
	if err != nil {
		return GuidancePreviewResponse{}, err
	}
	response.NextQuestionBlock = &next
	s.refreshKnowledgeAsync(workspaceID, userID, rawText, response.ConversationIntent, nil, false)
	return response, nil
}

func (s *GuidanceService) Confirm(ctx context.Context, workspaceID int, userID int, sessionID int, acceptedPatchIDs []int, resolutions []conflictResolution) (GuidanceConfirmResponse, error) {
	status, err := s.store.IntakeSessionStatus(ctx, workspaceID, sessionID)
	if err != nil {
		return GuidanceConfirmResponse{}, err
	}
	if status == SessionConfirmed {
		profile, err := s.store.CompanyProfile(ctx, workspaceID)
		if err != nil {
			return GuidanceConfirmResponse{}, err
		}
		readiness, err := s.store.KnowledgeBaseReadiness(ctx, workspaceID)
		if err != nil {
			return GuidanceConfirmResponse{}, err
		}
		question, err := s.store.ActiveQuestionBlock(ctx, workspaceID)
		if err != nil {
			return GuidanceConfirmResponse{}, err
		}
		return GuidanceConfirmResponse{
			Status:                 SessionConfirmed,
			Mode:                   guidanceMode(profile),
			CompanyProfile:         profile,
			KnowledgeBaseReadiness: readiness,
			NextQuestionBlock:      question,
			AppliedChanges:         IntakeConfirmResponse{SessionID: sessionID},
		}, nil
	}

	result, err := s.store.ConfirmIntake(ctx, workspaceID, userID, sessionID, acceptedPatchIDs, resolutions)
	if err != nil {
		return GuidanceConfirmResponse{}, err
	}
	if err := s.store.MarkQuestionAnsweredForSession(ctx, workspaceID, sessionID); err != nil {
		return GuidanceConfirmResponse{}, err
	}

	rawText, intent, err := s.sessionContext(ctx, workspaceID, sessionID)
	if err != nil {
		return GuidanceConfirmResponse{}, err
	}

	affected, err := s.store.SessionAffectedDocuments(ctx, workspaceID, sessionID)
	if err != nil {
		return GuidanceConfirmResponse{}, err
	}

	companyCardChanged, err := s.store.SessionChangedCompanyCard(ctx, workspaceID, sessionID)
	if err != nil {
		return GuidanceConfirmResponse{}, err
	}

	profile, err := s.refreshFirstGateProfileFromConfirmedAnswer(ctx, workspaceID, rawText, companyCardChanged)
	if err != nil {
		return GuidanceConfirmResponse{}, err
	}

	readiness, err := s.store.RecalculateKnowledgeBaseReadiness(ctx, workspaceID)
	if err != nil {
		return GuidanceConfirmResponse{}, err
	}
	next, err := s.createNextQuestion(ctx, workspaceID, userID, profile, readiness, intent, rawText)
	if err != nil {
		return GuidanceConfirmResponse{}, err
	}
	s.refreshKnowledgeAsync(workspaceID, userID, rawText, intent, affected, companyCardChanged)

	return GuidanceConfirmResponse{
		Status:                   SessionConfirmed,
		Mode:                     guidanceMode(profile),
		CompanyProfile:           profile,
		KnowledgeBaseReadiness:   readiness,
		DocumentReadinessUpdates: []DocumentReadiness{},
		NextQuestionBlock:        next,
		AppliedChanges:           result,
	}, nil
}

func (s *GuidanceService) Reject(ctx context.Context, workspaceID int, sessionID int) (GuidanceQuestionBlock, error) {
	if err := s.store.RejectIntake(ctx, workspaceID, sessionID); err != nil {
		return GuidanceQuestionBlock{}, err
	}
	question, err := s.store.SessionQuestionBlock(ctx, workspaceID, sessionID)
	if err != nil {
		return GuidanceQuestionBlock{}, err
	}
	return question, nil
}

func (s *GuidanceService) updateCompanyProfileIfNeeded(ctx context.Context, workspaceID int, userID int, latestMessage string, intent ConversationIntent, force bool) (CompanyProfile, error) {
	current, err := s.store.CompanyProfile(ctx, workspaceID)
	if err != nil {
		return CompanyProfile{}, err
	}
	if current.Status == ProfileStatusGreen && !force {
		return current, nil
	}

	entries, err := s.store.CompanyCardEntries(ctx, workspaceID)
	if err != nil {
		return CompanyProfile{}, err
	}
	aiEntries := make([]map[string]string, 0, len(entries))
	for _, entry := range entries {
		aiEntries = append(aiEntries, map[string]string{
			"entry_id":       entryIDString(entry.ID),
			"text":           entry.Text,
			"statement_type": entry.StatementType,
		})
	}
	input := map[string]any{
		"workspace_id":         fmt.Sprintf("%d", workspaceID),
		"output_language":      "ru",
		"company_card_entries": aiEntries,
		"latest_user_message":  latestMessage,
		"conversation_intent":  defaultConversationIntent(intent),
		"previous_company_profile": map[string]any{
			"company_gate_signal": current.Status,
			"profile_text":        current.ProfileText,
			"baseline_coverage":   rawJSONOrEmptyArray(current.BaselineCoverage),
		},
	}
	raw, err := s.generateLoggedJSON(ctx, workspaceID, userID, "company_profile_collector", CompanyProfileCollectorVersion, companyProfileCollectorPrompt, input)
	if err != nil {
		return CompanyProfile{}, err
	}
	var response companyProfileCollectorResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return CompanyProfile{}, err
	}
	if err := s.store.UpsertCompanyProfile(ctx, workspaceID, response, raw); err != nil {
		return CompanyProfile{}, err
	}
	return s.store.CompanyProfile(ctx, workspaceID)
}

func (s *GuidanceService) refreshFirstGateProfileFromConfirmedAnswer(ctx context.Context, workspaceID int, latestMessage string, companyCardChanged bool) (CompanyProfile, error) {
	profile, err := s.store.CompanyProfile(ctx, workspaceID)
	if err != nil {
		return CompanyProfile{}, err
	}
	if profile.Status == ProfileStatusGreen || !companyCardChanged {
		return profile, nil
	}

	entries, err := s.store.CompanyCardEntries(ctx, workspaceID)
	if err != nil {
		return CompanyProfile{}, err
	}

	textParts := []string{latestMessage, profile.ProfileText}
	for _, entry := range entries {
		textParts = append(textParts, entry.Text)
	}
	response, changed := deriveCompanyProfileCoverage(profile, strings.Join(textParts, "\n"))
	if !changed {
		return profile, nil
	}

	raw, _ := json.Marshal(map[string]any{
		"source":            "deterministic_first_gate_refresh",
		"profile_text":      response.ProfileText,
		"baseline_coverage": rawJSONOrEmptyArray(response.BaselineCoverage),
	})
	if err := s.store.UpsertCompanyProfile(ctx, workspaceID, response, raw); err != nil {
		return CompanyProfile{}, err
	}
	return s.store.CompanyProfile(ctx, workspaceID)
}

func (s *GuidanceService) runDocumentReadiness(ctx context.Context, workspaceID int, userID int, documentID int, documentType string, title string) (DocumentReadiness, error) {
	entries, err := s.store.DocumentEntriesForAI(ctx, workspaceID, documentID)
	if err != nil {
		return DocumentReadiness{}, err
	}
	aiEntries := make([]map[string]string, 0, len(entries))
	for _, entry := range entries {
		aiEntries = append(aiEntries, map[string]string{
			"entry_id":       entryIDString(entry.ID),
			"text":           entry.Text,
			"statement_type": entry.StatementType,
		})
	}
	input := map[string]any{
		"document_type":   documentType,
		"document_title":  title,
		"entries":         aiEntries,
		"output_language": "ru",
	}
	raw, err := s.generateLoggedJSON(ctx, workspaceID, userID, "document_readiness_preflight", DocumentReadinessVersion, documentReadinessPreflightPrompt, input)
	if err != nil {
		return DocumentReadiness{}, err
	}
	var response readinessPreflightResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return DocumentReadiness{}, err
	}
	if response.DocumentType == "" {
		response.DocumentType = documentType
	}
	if err := s.store.UpsertDocumentReadiness(ctx, workspaceID, documentID, response, raw); err != nil {
		return DocumentReadiness{}, err
	}
	list, err := s.store.DocumentReadinessList(ctx, workspaceID)
	if err != nil {
		return DocumentReadiness{}, err
	}
	for _, item := range list {
		if item.DocumentID == documentID {
			return item, nil
		}
	}
	return DocumentReadiness{}, sql.ErrNoRows
}

func (s *GuidanceService) runAffectedDocumentReadiness(ctx context.Context, workspaceID int, userID int, affected []DocumentReadiness) ([]DocumentReadiness, error) {
	if len(affected) == 0 {
		return []DocumentReadiness{}, nil
	}

	updates := make([]DocumentReadiness, len(affected))
	errs := make([]error, len(affected))
	var wg sync.WaitGroup
	for index, document := range affected {
		wg.Add(1)
		go func(index int, document DocumentReadiness) {
			defer wg.Done()
			updates[index], errs[index] = s.runDocumentReadiness(ctx, workspaceID, userID, document.DocumentID, document.DocumentType, document.Title)
		}(index, document)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return updates, nil
}

func (s *GuidanceService) createNextQuestion(ctx context.Context, workspaceID int, userID int, profile CompanyProfile, readiness KnowledgeBaseReadiness, intent ConversationIntent, latestMessage string) (GuidanceQuestionBlock, error) {
	return s.runPlanner(ctx, workspaceID, userID, profile, readiness, intent, latestMessage)
}

func (s *GuidanceService) refreshKnowledgeAsync(workspaceID int, userID int, rawText string, intent ConversationIntent, affected []DocumentReadiness, companyCardChanged bool) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		if _, err := s.updateCompanyProfileIfNeeded(ctx, workspaceID, userID, rawText, intent, companyCardChanged); err != nil {
			log.Printf("[WARN] background company profile refresh failed workspace_id=%d user_id=%d: %v", workspaceID, userID, err)
			return
		}
		if len(affected) > 0 {
			if _, err := s.runAffectedDocumentReadiness(ctx, workspaceID, userID, affected); err != nil {
				log.Printf("[WARN] background document readiness refresh failed workspace_id=%d user_id=%d: %v", workspaceID, userID, err)
				return
			}
		}
		if _, err := s.store.RecalculateKnowledgeBaseReadiness(ctx, workspaceID); err != nil {
			log.Printf("[WARN] background knowledge readiness refresh failed workspace_id=%d user_id=%d: %v", workspaceID, userID, err)
		}
		if len(affected) > 0 {
			s.composeDocumentsInBackground(ctx, workspaceID, userID, affected)
		}
	}()
}

func (s *GuidanceService) nextQuestionAfterAutoApply(ctx context.Context, workspaceID int, userID int, profile CompanyProfile, readiness KnowledgeBaseReadiness, intent ConversationIntent, rawText string, conflicts []IntakeConflict) (GuidanceQuestionBlock, error) {
	questions := conflictQuestions(conflicts)
	if len(questions) > 0 {
		return s.store.CreateQuestionBlock(ctx, workspaceID, GuidanceQuestionBlock{
			Source:               QuestionSourcePlanner,
			GuidanceStatus:       GuidanceStatusAskNextQuestion,
			QuestionType:         "conflict_clarification",
			IntendedFocusSummary: "Уточнить расхождения, которые нельзя безопасно применить автоматически.",
			Title:                "Уточню пару мест, чтобы не исказить смысл",
			Intro:                "Часть ответа я уже зафиксировал в Базе знаний. Ниже остались только места, где есть риск неверно понять или перезаписать старую формулировку.",
			Questions:            questions,
			Confidence:           ConfidenceHigh,
		})
	}
	return s.createNextQuestion(ctx, workspaceID, userID, profile, readiness, intent, rawText)
}

func conflictQuestions(conflicts []IntakeConflict) []string {
	questions := []string{}
	for _, conflict := range conflicts {
		question := strings.TrimSpace(conflict.Question)
		if question == "" {
			question = "Какая формулировка точнее описывает текущую реальность?"
		}
		existingText := strings.TrimSpace(conflict.OptionAText)
		if existingText == "" {
			existingText = strings.TrimSpace(conflict.ExistingText)
		}
		newText := strings.TrimSpace(conflict.OptionBText)
		if newText == "" {
			newText = strings.TrimSpace(conflict.NewText)
		}
		if existingText != "" && newText != "" {
			question = fmt.Sprintf("%s Было: «%s». Сейчас прозвучало: «%s». Что верно?", question, existingText, newText)
		}
		questions = append(questions, question)
		if len(questions) >= 3 {
			break
		}
	}
	return questions
}

func (s *GuidanceService) composeDocumentsInBackground(ctx context.Context, workspaceID int, userID int, affected []DocumentReadiness) {
	for _, document := range uniqueDocuments(affected) {
		if err := s.composeDocument(ctx, workspaceID, userID, document.DocumentID, document.DocumentType, document.Title); err != nil {
			log.Printf("[WARN] background document compose failed workspace_id=%d user_id=%d document_type=%s: %v", workspaceID, userID, document.DocumentType, err)
		}
	}
}

func (s *GuidanceService) composeDocument(ctx context.Context, workspaceID int, userID int, documentID int, documentType string, title string) error {
	entries, err := s.store.DocumentEntriesForAI(ctx, workspaceID, documentID)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	aiEntries := make([]map[string]string, 0, len(entries))
	for _, entry := range entries {
		aiEntries = append(aiEntries, map[string]string{
			"entry_id":       entryIDString(entry.ID),
			"text":           entry.Text,
			"statement_type": entry.StatementType,
		})
	}
	input := map[string]any{
		"workspace_id":    fmt.Sprintf("%d", workspaceID),
		"output_language": "ru",
		"document_type":   documentType,
		"document_title":  title,
		"entries":         aiEntries,
	}
	raw, err := s.generateLoggedJSON(ctx, workspaceID, userID, "knowledge_document_composer", DocumentComposerVersion, documentComposerPrompt, input)
	if err != nil {
		return err
	}
	var response documentComposerResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return err
	}
	if strings.TrimSpace(response.DocumentType) == "" {
		response.DocumentType = documentType
	}
	return s.store.UpsertDocumentView(ctx, workspaceID, documentID, documentType, response, raw)
}

func uniqueDocuments(documents []DocumentReadiness) []DocumentReadiness {
	result := []DocumentReadiness{}
	seen := map[int]bool{}
	for _, document := range documents {
		if document.DocumentID <= 0 || seen[document.DocumentID] {
			continue
		}
		seen[document.DocumentID] = true
		result = append(result, document)
	}
	return result
}

func (s *GuidanceService) createFirstGateQuestion(ctx context.Context, workspaceID int, profile CompanyProfile) (GuidanceQuestionBlock, error) {
	missingAreas := firstGateMissingAreas(profile)
	questions := firstGateQuestionsForAreas(missingAreas)
	title := "Уточним недостающий контекст"
	intro := firstGateIntro(profile)

	if isBlankFirstGate(profile, missingAreas) {
		title = "Давай начнём с контекста компании"
		intro = "Ответьте свободным текстом. Я сам разложу ответ по Базе знаний и перед сохранением покажу изменения."
		questions = []string{
			"Чем занимается компания и что вы продаёте или делаете для клиентов?",
			"На какой стадии сейчас бизнес и что сейчас сильнее всего болит?",
			"Какой сейчас масштаб: команда, рынок, география? По финансам можно дать примерный диапазон, написать “не знаю” или “не хочу раскрывать”.",
		}
	}
	if len(questions) == 0 {
		questions = []string{"Что ещё важно знать о компании прямо сейчас, чтобы точнее понимать бизнес-контекст?"}
	}

	block := GuidanceQuestionBlock{
		Source:         QuestionSourceFirstGate,
		GuidanceStatus: GuidanceStatusAskNextQuestion,
		QuestionType:   "new_area_opening",
		Title:          title,
		Intro:          intro,
		Questions:      questions,
		Confidence:     ConfidenceHigh,
	}
	return s.store.CreateQuestionBlock(ctx, workspaceID, block)
}

func (s *GuidanceService) runPlanner(ctx context.Context, workspaceID int, userID int, profile CompanyProfile, readiness KnowledgeBaseReadiness, intent ConversationIntent, latestMessage string) (GuidanceQuestionBlock, error) {
	documents, err := s.documentsForPlanner(ctx, workspaceID)
	if err != nil {
		return GuidanceQuestionBlock{}, err
	}
	history, err := s.store.RecentQuestionHistory(ctx, workspaceID)
	if err != nil {
		return GuidanceQuestionBlock{}, err
	}
	input := map[string]any{
		"workspace_id":             fmt.Sprintf("%d", workspaceID),
		"output_language":          "ru",
		"company_profile":          profile,
		"knowledge_base_readiness": readiness,
		"documents":                documents,
		"latest_user_message":      latestMessage,
		"conversation_intent":      defaultConversationIntent(intent),
		"recent_question_history":  history,
	}
	raw, err := s.generateLoggedJSON(ctx, workspaceID, userID, "strategic_guidance_question_planner", GuidancePlannerVersion, strategicGuidanceQuestionPlannerPrompt, input)
	if err != nil {
		return GuidanceQuestionBlock{}, err
	}
	var response guidancePlannerResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return GuidanceQuestionBlock{}, err
	}
	intendedDocuments, _ := json.Marshal(response.IntendedFocus.IntendedDocuments)
	handledIntent, _ := json.Marshal(response.HandledUserIntent)
	block := GuidanceQuestionBlock{
		Source:                  QuestionSourcePlanner,
		GuidanceStatus:          response.GuidanceStatus,
		QuestionType:            response.QuestionType,
		IntendedFocusSummary:    response.IntendedFocus.FocusSummary,
		IntendedDocuments:       intendedDocuments,
		SelectionReasonInternal: response.IntendedFocus.SelectionReasonInternal,
		Title:                   response.QuestionBlock.Title,
		Intro:                   response.QuestionBlock.Intro,
		Questions:               response.QuestionBlock.Questions,
		HandledUserIntent:       handledIntent,
		Confidence:              response.Confidence,
	}
	if block.GuidanceStatus == "" {
		block.GuidanceStatus = GuidanceStatusAskNextQuestion
	}
	if block.QuestionType == "" {
		block.QuestionType = "narrow_deepening"
	}
	if strings.TrimSpace(block.Title) == "" {
		block.Title = "Уточним бизнес-контекст"
	}
	if len(block.Questions) == 0 && block.GuidanceStatus != GuidanceStatusSuggestStrategyTransition {
		block.Questions = []string{"Что ещё важно знать о бизнесе прямо сейчас, чтобы лучше понимать контекст компании?"}
	}
	return s.store.CreateQuestionBlock(ctx, workspaceID, block)
}

func (s *GuidanceService) documentsForPlanner(ctx context.Context, workspaceID int) ([]map[string]any, error) {
	readiness, err := s.store.DocumentReadinessList(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(readiness))
	for _, item := range readiness {
		entries, err := s.store.DocumentEntriesForAI(ctx, workspaceID, item.DocumentID)
		if err != nil {
			return nil, err
		}
		aiEntries := make([]map[string]string, 0, len(entries))
		for _, entry := range entries {
			aiEntries = append(aiEntries, map[string]string{
				"entry_id":       entryIDString(entry.ID),
				"text":           entry.Text,
				"statement_type": entry.StatementType,
			})
		}
		result = append(result, map[string]any{
			"document_type":    item.DocumentType,
			"document_title":   item.Title,
			"readiness_status": item.ReadinessStatus,
			"readiness_reason": item.ReadinessReason,
			"entries":          aiEntries,
		})
	}
	return result, nil
}

func (s *GuidanceService) generateLoggedJSON(ctx context.Context, workspaceID int, userID int, module string, promptVersion string, prompt string, input any) (json.RawMessage, error) {
	inputJSON, _ := json.Marshal(input)
	logID, started, _ := s.store.CreateAICallLog(ctx, workspaceID, userID, module, promptVersion, s.ai.Model, json.RawMessage(inputJSON))
	raw, err := s.ai.GenerateJSON(ctx, prompt, string(inputJSON))
	s.store.FinishAICallLog(ctx, logID, started, raw, err)
	return raw, err
}

func (s *GuidanceService) sessionContext(ctx context.Context, workspaceID int, sessionID int) (string, ConversationIntent, error) {
	var rawText string
	var intentRaw []byte
	err := s.store.dbx.QueryRowContext(ctx, `
		SELECT raw_text, COALESCE(conversation_intent_json, '{}'::jsonb)
		FROM v2_knowledge_intake_sessions
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, sessionID).Scan(&rawText, &intentRaw)
	if err != nil {
		return "", ConversationIntent{}, err
	}
	var intent ConversationIntent
	_ = json.Unmarshal(intentRaw, &intent)
	return rawText, defaultConversationIntent(intent), nil
}

func guidanceMode(profile CompanyProfile) string {
	if profile.Status == ProfileStatusGreen {
		return "adaptive_guidance"
	}
	return "first_gate"
}

func firstGateIntro(profile CompanyProfile) string {
	if strings.TrimSpace(profile.ProfileText) != "" {
		return "Я уже вижу часть базового контекста. Давай закроем оставшиеся пробелы, чтобы дальше задавать более точные вопросы."
	}
	return "Привет. Я REUP — AI-помощник по управлению целями и фокусом бизнеса. Сначала соберём базовый контекст, чтобы дальше не строить стратегию и вопросы в вакууме."
}

type baselineCoverageItem struct {
	Area               string `json:"area"`
	Status             string `json:"status"`
	Summary            string `json:"summary"`
	Missing            bool   `json:"missing"`
	NeedsClarification bool   `json:"needs_clarification"`
}

func firstGateMissingAreas(profile CompanyProfile) []string {
	coverage := baselineCoverageMap(profile)
	requiredAreas := []string{
		"business_identity",
		"business_stage",
		"current_pain",
		"scale_and_team",
		"financial_scale",
	}
	missing := make([]string, 0, len(requiredAreas))
	for _, area := range requiredAreas {
		item, ok := coverage[area]
		if !ok || firstGateAreaMissing(area, item) {
			missing = append(missing, area)
		}
	}
	return missing
}

func baselineCoverageMap(profile CompanyProfile) map[string]baselineCoverageItem {
	result := map[string]baselineCoverageItem{}
	if len(profile.BaselineCoverage) == 0 {
		return result
	}
	var items []baselineCoverageItem
	if err := json.Unmarshal(profile.BaselineCoverage, &items); err != nil {
		return result
	}
	for _, item := range items {
		area := strings.TrimSpace(item.Area)
		if area != "" {
			result[area] = item
		}
	}
	return result
}

func firstGateAreaMissing(area string, item baselineCoverageItem) bool {
	if item.Missing || item.NeedsClarification {
		return true
	}
	status := strings.TrimSpace(item.Status)
	if area == "business_identity" {
		return status != "answered" && status != "approximate"
	}
	switch status {
	case "answered", "approximate", "unknown", "not_disclosed":
		return false
	default:
		return true
	}
}

func firstGateQuestionsForAreas(areas []string) []string {
	questionByArea := map[string]string{
		"business_identity": "Чем занимается компания и что вы продаёте или делаете для клиентов?",
		"business_stage":    "На какой стадии сейчас бизнес: запуск, первые продажи, стабильная работа, рост, масштабирование, кризис, перезапуск или поиск модели?",
		"current_pain":      "Что сейчас сильнее всего болит в бизнесе или больше всего мешает двигаться дальше?",
		"scale_and_team":    "Какой сейчас масштаб: сколько лет работаете, какая команда, рынок, география? Можно примерно.",
		"financial_scale":   "По финансам есть примерный порядок выручки или прибыли? Можно диапазоном, “не знаю” или “не раскрываю”.",
	}
	questions := make([]string, 0, len(areas))
	for _, area := range areas {
		if question, ok := questionByArea[area]; ok {
			questions = append(questions, question)
		}
	}
	if len(questions) > 4 {
		return questions[:4]
	}
	return questions
}

func isBlankFirstGate(profile CompanyProfile, missingAreas []string) bool {
	return strings.TrimSpace(profile.ProfileText) == "" && len(missingAreas) >= 5
}

func rawJSONOrEmptyArray(raw json.RawMessage) any {
	if len(raw) == 0 {
		return []any{}
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return []any{}
	}
	return value
}

func deriveCompanyProfileCoverage(profile CompanyProfile, sourceText string) (companyProfileCollectorResponse, bool) {
	coverage := baselineCoverageMap(profile)
	changed := false

	for _, area := range []string{"business_identity", "business_stage", "current_pain", "scale_and_team", "financial_scale"} {
		if _, ok := coverage[area]; !ok {
			coverage[area] = baselineCoverageItem{
				Area:               area,
				Status:             "empty",
				Summary:            "",
				Missing:            true,
				NeedsClarification: true,
			}
			changed = true
		}
	}

	normalized := normalizeForSignalSearch(sourceText)
	changed = markCoveredFromSignals(coverage, "business_identity", normalized, []string{
		"компани", "бизнес", "продукт", "сервис", "платформ", "прода", "делаем", "помога",
	}, sourceText) || changed
	changed = markCoveredFromSignals(coverage, "business_stage", normalized, []string{
		"первые продаж", "перв продаж", "запуск", "mvp", "продукт готов", "собран", "стабильн", "рост", "масштаб", "кризис", "перезапуск", "поиск модели", "платящих клиентов почти нет", "клиентов почти нет",
	}, sourceText) || changed
	changed = markCoveredFromSignals(coverage, "current_pain", normalized, []string{
		"боль", "проблем", "хаос", "мешает", "не хватает", "недостат", "фокус", "сложно", "узкое место",
	}, sourceText) || changed
	changed = markCoveredFromSignals(coverage, "scale_and_team", normalized, []string{
		"команда", "маленькая команда", "рынок", "географ", "россия", "русскоязыч", "лет работает", "сотрудник", "человек",
	}, sourceText) || changed
	changed = markFinancialCoverageFromSignals(coverage, normalized, sourceText) || changed

	items := []baselineCoverageItem{
		coverage["business_identity"],
		coverage["business_stage"],
		coverage["current_pain"],
		coverage["scale_and_team"],
		coverage["financial_scale"],
	}
	status := ProfileStatusGreen
	for _, item := range items {
		if firstGateAreaMissing(item.Area, item) {
			status = ProfileStatusOrange
			break
		}
	}
	if strings.TrimSpace(profile.ProfileText) == "" && status != ProfileStatusGreen {
		status = ProfileStatusRed
	}
	if status != profile.Status {
		changed = true
	}

	coverageJSON, _ := json.Marshal(items)
	profileText := strings.TrimSpace(profile.ProfileText)
	if profileText == "" {
		profileText = firstMeaningfulLine(sourceText)
		changed = true
	}

	return companyProfileCollectorResponse{
		CompanyGateSignal:             status,
		CanContinueToAdaptiveGuidance: status == ProfileStatusGreen,
		ProfileText:                   profileText,
		BaselineCoverage:              coverageJSON,
	}, changed
}

func markCoveredFromSignals(coverage map[string]baselineCoverageItem, area string, normalized string, signals []string, sourceText string) bool {
	item := coverage[area]
	if !firstGateAreaMissing(area, item) {
		return false
	}
	for _, signal := range signals {
		if strings.Contains(normalized, signal) {
			item.Status = "approximate"
			item.Summary = firstMeaningfulLine(sourceText)
			item.Missing = false
			item.NeedsClarification = false
			coverage[area] = item
			return true
		}
	}
	return false
}

func markFinancialCoverageFromSignals(coverage map[string]baselineCoverageItem, normalized string, sourceText string) bool {
	item := coverage["financial_scale"]
	if !firstGateAreaMissing("financial_scale", item) {
		return false
	}
	for _, signal := range []string{"не знаю", "неизвест", "не раскры", "не хочу раскры", "выруч", "прибыл", "оборот", "маржин", "₽", "руб", "млн", "тыс"} {
		if strings.Contains(normalized, signal) {
			if strings.Contains(normalized, "не знаю") || strings.Contains(normalized, "неизвест") {
				item.Status = "unknown"
			} else if strings.Contains(normalized, "не раскры") || strings.Contains(normalized, "не хочу раскры") {
				item.Status = "not_disclosed"
			} else {
				item.Status = "approximate"
			}
			item.Summary = firstMeaningfulLine(sourceText)
			item.Missing = false
			item.NeedsClarification = false
			coverage["financial_scale"] = item
			return true
		}
	}
	return false
}

func normalizeForSignalSearch(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "ё", "е")
	return strings.Join(strings.Fields(value), " ")
}

func firstMeaningfulLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len([]rune(line)) > 260 {
			runes := []rune(line)
			return string(runes[:260]) + "..."
		}
		return line
	}
	return ""
}
