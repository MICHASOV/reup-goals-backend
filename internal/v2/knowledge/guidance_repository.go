package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (s *Store) EnsureGuidanceBootstrap(ctx context.Context, workspaceID int) error {
	if err := s.EnsurePromptConfigs(ctx); err != nil {
		return err
	}
	if _, err := s.EnsureDocuments(ctx, workspaceID); err != nil {
		return err
	}
	if _, err := s.dbx.ExecContext(ctx, `
		INSERT INTO v2_company_profiles (workspace_id)
		VALUES ($1)
		ON CONFLICT (workspace_id) DO NOTHING
	`, workspaceID); err != nil {
		return err
	}
	if _, err := s.dbx.ExecContext(ctx, `
		INSERT INTO v2_knowledge_base_readiness (workspace_id)
		VALUES ($1)
		ON CONFLICT (workspace_id) DO NOTHING
	`, workspaceID); err != nil {
		return err
	}
	return s.ensureDocumentReadinessRows(ctx, workspaceID)
}

func (s *Store) EnsurePromptConfigs(ctx context.Context) error {
	configs := []struct {
		Name     string
		Version  string
		Template string
	}{
		{"company_profile_collector", CompanyProfileCollectorVersion, companyProfileCollectorPrompt},
		{"document_readiness_preflight", DocumentReadinessVersion, documentReadinessPreflightPrompt},
		{"strategic_guidance_question_planner", GuidancePlannerVersion, strategicGuidanceQuestionPlannerPrompt},
		{"knowledge_intake_router", RouterPromptVersion, routerSystemPrompt},
	}
	ready, err := s.promptConfigsReady(ctx, configs)
	if err != nil {
		return err
	}
	if ready {
		return nil
	}

	for _, config := range configs {
		if _, err := s.dbx.ExecContext(ctx, `
			INSERT INTO v2_ai_prompt_configs (prompt_name, prompt_version, template)
			VALUES ($1, $2, $3)
			ON CONFLICT (prompt_name, prompt_version) DO UPDATE SET
				template=EXCLUDED.template,
				updated_at=NOW()
		`, config.Name, config.Version, config.Template); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) promptConfigsReady(ctx context.Context, configs []struct {
	Name     string
	Version  string
	Template string
}) (bool, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT prompt_name, prompt_version
		FROM v2_ai_prompt_configs
	`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	existing := map[string]bool{}
	for rows.Next() {
		var name string
		var version string
		if err := rows.Scan(&name, &version); err != nil {
			return false, err
		}
		existing[name+"::"+version] = true
	}
	if err := rows.Err(); err != nil {
		return false, err
	}

	for _, config := range configs {
		if !existing[config.Name+"::"+config.Version] {
			return false, nil
		}
	}
	return true, nil
}

func (s *Store) CompanyProfile(ctx context.Context, workspaceID int) (CompanyProfile, error) {
	var profile CompanyProfile
	err := s.dbx.QueryRowContext(ctx, `
		SELECT company_profile_status, company_profile_text, baseline_coverage_json, COALESCE(company_profile_raw_json, '{}'::jsonb)
		FROM v2_company_profiles
		WHERE workspace_id=$1
	`, workspaceID).Scan(&profile.Status, &profile.ProfileText, &profile.BaselineCoverage, &profile.Raw)
	if errors.Is(err, sql.ErrNoRows) {
		return CompanyProfile{Status: ProfileStatusRed, BaselineCoverage: json.RawMessage("[]")}, nil
	}
	if len(profile.BaselineCoverage) == 0 {
		profile.BaselineCoverage = json.RawMessage("[]")
	}
	return profile, err
}

func (s *Store) UpsertCompanyProfile(ctx context.Context, workspaceID int, response companyProfileCollectorResponse, raw json.RawMessage) error {
	if response.CompanyGateSignal == "" {
		response.CompanyGateSignal = ProfileStatusRed
	}
	if len(response.BaselineCoverage) == 0 {
		response.BaselineCoverage = json.RawMessage("[]")
	}
	_, err := s.dbx.ExecContext(ctx, `
		INSERT INTO v2_company_profiles (
			workspace_id, company_profile_text, company_profile_status, company_profile_version,
			company_profile_raw_json, baseline_coverage_json, updated_at
		)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, NOW())
		ON CONFLICT (workspace_id) DO UPDATE SET
			company_profile_text=EXCLUDED.company_profile_text,
			company_profile_status=EXCLUDED.company_profile_status,
			company_profile_version=EXCLUDED.company_profile_version,
			company_profile_raw_json=EXCLUDED.company_profile_raw_json,
			baseline_coverage_json=EXCLUDED.baseline_coverage_json,
			updated_at=NOW()
	`, workspaceID, response.ProfileText, response.CompanyGateSignal, CompanyProfileCollectorVersion, nullableJSON(raw), string(response.BaselineCoverage))
	return err
}

func (s *Store) KnowledgeBaseReadiness(ctx context.Context, workspaceID int) (KnowledgeBaseReadiness, error) {
	var readiness KnowledgeBaseReadiness
	err := s.dbx.QueryRowContext(ctx, `
		SELECT overall_status, overall_score, strategy_transition_allowed
		FROM v2_knowledge_base_readiness
		WHERE workspace_id=$1
	`, workspaceID).Scan(&readiness.OverallStatus, &readiness.OverallScore, &readiness.StrategyTransitionAllowed)
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeBaseReadiness{OverallStatus: KnowledgeReadinessNotReady}, nil
	}
	return readiness, err
}

func (s *Store) RecalculateKnowledgeBaseReadiness(ctx context.Context, workspaceID int) (KnowledgeBaseReadiness, error) {
	profile, err := s.CompanyProfile(ctx, workspaceID)
	if err != nil {
		return KnowledgeBaseReadiness{}, err
	}
	readiness, err := s.DocumentReadinessList(ctx, workspaceID)
	if err != nil {
		return KnowledgeBaseReadiness{}, err
	}

	points := 0
	foundationalRed := false
	foundational := map[string]bool{
		"current_business_model":          true,
		"clients_and_demand":              true,
		"business_economics":              true,
		"constraints_and_non_negotiables": true,
		"strategic_challenge":             true,
	}
	for _, item := range readiness {
		switch item.ReadinessStatus {
		case ReadinessGreen:
			points += 2
		case ReadinessYellow:
			points++
		}
		if foundational[item.DocumentType] && item.ReadinessStatus == ReadinessRed {
			foundationalRed = true
		}
	}
	score := 0
	if len(documentDefinitions) > 0 {
		score = points * 100 / (len(documentDefinitions) * 2)
	}

	status := KnowledgeReadinessNotReady
	allowed := false
	if profile.Status == ProfileStatusGreen && !foundationalRed && score >= 60 {
		status = KnowledgeReadinessStrategyReady
		allowed = true
	} else if profile.Status == ProfileStatusGreen && score >= 30 {
		status = KnowledgeReadinessAlmostReady
	}

	_, err = s.dbx.ExecContext(ctx, `
		INSERT INTO v2_knowledge_base_readiness (
			workspace_id, overall_status, overall_score, strategy_transition_allowed, updated_at
		)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (workspace_id) DO UPDATE SET
			overall_status=EXCLUDED.overall_status,
			overall_score=EXCLUDED.overall_score,
			strategy_transition_allowed=EXCLUDED.strategy_transition_allowed,
			updated_at=NOW()
	`, workspaceID, status, score, allowed)
	return KnowledgeBaseReadiness{OverallStatus: status, OverallScore: score, StrategyTransitionAllowed: allowed}, err
}

func (s *Store) DocumentReadinessList(ctx context.Context, workspaceID int) ([]DocumentReadiness, error) {
	if err := s.ensureDocumentReadinessRows(ctx, workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT d.id, d.document_type, d.title, r.readiness_status, r.readiness_reason,
			r.main_missing_areas_json, r.should_run_deep_evaluator, r.confidence,
			COUNT(e.id) FILTER (WHERE e.status='active') AS entries_count
		FROM v2_knowledge_documents d
		LEFT JOIN v2_knowledge_document_readiness r ON r.document_id=d.id AND r.workspace_id=d.workspace_id
		LEFT JOIN v2_knowledge_document_entries e ON e.document_id=d.id AND e.workspace_id=d.workspace_id
		WHERE d.workspace_id=$1
		GROUP BY d.id, d.document_type, d.title, r.readiness_status, r.readiness_reason,
			r.main_missing_areas_json, r.should_run_deep_evaluator, r.confidence
		ORDER BY d.id ASC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []DocumentReadiness{}
	for rows.Next() {
		var item DocumentReadiness
		if err := rows.Scan(&item.DocumentID, &item.DocumentType, &item.Title, &item.ReadinessStatus,
			&item.ReadinessReason, &item.MainMissingAreas, &item.ShouldRunDeepEvaluator, &item.Confidence,
			&item.EntriesCount); err != nil {
			return nil, err
		}
		if item.ReadinessStatus == "" {
			item.ReadinessStatus = ReadinessRed
		}
		if len(item.MainMissingAreas) == 0 {
			item.MainMissingAreas = json.RawMessage("[]")
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) UpsertDocumentReadiness(ctx context.Context, workspaceID int, documentID int, response readinessPreflightResponse, raw json.RawMessage) error {
	areas, err := json.Marshal(response.MainMissingAreas)
	if err != nil {
		return err
	}
	if response.ReadinessStatus == "" {
		response.ReadinessStatus = ReadinessRed
	}
	if response.Confidence == "" {
		response.Confidence = ConfidenceLow
	}
	_, err = s.dbx.ExecContext(ctx, `
		INSERT INTO v2_knowledge_document_readiness (
			workspace_id, document_id, document_type, readiness_status, readiness_reason,
			main_missing_areas_json, should_run_deep_evaluator, confidence, prompt_version, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, NOW())
		ON CONFLICT (workspace_id, document_id) DO UPDATE SET
			readiness_status=EXCLUDED.readiness_status,
			readiness_reason=EXCLUDED.readiness_reason,
			main_missing_areas_json=EXCLUDED.main_missing_areas_json,
			should_run_deep_evaluator=EXCLUDED.should_run_deep_evaluator,
			confidence=EXCLUDED.confidence,
			prompt_version=EXCLUDED.prompt_version,
			updated_at=NOW()
	`, workspaceID, documentID, response.DocumentType, response.ReadinessStatus, response.ReadinessReason,
		string(areas), response.ShouldRunDeepEvaluator, response.Confidence, DocumentReadinessVersion)
	return err
}

func (s *Store) ActiveQuestionBlock(ctx context.Context, workspaceID int) (GuidanceQuestionBlock, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT id, source, guidance_status, question_type, intended_focus_summary, intended_documents_json,
			selection_reason_internal, title, intro, questions_json, handled_user_intent_json, confidence
		FROM v2_guidance_question_blocks
		WHERE workspace_id=$1 AND status=$2
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, workspaceID, QuestionStatusActive)
	return scanQuestionBlock(row)
}

func (s *Store) ValidateActiveQuestionBlock(ctx context.Context, workspaceID int, questionBlockID int) error {
	if questionBlockID <= 0 {
		return errors.New("invalid_question_block")
	}
	var exists bool
	if err := s.dbx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM v2_guidance_question_blocks
			WHERE workspace_id=$1 AND id=$2 AND status=$3
		)
	`, workspaceID, questionBlockID, QuestionStatusActive).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errors.New("invalid_question_block")
	}
	return nil
}

