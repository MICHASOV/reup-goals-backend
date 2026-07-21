package strategicmemory

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

func (s *Store) TryStartKnowledgeCandidate(
	ctx context.Context,
	workspaceID int,
	revision int,
	throughSourceID int,
	reason string,
) (KnowledgePipelineState, bool, error) {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return KnowledgePipelineState{}, false, err
	}
	defer tx.Rollback()

	state, err := pipelineStateForUpdate(ctx, tx, workspaceID)
	if err != nil {
		return KnowledgePipelineState{}, false, err
	}
	if !pipelineCanStartCandidate(state, revision, throughSourceID) {
		return state, false, tx.Commit()
	}

	state.Status = KnowledgePipelineAuditCandidate
	state.CandidateRevision = revision
	state.CandidateSourceID = throughSourceID
	state.CandidateReason = strings.TrimSpace(reason)
	err = tx.QueryRowContext(ctx, `
		UPDATE strategic_knowledge_pipeline_state
		SET status=$2, candidate_revision=$3, candidate_source_id=$4,
			candidate_reason=$5, candidate_report_json='{}'::jsonb, updated_at=NOW()
		WHERE workspace_id=$1
		RETURNING workspace_id, status, conversation_revision, last_user_source_id,
			last_extracted_source_id, last_audited_source_id, candidate_revision,
			candidate_source_id, ready_revision, compiled_revision, candidate_reason,
			audit_feedback_json, candidate_report_json, feedback_delivered_revision, updated_at
	`, workspaceID, state.Status, revision, throughSourceID, state.CandidateReason).Scan(pipelineStateDestinations(&state)...)
	if err != nil {
		return KnowledgePipelineState{}, false, err
	}
	return state, true, tx.Commit()
}

func pipelineCanStartCandidate(state KnowledgePipelineState, revision int, throughSourceID int) bool {
	if state.Status == KnowledgePipelineReady || state.Status == KnowledgePipelineAuditCandidate ||
		state.Status == KnowledgePipelineExtracting || state.Status == KnowledgePipelineReviewing ||
		state.Status == KnowledgePipelineCompiling {
		return false
	}
	if revision <= 0 || throughSourceID <= 0 {
		return false
	}
	if revision != state.ConversationRevision || throughSourceID != state.LastUserSourceID {
		return false
	}
	return throughSourceID > state.LastAuditedSourceID
}

func (s *Store) UpdateKnowledgePipelineStatus(ctx context.Context, workspaceID int, revision int, status string) error {
	result, err := s.dbx.ExecContext(ctx, `
		UPDATE strategic_knowledge_pipeline_state
		SET status=$3, updated_at=NOW()
		WHERE workspace_id=$1 AND candidate_revision=$2 AND status<>'ready'
	`, workspaceID, revision, status)
	if err != nil {
		return err
	}
	return requirePipelineUpdate(result)
}

func (s *Store) MarkKnowledgeExtracted(ctx context.Context, workspaceID int, revision int, throughSourceID int) error {
	result, err := s.dbx.ExecContext(ctx, `
		UPDATE strategic_knowledge_pipeline_state
		SET last_extracted_source_id=GREATEST(last_extracted_source_id, $3),
			status='reviewing', updated_at=NOW()
		WHERE workspace_id=$1 AND candidate_revision=$2 AND status<>'ready'
	`, workspaceID, revision, throughSourceID)
	if err != nil {
		return err
	}
	return requirePipelineUpdate(result)
}

func (s *Store) CompleteKnowledgeReview(
	ctx context.Context,
	workspaceID int,
	revision int,
	throughSourceID int,
	report QualityReport,
) error {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	state, err := pipelineStateForUpdate(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	if state.CandidateRevision != revision || state.CandidateSourceID != throughSourceID ||
		state.LastUserSourceID != throughSourceID || state.Status == KnowledgePipelineReady {
		return sql.ErrNoRows
	}

	status := KnowledgePipelineNeedsMoreContext
	if report.StrategyGate.CanStartStrategy {
		status = KnowledgePipelineCompiling
	} else if err := insertQualityReport(ctx, tx, workspaceID, report); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE strategic_knowledge_pipeline_state
		SET status=$4, last_audited_source_id=GREATEST(last_audited_source_id, $3),
			audit_feedback_json=$5, candidate_report_json=$6,
			feedback_delivered_revision=0, updated_at=NOW()
		WHERE workspace_id=$1 AND candidate_revision=$2 AND last_user_source_id=$3 AND status<>'ready'
	`, workspaceID, revision, throughSourceID, status, compactAuditFeedback(report), mustJSON(report))
	if err != nil {
		return err
	}
	if err := requirePipelineUpdate(result); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) PublishKnowledgeCompilation(
	ctx context.Context,
	workspaceID int,
	revision int,
	throughSourceID int,
	report QualityReport,
	documents []StrategicDocument,
) (int, error) {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	state, err := pipelineStateForUpdate(ctx, tx, workspaceID)
	if err != nil {
		return 0, err
	}
	if state.CandidateRevision != revision || state.CandidateSourceID != throughSourceID ||
		state.LastUserSourceID != throughSourceID || state.Status != KnowledgePipelineCompiling {
		return 0, sql.ErrNoRows
	}
	updated, err := upsertDocuments(ctx, tx, workspaceID, documents)
	if err != nil {
		return 0, err
	}
	if err := insertQualityReport(ctx, tx, workspaceID, report); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE strategic_knowledge_pipeline_state
		SET status='ready', ready_revision=$2, compiled_revision=$2,
			last_audited_source_id=GREATEST(last_audited_source_id, $3),
			audit_feedback_json=$4, candidate_report_json='{}'::jsonb,
			feedback_delivered_revision=$2, updated_at=NOW()
		WHERE workspace_id=$1 AND candidate_revision=$2 AND last_user_source_id=$3
	`, workspaceID, revision, throughSourceID, compactAuditFeedback(report))
	if err != nil {
		return 0, err
	}
	if err := requirePipelineUpdate(result); err != nil {
		return 0, err
	}
	return updated, tx.Commit()
}

