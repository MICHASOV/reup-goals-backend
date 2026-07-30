package strategy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func (s *Store) CreateSynthesisRun(
	ctx context.Context,
	workspaceID int,
	strategyID int,
	userID int,
	sessionRevision int,
	throughMessageID int,
	model string,
) (StrategySynthesisRun, bool, error) {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return StrategySynthesisRun{}, false, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, workspaceID+6000000); err != nil {
		return StrategySynthesisRun{}, false, err
	}
	var currentRevision int
	var currentMessageID int
	if err := tx.QueryRowContext(ctx, `
		SELECT revision, last_user_message_id
		FROM v2_strategy_session_state
		WHERE workspace_id=$1
		FOR SHARE
	`, workspaceID).Scan(&currentRevision, &currentMessageID); err != nil {
		return StrategySynthesisRun{}, false, err
	}
	if currentRevision != sessionRevision || currentMessageID != throughMessageID {
		return StrategySynthesisRun{}, false, errors.New("strategy_synthesis_stale_revision")
	}

	_, _ = tx.ExecContext(ctx, `
		UPDATE v2_strategy_synthesis_runs
		SET status=$1,
			error='Synthesis did not finish before the stale-run timeout.',
			completed_at=NOW()
		WHERE workspace_id=$2
			AND status IN ($3, $4)
			AND created_at < NOW() - INTERVAL '15 minutes'
	`, SynthesisStatusFailed, workspaceID, SynthesisStatusQueued, SynthesisStatusRunning)
	_, _ = tx.ExecContext(ctx, `
		UPDATE v2_strategy_synthesis_runs
		SET status=$1,
			error='Synthesis was superseded by a newer strategy-session revision.',
			completed_at=NOW()
		WHERE workspace_id=$2
			AND status IN ($3, $4)
			AND session_revision < $5
	`, SynthesisStatusSuperseded, workspaceID, SynthesisStatusQueued, SynthesisStatusRunning, sessionRevision)

	row := tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, strategy_id, version, session_revision, through_message_id, status, model, prompt_version,
			summary, openai_response_id, input_tokens, output_tokens, duration_ms,
			error, created_by, created_at, started_at, completed_at
		FROM v2_strategy_synthesis_runs
		WHERE workspace_id=$1 AND session_revision=$2 AND status IN ($3, $4)
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, workspaceID, sessionRevision, SynthesisStatusQueued, SynthesisStatusRunning)
	if current, scanErr := scanSynthesisRun(row); scanErr == nil {
		if err := tx.Commit(); err != nil {
			return StrategySynthesisRun{}, false, err
		}
		return current, false, nil
	} else if !errors.Is(scanErr, sql.ErrNoRows) {
		return StrategySynthesisRun{}, false, scanErr
	}

	var version int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1
		FROM v2_strategy_synthesis_runs
		WHERE workspace_id=$1
	`, workspaceID).Scan(&version); err != nil {
		return StrategySynthesisRun{}, false, err
	}

	row = tx.QueryRowContext(ctx, `
		INSERT INTO v2_strategy_synthesis_runs (
			workspace_id, strategy_id, version, session_revision, through_message_id,
			status, model, prompt_version, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, workspace_id, strategy_id, version, session_revision, through_message_id, status, model, prompt_version,
			summary, openai_response_id, input_tokens, output_tokens, duration_ms,
			error, created_by, created_at, started_at, completed_at
	`, workspaceID, strategyID, version, sessionRevision, throughMessageID,
		SynthesisStatusQueued, strings.TrimSpace(model), StrategySynthesizerPromptVersion, userID)
	run, err := scanSynthesisRun(row)
	if err != nil {
		return StrategySynthesisRun{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return StrategySynthesisRun{}, false, err
	}
	return run, true, nil
}

func (s *Store) MarkSynthesisRunRunning(ctx context.Context, workspaceID int, runID int) error {
	result, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_strategy_synthesis_runs
		SET status=$1, started_at=COALESCE(started_at, NOW()), error=''
		WHERE id=$2 AND workspace_id=$3 AND status IN ($1, $4)
	`, SynthesisStatusRunning, runID, workspaceID, SynthesisStatusQueued)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) SynthesisRun(ctx context.Context, workspaceID int, runID int) (StrategySynthesisRun, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, strategy_id, version, session_revision, through_message_id, status, model, prompt_version,
			summary, openai_response_id, input_tokens, output_tokens, duration_ms,
			error, created_by, created_at, started_at, completed_at
		FROM v2_strategy_synthesis_runs
		WHERE id=$1 AND workspace_id=$2
	`, runID, workspaceID)
	return scanSynthesisRun(row)
}

func (s *Store) CompleteSynthesisRun(
	ctx context.Context,
	workspaceID int,
	runID int,
	output strategySynthesisModelOutput,
	documents []StrategySynthesisDocument,
	responseID string,
	inputTokens int,
	outputTokens int,
	durationMS int64,
) error {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM v2_strategy_synthesis_documents WHERE run_id=$1 AND workspace_id=$2`, runID, workspaceID); err != nil {
		return err
	}

	for _, document := range documents {
		contentRaw, err := json.Marshal(document.ContentBlocks)
		if err != nil {
			return err
		}
		sourcesRaw, err := json.Marshal(document.SourceRefs)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO v2_strategy_synthesis_documents (
				run_id, workspace_id, document_type, title, status,
				content_json, source_refs_json,
				display_title, frame_title, frame_subtitle, primary_signal,
				visual_status, formatted_markdown, open_questions_json,
				sort_order
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		`,
			runID,
			workspaceID,
			document.DocumentType,
			document.Title,
			document.Status,
			contentRaw,
			sourcesRaw,
			strings.TrimSpace(document.DisplayTitle),
			strings.TrimSpace(document.FrameTitle),
			strings.TrimSpace(document.FrameSubtitle),
			strings.TrimSpace(document.PrimarySignal),
			strings.TrimSpace(document.VisualStatus),
			strings.TrimSpace(document.FormattedDocument),
			mustJSON(document.OpenQuestions),
			document.SortOrder,
		); err != nil {
			return err
		}
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE v2_strategy_synthesis_runs
		SET status=$1,
			summary=$2,
			openai_response_id=$3,
			input_tokens=$4,
			output_tokens=$5,
			duration_ms=$6,
			error='',
			completed_at=NOW()
		WHERE id=$7 AND workspace_id=$8 AND status=$9
			AND EXISTS (
				SELECT 1 FROM v2_strategy_session_state session
				WHERE session.workspace_id=v2_strategy_synthesis_runs.workspace_id
					AND session.revision=v2_strategy_synthesis_runs.session_revision
					AND session.last_user_message_id=v2_strategy_synthesis_runs.through_message_id
			)
	`, SynthesisStatusCompleted, strings.TrimSpace(output.Summary), strings.TrimSpace(responseID), inputTokens, outputTokens, durationMS, runID, workspaceID, SynthesisStatusRunning)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return err
		}
		return sql.ErrNoRows
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE v2_strategies strategy
		SET status=$1, updated_at=NOW()
		WHERE strategy.id=(
				SELECT run.strategy_id
				FROM v2_strategy_synthesis_runs run
				WHERE run.id=$2 AND run.workspace_id=$3 AND run.status=$4
			)
			AND strategy.workspace_id=$3
			AND strategy.status=$5
			AND strategy.archived_at IS NULL
			AND EXISTS (
				SELECT 1
				FROM v2_strategy_synthesis_runs run
				JOIN v2_strategy_session_state session
					ON session.workspace_id=run.workspace_id
					AND session.revision=run.session_revision
					AND session.last_user_message_id=run.through_message_id
				JOIN v2_strategy_readiness_runs readiness
					ON readiness.workspace_id=run.workspace_id
					AND readiness.strategy_id=run.strategy_id
					AND readiness.session_revision=run.session_revision
					AND readiness.validated_through_message_id=run.through_message_id
					AND readiness.status=$6
					AND readiness.verdict=$7
					AND readiness.can_synthesize=TRUE
				WHERE run.id=$2
					AND run.workspace_id=$3
					AND run.status=$4
			)
	`, StatusReadyForReview, runID, workspaceID, SynthesisStatusCompleted, StatusDraft, ReadinessRunCompleted, ReadinessVerdictReady); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) SupersedeSynthesisRun(ctx context.Context, workspaceID int, runID int) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_strategy_synthesis_runs
		SET status=$1,
			error='Synthesis was superseded by a newer strategy-session revision.',
			completed_at=NOW()
		WHERE id=$2 AND workspace_id=$3 AND status IN ($4, $5)
	`, SynthesisStatusSuperseded, runID, workspaceID, SynthesisStatusQueued, SynthesisStatusRunning)
	return err
}

