package tasks

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func (s *Store) QueueTaskEvaluation(ctx context.Context, workspaceID int, userID int, taskID int, force bool) error {
	_, err := s.Get(ctx, workspaceID, taskID)
	if err != nil {
		return err
	}
	_, err = s.dbx.ExecContext(ctx, `
		INSERT INTO v2_task_evaluation_jobs (
			workspace_id, task_id, requested_by, status, attempts, not_before, error_text
		)
		VALUES ($1, $2, $3, $4, 0, NOW(), '')
		ON CONFLICT (task_id) DO UPDATE SET
			workspace_id=EXCLUDED.workspace_id,
			requested_by=EXCLUDED.requested_by,
			status=CASE
				WHEN v2_task_evaluation_jobs.status='running' THEN v2_task_evaluation_jobs.status
				WHEN $5::BOOLEAN THEN $4
				WHEN v2_task_evaluation_jobs.status='queued' THEN v2_task_evaluation_jobs.status
				ELSE $4
			END,
			attempts=CASE WHEN $5::BOOLEAN AND v2_task_evaluation_jobs.status <> 'running' THEN 0 ELSE v2_task_evaluation_jobs.attempts END,
			revision=v2_task_evaluation_jobs.revision + 1,
			not_before=CASE WHEN $5::BOOLEAN AND v2_task_evaluation_jobs.status <> 'running' THEN NOW() ELSE v2_task_evaluation_jobs.not_before END,
			error_text=CASE WHEN $5::BOOLEAN THEN '' ELSE v2_task_evaluation_jobs.error_text END,
			updated_at=NOW()
	`, workspaceID, taskID, userID, EvaluationQueued, force)
	return err
}

func (s *Store) ClaimDueTaskEvaluation(ctx context.Context) (TaskEvaluationJob, error) {
	var item TaskEvaluationJob
	var requestedBy sql.NullInt64
	err := s.dbx.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT id
			FROM v2_task_evaluation_jobs
			WHERE status=$1 AND not_before <= NOW()
			ORDER BY not_before ASC, id ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE v2_task_evaluation_jobs job
		SET status=$2, attempts=job.attempts + 1, running_revision=job.revision, updated_at=NOW()
		FROM candidate
		WHERE job.id=candidate.id
		RETURNING job.id, job.workspace_id, job.task_id, job.requested_by, job.attempts, job.running_revision
	`, EvaluationQueued, EvaluationRunning).Scan(
		&item.ID, &item.WorkspaceID, &item.TaskID, &requestedBy, &item.Attempts, &item.Revision,
	)
	if requestedBy.Valid {
		value := int(requestedBy.Int64)
		item.RequestedBy = &value
	}
	return item, err
}

func (s *Store) SaveTaskEvaluation(
	ctx context.Context,
	job TaskEvaluationJob,
	model string,
	output taskEvaluatorModelOutput,
	priorityScore int,
	priorityTier string,
	fingerprint string,
	inputTokens int,
	outputTokens int,
	durationMS int64,
) error {
	_, err := s.dbx.ExecContext(ctx, `
		INSERT INTO v2_task_evaluations (
			workspace_id, task_id, model, prompt_version,
			strategic_relevance, course_alignment, tactical_alignment,
			expected_impact, urgency, effort, confidence,
			priority_score, priority_tier, recommendation, priority_reason,
			clarification_question, missing_information_json, flags_json, backlog_category, context_fingerprint,
			input_tokens, output_tokens, duration_ms
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
			$12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23
		)
	`, job.WorkspaceID, job.TaskID, strings.TrimSpace(model), taskEvaluatorPromptVersion,
		output.StrategicRelevance, output.CourseAlignment, output.TacticalAlignment,
		output.ExpectedImpact, output.Urgency, output.Effort, output.Confidence,
		priorityScore, priorityTier, output.Recommendation, output.PriorityReason,
		output.ClarificationQuestion, taskJSON(output.MissingInformation), taskJSON(output.Flags), output.BacklogCategory, fingerprint,
		inputTokens, outputTokens, durationMS)
	return err
}

func (s *Store) CompleteTaskEvaluationJob(ctx context.Context, jobID int) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_task_evaluation_jobs
		SET status=CASE WHEN revision > running_revision THEN $2 ELSE $3 END,
			not_before=CASE WHEN revision > running_revision THEN NOW() ELSE not_before END,
			error_text='', updated_at=NOW()
		WHERE id=$1 AND status=$4
	`, jobID, EvaluationQueued, EvaluationReady, EvaluationRunning)
	return err
}

func (s *Store) FailTaskEvaluationJob(ctx context.Context, job TaskEvaluationJob, errorText string) error {
	status := EvaluationFailed
	notBefore := time.Now().UTC()
	if job.Attempts < 3 {
		status = EvaluationQueued
		notBefore = notBefore.Add(time.Duration(job.Attempts) * 20 * time.Second)
	}
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_task_evaluation_jobs
		SET status=CASE WHEN revision > $5 THEN $6 ELSE $2 END,
			not_before=CASE WHEN revision > $5 THEN NOW() ELSE $3 END,
			error_text=$4, updated_at=NOW()
		WHERE id=$1
	`, job.ID, status, notBefore, truncateRunes(errorText, 2000), job.Revision, EvaluationQueued)
	return err
}

func (s *Store) RecoverStaleTaskEvaluations(ctx context.Context) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_task_evaluation_jobs
		SET status=$1, not_before=NOW(), error_text='', updated_at=NOW()
		WHERE status=$2 AND updated_at < NOW() - INTERVAL '5 minutes'
	`, EvaluationQueued, EvaluationRunning)
	return err
}