func insertQualityReport(ctx context.Context, dbx strategicExecQueryer, workspaceID int, report QualityReport) error {
	readinessScore := clampScore(report.ReadinessScore)
	readinessStatus := normalizeReadinessStatus(defaultString(report.ReadinessStatus, report.Overall.ReadinessStatus))
	payload := map[string]any{
		"overall": report.Overall, "documents": report.Documents,
		"chat_guidance": report.ChatGuidance, "strategy_gate": report.StrategyGate,
	}
	_, err := dbx.ExecContext(ctx, `
		INSERT INTO strategic_quality_reports (
			workspace_id, readiness_score, readiness_status,
			changed_document_types_json, report_json
		) VALUES ($1, $2, $3, $4, $5)
	`, workspaceID, readinessScore, readinessStatus, mustJSON(report.ChangedDocumentTypes), mustJSON(payload))
	return err
}

func (s *Store) SupersedeKnowledgeCandidate(ctx context.Context, workspaceID int, revision int) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE strategic_knowledge_pipeline_state
		SET status=CASE WHEN status='ready' THEN status ELSE 'collecting' END,
			candidate_report_json='{}'::jsonb, updated_at=NOW()
		WHERE workspace_id=$1 AND candidate_revision=$2
	`, workspaceID, revision)
	return err
}

func (s *Store) FailKnowledgeCandidate(ctx context.Context, workspaceID int, revision int) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE strategic_knowledge_pipeline_state
		SET status=CASE WHEN status='ready' THEN status ELSE 'collecting' END,
			last_audited_source_id=CASE
				WHEN status='ready' THEN last_audited_source_id
				ELSE LEAST(last_audited_source_id, GREATEST(candidate_source_id - 1, 0))
			END,
			candidate_report_json='{}'::jsonb, updated_at=NOW()
		WHERE workspace_id=$1 AND candidate_revision=$2
	`, workspaceID, revision)
	return err
}

func (s *Store) MarkKnowledgeFeedbackDelivered(ctx context.Context, workspaceID int, candidateRevision int) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE strategic_knowledge_pipeline_state
		SET feedback_delivered_revision=GREATEST(feedback_delivered_revision, $2), updated_at=NOW()
		WHERE workspace_id=$1
	`, workspaceID, candidateRevision)
	return err
}

func (s *Store) KnowledgeSourcesRange(ctx context.Context, workspaceID int, afterID int, throughID int) ([]RawSource, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, workspace_id, user_id, source_type, content, metadata_json, created_at
		FROM strategic_raw_sources
		WHERE workspace_id=$1 AND id>$2 AND id<=$3
			AND source_type IN ($4, $5, $6)
		ORDER BY id
	`, workspaceID, afterID, throughID, SourceTypeUserMessage, SourceTypeAssistantMessage, SourceTypeFileUpload)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []RawSource{}
	for rows.Next() {
		var item RawSource
		var userID sql.NullInt64
		if err := rows.Scan(&item.ID, &item.WorkspaceID, &userID, &item.SourceType, &item.Content, &item.Metadata, &item.CreatedAt); err != nil {
			return nil, err
		}
		if userID.Valid {
			value := int(userID.Int64)
			item.UserID = &value
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func pipelineStateForUpdate(ctx context.Context, tx *sql.Tx, workspaceID int) (KnowledgePipelineState, error) {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO strategic_knowledge_pipeline_state (workspace_id)
		VALUES ($1) ON CONFLICT (workspace_id) DO NOTHING
	`, workspaceID); err != nil {
		return KnowledgePipelineState{}, err
	}
	var state KnowledgePipelineState
	err := tx.QueryRowContext(ctx, `
		SELECT workspace_id, status, conversation_revision, last_user_source_id,
			last_extracted_source_id, last_audited_source_id, candidate_revision,
			candidate_source_id, ready_revision, compiled_revision, candidate_reason,
			audit_feedback_json, candidate_report_json, feedback_delivered_revision, updated_at
		FROM strategic_knowledge_pipeline_state
		WHERE workspace_id=$1
		FOR UPDATE
	`, workspaceID).Scan(pipelineStateDestinations(&state)...)
	return state, err
}

func pipelineStateDestinations(state *KnowledgePipelineState) []any {
	return []any{
		&state.WorkspaceID, &state.Status, &state.ConversationRevision, &state.LastUserSourceID,
		&state.LastExtractedSourceID, &state.LastAuditedSourceID, &state.CandidateRevision,
		&state.CandidateSourceID, &state.ReadyRevision, &state.CompiledRevision,
		&state.CandidateReason, &state.AuditFeedback, &state.CandidateReport, &state.FeedbackDeliveredRevision,
		&state.UpdatedAt,
	}
}

func requirePipelineUpdate(result sql.Result) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func compactAuditFeedback(report QualityReport) json.RawMessage {
	return mustJSON(map[string]any{
		"readiness_score":     report.ReadinessScore,
		"readiness_status":    report.ReadinessStatus,
		"critical_blockers":   report.Overall.CriticalBlockers,
		"missing_information": report.Overall.MostImportantMissingInfo,
		"next_questions":      report.ChatGuidance.NextBestQuestions,
		"blind_spots":         report.ChatGuidance.BlindSpots,
		"strategy_gate":       report.StrategyGate,
	})
}