func (s *Store) FailSynthesisRun(ctx context.Context, workspaceID int, runID int, durationMS int64, errorText string) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_strategy_synthesis_runs
		SET status=$1, duration_ms=$2, error=$3, completed_at=NOW()
		WHERE id=$4 AND workspace_id=$5 AND status IN ($6, $7)
	`, SynthesisStatusFailed, durationMS, strings.TrimSpace(errorText), runID, workspaceID, SynthesisStatusQueued, SynthesisStatusRunning)
	return err
}

func (s *Store) RequeueSynthesisRun(ctx context.Context, workspaceID int, runID int, errorText string) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_strategy_synthesis_runs
		SET status=$1, error=$2, started_at=NULL
		WHERE id=$3 AND workspace_id=$4 AND status IN ($5, $6)
	`, SynthesisStatusQueued, strings.TrimSpace(errorText), runID, workspaceID, SynthesisStatusQueued, SynthesisStatusRunning)
	return err
}

func (s *Store) LatestSynthesis(ctx context.Context, workspaceID int, strategyID int) (StrategySynthesisResponse, error) {
	return s.latestSynthesis(ctx, workspaceID, strategyID, false)
}

func (s *Store) LatestSynthesisSummary(ctx context.Context, workspaceID int, strategyID int) (StrategySynthesisResponse, error) {
	return s.latestSynthesis(ctx, workspaceID, strategyID, true)
}

