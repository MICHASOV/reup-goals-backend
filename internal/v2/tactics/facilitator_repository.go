package tactics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"reup-goals-backend/internal/v2/aiactions"
)

func (s *Store) CreateChatMessage(ctx context.Context, workspaceID int, userID *int, role string, content string, metadata any) (int, error) {
	return s.CreateScopedChatMessage(ctx, workspaceID, userID, role, content, metadata, nil)
}

func (s *Store) CreateScopedChatMessage(ctx context.Context, workspaceID int, userID *int, role string, content string, metadata any, scope *TacticsMessageScope) (int, error) {
	role = strings.TrimSpace(role)
	if role != "assistant" && role != "user" {
		role = "user"
	}
	scopeType, scopeID := tacticsScopeKey(scope)
	var id int
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_tactics_chat_messages (
			workspace_id, user_id, role, content, metadata_json, scope_type, scope_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, workspaceID, userID, role, strings.TrimSpace(content), tacticsJSON(metadata), scopeType, scopeID).Scan(&id)
	return id, err
}

func (s *Store) ChatMessages(ctx context.Context, workspaceID int, limit int) ([]TacticsChatMessage, error) {
	return s.ScopedChatMessages(ctx, workspaceID, nil, limit)
}

func (s *Store) ScopedChatMessages(ctx context.Context, workspaceID int, scope *TacticsMessageScope, limit int) ([]TacticsChatMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 300
	}
	scopeType, scopeID := tacticsScopeKey(scope)
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, role, content, metadata_json, created_at
		FROM v2_tactics_chat_messages
		WHERE workspace_id=$1 AND scope_type=$2 AND scope_id=$3
		ORDER BY created_at DESC, id DESC
		LIMIT $4
	`, workspaceID, scopeType, scopeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []TacticsChatMessage{}
	for rows.Next() {
		var item TacticsChatMessage
		var metadataRaw json.RawMessage
		if err := rows.Scan(&item.ID, &item.Role, &item.Content, &metadataRaw, &item.CreatedAt); err != nil {
			return nil, err
		}
		var metadata struct {
			DraftChanges []TacticsDraftChange `json:"draft_changes"`
		}
		_ = json.Unmarshal(metadataRaw, &metadata)
		item.ProposedChanges = normalizeTacticsDraftChanges(metadata.DraftChanges)
		item.AppliedIndices = []int{}
		items = append(items, item)
	}
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	applied, err := s.tacticsAppliedIndices(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for index := range items {
		items[index].AppliedIndices = applied[items[index].ID]
		if items[index].AppliedIndices == nil {
			items[index].AppliedIndices = []int{}
		}
	}
	actionStates, err := s.tacticsActionStates(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for index := range items {
		items[index].ActionStates = actionStates[items[index].ID]
		if items[index].ActionStates == nil {
			items[index].ActionStates = []aiactions.Action{}
		}
	}
	return items, rows.Err()
}

func (s *Store) AssistantDraftChanges(ctx context.Context, workspaceID int, messageID int) ([]TacticsDraftChange, error) {
	var metadataRaw json.RawMessage
	err := s.dbx.QueryRowContext(ctx, `
		SELECT metadata_json
		FROM v2_tactics_chat_messages
		WHERE id=$1 AND workspace_id=$2 AND role='assistant'
	`, messageID, workspaceID).Scan(&metadataRaw)
	if err != nil {
		return nil, err
	}
	var metadata struct {
		DraftChanges []TacticsDraftChange `json:"draft_changes"`
	}
	if err := json.Unmarshal(metadataRaw, &metadata); err != nil {
		return nil, err
	}
	return normalizeTacticsDraftChanges(metadata.DraftChanges), nil
}

func (s *Store) tacticsAppliedIndices(ctx context.Context, workspaceID int) (map[int][]int, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT message_id, action_index
		FROM v2_ai_actions
		WHERE workspace_id=$1 AND scenario=$2 AND status=$3
		ORDER BY action_index ASC
	`, workspaceID, aiactions.ScenarioTacticsFacilitator, aiactions.StatusApplied)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int][]int{}
	for rows.Next() {
		var messageID int
		var actionIndex int
		if err := rows.Scan(&messageID, &actionIndex); err != nil {
			return nil, err
		}
		result[messageID] = append(result[messageID], actionIndex)
	}
	return result, rows.Err()
}

