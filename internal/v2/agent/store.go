package agent

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

type Store struct {
	dbx *sql.DB
}

func NewStore(dbx *sql.DB) *Store {
	return &Store{dbx: dbx}
}

func (s *Store) Create(
	ctx context.Context,
	workspaceID int,
	userID int,
	threadID int,
	userMessageID int,
	scope Scope,
	model string,
	input string,
) (Run, error) {
	publicID, err := randomPublicID()
	if err != nil {
		return Run{}, err
	}
	scope.Type = strings.TrimSpace(scope.Type)
	if scope.Type == "" {
		scope.Type = "workspace"
	}
	if scope.ID < 0 {
		scope.ID = 0
	}
	scope.Label = truncate(scope.Label, 160)
	row := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_agent_runs (
			public_id, workspace_id, user_id, thread_id, user_message_id,
			scope_type, scope_id, scope_label, status, model, prompt_version, input_text
		)
		VALUES ($1, $2, $3, $4, NULLIF($5, 0), $6, $7, $8, $9, $10, $11, $12)
		RETURNING
			id, public_id, workspace_id, user_id, thread_id,
			COALESCE(user_message_id, 0), COALESCE(assistant_message_id, 0),
			scope_type, scope_id, scope_label, status, model, prompt_version,
			input_text, output_text, partial_output, previous_response_id,
			conversation_id, vector_store_id, state_ciphertext, error_text,
			reservation_id, usage_requests, usage_input_tokens, usage_output_tokens,
			usage_total_tokens, started_at, completed_at, created_at, updated_at
	`, publicID, workspaceID, userID, threadID, userMessageID, scope.Type, scope.ID,
		scope.Label, StatusQueued, model, PromptVersion, strings.TrimSpace(input))
	return scanRun(row)
}

func (s *Store) ByPublicID(ctx context.Context, publicID string) (Run, error) {
	return scanRun(s.dbx.QueryRowContext(ctx, runSelect+` WHERE public_id=$1`, publicID))
}

func (s *Store) ByPublicIDForUser(ctx context.Context, publicID string, workspaceID int, userID int) (Run, error) {
	return scanRun(s.dbx.QueryRowContext(ctx, runSelect+`
		WHERE public_id=$1 AND workspace_id=$2 AND user_id=$3
	`, publicID, workspaceID, userID))
}

func (s *Store) ActiveForThread(ctx context.Context, workspaceID int, userID int, threadID int) (Run, error) {
	return scanRun(s.dbx.QueryRowContext(ctx, runSelect+`
		WHERE workspace_id=$1 AND user_id=$2 AND thread_id=$3
			AND status IN ($4, $5, $6)
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, workspaceID, userID, threadID, StatusQueued, StatusRunning, StatusWaitingApproval))
}

func (s *Store) SetRunning(ctx context.Context, runID int64, reservationID string) error {
	result, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_agent_runs
		SET status=$2, reservation_id=$3, error_text='', started_at=COALESCE(started_at, NOW()), updated_at=NOW()
		WHERE id=$1 AND status IN ($4, $5)
	`, runID, StatusRunning, reservationID, StatusQueued, StatusFailed)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("agent_run_not_claimable")
	}
	return nil
}

func (s *Store) SetWaiting(
	ctx context.Context,
	runID int64,
	partialOutput string,
	previousResponseID string,
	stateCiphertext string,
	usage RuntimeUsage,
) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_agent_runs
		SET status=$2, partial_output=$3, previous_response_id=$4,
			state_ciphertext=$5, usage_requests=usage_requests+$6,
			usage_input_tokens=usage_input_tokens+$7,
			usage_output_tokens=usage_output_tokens+$8,
			usage_total_tokens=usage_total_tokens+$9,
			reservation_id='', error_text='', updated_at=NOW()
		WHERE id=$1
	`, runID, StatusWaitingApproval, truncate(partialOutput, 120000), previousResponseID,
		stateCiphertext, usage.Requests, usage.InputTokens, usage.OutputTokens, usage.TotalTokens)
	return err
}

