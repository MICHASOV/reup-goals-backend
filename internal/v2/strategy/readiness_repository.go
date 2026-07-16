package strategy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const readinessAuditCooldown = 10 * time.Minute

func (s *Store) StrategyByID(ctx context.Context, workspaceID int, strategyID int) (Strategy, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, status, version, title, summary, source_type,
			created_by, approved_by, created_at, updated_at, approved_at, activated_at
		FROM v2_strategies
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, strategyID, workspaceID)
	return scanStrategy(row)
}

func (s *Store) SessionState(ctx context.Context, workspaceID int) (StrategySessionState, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT workspace_id, revision, last_user_message_id, last_user_id,
			facilitator_status, status_reason, remaining_uncertainties_json,
			last_audited_revision, last_readiness_run_id, last_synthesis_run_id, updated_at
		FROM v2_strategy_session_state
		WHERE workspace_id=$1
	`, workspaceID)
	state, err := scanStrategySessionState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return StrategySessionState{
			WorkspaceID:            workspaceID,
			FacilitatorStatus:      FacilitatorStatusContinue,
			RemainingUncertainties: []string{},
		}, nil
	}
	return state, err
}

func (s *Store) InvalidateSessionAfterContextChange(ctx context.Context, workspaceID int) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_strategy_session_state
		SET revision=revision + 1,
			facilitator_status=$2,
			status_reason='The strategy context changed after a file upload.',
			remaining_uncertainties_json='[]'::jsonb,
			updated_at=NOW()
		WHERE workspace_id=$1
	`, workspaceID, FacilitatorStatusContinue)
	return err
}

func (s *Store) BeginFacilitatorTurn(
	ctx context.Context,
	workspaceID int,
	userID int,
	userMessageID int,
) (StrategySessionState, error) {
	row := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_strategy_session_state (
			workspace_id, revision, last_user_message_id, last_user_id,
			facilitator_status, status_reason, remaining_uncertainties_json
		)
		VALUES ($1, 1, $2, $3, $4, '', '[]'::jsonb)
		ON CONFLICT (workspace_id) DO UPDATE SET
			revision=v2_strategy_session_state.revision + 1,
			last_user_message_id=GREATEST(v2_strategy_session_state.last_user_message_id, EXCLUDED.last_user_message_id),
			last_user_id=CASE
				WHEN EXCLUDED.last_user_message_id >= v2_strategy_session_state.last_user_message_id THEN EXCLUDED.last_user_id
				ELSE v2_strategy_session_state.last_user_id
			END,
			facilitator_status=CASE
				WHEN EXCLUDED.last_user_message_id >= v2_strategy_session_state.last_user_message_id THEN $4
				ELSE v2_strategy_session_state.facilitator_status
			END,
			status_reason=CASE
				WHEN EXCLUDED.last_user_message_id >= v2_strategy_session_state.last_user_message_id THEN ''
				ELSE v2_strategy_session_state.status_reason
			END,
			remaining_uncertainties_json=CASE
				WHEN EXCLUDED.last_user_message_id >= v2_strategy_session_state.last_user_message_id THEN '[]'::jsonb
				ELSE v2_strategy_session_state.remaining_uncertainties_json
			END,
			updated_at=NOW()
		RETURNING workspace_id, revision, last_user_message_id, last_user_id,
			facilitator_status, status_reason, remaining_uncertainties_json,
			last_audited_revision, last_readiness_run_id, last_synthesis_run_id, updated_at
	`, workspaceID, userMessageID, userID, FacilitatorStatusContinue)
	state, err := scanStrategySessionState(row)
	if err != nil {
		return StrategySessionState{}, err
	}
	_, err = s.dbx.ExecContext(ctx, `
		DELETE FROM v2_strategy_readiness_queue
		WHERE workspace_id=$1 AND session_revision < $2
	`, workspaceID, state.Revision)
	return state, err
}

func (s *Store) RecordFacilitatorAssessment(
	ctx context.Context,
	workspaceID int,
	userMessageID int,
	output strategyFacilitatorModelOutput,
) (StrategySessionState, error) {
	uncertainties := cleanStringListLocal(output.RemainingUncertainties)
	raw, err := json.Marshal(uncertainties)
	if err != nil {
		return StrategySessionState{}, err
	}
	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_strategy_session_state
		SET facilitator_status=$3,
			status_reason=$4,
			remaining_uncertainties_json=$5,
			updated_at=NOW()
		WHERE workspace_id=$1 AND last_user_message_id=$2
		RETURNING workspace_id, revision, last_user_message_id, last_user_id,
			facilitator_status, status_reason, remaining_uncertainties_json,
			last_audited_revision, last_readiness_run_id, last_synthesis_run_id, updated_at
	`, workspaceID, userMessageID, normalizeFacilitatorStatus(output.SessionStatus), strings.TrimSpace(output.StatusReason), raw)
	state, err := scanStrategySessionState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return s.SessionState(ctx, workspaceID)
	}
	return state, err
}

