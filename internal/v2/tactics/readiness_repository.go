package tactics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const tacticsReadinessCooldown = 10 * time.Minute

func (s *Store) QueueTacticsReadinessAudit(
	ctx context.Context,
	state TacticsSessionState,
	plan TacticalPlan,
	force bool,
) (TacticsReadinessQueueItem, error) {
	notBefore := time.Now()
	if !force {
		var lastCompleted sql.NullTime
		err := s.dbx.QueryRowContext(ctx, `
			SELECT MAX(completed_at)
			FROM v2_tactics_readiness_runs
			WHERE workspace_id=$1 AND status IN ($2, $3)
		`, state.WorkspaceID, TacticsReadinessRunCompleted, TacticsReadinessRunSuperseded).Scan(&lastCompleted)
		if err != nil {
			return TacticsReadinessQueueItem{}, err
		}
		if lastCompleted.Valid && lastCompleted.Time.Add(tacticsReadinessCooldown).After(notBefore) {
			notBefore = lastCompleted.Time.Add(tacticsReadinessCooldown)
		}
	}

	row := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_tactics_readiness_queue (
			workspace_id, tactical_plan_id, strategy_id, course_id, session_revision,
			tactical_plan_revision, through_message_id, requested_by, not_before
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (workspace_id) DO UPDATE SET
			tactical_plan_id=EXCLUDED.tactical_plan_id,
			strategy_id=EXCLUDED.strategy_id,
			course_id=EXCLUDED.course_id,
			session_revision=EXCLUDED.session_revision,
			tactical_plan_revision=EXCLUDED.tactical_plan_revision,
			through_message_id=EXCLUDED.through_message_id,
			requested_by=EXCLUDED.requested_by,
			not_before=EXCLUDED.not_before,
			updated_at=NOW()
		RETURNING workspace_id, tactical_plan_id, strategy_id, course_id, session_revision,
			tactical_plan_revision, through_message_id, requested_by, not_before, updated_at
	`, state.WorkspaceID, plan.ID, plan.StrategyID, plan.CourseID, state.Revision,
		plan.Revision, state.LastUserMessageID, state.LastUserID, notBefore)
	return scanTacticsReadinessQueueItem(row)
}

func (s *Store) ClaimDueTacticsReadinessAudit(ctx context.Context, model string) (TacticsReadinessRun, error) {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return TacticsReadinessRun{}, err
	}
	defer tx.Rollback()

	_, _ = tx.ExecContext(ctx, `
		UPDATE v2_tactics_readiness_runs
		SET status=$1, error='Tactics readiness audit did not finish before the stale-run timeout.', completed_at=NOW()
		WHERE status=$2 AND started_at < NOW() - INTERVAL '15 minutes'
	`, TacticsReadinessRunFailed, TacticsReadinessRunRunning)

	row := tx.QueryRowContext(ctx, `
		SELECT workspace_id, tactical_plan_id, strategy_id, course_id, session_revision,
			tactical_plan_revision, through_message_id, requested_by, not_before, updated_at
		FROM v2_tactics_readiness_queue queue
		WHERE not_before <= NOW()
			AND NOT EXISTS (
				SELECT 1 FROM v2_tactics_readiness_runs run
				WHERE run.workspace_id=queue.workspace_id AND run.status=$1
			)
		ORDER BY not_before ASC, updated_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, TacticsReadinessRunRunning)
	item, err := scanTacticsReadinessQueueItem(row)
	if err != nil {
		return TacticsReadinessRun{}, err
	}

	row = tx.QueryRowContext(ctx, `
		INSERT INTO v2_tactics_readiness_runs (
			workspace_id, tactical_plan_id, strategy_id, course_id, session_revision,
			tactical_plan_revision, validated_through_message_id, status, model,
			prompt_version, created_by, started_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
		RETURNING id, workspace_id, tactical_plan_id, strategy_id, course_id, session_revision,
			tactical_plan_revision, validated_through_message_id, status, verdict,
			can_activate, overall_score, confidence, report_json, model, prompt_version,
			input_tokens, output_tokens, duration_ms, error, created_by, created_at,
			started_at, completed_at
	`, item.WorkspaceID, item.TacticalPlanID, item.StrategyID, item.CourseID,
		item.SessionRevision, item.TacticalPlanRevision, item.ThroughMessageID,
		TacticsReadinessRunRunning, strings.TrimSpace(model), TacticsReadinessPromptVersion, item.RequestedBy)
	run, err := scanTacticsReadinessRun(row)
	if err != nil {
		return TacticsReadinessRun{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM v2_tactics_readiness_queue WHERE workspace_id=$1`, item.WorkspaceID); err != nil {
		return TacticsReadinessRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return TacticsReadinessRun{}, err
	}
	return run, nil
}

func (s *Store) CompleteTacticsReadinessAudit(
	ctx context.Context,
	run TacticsReadinessRun,
	report TacticsReadinessReport,
	inputTokens int,
	outputTokens int,
	durationMS int64,
) (bool, error) {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var currentSessionRevision int
	var currentMessageID int
	if err := tx.QueryRowContext(ctx, `
		SELECT revision, last_user_message_id
		FROM v2_tactics_session_state
		WHERE workspace_id=$1
		FOR UPDATE
	`, run.WorkspaceID).Scan(&currentSessionRevision, &currentMessageID); err != nil {
		return false, err
	}
	var currentPlanRevision int
	var currentStrategyID int
	var currentCourseID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT revision, strategy_id, course_id FROM v2_tactical_plans
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
		FOR UPDATE
	`, run.TacticalPlanID, run.WorkspaceID).Scan(&currentPlanRevision, &currentStrategyID, &currentCourseID); err != nil {
		return false, err
	}

	isCurrent := currentSessionRevision == run.SessionRevision &&
		currentMessageID == run.ValidatedThroughMessageID &&
		currentPlanRevision == run.TacticalPlanRevision &&
		currentStrategyID == run.StrategyID && nullableIntEqual(run.CourseID, currentCourseID)
	status := TacticsReadinessRunCompleted
	if !isCurrent {
		status = TacticsReadinessRunSuperseded
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE v2_tactics_readiness_runs
		SET status=$1, verdict=$2, can_activate=$3, overall_score=$4, confidence=$5,
			report_json=$6, input_tokens=$7, output_tokens=$8, duration_ms=$9,
			error='', completed_at=NOW()
		WHERE id=$10 AND workspace_id=$11 AND status=$12
	`, status, report.Verdict, report.CanActivate, report.OverallScore, report.Confidence,
		raw, inputTokens, outputTokens, durationMS, run.ID, run.WorkspaceID, TacticsReadinessRunRunning)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected != 1 {
		return false, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return isCurrent, nil
}

func (s *Store) FailTacticsReadinessAudit(ctx context.Context, run TacticsReadinessRun, durationMS int64, errorText string) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_tactics_readiness_runs
		SET status=$1, duration_ms=$2, error=$3, completed_at=NOW()
		WHERE id=$4 AND workspace_id=$5 AND status=$6
	`, TacticsReadinessRunFailed, durationMS, strings.TrimSpace(errorText), run.ID, run.WorkspaceID, TacticsReadinessRunRunning)
	return err
}

func (s *Store) SupersedeTacticsReadinessAudit(ctx context.Context, run TacticsReadinessRun) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_tactics_readiness_runs
		SET status=$1,
			error='Tactics readiness audit was superseded by a newer session or tactical-plan revision.',
			completed_at=NOW()
		WHERE id=$2 AND workspace_id=$3 AND status=$4
	`, TacticsReadinessRunSuperseded, run.ID, run.WorkspaceID, TacticsReadinessRunRunning)
	return err
}

func (s *Store) LatestTacticsReadinessAudit(ctx context.Context, workspaceID int) (*TacticsReadinessRun, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, tactical_plan_id, strategy_id, course_id, session_revision,
			tactical_plan_revision, validated_through_message_id, status, verdict,
			can_activate, overall_score, confidence, report_json, model, prompt_version,
			input_tokens, output_tokens, duration_ms, error, created_by, created_at,
			started_at, completed_at
		FROM v2_tactics_readiness_runs
		WHERE workspace_id=$1 AND status<>$2
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, workspaceID, TacticsReadinessRunSuperseded)
	run, err := scanTacticsReadinessRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *Store) IsTacticsReadinessCurrent(ctx context.Context, workspaceID int, run *TacticsReadinessRun) (bool, error) {
	if run == nil || run.Status != TacticsReadinessRunCompleted {
		return false, nil
	}
	state, err := s.SessionState(ctx, workspaceID)
	if err != nil {
		return false, err
	}
	plan, err := s.planByID(ctx, workspaceID, run.TacticalPlanID)
	if err != nil {
		return false, err
	}
	return run.SessionRevision == state.Revision &&
		run.ValidatedThroughMessageID == state.LastUserMessageID &&
		run.TacticalPlanRevision == plan.Revision &&
		run.StrategyID == plan.StrategyID && intPointerEqual(run.CourseID, plan.CourseID), nil
}

func (s *Store) CanActivateTacticalPlan(ctx context.Context, workspaceID int, planID int) (bool, error) {
	run, err := s.LatestTacticsReadinessAudit(ctx, workspaceID)
	if err != nil || run == nil || run.TacticalPlanID != planID || !run.CanActivate {
		return false, err
	}
	return s.IsTacticsReadinessCurrent(ctx, workspaceID, run)
}

func scanTacticsReadinessQueueItem(scanner scanner) (TacticsReadinessQueueItem, error) {
	var item TacticsReadinessQueueItem
	var courseID sql.NullInt64
	var requestedBy sql.NullInt64
	err := scanner.Scan(
		&item.WorkspaceID, &item.TacticalPlanID, &item.StrategyID, &courseID,
		&item.SessionRevision, &item.TacticalPlanRevision, &item.ThroughMessageID,
		&requestedBy, &item.NotBefore, &item.UpdatedAt,
	)
	if err != nil {
		return TacticsReadinessQueueItem{}, err
	}
	if courseID.Valid {
		value := int(courseID.Int64)
		item.CourseID = &value
	}
	if requestedBy.Valid {
		value := int(requestedBy.Int64)
		item.RequestedBy = &value
	}
	return item, nil
}

func scanTacticsReadinessRun(scanner scanner) (TacticsReadinessRun, error) {
	var run TacticsReadinessRun
	var courseID sql.NullInt64
	var reportRaw []byte
	var createdBy sql.NullInt64
	var startedAt sql.NullTime
	var completedAt sql.NullTime
	err := scanner.Scan(
		&run.ID, &run.WorkspaceID, &run.TacticalPlanID, &run.StrategyID, &courseID,
		&run.SessionRevision, &run.TacticalPlanRevision, &run.ValidatedThroughMessageID,
		&run.Status, &run.Verdict, &run.CanActivate, &run.OverallScore, &run.Confidence,
		&reportRaw, &run.Model, &run.PromptVersion, &run.InputTokens, &run.OutputTokens,
		&run.DurationMS, &run.Error, &createdBy, &run.CreatedAt, &startedAt, &completedAt,
	)
	if err != nil {
		return TacticsReadinessRun{}, err
	}
	if courseID.Valid {
		value := int(courseID.Int64)
		run.CourseID = &value
	}
	if len(reportRaw) > 0 && string(reportRaw) != "{}" {
		var report TacticsReadinessReport
		if err := json.Unmarshal(reportRaw, &report); err != nil {
			return TacticsReadinessRun{}, err
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

func nullableIntEqual(pointer *int, value sql.NullInt64) bool {
	if pointer == nil {
		return !value.Valid
	}
	return value.Valid && *pointer == int(value.Int64)
}

func intPointerEqual(left *int, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
