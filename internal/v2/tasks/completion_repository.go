package tasks

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/lib/pq"
)

func (s *Store) ReplaceCompletionFiles(ctx context.Context, workspaceID int, taskID int, fileIDs []int) error {
	if len(fileIDs) > 10 {
		return ErrInvalidCompletionFile
	}
	seen := map[int]bool{}
	cleaned := make([]int, 0, len(fileIDs))
	for _, fileID := range fileIDs {
		if fileID <= 0 || seen[fileID] {
			return ErrInvalidCompletionFile
		}
		seen[fileID] = true
		cleaned = append(cleaned, fileID)
	}
	if len(cleaned) > 0 {
		var count int
		if err := s.dbx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM strategic_openai_files
			WHERE workspace_id=$1 AND id=ANY($2) AND status IN ('completed', 'ready')
		`, workspaceID, pq.Array(cleaned)).Scan(&count); err != nil {
			return err
		}
		if count != len(cleaned) {
			return ErrInvalidCompletionFile
		}
	}

	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM v2_task_completion_files WHERE workspace_id=$1 AND task_id=$2
	`, workspaceID, taskID); err != nil {
		return err
	}
	for _, fileID := range cleaned {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO v2_task_completion_files (workspace_id, task_id, strategic_file_id)
			VALUES ($1, $2, $3)
		`, workspaceID, taskID, fileID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SaveCompletionEvaluation(ctx context.Context, workspaceID int, taskID int, model string, output taskCompletionModelOutput, inputTokens int, outputTokens int, durationMS int64) error {
	_, err := s.dbx.ExecContext(ctx, `
		INSERT INTO v2_task_completion_evaluations (
			workspace_id, task_id, model, prompt_version, status, sufficient,
			quality_score, reason, missing_information_json, input_tokens, output_tokens, duration_ms
		)
		VALUES ($1, $2, $3, $4, 'ready', $5, $6, $7, $8, $9, $10, $11)
	`, workspaceID, taskID, strings.TrimSpace(model), taskCompletionPromptVersion, output.Sufficient,
		output.QualityScore, output.Reason, taskJSON(output.MissingInformation), inputTokens, outputTokens, durationMS)
	return err
}

func (s *Store) SaveCompletionEvaluationFailure(ctx context.Context, workspaceID int, taskID int, model string, errorText string, durationMS int64) error {
	_, err := s.dbx.ExecContext(ctx, `
		INSERT INTO v2_task_completion_evaluations (
			workspace_id, task_id, model, prompt_version, status, sufficient,
			quality_score, reason, missing_information_json, duration_ms, error_text
		)
		VALUES ($1, $2, $3, $4, 'failed', false, 0, '', '[]'::jsonb, $5, $6)
	`, workspaceID, taskID, strings.TrimSpace(model), taskCompletionPromptVersion, durationMS, truncateRunes(errorText, 2000))
	return err
}

func decodeStringList(raw json.RawMessage) []string {
	items := []string{}
	_ = json.Unmarshal(raw, &items)
	return items
}