func (s *Store) tacticsActionStates(ctx context.Context, workspaceID int) (map[int][]aiactions.Action, error) {
	items, err := s.aiActions.List(ctx, workspaceID, aiactions.ScenarioTacticsFacilitator, 0, 500)
	if err != nil {
		return nil, err
	}
	result := map[int][]aiactions.Action{}
	for _, item := range items {
		result[item.MessageID] = append(result[item.MessageID], item)
	}
	return result, nil
}

func (s *Store) RegisterTacticsActions(ctx context.Context, workspaceID int, planID int, messageID int, changes []TacticsDraftChange) error {
	proposals := make([]aiactions.Proposal, 0, len(changes))
	for _, change := range changes {
		proposals = append(proposals, aiactions.Proposal{
			ActionType: change.Operation + ":" + change.EntityType,
			Payload:    change,
		})
	}
	_, err := s.aiActions.Register(
		ctx,
		workspaceID,
		aiactions.ScenarioTacticsFacilitator,
		"tactical_plan",
		planID,
		messageID,
		nil,
		proposals,
	)
	return err
}

func (s *Store) ClaimTacticsActionApplication(
	ctx context.Context,
	workspaceID int,
	planID int,
	messageID int,
	actionIndex int,
	change TacticsDraftChange,
	userID int,
) (bool, error) {
	result, err := s.dbx.ExecContext(ctx, `
		INSERT INTO v2_tactics_action_applications (
			workspace_id, tactical_plan_id, message_id, action_index, operation, entity_type, created_by, status
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'applying')
		ON CONFLICT (message_id, action_index) DO UPDATE SET
			status='applying', error_text='', updated_at=NOW()
		WHERE v2_tactics_action_applications.status='failed'
			OR (
				v2_tactics_action_applications.status='applying'
				AND v2_tactics_action_applications.updated_at < NOW() - INTERVAL '5 minutes'
			)
	`, workspaceID, planID, messageID, actionIndex, change.Operation, change.EntityType, userID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

func (s *Store) CompleteTacticsActionApplication(
	ctx context.Context,
	workspaceID int,
	messageID int,
	actionIndex int,
	entityID int,
) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_tactics_action_applications
		SET status='applied', entity_id=$4, error_text='', updated_at=NOW()
		WHERE workspace_id=$1 AND message_id=$2 AND action_index=$3 AND status='applying'
	`, workspaceID, messageID, actionIndex, entityID)
	return err
}

func (s *Store) FailTacticsActionApplication(
	ctx context.Context,
	workspaceID int,
	messageID int,
	actionIndex int,
	errorText string,
) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_tactics_action_applications
		SET status='failed', error_text=$4, updated_at=NOW()
		WHERE workspace_id=$1 AND message_id=$2 AND action_index=$3 AND status='applying'
	`, workspaceID, messageID, actionIndex, strings.TrimSpace(errorText))
	return err
}

