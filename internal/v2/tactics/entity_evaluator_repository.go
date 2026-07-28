package tactics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
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
	rows, err := s.dbx.QueryContext(ctx, `
		WITH relevant AS (
			SELECT $2::TEXT AS entity_type, id AS entity_id
			FROM v2_tactical_workstreams
			WHERE workspace_id=$1 AND archived_at IS NULL
			UNION ALL
			SELECT $3::TEXT AS entity_type, id AS entity_id
			FROM v2_tactical_projects
			WHERE workspace_id=$1 AND archived_at IS NULL
		),
		latest AS (
			SELECT DISTINCT ON (entity_type, entity_id)
				id, entity_type, entity_id, strategic_relevance, expected_impact,
				clarity, feasibility, measurability, confidence, priority_score,
				priority_tier, priority_reason, missing_information_json,
				context_fingerprint, created_at
			FROM v2_tactical_entity_evaluations
			WHERE workspace_id=$1 AND entity_type IN ($2, $3)
			ORDER BY entity_type, entity_id, created_at DESC, id DESC
		)
		SELECT relevant.entity_type,
			relevant.entity_id,
			COALESCE(
				jobs.status,
				CASE WHEN latest.id IS NULL THEN 'not_evaluated' ELSE $4 END
			),
			latest.id,
			latest.strategic_relevance,
			latest.expected_impact,
			latest.clarity,
			latest.feasibility,
			latest.measurability,
			latest.confidence,
			latest.priority_score,
			latest.priority_tier,
			latest.priority_reason,
			latest.missing_information_json,
			latest.context_fingerprint,
			latest.created_at
		FROM relevant
		LEFT JOIN v2_tactical_entity_evaluation_jobs jobs
			ON jobs.workspace_id=$1
			AND jobs.entity_type=relevant.entity_type
			AND jobs.entity_id=relevant.entity_id
		LEFT JOIN latest
			ON latest.entity_type=relevant.entity_type
			AND latest.entity_id=relevant.entity_id
	`, workspaceID, EntityWorkstream, EntityProject, entityEvaluationReady)
	if err != nil {
		return err
	}
	defer rows.Close()

	states := make(map[string]entityEvaluationState)
	for rows.Next() {
		var entityType string
		var entityID int
		var state entityEvaluationState
		var evaluationID sql.NullInt64
		var strategicRelevance sql.NullInt64
		var expectedImpact sql.NullInt64
		var clarity sql.NullInt64
		var feasibility sql.NullInt64
		var measurability sql.NullInt64
		var confidence sql.NullInt64
		var priorityScore sql.NullInt64
		var priorityTier sql.NullString
		var priorityReason sql.NullString
		var missingInformationJSON sql.NullString
		var contextFingerprint sql.NullString
		var createdAt sql.NullTime
		if err := rows.Scan(
			&entityType,
			&entityID,
			&state.Status,
			&evaluationID,
			&strategicRelevance,
			&expectedImpact,
			&clarity,
			&feasibility,
			&measurability,
			&confidence,
			&priorityScore,
			&priorityTier,
			&priorityReason,
			&missingInformationJSON,
			&contextFingerprint,
			&createdAt,
		); err != nil {
			return err
		}
		if evaluationID.Valid {
			state.Evaluation = &TacticalEntityEvaluation{
				ID:                 evaluationID.Int64,
				EntityType:         entityType,
				EntityID:           entityID,
				StrategicRelevance: int(strategicRelevance.Int64),
				ExpectedImpact:     int(expectedImpact.Int64),
				Clarity:            int(clarity.Int64),
				Feasibility:        int(feasibility.Int64),
				Measurability:      int(measurability.Int64),
				Confidence:         int(confidence.Int64),
				PriorityScore:      int(priorityScore.Int64),
				PriorityTier:       priorityTier.String,
				PriorityReason:     priorityReason.String,
				MissingInformation: []string{},
				ContextFingerprint: contextFingerprint.String,
				CreatedAt:          createdAt.Time,
			}
			if missingInformationJSON.Valid {
				_ = json.Unmarshal([]byte(missingInformationJSON.String), &state.Evaluation.MissingInformation)
			}
			if state.Evaluation.MissingInformation == nil {
				state.Evaluation.MissingInformation = []string{}
			}
		}
		states[entityEvaluationKey(entityType, entityID)] = state
	}
	if err := rows.Err(); err != nil {
		return err
	}
	assignEntityEvaluations(workstreams, states)
	return nil
}

type entityEvaluationState struct {
	Status     string
	Evaluation *TacticalEntityEvaluation
}

func assignEntityEvaluations(workstreams []Workstream, states map[string]entityEvaluationState) {
	for index := range workstreams {
		workstream := &workstreams[index]
		state, found := states[entityEvaluationKey(EntityWorkstream, workstream.ID)]
		if !found {
			state.Status = "not_evaluated"
		}
		workstream.Evaluation = state.Evaluation
		workstream.EvaluationStatus = state.Status
		for projectIndex := range workstream.Projects {
			project := &workstream.Projects[projectIndex]
			state, found := states[entityEvaluationKey(EntityProject, project.ID)]
			if !found {
				state.Status = "not_evaluated"
			}
			project.Evaluation = state.Evaluation
			project.EvaluationStatus = state.Status
		}
	}
}

func entityEvaluationKey(entityType string, entityID int) string {
	return entityType + ":" + strconv.Itoa(entityID)
}
