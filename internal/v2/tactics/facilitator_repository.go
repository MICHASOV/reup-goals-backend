package tactics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func (s *Store) CreateChatMessage(ctx context.Context, workspaceID int, userID *int, role string, content string, metadata any) (int, error) {
	role = strings.TrimSpace(role)
	if role != "assistant" && role != "user" {
		role = "user"
	}
	var id int
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_tactics_chat_messages (workspace_id, user_id, role, content, metadata_json)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, workspaceID, userID, role, strings.TrimSpace(content), tacticsJSON(metadata)).Scan(&id)
	return id, err
}

func (s *Store) ChatMessages(ctx context.Context, workspaceID int, limit int) ([]TacticsChatMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 300
	}
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, role, content, created_at
		FROM v2_tactics_chat_messages
		WHERE workspace_id=$1
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []TacticsChatMessage{}
	for rows.Next() {
		var item TacticsChatMessage
		if err := rows.Scan(&item.ID, &item.Role, &item.Content, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	return items, rows.Err()
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
	if compactThreshold <= 0 {
		compactThreshold = 120000
	}
	promptCacheKey := fmt.Sprintf("reupgoals-tactics-facilitator-workspace-%d-v1", workspaceID)
	var item TacticsOpenAISession
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_tactics_openai_sessions (
			workspace_id, compact_threshold, prompt_cache_key, context_fingerprint
		)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (workspace_id) DO UPDATE SET
			previous_response_id=CASE
				WHEN v2_tactics_openai_sessions.context_fingerprint <> EXCLUDED.context_fingerprint THEN ''
				ELSE v2_tactics_openai_sessions.previous_response_id
			END,
			compact_threshold=EXCLUDED.compact_threshold,
			prompt_cache_key=EXCLUDED.prompt_cache_key,
			context_fingerprint=EXCLUDED.context_fingerprint,
			updated_at=NOW()
		RETURNING id, workspace_id, previous_response_id, compact_threshold,
			prompt_cache_key, context_fingerprint, created_at, updated_at
	`, workspaceID, compactThreshold, promptCacheKey, fingerprint).Scan(
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
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_tactics_openai_sessions
		SET previous_response_id=$2, updated_at=NOW()
		WHERE workspace_id=$1
	`, workspaceID, strings.TrimSpace(responseID))
	return err
}

func (s *Store) ResetOpenAITacticsSession(ctx context.Context, workspaceID int) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_tactics_openai_sessions
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
	default:
		return nil, fmt.Errorf("invalid_tactics_scope")
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
