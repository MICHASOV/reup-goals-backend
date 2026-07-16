package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func (s *Store) CreateBrainstormMessage(
	ctx context.Context,
	workspaceID int,
	workstreamID int,
	userID *int,
	role string,
	content string,
	actions []BrainstormAction,
	metadata any,
) (BrainstormMessage, error) {
	role = strings.TrimSpace(role)
	if role != "assistant" && role != "user" {
		role = "user"
	}
	if actions == nil {
		actions = []BrainstormAction{}
	}
	var item BrainstormMessage
	var actionsRaw json.RawMessage
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_task_brainstorm_messages (
			workspace_id, workstream_id, user_id, role, content, actions_json, metadata_json
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, role, content, actions_json, created_at
	`, workspaceID, workstreamID, nullableInt(userID), role, strings.TrimSpace(content), taskJSON(actions), taskJSON(metadata)).Scan(
		&item.ID, &item.Role, &item.Content, &actionsRaw, &item.CreatedAt,
	)
	if err != nil {
		return BrainstormMessage{}, err
	}
	_ = json.Unmarshal(actionsRaw, &item.Actions)
	if item.Actions == nil {
		item.Actions = []BrainstormAction{}
	}
	item.Applied = []int{}
	return item, nil
}

func (s *Store) BrainstormMessages(ctx context.Context, workspaceID int, workstreamID int, limit int) ([]BrainstormMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 300
	}
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, role, content, actions_json, created_at
		FROM v2_task_brainstorm_messages
		WHERE workspace_id=$1 AND workstream_id=$2
		ORDER BY created_at DESC, id DESC
		LIMIT $3
	`, workspaceID, workstreamID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []BrainstormMessage{}
	for rows.Next() {
		var item BrainstormMessage
		var actionsRaw json.RawMessage
		if err := rows.Scan(&item.ID, &item.Role, &item.Content, &actionsRaw, &item.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(actionsRaw, &item.Actions)
		if item.Actions == nil {
			item.Actions = []BrainstormAction{}
		}
		item.Applied = []int{}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}

	applied, err := s.brainstormAppliedIndices(ctx, workspaceID, workstreamID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Applied = applied[items[i].ID]
		if items[i].Applied == nil {
			items[i].Applied = []int{}
		}
	}
	return items, nil
}

func (s *Store) BrainstormAssistantMessage(ctx context.Context, workspaceID int, workstreamID int, messageID int) (BrainstormMessage, error) {
	var item BrainstormMessage
	var actionsRaw json.RawMessage
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id, role, content, actions_json, created_at
		FROM v2_task_brainstorm_messages
		WHERE id=$1 AND workspace_id=$2 AND workstream_id=$3 AND role='assistant'
	`, messageID, workspaceID, workstreamID).Scan(
		&item.ID, &item.Role, &item.Content, &actionsRaw, &item.CreatedAt,
	)
	if err != nil {
		return BrainstormMessage{}, err
	}
	_ = json.Unmarshal(actionsRaw, &item.Actions)
	if item.Actions == nil {
		item.Actions = []BrainstormAction{}
	}
	applied, err := s.brainstormAppliedIndices(ctx, workspaceID, workstreamID)
	if err != nil {
		return BrainstormMessage{}, err
	}
	item.Applied = applied[item.ID]
	if item.Applied == nil {
		item.Applied = []int{}
	}
	return item, nil
}

func (s *Store) brainstormAppliedIndices(ctx context.Context, workspaceID int, workstreamID int) (map[int][]int, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT message_id, action_index
		FROM v2_task_brainstorm_action_applications
		WHERE workspace_id=$1 AND workstream_id=$2 AND status='applied'
		ORDER BY action_index ASC
	`, workspaceID, workstreamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int][]int{}
	for rows.Next() {
		var messageID int
		var actionIndex int
		if err := rows.Scan(&messageID, &actionIndex); err != nil {
			return nil, err
		}
		result[messageID] = append(result[messageID], actionIndex)
	}
	return result, rows.Err()
}

func (s *Store) ClaimBrainstormActionApplication(
	ctx context.Context,
	workspaceID int,
	workstreamID int,
	messageID int,
	actionIndex int,
	actionType string,
	userID int,
) (bool, error) {
	result, err := s.dbx.ExecContext(ctx, `
		INSERT INTO v2_task_brainstorm_action_applications (
			workspace_id, workstream_id, message_id, action_index, action_type, created_by, status
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'applying')
		ON CONFLICT (message_id, action_index) DO UPDATE SET
			status='applying',
			error_text='',
			updated_at=NOW()
		WHERE v2_task_brainstorm_action_applications.status='failed'
			OR (
				v2_task_brainstorm_action_applications.status='applying'
				AND v2_task_brainstorm_action_applications.updated_at < NOW() - INTERVAL '5 minutes'
			)
	`, workspaceID, workstreamID, messageID, actionIndex, strings.TrimSpace(actionType), userID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

func (s *Store) CompleteBrainstormActionApplication(
	ctx context.Context,
	workspaceID int,
	messageID int,
	actionIndex int,
	taskID int,
) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_task_brainstorm_action_applications
		SET status='applied', task_id=$4, error_text='', updated_at=NOW()
		WHERE workspace_id=$1 AND message_id=$2 AND action_index=$3 AND status='applying'
	`, workspaceID, messageID, actionIndex, taskID)
	return err
}

func (s *Store) FailBrainstormActionApplication(
	ctx context.Context,
	workspaceID int,
	messageID int,
	actionIndex int,
	errorText string,
) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_task_brainstorm_action_applications
		SET status='failed', error_text=$4, updated_at=NOW()
		WHERE workspace_id=$1 AND message_id=$2 AND action_index=$3 AND status='applying'
	`, workspaceID, messageID, actionIndex, truncateRunes(errorText, 2000))
	return err
}

func (s *Store) BrainstormSession(ctx context.Context, workspaceID int, workstreamID int, compactThreshold int, fingerprint string) (BrainstormSession, error) {
	if compactThreshold <= 0 {
		compactThreshold = 120000
	}
	promptCacheKey := fmt.Sprintf("reupgoals-task-brainstorm-workspace-%d-workstream-%d-v1", workspaceID, workstreamID)
	var item BrainstormSession
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_task_brainstorm_sessions (
			workspace_id, workstream_id, compact_threshold, prompt_cache_key, context_fingerprint
		)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (workspace_id, workstream_id) DO UPDATE SET
			previous_response_id=CASE
				WHEN v2_task_brainstorm_sessions.context_fingerprint <> EXCLUDED.context_fingerprint THEN ''
				ELSE v2_task_brainstorm_sessions.previous_response_id
			END,
			compact_threshold=EXCLUDED.compact_threshold,
			prompt_cache_key=EXCLUDED.prompt_cache_key,
			context_fingerprint=EXCLUDED.context_fingerprint,
			updated_at=NOW()
		RETURNING previous_response_id, compact_threshold, prompt_cache_key, context_fingerprint
	`, workspaceID, workstreamID, compactThreshold, promptCacheKey, fingerprint).Scan(
		&item.PreviousResponseID, &item.CompactThreshold, &item.PromptCacheKey, &item.ContextFingerprint,
	)
	return item, err
}

func (s *Store) UpdateBrainstormPreviousResponseID(ctx context.Context, workspaceID int, workstreamID int, responseID string) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_task_brainstorm_sessions
		SET previous_response_id=$3, updated_at=NOW()
		WHERE workspace_id=$1 AND workstream_id=$2
	`, workspaceID, workstreamID, strings.TrimSpace(responseID))
	return err
}

func taskJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte(`{}`)
	}
	return raw
}