func (s *Store) CreateQuestionBlock(ctx context.Context, workspaceID int, block GuidanceQuestionBlock) (GuidanceQuestionBlock, error) {
	if block.GuidanceStatus == "" {
		block.GuidanceStatus = GuidanceStatusAskNextQuestion
	}
	if block.QuestionType == "" {
		block.QuestionType = "new_area_opening"
	}
	if block.Confidence == "" {
		block.Confidence = ConfidenceMedium
	}
	if len(block.IntendedDocuments) == 0 {
		block.IntendedDocuments = json.RawMessage("[]")
	}
	if len(block.HandledUserIntent) == 0 {
		block.HandledUserIntent = json.RawMessage("{}")
	}
	questions, err := json.Marshal(block.Questions)
	if err != nil {
		return GuidanceQuestionBlock{}, err
	}

	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return GuidanceQuestionBlock{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE v2_guidance_question_blocks
		SET status='expired'
		WHERE workspace_id=$1 AND status=$2
	`, workspaceID, QuestionStatusActive); err != nil {
		return GuidanceQuestionBlock{}, err
	}

	row := tx.QueryRowContext(ctx, `
		INSERT INTO v2_guidance_question_blocks (
			workspace_id, source, guidance_status, question_type, intended_focus_summary,
			intended_documents_json, selection_reason_internal, title, intro, questions_json,
			handled_user_intent_json, confidence, status
		)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10::jsonb, $11::jsonb, $12, $13)
		RETURNING id, source, guidance_status, question_type, intended_focus_summary, intended_documents_json,
			selection_reason_internal, title, intro, questions_json, handled_user_intent_json, confidence
	`, workspaceID, block.Source, block.GuidanceStatus, block.QuestionType, block.IntendedFocusSummary,
		string(block.IntendedDocuments), block.SelectionReasonInternal, block.Title, block.Intro, string(questions),
		string(block.HandledUserIntent), block.Confidence, QuestionStatusActive)
	created, err := scanQuestionBlock(row)
	if err != nil {
		return GuidanceQuestionBlock{}, err
	}
	return created, tx.Commit()
}

func (s *Store) MarkQuestionAnsweredForSession(ctx context.Context, workspaceID int, sessionID int) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_guidance_question_blocks q
		SET status=$1, answered_at=NOW()
		FROM v2_knowledge_intake_sessions s
		WHERE s.guidance_question_block_id=q.id
			AND s.workspace_id=$2
			AND s.id=$3
			AND q.status=$4
	`, QuestionStatusAnswered, workspaceID, sessionID, QuestionStatusActive)
	return err
}