func (s *Store) SessionState(ctx context.Context, workspaceID int) (TacticsSessionState, error) {
	row := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_tactics_session_state (workspace_id)
		VALUES ($1)
		ON CONFLICT (workspace_id) DO UPDATE SET workspace_id=EXCLUDED.workspace_id
		RETURNING workspace_id, revision, last_user_message_id, last_user_id,
			facilitator_status, status_reason, current_focus_json, decisions_json,
			open_questions_json, needs_strategy_review, strategy_review_reason, updated_at
	`, workspaceID)
	return scanTacticsSessionState(row)
}

func (s *Store) BeginFacilitatorTurn(ctx context.Context, workspaceID int, userID int, userMessageID int) (TacticsSessionState, error) {
	row := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_tactics_session_state (
			workspace_id, revision, last_user_message_id, last_user_id, facilitator_status
		)
		VALUES ($1, 1, $2, $3, $4)
		ON CONFLICT (workspace_id) DO UPDATE SET
			revision=v2_tactics_session_state.revision + 1,
			last_user_message_id=GREATEST(v2_tactics_session_state.last_user_message_id, EXCLUDED.last_user_message_id),
			last_user_id=CASE
				WHEN EXCLUDED.last_user_message_id >= v2_tactics_session_state.last_user_message_id THEN EXCLUDED.last_user_id
				ELSE v2_tactics_session_state.last_user_id
			END,
			facilitator_status=CASE
				WHEN EXCLUDED.last_user_message_id >= v2_tactics_session_state.last_user_message_id THEN $4
				ELSE v2_tactics_session_state.facilitator_status
			END,
			status_reason=CASE
				WHEN EXCLUDED.last_user_message_id >= v2_tactics_session_state.last_user_message_id THEN ''
				ELSE v2_tactics_session_state.status_reason
			END,
			updated_at=NOW()
		RETURNING workspace_id, revision, last_user_message_id, last_user_id,
			facilitator_status, status_reason, current_focus_json, decisions_json,
			open_questions_json, needs_strategy_review, strategy_review_reason, updated_at
	`, workspaceID, userMessageID, userID, FacilitatorStatusInProgress)
	return scanTacticsSessionState(row)
}

func (s *Store) RecordFacilitatorAssessment(ctx context.Context, workspaceID int, userMessageID int, output tacticsFacilitatorModelOutput) (TacticsSessionState, error) {
	current, err := s.SessionState(ctx, workspaceID)
	if err != nil {
		return TacticsSessionState{}, err
	}
	decisions := appendUniqueStrings(current.Decisions, output.DecisionsDetected, 30)
	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_tactics_session_state
		SET facilitator_status=$3,
			status_reason=$4,
			current_focus_json=$5,
			decisions_json=$6,
			open_questions_json=$7,
			needs_strategy_review=$8,
			strategy_review_reason=$9,
			updated_at=NOW()
		WHERE workspace_id=$1 AND last_user_message_id=$2
		RETURNING workspace_id, revision, last_user_message_id, last_user_id,
			facilitator_status, status_reason, current_focus_json, decisions_json,
			open_questions_json, needs_strategy_review, strategy_review_reason, updated_at
	`, workspaceID, userMessageID, normalizeTacticsStatus(output.SessionStatus), strings.TrimSpace(output.StatusReason),
		tacticsJSON(output.CurrentFocus), tacticsJSON(decisions), tacticsJSON(cleanTacticsStrings(output.OpenQuestions, 20)),
		output.NeedsStrategyReview, strings.TrimSpace(output.StrategyReviewReason))
	state, err := scanTacticsSessionState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return s.SessionState(ctx, workspaceID)
	}
	return state, err
}

func (s *Store) OpenAITacticsSession(ctx context.Context, workspaceID int, compactThreshold int, fingerprint string) (TacticsOpenAISession, error) {
	return s.OpenAITacticsScopeSession(ctx, workspaceID, nil, compactThreshold, fingerprint)
}

func (s *Store) OpenAITacticsScopeSession(ctx context.Context, workspaceID int, scope *TacticsMessageScope, compactThreshold int, fingerprint string) (TacticsOpenAISession, error) {
	if compactThreshold <= 0 {
		compactThreshold = 120000
	}
	scopeType, scopeID := tacticsScopeKey(scope)
	promptCacheKey := fmt.Sprintf("reupgoals-tactics-%d-%s-%d-v1", workspaceID, scopeType, scopeID)
	var item TacticsOpenAISession
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_tactics_scope_sessions (
			workspace_id, scope_type, scope_id, compact_threshold, prompt_cache_key, context_fingerprint
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (workspace_id, scope_type, scope_id) DO UPDATE SET
			previous_response_id=CASE
				WHEN v2_tactics_scope_sessions.context_fingerprint <> EXCLUDED.context_fingerprint THEN ''
				ELSE v2_tactics_scope_sessions.previous_response_id
			END,
			compact_threshold=EXCLUDED.compact_threshold,
			prompt_cache_key=EXCLUDED.prompt_cache_key,
			context_fingerprint=EXCLUDED.context_fingerprint,
			updated_at=NOW()
		RETURNING id, workspace_id, previous_response_id, compact_threshold,
			prompt_cache_key, context_fingerprint, created_at, updated_at
	`, workspaceID, scopeType, scopeID, compactThreshold, promptCacheKey, fingerprint).Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.PreviousResponseID,
		&item.CompactThreshold,
		&item.PromptCacheKey,
		&item.ContextFingerprint,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	return item, err
}