func (s *Store) latestSynthesis(ctx context.Context, workspaceID int, strategyID int, summaryOnly bool) (StrategySynthesisResponse, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, strategy_id, version, session_revision, through_message_id, status, model, prompt_version,
			summary, openai_response_id, input_tokens, output_tokens, duration_ms,
			error, created_by, created_at, started_at, completed_at
		FROM v2_strategy_synthesis_runs
		WHERE workspace_id=$1 AND strategy_id=$2
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, workspaceID, strategyID)
	run, err := scanSynthesisRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return StrategySynthesisResponse{Documents: []StrategySynthesisDocument{}}, nil
	}
	if err != nil {
		return StrategySynthesisResponse{}, err
	}
	documentsRun := run
	documents, err := s.listSynthesisDocuments(ctx, workspaceID, documentsRun.ID, summaryOnly)
	if err != nil {
		return StrategySynthesisResponse{}, err
	}
	if len(documents) == 0 {
		completed, completedErr := s.latestCompletedSynthesis(ctx, workspaceID, strategyID)
		if completedErr != nil && !errors.Is(completedErr, sql.ErrNoRows) {
			return StrategySynthesisResponse{}, completedErr
		}
		if completedErr == nil {
			documentsRun = completed
			documents, err = s.listSynthesisDocuments(ctx, workspaceID, documentsRun.ID, summaryOnly)
			if err != nil {
				return StrategySynthesisResponse{}, err
			}
		}
	}
	var currentRevision int
	_ = s.dbx.QueryRowContext(ctx, `
		SELECT revision FROM v2_strategy_session_state WHERE workspace_id=$1
	`, workspaceID).Scan(&currentRevision)
	isCurrent := documentsRun.Status == SynthesisStatusCompleted && documentsRun.SessionRevision == currentRevision
	return StrategySynthesisResponse{
		Run:          &run,
		DocumentsRun: &documentsRun,
		Documents:    documents,
		IsCurrent:    isCurrent,
	}, nil
}

func (s *Store) latestCompletedSynthesis(ctx context.Context, workspaceID int, strategyID int) (StrategySynthesisRun, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, strategy_id, version, session_revision, through_message_id, status, model, prompt_version,
			summary, openai_response_id, input_tokens, output_tokens, duration_ms,
			error, created_by, created_at, started_at, completed_at
		FROM v2_strategy_synthesis_runs
		WHERE workspace_id=$1 AND strategy_id=$2 AND status=$3
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, workspaceID, strategyID, SynthesisStatusCompleted)
	return scanSynthesisRun(row)
}

func (s *Store) ChatMessages(ctx context.Context, workspaceID int, limit int) ([]StrategyChatMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 300
	}
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, role, content, created_at
		FROM v2_strategy_chat_messages
		WHERE workspace_id=$1
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := []StrategyChatMessage{}
	for rows.Next() {
		var item StrategyChatMessage
		if err := rows.Scan(&item.ID, &item.Role, &item.Content, &item.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, item)
	}
	reverseChatMessages(messages)
	return messages, rows.Err()
}