func (s *Store) SetCompleted(
	ctx context.Context,
	runID int64,
	output string,
	partialOutput string,
	previousResponseID string,
	assistantMessageID int,
	usage RuntimeUsage,
) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_agent_runs
		SET status=$2, output_text=$3, partial_output=$4, previous_response_id=$5,
			assistant_message_id=NULLIF($6, 0), state_ciphertext='', reservation_id='',
			usage_requests=usage_requests+$7, usage_input_tokens=usage_input_tokens+$8,
			usage_output_tokens=usage_output_tokens+$9, usage_total_tokens=usage_total_tokens+$10,
			error_text='', completed_at=NOW(), updated_at=NOW()
		WHERE id=$1
	`, runID, StatusCompleted, truncate(output, 120000), truncate(partialOutput, 120000),
		previousResponseID, assistantMessageID, usage.Requests, usage.InputTokens,
		usage.OutputTokens, usage.TotalTokens)
	return err
}

func (s *Store) SetFailed(ctx context.Context, runID int64, errorText string, terminal bool) error {
	status := StatusQueued
	if terminal {
		status = StatusFailed
	}
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_agent_runs
		SET status=$2, error_text=$3, reservation_id='',
			completed_at=CASE WHEN $2=$4 THEN NOW() ELSE completed_at END,
			updated_at=NOW()
		WHERE id=$1
	`, runID, status, truncate(errorText, 4000), StatusFailed)
	return err
}

func (s *Store) QueueResume(ctx context.Context, runID int64) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_agent_runs
		SET status=$2, error_text='', completed_at=NULL, updated_at=NOW()
		WHERE id=$1 AND status=$3
	`, runID, StatusQueued, StatusWaitingApproval)
	return err
}

func (s *Store) SetVectorStore(ctx context.Context, runID int64, vectorStoreID string) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_agent_runs SET vector_store_id=$2, updated_at=NOW() WHERE id=$1
	`, runID, strings.TrimSpace(vectorStoreID))
	return err
}