func (s *Store) UpdateOpenAITacticsPreviousResponseID(ctx context.Context, workspaceID int, responseID string) error {
	return s.UpdateOpenAITacticsScopePreviousResponseID(ctx, workspaceID, nil, responseID)
}

func (s *Store) UpdateOpenAITacticsScopePreviousResponseID(ctx context.Context, workspaceID int, scope *TacticsMessageScope, responseID string) error {
	scopeType, scopeID := tacticsScopeKey(scope)
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_tactics_scope_sessions
		SET previous_response_id=$4, updated_at=NOW()
		WHERE workspace_id=$1 AND scope_type=$2 AND scope_id=$3
	`, workspaceID, scopeType, scopeID, strings.TrimSpace(responseID))
	return err
}

func (s *Store) ResetOpenAITacticsSession(ctx context.Context, workspaceID int) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_tactics_scope_sessions
		SET previous_response_id='', context_fingerprint='', updated_at=NOW()
		WHERE workspace_id=$1
	`, workspaceID)
	return err
}

func (s *Store) StrategyDocuments(ctx context.Context, workspaceID int, strategyID int) ([]TacticsStrategyDocument, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT document.run_id, document.document_type,
			COALESCE(NULLIF(document.display_title, ''), document.title), document.status,
			COALESCE(NULLIF(document.formatted_markdown, ''), document.content_json::TEXT),
			document.source_refs_json, document.open_questions_json
		FROM v2_strategy_synthesis_documents document
		JOIN v2_strategy_synthesis_runs run ON run.id=document.run_id
		WHERE document.workspace_id=$1 AND run.strategy_id=$2 AND run.status='completed'
			AND run.id=(
				SELECT id FROM v2_strategy_synthesis_runs
				WHERE workspace_id=$1 AND strategy_id=$2 AND status='completed'
				ORDER BY created_at DESC, id DESC LIMIT 1
			)
		ORDER BY document.sort_order ASC, document.id ASC
	`, workspaceID, strategyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	documents := []TacticsStrategyDocument{}
	for rows.Next() {
		var item TacticsStrategyDocument
		if err := rows.Scan(&item.RunID, &item.DocumentType, &item.Title, &item.Status, &item.Content, &item.SourceRefs, &item.OpenQuestions); err != nil {
			return nil, err
		}
		documents = append(documents, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(documents) > 0 {
		return documents, nil
	}
	return s.strategyArtifactFallback(ctx, workspaceID, strategyID)
}

func (s *Store) strategyArtifactFallback(ctx context.Context, workspaceID int, strategyID int) ([]TacticsStrategyDocument, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT type, title, status, content
		FROM v2_strategy_artifacts
		WHERE workspace_id=$1 AND strategy_id=$2
		ORDER BY sort_order ASC, id ASC
	`, workspaceID, strategyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	documents := []TacticsStrategyDocument{}
	for rows.Next() {
		var item TacticsStrategyDocument
		if err := rows.Scan(&item.DocumentType, &item.Title, &item.Status, &item.Content); err != nil {
			return nil, err
		}
		documents = append(documents, item)
	}
	return documents, rows.Err()
}