func (s *Store) QueueReadinessAudit(
	ctx context.Context,
	state StrategySessionState,
	strategyID int,
	force bool,
) (StrategyReadinessQueueItem, error) {
	notBefore := time.Now()
	if !force {
		var lastCompleted sql.NullTime
		err := s.dbx.QueryRowContext(ctx, `
			SELECT MAX(completed_at)
			FROM v2_strategy_readiness_runs
			WHERE workspace_id=$1 AND status IN ($2, $3)
		`, state.WorkspaceID, ReadinessRunCompleted, ReadinessRunSuperseded).Scan(&lastCompleted)
		if err != nil {
			return StrategyReadinessQueueItem{}, err
		}
		if lastCompleted.Valid && lastCompleted.Time.Add(readinessAuditCooldown).After(notBefore) {
			notBefore = lastCompleted.Time.Add(readinessAuditCooldown)
		}
	}

	row := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_strategy_readiness_queue (
			workspace_id, strategy_id, session_revision, through_message_id, requested_by, not_before
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (workspace_id) DO UPDATE SET
			strategy_id=EXCLUDED.strategy_id,
			session_revision=EXCLUDED.session_revision,
			through_message_id=EXCLUDED.through_message_id,
			requested_by=EXCLUDED.requested_by,
			not_before=EXCLUDED.not_before,
			updated_at=NOW()
		RETURNING workspace_id, strategy_id, session_revision, through_message_id,
			requested_by, not_before, updated_at
	`, state.WorkspaceID, strategyID, state.Revision, state.LastUserMessageID, state.LastUserID, notBefore)
	return scanReadinessQueueItem(row)
}

func (s *Store) ClaimDueReadinessAudit(ctx context.Context, model string) (StrategyReadinessRun, error) {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return StrategyReadinessRun{}, err
	}
	defer tx.Rollback()

	_, _ = tx.ExecContext(ctx, `
		UPDATE v2_strategy_readiness_runs
		SET status=$1, error='Readiness audit did not finish before the stale-run timeout.', completed_at=NOW()
		WHERE status=$2 AND started_at < NOW() - INTERVAL '15 minutes'
	`, ReadinessRunFailed, ReadinessRunRunning)

	row := tx.QueryRowContext(ctx, `
		SELECT workspace_id, strategy_id, session_revision, through_message_id,
			requested_by, not_before, updated_at
		FROM v2_strategy_readiness_queue
		WHERE not_before <= NOW()
			AND NOT EXISTS (
				SELECT 1 FROM v2_strategy_readiness_runs r
				WHERE r.workspace_id=v2_strategy_readiness_queue.workspace_id AND r.status=$1
			)
		ORDER BY not_before ASC, updated_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, ReadinessRunRunning)
	item, err := scanReadinessQueueItem(row)
	if err != nil {
		return StrategyReadinessRun{}, err
	}

	row = tx.QueryRowContext(ctx, `
		INSERT INTO v2_strategy_readiness_runs (
			workspace_id, strategy_id, session_revision, validated_through_message_id,
			status, model, prompt_version, created_by, started_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		RETURNING id, workspace_id, strategy_id, session_revision, validated_through_message_id,
			status, verdict, can_synthesize, confidence, report_json, model, prompt_version,
			input_tokens, output_tokens, duration_ms, error, created_by, created_at, started_at, completed_at
	`, item.WorkspaceID, item.StrategyID, item.SessionRevision, item.ThroughMessageID,
		ReadinessRunRunning, strings.TrimSpace(model), StrategyReadinessPromptVersion, item.RequestedBy)
	run, err := scanStrategyReadinessRun(row)
	if err != nil {
		return StrategyReadinessRun{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM v2_strategy_readiness_queue WHERE workspace_id=$1`, item.WorkspaceID); err != nil {
		return StrategyReadinessRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return StrategyReadinessRun{}, err
	}
	return run, nil
}

func (s *Store) CompleteReadinessAudit(
	ctx context.Context,
	run StrategyReadinessRun,
	report StrategyReadinessReport,
	inputTokens int,
	outputTokens int,
	durationMS int64,
) (bool, error) {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var currentRevision int
	var currentMessageID int
	err = tx.QueryRowContext(ctx, `
		SELECT revision, last_user_message_id
		FROM v2_strategy_session_state
		WHERE workspace_id=$1
		FOR UPDATE
	`, run.WorkspaceID).Scan(&currentRevision, &currentMessageID)
	if err != nil {
		return false, err
	}
	isCurrent := currentRevision == run.SessionRevision && currentMessageID == run.ValidatedThroughMessageID
	status := ReadinessRunCompleted
	if !isCurrent {
		status = ReadinessRunSuperseded
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE v2_strategy_readiness_runs
		SET status=$1, verdict=$2, can_synthesize=$3, confidence=$4,
			report_json=$5, input_tokens=$6, output_tokens=$7, duration_ms=$8,
			error='', completed_at=NOW()
		WHERE id=$9 AND workspace_id=$10 AND status=$11
	`, status, report.Verdict, report.CanSynthesize, report.Confidence, raw,
		inputTokens, outputTokens, durationMS, run.ID, run.WorkspaceID, ReadinessRunRunning)
	if err != nil {
		return false, err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return false, err
		}
		return false, sql.ErrNoRows
	}
	if isCurrent {
		if _, err := tx.ExecContext(ctx, `
			UPDATE v2_strategy_session_state
			SET last_audited_revision=$2, last_readiness_run_id=$3, updated_at=NOW()
			WHERE workspace_id=$1
		`, run.WorkspaceID, run.SessionRevision, run.ID); err != nil {
			return false, err
		}
		if err := syncStrategyResearchRequestsTx(ctx, tx, run, report); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return isCurrent, nil
}

func syncStrategyResearchRequestsTx(
	ctx context.Context,
	tx *sql.Tx,
	run StrategyReadinessRun,
	report StrategyReadinessReport,
) error {
	for _, item := range report.FacilitatorGuidance {
		goal := strings.TrimSpace(item.ResearchGoal)
		if goal == "" || (report.Verdict == ReadinessVerdictReady && !item.Blocking && item.Priority == "low") {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO v2_strategy_research_requests (
				workspace_id, strategy_id, source_readiness_run_id, area, research_goal,
				why_it_matters, context_to_carry, priority, blocking, created_by
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (strategy_id, area, research_goal) DO UPDATE SET
				source_readiness_run_id=EXCLUDED.source_readiness_run_id,
				why_it_matters=EXCLUDED.why_it_matters,
				context_to_carry=EXCLUDED.context_to_carry,
				priority=EXCLUDED.priority,
				blocking=EXCLUDED.blocking,
				updated_at=NOW()
		`, run.WorkspaceID, run.StrategyID, run.ID, strings.TrimSpace(item.Area), goal,
			strings.TrimSpace(item.WhyItMatters), strings.TrimSpace(item.ContextToCarry),
			normalizeResearchPriority(item.Priority), item.Blocking, nullableResearchUser(run.CreatedBy)); err != nil {
			return err
		}
	}
	return nil
}

func normalizeResearchPriority(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "high":
		return "high"
	case "low":
		return "low"
	default:
		return "medium"
	}
}

func nullableResearchUser(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func (s *Store) LinkSynthesisToSession(ctx context.Context, workspaceID int, revision int, runID int) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_strategy_session_state
		SET last_synthesis_run_id=$3, updated_at=NOW()
		WHERE workspace_id=$1 AND revision=$2
	`, workspaceID, revision, runID)
	return err
}

func (s *Store) FailReadinessAudit(ctx context.Context, run StrategyReadinessRun, durationMS int64, errorText string) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_strategy_readiness_runs
		SET status=$1, duration_ms=$2, error=$3, completed_at=NOW()
		WHERE id=$4 AND workspace_id=$5 AND status=$6
	`, ReadinessRunFailed, durationMS, strings.TrimSpace(errorText), run.ID, run.WorkspaceID, ReadinessRunRunning)
	return err
}

func (s *Store) SupersedeReadinessAudit(ctx context.Context, run StrategyReadinessRun) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_strategy_readiness_runs
		SET status=$1,
			error='Readiness audit was superseded by a newer strategy-session revision.',
			completed_at=NOW()
		WHERE id=$2 AND workspace_id=$3 AND status=$4
	`, ReadinessRunSuperseded, run.ID, run.WorkspaceID, ReadinessRunRunning)
	return err
}

func (s *Store) LatestReadinessAudit(ctx context.Context, workspaceID int) (*StrategyReadinessRun, error) {
	return s.latestReadinessAudit(ctx, workspaceID, false)
}

func (s *Store) LatestCompletedReadinessAudit(ctx context.Context, workspaceID int) (*StrategyReadinessRun, error) {
	return s.latestReadinessAudit(ctx, workspaceID, true)
}

func (s *Store) latestReadinessAudit(ctx context.Context, workspaceID int, completedOnly bool) (*StrategyReadinessRun, error) {
	completedFilter := ""
	if completedOnly {
		completedFilter = " AND status='completed'"
	}
	row := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, strategy_id, session_revision, validated_through_message_id,
			status, verdict, can_synthesize, confidence, report_json, model, prompt_version,
			input_tokens, output_tokens, duration_ms, error, created_by, created_at, started_at, completed_at
		FROM v2_strategy_readiness_runs
		WHERE workspace_id=$1 AND status<>$2`+completedFilter+`
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, workspaceID, ReadinessRunSuperseded)
	run, err := scanStrategyReadinessRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func scanStrategySessionState(scanner scanner) (StrategySessionState, error) {
	var state StrategySessionState
	var lastUserID sql.NullInt64
	var readinessRunID sql.NullInt64
	var synthesisRunID sql.NullInt64
	var uncertaintiesRaw []byte
	err := scanner.Scan(
		&state.WorkspaceID,
		&state.Revision,
		&state.LastUserMessageID,
		&lastUserID,
		&state.FacilitatorStatus,
		&state.StatusReason,
		&uncertaintiesRaw,
		&state.LastAuditedRevision,
		&readinessRunID,
		&synthesisRunID,
		&state.UpdatedAt,
	)
	if err != nil {
		return StrategySessionState{}, err
	}
	state.RemainingUncertainties = []string{}
	if len(uncertaintiesRaw) > 0 {
		_ = json.Unmarshal(uncertaintiesRaw, &state.RemainingUncertainties)
	}
	if lastUserID.Valid {
		value := int(lastUserID.Int64)
		state.LastUserID = &value
	}
	if readinessRunID.Valid {
		value := int(readinessRunID.Int64)
		state.LastReadinessRunID = &value
	}
	if synthesisRunID.Valid {
		value := int(synthesisRunID.Int64)
		state.LastSynthesisRunID = &value
	}
	return state, nil
}

func scanReadinessQueueItem(scanner scanner) (StrategyReadinessQueueItem, error) {
	var item StrategyReadinessQueueItem
	var requestedBy sql.NullInt64
	err := scanner.Scan(
		&item.WorkspaceID,
		&item.StrategyID,
		&item.SessionRevision,
		&item.ThroughMessageID,
		&requestedBy,
		&item.NotBefore,
		&item.UpdatedAt,
	)
	if err != nil {
		return StrategyReadinessQueueItem{}, err
	}
	if requestedBy.Valid {
		value := int(requestedBy.Int64)
		item.RequestedBy = &value
	}
	return item, nil
}

func scanStrategyReadinessRun(scanner scanner) (StrategyReadinessRun, error) {
	var run StrategyReadinessRun
	var reportRaw []byte
	var createdBy sql.NullInt64
	var startedAt sql.NullTime
	var completedAt sql.NullTime
	err := scanner.Scan(
		&run.ID,
		&run.WorkspaceID,
		&run.StrategyID,
		&run.SessionRevision,
		&run.ValidatedThroughMessageID,
		&run.Status,
		&run.Verdict,
		&run.CanSynthesize,
		&run.Confidence,
		&reportRaw,
		&run.Model,
		&run.PromptVersion,
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
		return StrategyReadinessRun{}, err
	}
	if len(reportRaw) > 0 && string(reportRaw) != "{}" {
		var report StrategyReadinessReport
		if err := json.Unmarshal(reportRaw, &report); err != nil {
			return StrategyReadinessRun{}, err
		}
		run.Report = &report
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

func cleanStringListLocal(items []string) []string {
	result := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		result = append(result, item)
	}
	return result
}