func (s *Store) LatestThreadSession(ctx context.Context, workspaceID int, userID int, threadID int) (string, string, error) {
	var previousResponseID, conversationID string
	err := s.dbx.QueryRowContext(ctx, `
		SELECT previous_response_id, conversation_id
		FROM v2_agent_runs
		WHERE workspace_id=$1 AND user_id=$2 AND thread_id=$3
			AND status IN ($4, $5) AND previous_response_id <> ''
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, workspaceID, userID, threadID, StatusCompleted, StatusWaitingApproval).
		Scan(&previousResponseID, &conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return previousResponseID, conversationID, err
}

func (s *Store) InsertEvent(ctx context.Context, runID int64, event RuntimeEvent) error {
	event.Type = truncate(strings.TrimSpace(event.Type), 80)
	event.Stage = truncate(strings.TrimSpace(event.Stage), 80)
	event.Title = truncate(strings.TrimSpace(event.Title), 300)
	if event.Type == "" || event.Title == "" {
		return errors.New("invalid_agent_event")
	}
	_, err := s.dbx.ExecContext(ctx, `
		INSERT INTO v2_agent_run_events (
			run_id, event_type, stage, title, detail, tool_name, tool_call_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (run_id, event_type, tool_call_id)
			WHERE tool_call_id <> ''
		DO NOTHING
	`, runID, event.Type, event.Stage, event.Title, truncate(event.Detail, 1000),
		truncate(event.ToolName, 120), truncate(event.ToolCallID, 240))
	return err
}

func (s *Store) EventCount(ctx context.Context, runID int64) (int, error) {
	var count int
	err := s.dbx.QueryRowContext(ctx, `SELECT COUNT(*) FROM v2_agent_run_events WHERE run_id=$1`, runID).Scan(&count)
	return count, err
}

func (s *Store) Events(ctx context.Context, runID int64, afterID int64) ([]Event, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, event_type, stage, title, detail, tool_name, tool_call_id, payload_json, created_at
		FROM v2_agent_run_events
		WHERE run_id=$1 AND id>$2
		ORDER BY id ASC
		LIMIT 300
	`, runID, afterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Event{}
	for rows.Next() {
		var item Event
		if err := rows.Scan(&item.ID, &item.Type, &item.Stage, &item.Title, &item.Detail,
			&item.ToolName, &item.ToolCallID, &item.Payload, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) InsertApprovals(ctx context.Context, runID int64, interruptions []RuntimeInterruption) error {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for index, item := range interruptions {
		raw, err := json.Marshal(item.Arguments)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO v2_agent_run_approvals (
				run_id, call_id, tool_name, arguments_json, status, action_index
			)
			VALUES ($1, $2, $3, $4, 'pending', $5)
			ON CONFLICT (run_id, call_id) DO NOTHING
		`, runID, item.CallID, item.ToolName, raw, index); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Approvals(ctx context.Context, runID int64) ([]Approval, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, call_id, tool_name, arguments_json, status, action_index,
			result_json, error_text, decided_at, applied_at
		FROM v2_agent_run_approvals
		WHERE run_id=$1
		ORDER BY action_index ASC, id ASC
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Approval{}
	for rows.Next() {
		var item Approval
		if err := rows.Scan(&item.ID, &item.CallID, &item.ToolName, &item.Arguments,
			&item.Status, &item.ActionIndex, &item.Result, &item.ErrorText,
			&item.DecidedAt, &item.AppliedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) Decide(ctx context.Context, runID int64, userID int, decisions []Decision) error {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, decision := range decisions {
		status := "rejected"
		if decision.Approved {
			status = "approved"
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE v2_agent_run_approvals
			SET status=$1, decided_by=$2, decided_at=NOW(), updated_at=NOW()
			WHERE run_id=$3 AND call_id=$4 AND status='pending'
		`, status, userID, runID, decision.CallID)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return errors.New("agent_approval_not_pending")
		}
	}
	var remaining int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM v2_agent_run_approvals WHERE run_id=$1 AND status='pending'
	`, runID).Scan(&remaining); err != nil {
		return err
	}
	if remaining != 0 {
		return errors.New("agent_approval_decision_incomplete")
	}
	return tx.Commit()
}

func (s *Store) SetApprovalResult(ctx context.Context, runID int64, actionIndex int, result any, applyErr error) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	status := "applied"
	errorText := ""
	if applyErr != nil {
		status = "failed"
		errorText = applyErr.Error()
	}
	_, err = s.dbx.ExecContext(ctx, `
		UPDATE v2_agent_run_approvals
		SET status=$1, result_json=$2, error_text=$3,
			applied_at=CASE WHEN $1='applied' THEN NOW() ELSE applied_at END,
			updated_at=NOW()
		WHERE run_id=$4 AND action_index=$5 AND status='approved'
	`, status, raw, truncate(errorText, 2000), runID, actionIndex)
	return err
}

const runSelect = `
	SELECT
		id, public_id, workspace_id, user_id, thread_id,
		COALESCE(user_message_id, 0), COALESCE(assistant_message_id, 0),
		scope_type, scope_id, scope_label, status, model, prompt_version,
		input_text, output_text, partial_output, previous_response_id,
		conversation_id, vector_store_id, state_ciphertext, error_text,
		reservation_id, usage_requests, usage_input_tokens, usage_output_tokens,
		usage_total_tokens, started_at, completed_at, created_at, updated_at
	FROM v2_agent_runs
`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRun(row rowScanner) (Run, error) {
	var item Run
	err := row.Scan(
		&item.ID, &item.PublicID, &item.WorkspaceID, &item.UserID, &item.ThreadID,
		&item.UserMessageID, &item.AssistantMessageID, &item.Scope.Type, &item.Scope.ID,
		&item.Scope.Label, &item.Status, &item.Model, &item.PromptVersion, &item.InputText,
		&item.OutputText, &item.PartialOutput, &item.PreviousResponseID, &item.ConversationID,
		&item.VectorStoreID, &item.StateCiphertext, &item.ErrorText, &item.ReservationID,
		&item.UsageRequests, &item.UsageInputTokens, &item.UsageOutputTokens,
		&item.UsageTotalTokens, &item.StartedAt, &item.CompletedAt, &item.CreatedAt, &item.UpdatedAt,
	)
	return item, err
}

func randomPublicID() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "run_" + hex.EncodeToString(raw), nil
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