func (s *Store) ScopeContext(ctx context.Context, workspaceID int, scope *TacticsMessageScope) (any, error) {
	if scope == nil || scope.EntityID <= 0 {
		return nil, nil
	}
	switch strings.TrimSpace(scope.EntityType) {
	case EntityPlan:
		return s.planByID(ctx, workspaceID, scope.EntityID)
	case EntityWorkstream:
		item, err := s.workstreamByID(ctx, workspaceID, scope.EntityID)
		if err != nil {
			return nil, err
		}
		workstreams := []Workstream{item}
		projects, err := s.listProjects(ctx, workspaceID, workstreams)
		if err == nil {
			item.Projects = projects[item.ID]
		}
		return item, err
	case EntityProject:
		return s.projectByID(ctx, workspaceID, scope.EntityID)
	case EntityRisk:
		return s.riskByID(ctx, workspaceID, scope.EntityID)
	case EntityOpportunity:
		return s.opportunityByID(ctx, workspaceID, scope.EntityID)
	default:
		return nil, fmt.Errorf("invalid_tactics_scope")
	}
}

func tacticsScopeKey(scope *TacticsMessageScope) (string, int) {
	if scope == nil || scope.EntityID <= 0 {
		return EntityPlan, 0
	}
	switch strings.TrimSpace(scope.EntityType) {
	case EntityWorkstream, EntityProject, EntityRisk, EntityOpportunity:
		return strings.TrimSpace(scope.EntityType), scope.EntityID
	default:
		return EntityPlan, 0
	}
}

func scanTacticsSessionState(scanner scanner) (TacticsSessionState, error) {
	var item TacticsSessionState
	var userID sql.NullInt64
	var focusRaw []byte
	var decisionsRaw []byte
	var questionsRaw []byte
	err := scanner.Scan(
		&item.WorkspaceID,
		&item.Revision,
		&item.LastUserMessageID,
		&userID,
		&item.FacilitatorStatus,
		&item.StatusReason,
		&focusRaw,
		&decisionsRaw,
		&questionsRaw,
		&item.NeedsStrategyReview,
		&item.StrategyReviewReason,
		&item.UpdatedAt,
	)
	if err != nil {
		return TacticsSessionState{}, err
	}
	if userID.Valid {
		value := int(userID.Int64)
		item.LastUserID = &value
	}
	_ = json.Unmarshal(focusRaw, &item.CurrentFocus)
	_ = json.Unmarshal(decisionsRaw, &item.Decisions)
	_ = json.Unmarshal(questionsRaw, &item.OpenQuestions)
	if item.Decisions == nil {
		item.Decisions = []string{}
	}
	if item.OpenQuestions == nil {
		item.OpenQuestions = []string{}
	}
	item.FacilitatorStatus = normalizeTacticsStatus(item.FacilitatorStatus)
	return item, nil
}

func normalizeTacticsStatus(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case FacilitatorStatusCandidateReady:
		return FacilitatorStatusCandidateReady
	case FacilitatorStatusBlocked:
		return FacilitatorStatusBlocked
	default:
		return FacilitatorStatusInProgress
	}
}

func cleanTacticsStrings(values []string, limit int) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		clean := strings.TrimSpace(value)
		key := strings.ToLower(clean)
		if clean == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, clean)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}

func appendUniqueStrings(existing []string, additions []string, limit int) []string {
	return cleanTacticsStrings(append(append([]string{}, existing...), additions...), limit)
}

func tacticsJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte("{}")
	}
	return raw
}