func (s *Store) listSynthesisDocuments(ctx context.Context, workspaceID int, runID int, summaryOnly bool) ([]StrategySynthesisDocument, error) {
	contentColumns := `content_json, source_refs_json, formatted_markdown, open_questions_json`
	if summaryOnly {
		contentColumns = `'[]'::jsonb, '[]'::jsonb, ''::text, '[]'::jsonb`
	}
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, run_id, workspace_id, document_type, title, status,
			`+contentColumns+`,
			display_title, frame_title, frame_subtitle, primary_signal,
			visual_status,
			sort_order, created_at
		FROM v2_strategy_synthesis_documents
		WHERE workspace_id=$1 AND run_id=$2
		ORDER BY sort_order ASC, id ASC
	`, workspaceID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	documents := []StrategySynthesisDocument{}
	for rows.Next() {
		var document StrategySynthesisDocument
		var contentRaw []byte
		var sourcesRaw []byte
		var openQuestionsRaw []byte
		if err := rows.Scan(
			&document.ID,
			&document.RunID,
			&document.WorkspaceID,
			&document.DocumentType,
			&document.Title,
			&document.Status,
			&contentRaw,
			&sourcesRaw,
			&document.FormattedDocument,
			&openQuestionsRaw,
			&document.DisplayTitle,
			&document.FrameTitle,
			&document.FrameSubtitle,
			&document.PrimarySignal,
			&document.VisualStatus,
			&document.SortOrder,
			&document.CreatedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(contentRaw, &document.ContentBlocks); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(sourcesRaw, &document.SourceRefs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(openQuestionsRaw, &document.OpenQuestions); err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	return documents, rows.Err()
}

func (s *Store) SynthesisDocument(ctx context.Context, workspaceID int, documentID int) (StrategySynthesisDocument, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT id, run_id, workspace_id, document_type, title, status,
			content_json, source_refs_json, formatted_markdown, open_questions_json,
			display_title, frame_title, frame_subtitle, primary_signal, visual_status,
			sort_order, created_at
		FROM v2_strategy_synthesis_documents
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, documentID)
	var document StrategySynthesisDocument
	var contentRaw, sourcesRaw, openQuestionsRaw []byte
	if err := row.Scan(
		&document.ID, &document.RunID, &document.WorkspaceID, &document.DocumentType,
		&document.Title, &document.Status, &contentRaw, &sourcesRaw,
		&document.FormattedDocument, &openQuestionsRaw, &document.DisplayTitle,
		&document.FrameTitle, &document.FrameSubtitle, &document.PrimarySignal,
		&document.VisualStatus, &document.SortOrder, &document.CreatedAt,
	); err != nil {
		return StrategySynthesisDocument{}, err
	}
	if err := json.Unmarshal(contentRaw, &document.ContentBlocks); err != nil {
		return StrategySynthesisDocument{}, err
	}
	if err := json.Unmarshal(sourcesRaw, &document.SourceRefs); err != nil {
		return StrategySynthesisDocument{}, err
	}
	if err := json.Unmarshal(openQuestionsRaw, &document.OpenQuestions); err != nil {
		return StrategySynthesisDocument{}, err
	}
	return document, nil
}

func scanSynthesisRun(scanner scanner) (StrategySynthesisRun, error) {
	var run StrategySynthesisRun
	var createdBy sql.NullInt64
	var startedAt sql.NullTime
	var completedAt sql.NullTime
	err := scanner.Scan(
		&run.ID,
		&run.WorkspaceID,
		&run.StrategyID,
		&run.Version,
		&run.SessionRevision,
		&run.ThroughMessageID,
		&run.Status,
		&run.Model,
		&run.PromptVersion,
		&run.Summary,
		&run.OpenAIResponseID,
		&run.InputTokens,
		&run.OutputTokens,
		&run.DurationMS,
		&run.Error,
		&createdBy,
		&run.CreatedAt,
		&startedAt,
		&completedAt,
	)
	if err != nil {
		return StrategySynthesisRun{}, err
	}
	if createdBy.Valid {
		value := int(createdBy.Int64)
		run.CreatedBy = &value
	}
	if startedAt.Valid {
		run.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		run.CompletedAt = &completedAt.Time
	}
	return run, nil
}
