package tasks

import (
	"context"
	"encoding/json"

	"github.com/lib/pq"
)

func (s *Store) taskSecondaryWorkstreams(ctx context.Context, workspaceID int, taskIDs []int) (map[int][]int, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT task_id, workstream_id
		FROM v2_task_secondary_workstreams
		WHERE workspace_id=$1 AND task_id=ANY($2)
		ORDER BY task_id, workstream_id
	`, workspaceID, pq.Array(taskIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := map[int][]int{}
	for rows.Next() {
		var taskID int
		var workstreamID int
		if err := rows.Scan(&taskID, &workstreamID); err != nil {
			return nil, err
		}
		items[taskID] = append(items[taskID], workstreamID)
	}
	return items, rows.Err()
}

func (s *Store) taskDependencies(ctx context.Context, workspaceID int, taskIDs []int) (map[int][]BlockingTask, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT dependency.task_id, blocker.id, blocker.title, blocker.status
		FROM v2_task_dependencies dependency
		JOIN v2_tasks blocker
			ON blocker.id=dependency.blocker_task_id AND blocker.workspace_id=dependency.workspace_id
		WHERE dependency.workspace_id=$1 AND dependency.task_id=ANY($2)
		ORDER BY dependency.task_id, blocker.title, blocker.id
	`, workspaceID, pq.Array(taskIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := map[int][]BlockingTask{}
	for rows.Next() {
		var taskID int
		var blocker BlockingTask
		if err := rows.Scan(&taskID, &blocker.ID, &blocker.Title, &blocker.Status); err != nil {
			return nil, err
		}
		items[taskID] = append(items[taskID], blocker)
	}
	return items, rows.Err()
}

func (s *Store) taskCompletionFiles(ctx context.Context, workspaceID int, taskIDs []int) (map[int][]TaskCompletionFile, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT link.task_id, file.id, file.filename, file.content_type, file.size_bytes, file.status, link.created_at
		FROM v2_task_completion_files link
		JOIN strategic_openai_files file
			ON file.id=link.strategic_file_id AND file.workspace_id=link.workspace_id
		WHERE link.workspace_id=$1 AND link.task_id=ANY($2)
		ORDER BY link.task_id, link.created_at, file.id
	`, workspaceID, pq.Array(taskIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := map[int][]TaskCompletionFile{}
	for rows.Next() {
		var taskID int
		var item TaskCompletionFile
		if err := rows.Scan(&taskID, &item.ID, &item.Filename, &item.ContentType, &item.SizeBytes, &item.Status, &item.CreatedAt); err != nil {
			return nil, err
		}
		items[taskID] = append(items[taskID], item)
	}
	return items, rows.Err()
}

func (s *Store) taskCompletionEvaluations(
	ctx context.Context,
	workspaceID int,
	taskIDs []int,
) (map[int]TaskCompletionEvaluation, map[int]string, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT DISTINCT ON (task_id)
			task_id, id, status, sufficient, quality_score, reason, missing_information_json, created_at
		FROM v2_task_completion_evaluations
		WHERE workspace_id=$1 AND task_id=ANY($2)
		ORDER BY task_id, created_at DESC, id DESC
	`, workspaceID, pq.Array(taskIDs))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	items := map[int]TaskCompletionEvaluation{}
	statuses := map[int]string{}
	for rows.Next() {
		var taskID int
		var status string
		var missingRaw json.RawMessage
		var item TaskCompletionEvaluation
		if err := rows.Scan(&taskID, &item.ID, &status, &item.Sufficient, &item.QualityScore, &item.Reason, &missingRaw, &item.CreatedAt); err != nil {
			return nil, nil, err
		}
		statuses[taskID] = status
		if status == EvaluationReady {
			item.MissingInformation = decodeStringList(missingRaw)
			items[taskID] = item
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return items, statuses, nil
}

func (s *Store) taskEvaluations(ctx context.Context, workspaceID int, taskIDs []int) (map[int]TaskEvaluation, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT DISTINCT ON (task_id)
			id, task_id, strategic_relevance, course_alignment, tactical_alignment,
			expected_impact, urgency, effort, confidence, priority_score, priority_tier,
			recommendation, priority_reason, clarification_question,
			missing_information_json, flags_json, backlog_category, created_at
		FROM v2_task_evaluations
		WHERE workspace_id=$1 AND task_id=ANY($2)
		ORDER BY task_id, created_at DESC, id DESC
	`, workspaceID, pq.Array(taskIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := map[int]TaskEvaluation{}
	for rows.Next() {
		var item TaskEvaluation
		var missingRaw json.RawMessage
		var flagsRaw json.RawMessage
		if err := rows.Scan(
			&item.ID, &item.TaskID, &item.StrategicRelevance, &item.CourseAlignment,
			&item.TacticalAlignment, &item.ExpectedImpact, &item.Urgency, &item.Effort,
			&item.Confidence, &item.PriorityScore, &item.PriorityTier, &item.Recommendation,
			&item.PriorityReason, &item.ClarificationQuestion, &missingRaw, &flagsRaw, &item.BacklogCategory, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(missingRaw, &item.MissingInformation)
		_ = json.Unmarshal(flagsRaw, &item.Flags)
		items[item.TaskID] = item
	}
	return items, rows.Err()
}

func (s *Store) taskEvaluationJobStatuses(ctx context.Context, workspaceID int, taskIDs []int) (map[int]string, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT task_id, status
		FROM v2_task_evaluation_jobs
		WHERE workspace_id=$1 AND task_id=ANY($2)
	`, workspaceID, pq.Array(taskIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := map[int]string{}
	for rows.Next() {
		var taskID int
		var status string
		if err := rows.Scan(&taskID, &status); err != nil {
			return nil, err
		}
		items[taskID] = status
	}
	return items, rows.Err()
}