func (s *Store) SessionQuestionBlock(ctx context.Context, workspaceID int, sessionID int) (GuidanceQuestionBlock, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT q.id, q.source, q.guidance_status, q.question_type, q.intended_focus_summary, q.intended_documents_json,
			q.selection_reason_internal, q.title, q.intro, q.questions_json, q.handled_user_intent_json, q.confidence
		FROM v2_guidance_question_blocks q
		JOIN v2_knowledge_intake_sessions s ON s.guidance_question_block_id=q.id
		WHERE s.workspace_id=$1 AND s.id=$2
	`, workspaceID, sessionID)
	return scanQuestionBlock(row)
}

func (s *Store) SessionAffectedDocuments(ctx context.Context, workspaceID int, sessionID int) ([]DocumentReadiness, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT DISTINCT d.id, d.document_type, d.title, 'red', '', '[]'::jsonb, false, 'low', 0
		FROM v2_knowledge_documents d
		WHERE d.workspace_id=$1
			AND (
			EXISTS (
				SELECT 1 FROM v2_proposed_document_patches p
				WHERE p.workspace_id=$1 AND p.session_id=$2 AND p.document_id=d.id
			)
			OR EXISTS (
				SELECT 1 FROM v2_proposed_document_conflicts c
				WHERE c.workspace_id=$1 AND c.session_id=$2 AND c.document_id=d.id
			)
		)
		ORDER BY d.id ASC
	`, workspaceID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []DocumentReadiness{}
	for rows.Next() {
		var item DocumentReadiness
		if err := rows.Scan(&item.DocumentID, &item.DocumentType, &item.Title, &item.ReadinessStatus,
			&item.ReadinessReason, &item.MainMissingAreas, &item.ShouldRunDeepEvaluator, &item.Confidence,
			&item.EntriesCount); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) SessionChangedCompanyCard(ctx context.Context, workspaceID int, sessionID int) (bool, error) {
	var changed bool
	err := s.dbx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM v2_proposed_document_patches p
			WHERE p.workspace_id=$1
				AND p.session_id=$2
				AND p.document_type='company_card'
				AND p.status=$3
		) OR EXISTS(
			SELECT 1
			FROM v2_proposed_document_conflicts c
			WHERE c.workspace_id=$1
				AND c.session_id=$2
				AND c.document_type='company_card'
				AND c.status=$4
				AND c.selected_option=$5
		)
	`, workspaceID, sessionID, PatchStatusApplied, ConflictStatusResolved, ConflictOptionNew).Scan(&changed)
	return changed, err
}

func (s *Store) DocumentEntriesForAI(ctx context.Context, workspaceID int, documentID int) ([]documentEntry, error) {
	return s.DocumentEntries(ctx, workspaceID, documentID)
}

func (s *Store) UpsertDocumentView(ctx context.Context, workspaceID int, documentID int, documentType string, response documentComposerResponse, raw json.RawMessage) error {
	sections, err := json.Marshal(response.Sections)
	if err != nil {
		return err
	}
	sourceEntryIDs, err := json.Marshal(response.SourceEntryIDs)
	if err != nil {
		return err
	}
	title := strings.TrimSpace(response.Title)
	if title == "" {
		if definition, ok := documentDefinitionByType(documentType); ok {
			title = definition.Title
		}
	}
	_, err = s.dbx.ExecContext(ctx, `
		INSERT INTO v2_knowledge_document_views (
			workspace_id, document_id, document_type, title, rendered_text, sections_json,
			source_entry_ids_json, composer_prompt_version, composer_raw_json, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9::jsonb, NOW())
		ON CONFLICT (workspace_id, document_id) DO UPDATE SET
			document_type=EXCLUDED.document_type,
			title=EXCLUDED.title,
			rendered_text=EXCLUDED.rendered_text,
			sections_json=EXCLUDED.sections_json,
			source_entry_ids_json=EXCLUDED.source_entry_ids_json,
			composer_prompt_version=EXCLUDED.composer_prompt_version,
			composer_raw_json=EXCLUDED.composer_raw_json,
			updated_at=NOW()
	`, workspaceID, documentID, documentType, title, strings.TrimSpace(response.RenderedText),
		string(sections), string(sourceEntryIDs), DocumentComposerVersion, nullableJSON(raw))
	return err
}

func (s *Store) CompanyCardEntries(ctx context.Context, workspaceID int) ([]documentEntry, error) {
	docs, err := s.EnsureDocuments(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return s.DocumentEntries(ctx, workspaceID, docs["company_card"])
}

func (s *Store) RecentQuestionHistory(ctx context.Context, workspaceID int) ([]GuidanceQuestionBlock, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, source, guidance_status, question_type, intended_focus_summary, intended_documents_json,
			selection_reason_internal, title, intro, questions_json, handled_user_intent_json, confidence
		FROM v2_guidance_question_blocks
		WHERE workspace_id=$1
		ORDER BY created_at DESC, id DESC
		LIMIT 5
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []GuidanceQuestionBlock{}
	for rows.Next() {
		block, err := scanQuestionBlock(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, block)
	}
	return result, rows.Err()
}

func (s *Store) CreateAICallLog(ctx context.Context, workspaceID int, userID int, module string, promptVersion string, model string, input any) (int, time.Time, error) {
	inputJSON, _ := json.Marshal(input)
	var id int
	started := time.Now()
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_ai_call_logs (
			workspace_id, user_id, ai_module, prompt_version, model, input_json, status
		)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, 'processing')
		RETURNING id
	`, workspaceID, userID, module, promptVersion, model, string(inputJSON)).Scan(&id)
	return id, started, err
}

