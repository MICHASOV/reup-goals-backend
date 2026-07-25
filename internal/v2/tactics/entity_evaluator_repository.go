package tactics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	entityEvaluationQueued           = "queued"
	entityEvaluationRunning          = "running"
	entityEvaluationReady            = "ready"
	entityEvaluationFailed           = "failed"
	entityEvaluationAwaitingStrategy = "awaiting_strategy"
)

func (s *Store) QueueEntityEvaluation(ctx context.Context, workspaceID, userID int, entityType string, entityID int) (string, error) {
	if entityType != EntityWorkstream && entityType != EntityProject {
		return "", errors.New("invalid_tactical_evaluation_entity")
	}
	if err := s.validateEvaluationEntity(ctx, workspaceID, entityType, entityID); err != nil {
		return "", err
	}
	status := entityEvaluationQueued
	if _, err := s.activeStrategy(ctx, workspaceID); errors.Is(err, sql.ErrNoRows) {
		status = entityEvaluationAwaitingStrategy
	} else if err != nil {
		return "", err
	}
	_, err := s.dbx.ExecContext(ctx, `
		INSERT INTO v2_tactical_entity_evaluation_jobs (
			workspace_id, entity_type, entity_id, requested_by, status, attempts, not_before
		)
		VALUES ($1, $2, $3, $4, $5, 0, NOW())
		ON CONFLICT (workspace_id, entity_type, entity_id) DO UPDATE SET
			requested_by=EXCLUDED.requested_by,
			status=CASE
				WHEN v2_tactical_entity_evaluation_jobs.status=$6 THEN v2_tactical_entity_evaluation_jobs.status
				ELSE EXCLUDED.status
			END,
			attempts=CASE WHEN v2_tactical_entity_evaluation_jobs.status=$6 THEN attempts ELSE 0 END,
			revision=revision + 1,
			not_before=NOW(),
			error_text='',
		updated_at=NOW()
	`, workspaceID, entityType, entityID, userID, status, entityEvaluationRunning)
	return status, err
}

func (s *Store) validateEvaluationEntity(ctx context.Context, workspaceID int, entityType string, entityID int) error {
	table := "v2_tactical_workstreams"
	if entityType == EntityProject {
		table = "v2_tactical_projects"
	}
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM ` + table + ` WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL)`
	if err := s.dbx.QueryRowContext(ctx, query, workspaceID, entityID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ClaimDueEntityEvaluation(ctx context.Context) (TacticalEntityEvaluationJob, error) {
	var job TacticalEntityEvaluationJob
	var requestedBy sql.NullInt64
	err := s.dbx.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT id
			FROM v2_tactical_entity_evaluation_jobs
			WHERE status=$1 AND not_before <= NOW()
			ORDER BY not_before ASC, id ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE v2_tactical_entity_evaluation_jobs job
		SET status=$2, attempts=job.attempts + 1, running_revision=job.revision, updated_at=NOW()
		FROM candidate
		WHERE job.id=candidate.id
		RETURNING job.id, job.workspace_id, job.entity_type, job.entity_id,
			job.requested_by, job.attempts, job.running_revision
	`, entityEvaluationQueued, entityEvaluationRunning).Scan(
		&job.ID, &job.WorkspaceID, &job.EntityType, &job.EntityID,
		&requestedBy, &job.Attempts, &job.Revision,
	)
	if requestedBy.Valid {
		value := int(requestedBy.Int64)
		job.RequestedBy = &value
	}
	return job, err
}

func (s *Store) LatestEntityEvaluation(ctx context.Context, workspaceID int, entityType string, entityID int) (*TacticalEntityEvaluation, string, error) {
	status := entityEvaluationReady
	_ = s.dbx.QueryRowContext(ctx, `
		SELECT status
		FROM v2_tactical_entity_evaluation_jobs
		WHERE workspace_id=$1 AND entity_type=$2 AND entity_id=$3
	`, workspaceID, entityType, entityID).Scan(&status)

	var item TacticalEntityEvaluation
	var missingJSON []byte
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id, entity_type, entity_id, strategic_relevance, expected_impact,
			clarity, feasibility, measurability, confidence, priority_score,
			priority_tier, priority_reason, missing_information_json, context_fingerprint, created_at
		FROM v2_tactical_entity_evaluations
		WHERE workspace_id=$1 AND entity_type=$2 AND entity_id=$3
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, workspaceID, entityType, entityID).Scan(
		&item.ID, &item.EntityType, &item.EntityID, &item.StrategicRelevance,
		&item.ExpectedImpact, &item.Clarity, &item.Feasibility, &item.Measurability,
		&item.Confidence, &item.PriorityScore, &item.PriorityTier, &item.PriorityReason,
		&missingJSON, &item.ContextFingerprint, &item.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		if status == entityEvaluationReady {
			status = "not_evaluated"
		}
		return nil, status, nil
	}
	if err != nil {
		return nil, "", err
	}
	_ = json.Unmarshal(missingJSON, &item.MissingInformation)
	if item.MissingInformation == nil {
		item.MissingInformation = []string{}
	}
	return &item, status, nil
}

