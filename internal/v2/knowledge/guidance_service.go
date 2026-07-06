package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
		return response, nil
	}

	profile, err := s.updateCompanyProfileIfNeeded(ctx, workspaceID, userID, rawText, response.ConversationIntent, false)
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

	profile, err := s.updateCompanyProfileIfNeeded(ctx, workspaceID, userID, rawText, intent, companyCardChanged)
	if err != nil {
		return GuidanceConfirmResponse{}, err
	}

	updates := []DocumentReadiness{}
	for _, document := range affected {
		update, err := s.runDocumentReadiness(ctx, workspaceID, userID, document.DocumentID, document.DocumentType, document.Title)
		if err != nil {
			return GuidanceConfirmResponse{}, err
		}
		updates = append(updates, update)
	}

	readiness, err := s.store.RecalculateKnowledgeBaseReadiness(ctx, workspaceID)
	if err != nil {
		return GuidanceConfirmResponse{}, err
	}
	next, err := s.createNextQuestion(ctx, workspaceID, userID, profile, readiness, intent, rawText)
	if err != nil {
		return GuidanceConfirmResponse{}, err
	}

	return GuidanceConfirmResponse{
		Status:                   SessionConfirmed,
		Mode:                     guidanceMode(profile),
		CompanyProfile:           profile,
		KnowledgeBaseReadiness:   readiness,
		DocumentReadinessUpdates: updates,
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

func (s *GuidanceService) createNextQuestion(ctx context.Context, workspaceID int, userID int, profile CompanyProfile, readiness KnowledgeBaseReadiness, intent ConversationIntent, latestMessage string) (GuidanceQuestionBlock, error) {
	if profile.Status != ProfileStatusGreen {
		return s.createFirstGateQuestion(ctx, workspaceID, profile)
	}
	return s.runPlanner(ctx, workspaceID, userID, profile, readiness, intent, latestMessage)
}

func (s *GuidanceService) createFirstGateQuestion(ctx context.Context, workspaceID int, profile CompanyProfile) (GuidanceQuestionBlock, error) {
	block := GuidanceQuestionBlock{
		Source:         QuestionSourceFirstGate,
		GuidanceStatus: GuidanceStatusAskNextQuestion,
		QuestionType:   "new_area_opening",
		Title:          "Давай начнём с базового знакомства с компанией",
		Intro:          firstGateIntro(profile),
		Questions: []string{
			"Чем занимается компания? Что вы продаёте или делаете для клиентов?",
			"На какой стадии сейчас бизнес: запуск, первые продажи, стабильная работа, рост, масштабирование, кризис, перезапуск, поиск модели или что-то другое?",
			"Что сейчас сильнее всего болит в бизнесе?",
			"Какой сейчас масштаб бизнеса: сколько лет работаете, сколько людей в команде, в каком городе/стране/рынке работаете?",
			"Какая у компании выручка за последний месяц или год? Какая чистая прибыль? Можно диапазоном или написать, что пока не знаете/не хотите раскрывать.",
		},
		Confidence: ConfidenceHigh,
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