func (s *Store) FinishAICallLog(ctx context.Context, id int, started time.Time, output json.RawMessage, err error) {
	if id <= 0 {
		return
	}
	if ctx.Err() != nil {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ctx = bgCtx
	}
	status := "ok"
	errText := ""
	if err != nil {
		status = "error"
		errText = err.Error()
	}
	_, _ = s.dbx.ExecContext(ctx, `
		UPDATE v2_ai_call_logs
		SET output_json=$1::jsonb, status=$2, error=$3, latency_ms=$4
		WHERE id=$5
	`, nullableJSON(output), status, errText, int(time.Since(started).Milliseconds()), id)
}

func (s *Store) RecentAICallLogs(ctx context.Context, workspaceID int, limit int) ([]AICallLogItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}

	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, ai_module, prompt_version, model, status, error, latency_ms, created_at
		FROM v2_ai_call_logs
		WHERE workspace_id=$1
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []AICallLogItem{}
	for rows.Next() {
		var item AICallLogItem
		if err := rows.Scan(&item.ID, &item.Module, &item.PromptVersion, &item.Model, &item.Status, &item.Error, &item.LatencyMS, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ensureDocumentReadinessRows(ctx context.Context, workspaceID int) error {
	documents, err := s.EnsureDocuments(ctx, workspaceID)
	if err != nil {
		return err
	}
	ready, err := s.documentReadinessRowsReady(ctx, workspaceID, documents)
	if err != nil {
		return err
	}
	if ready {
		return nil
	}
	for documentType, documentID := range documents {
		if _, err := s.dbx.ExecContext(ctx, `
			INSERT INTO v2_knowledge_document_readiness (
				workspace_id, document_id, document_type, readiness_status, confidence
			)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (workspace_id, document_id) DO NOTHING
		`, workspaceID, documentID, documentType, ReadinessRed, ConfidenceLow); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) documentReadinessRowsReady(ctx context.Context, workspaceID int, documents map[string]int) (bool, error) {
	if len(documents) == 0 {
		return false, nil
	}

	rows, err := s.dbx.QueryContext(ctx, `
		SELECT document_id
		FROM v2_knowledge_document_readiness
		WHERE workspace_id=$1
	`, workspaceID)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	existing := map[int]bool{}
	for rows.Next() {
		var documentID int
		if err := rows.Scan(&documentID); err != nil {
			return false, err
		}
		existing[documentID] = true
	}
	if err := rows.Err(); err != nil {
		return false, err
	}

	for _, documentID := range documents {
		if documentID == 0 || !existing[documentID] {
			return false, nil
		}
	}
	return true, nil
}

type questionBlockScanner interface {
	Scan(dest ...any) error
}

func scanQuestionBlock(scanner questionBlockScanner) (GuidanceQuestionBlock, error) {
	var block GuidanceQuestionBlock
	var questionsRaw []byte
	err := scanner.Scan(
		&block.ID,
		&block.Source,
		&block.GuidanceStatus,
		&block.QuestionType,
		&block.IntendedFocusSummary,
		&block.IntendedDocuments,
		&block.SelectionReasonInternal,
		&block.Title,
		&block.Intro,
		&questionsRaw,
		&block.HandledUserIntent,
		&block.Confidence,
	)
	if err != nil {
		return GuidanceQuestionBlock{}, err
	}
	if err := json.Unmarshal(questionsRaw, &block.Questions); err != nil {
		return GuidanceQuestionBlock{}, err
	}
	if len(block.IntendedDocuments) == 0 {
		block.IntendedDocuments = json.RawMessage("[]")
	}
	if len(block.HandledUserIntent) == 0 {
		block.HandledUserIntent = json.RawMessage("{}")
	}
	return block, nil
}