func (s *Store) SaveEntityEvaluation(
	ctx context.Context,
	job TacticalEntityEvaluationJob,
	model string,
	output tacticalEntityEvaluatorOutput,
	score int,
	tier string,
	fingerprint string,
	inputTokens int,
	outputTokens int,
	durationMS int64,
) error {
	missingJSON, _ := json.Marshal(output.MissingInformation)
	_, err := s.dbx.ExecContext(ctx, `
		INSERT INTO v2_tactical_entity_evaluations (
			workspace_id, entity_type, entity_id, model, prompt_version,
			strategic_relevance, expected_impact, clarity, feasibility, measurability,
			confidence, priority_score, priority_tier, priority_reason,
			missing_information_json, context_fingerprint, input_tokens, output_tokens, duration_ms
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15::jsonb,$16,$17,$18,$19)
	`, job.WorkspaceID, job.EntityType, job.EntityID, strings.TrimSpace(model),
		tacticalEntityEvaluatorPromptVersion, output.StrategicRelevance, output.ExpectedImpact,
		output.Clarity, output.Feasibility, output.Measurability, output.Confidence,
		score, tier, output.PriorityReason, missingJSON, fingerprint, inputTokens, outputTokens, durationMS)
	return err
}

func (s *Store) CompleteEntityEvaluationJob(ctx context.Context, job TacticalEntityEvaluationJob) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_tactical_entity_evaluation_jobs
		SET status=CASE WHEN revision > running_revision THEN $2 ELSE $3 END,
			not_before=CASE WHEN revision > running_revision THEN NOW() ELSE not_before END,
			error_text='', updated_at=NOW()
		WHERE id=$1 AND status=$4
	`, job.ID, entityEvaluationQueued, entityEvaluationReady, entityEvaluationRunning)
	return err
}

func (s *Store) FailEntityEvaluationJob(ctx context.Context, job TacticalEntityEvaluationJob, errorText string) error {
	status := entityEvaluationFailed
	notBefore := time.Now().UTC()
	if job.Attempts < 3 {
		status = entityEvaluationQueued
		notBefore = notBefore.Add(time.Duration(job.Attempts) * 20 * time.Second)
	}
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_tactical_entity_evaluation_jobs
		SET status=CASE WHEN revision > $5 THEN $6 ELSE $2 END,
			not_before=CASE WHEN revision > $5 THEN NOW() ELSE $3 END,
			error_text=$4, updated_at=NOW()
		WHERE id=$1
	`, job.ID, status, notBefore, truncateTacticalText(errorText, 2000), job.Revision, entityEvaluationQueued)
	return err
}

func (s *Store) RecoverStaleEntityEvaluations(ctx context.Context) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_tactical_entity_evaluation_jobs
		SET status=$1, not_before=NOW(), error_text='', updated_at=NOW()
		WHERE status=$2 AND updated_at < NOW() - INTERVAL '5 minutes'
	`, entityEvaluationQueued, entityEvaluationRunning)
	return err
}

func (s *Store) hydrateEntityEvaluations(ctx context.Context, workspaceID int, workstreams []Workstream) error {
	for index := range workstreams {
		evaluation, status, err := s.LatestEntityEvaluation(ctx, workspaceID, EntityWorkstream, workstreams[index].ID)
		if err != nil {
			return err
		}
		workstreams[index].Evaluation = evaluation
		workstreams[index].EvaluationStatus = status
		for projectIndex := range workstreams[index].Projects {
			evaluation, status, err := s.LatestEntityEvaluation(ctx, workspaceID, EntityProject, workstreams[index].Projects[projectIndex].ID)
			if err != nil {
				return err
			}
			workstreams[index].Projects[projectIndex].Evaluation = evaluation
			workstreams[index].Projects[projectIndex].EvaluationStatus = status
		}
	}
	return nil
}
