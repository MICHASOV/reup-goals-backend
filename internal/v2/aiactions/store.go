package aiactions

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

type Store struct {
	dbx *sql.DB
}

func NewStore(dbx *sql.DB) *Store {
	return &Store{dbx: dbx}
}

func (s *Store) Register(
	ctx context.Context,
	workspaceID int,
	scenario string,
	scopeType string,
	scopeID int,
	messageID int,
	proposedBy *int,
	proposals []Proposal,
) ([]Action, error) {
	if workspaceID <= 0 || scopeID <= 0 || messageID <= 0 || strings.TrimSpace(scenario) == "" || strings.TrimSpace(scopeType) == "" {
		return nil, fmt.Errorf("invalid_ai_action_scope")
	}
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE v2_ai_actions
		SET status=$1, expires_at=NOW(), updated_at=NOW()
		WHERE workspace_id=$2 AND scenario=$3 AND scope_type=$4 AND scope_id=$5
			AND status IN ($6, $7) AND message_id <> $8
	`, StatusExpired, workspaceID, scenario, scopeType, scopeID, StatusProposed, StatusEdited, messageID); err != nil {
		return nil, err
	}

	items := make([]Action, 0, len(proposals))
	for index, proposal := range proposals {
		actionType := strings.TrimSpace(proposal.ActionType)
		if actionType == "" {
			continue
		}
		payload, err := json.Marshal(proposal.Payload)
		if err != nil {
			return nil, err
		}
		row := tx.QueryRowContext(ctx, `
			INSERT INTO v2_ai_actions (
				workspace_id, scenario, scope_type, scope_id, message_id, action_index,
				action_type, payload_json, status, proposed_by
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (scenario, message_id, action_index) DO UPDATE SET
				action_type=EXCLUDED.action_type,
				payload_json=EXCLUDED.payload_json,
				updated_at=NOW()
			WHERE v2_ai_actions.status IN ('proposed', 'edited')
			RETURNING id, workspace_id, scenario, scope_type, scope_id, message_id,
				action_index, action_type, payload_json, status, entity_type, entity_id,
				proposed_by, confirmed_by, edited_by, rejected_by, error_text,
				expires_at, confirmed_at, applied_at, rejected_at, created_at, updated_at
		`, workspaceID, scenario, scopeType, scopeID, messageID, index, actionType, payload, StatusProposed, proposedBy)
		item, err := scanAction(row)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) Confirm(ctx context.Context, workspaceID int, scenario string, messageID int, actionIndex int, userID int) (Action, bool, error) {
	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_ai_actions
		SET status=$1, confirmed_by=$2, confirmed_at=NOW(), error_text='', updated_at=NOW()
		WHERE workspace_id=$3 AND scenario=$4 AND message_id=$5 AND action_index=$6
			AND status IN ($7, $8, $9, $10)
		RETURNING id, workspace_id, scenario, scope_type, scope_id, message_id,
			action_index, action_type, payload_json, status, entity_type, entity_id,
			proposed_by, confirmed_by, edited_by, rejected_by, error_text,
			expires_at, confirmed_at, applied_at, rejected_at, created_at, updated_at
	`, StatusConfirmed, userID, workspaceID, scenario, messageID, actionIndex, StatusProposed, StatusEdited, StatusFailed, StatusConfirmed)
	item, err := scanAction(row)
	if err == nil {
		return item, true, nil
	}
	if err != sql.ErrNoRows {
		return Action{}, false, err
	}
	item, err = s.ByReference(ctx, workspaceID, scenario, messageID, actionIndex)
	if err != nil {
		return Action{}, false, err
	}
	return item, false, nil
}

func (s *Store) MarkApplied(ctx context.Context, workspaceID int, scenario string, messageID int, actionIndex int, entityType string, entityID int) error {
	result, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_ai_actions
		SET status=$1, entity_type=$2, entity_id=$3, applied_at=NOW(), error_text='', updated_at=NOW()
		WHERE workspace_id=$4 AND scenario=$5 AND message_id=$6 AND action_index=$7 AND status=$8
	`, StatusApplied, strings.TrimSpace(entityType), entityID, workspaceID, scenario, messageID, actionIndex, StatusConfirmed)
	if err != nil {
		return err
	}
	return requireOne(result, "ai_action_not_confirmed")
}

func (s *Store) MarkFailed(ctx context.Context, workspaceID int, scenario string, messageID int, actionIndex int, errorText string) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_ai_actions
		SET status=$1, error_text=$2, updated_at=NOW()
		WHERE workspace_id=$3 AND scenario=$4 AND message_id=$5 AND action_index=$6
			AND status=$7
	`, StatusFailed, truncate(errorText, 2000), workspaceID, scenario, messageID, actionIndex, StatusConfirmed)
	return err
}

func (s *Store) ByReference(ctx context.Context, workspaceID int, scenario string, messageID int, actionIndex int) (Action, error) {
	return scanAction(s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, scenario, scope_type, scope_id, message_id,
			action_index, action_type, payload_json, status, entity_type, entity_id,
			proposed_by, confirmed_by, edited_by, rejected_by, error_text,
			expires_at, confirmed_at, applied_at, rejected_at, created_at, updated_at
		FROM v2_ai_actions
		WHERE workspace_id=$1 AND scenario=$2 AND message_id=$3 AND action_index=$4
	`, workspaceID, scenario, messageID, actionIndex))
}

func (s *Store) List(ctx context.Context, workspaceID int, scenario string, messageID int, limit int) ([]Action, error) {
	if limit <= 0 || limit > 500 {
		limit = 300
	}
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, workspace_id, scenario, scope_type, scope_id, message_id,
			action_index, action_type, payload_json, status, entity_type, entity_id,
			proposed_by, confirmed_by, edited_by, rejected_by, error_text,
			expires_at, confirmed_at, applied_at, rejected_at, created_at, updated_at
		FROM v2_ai_actions
		WHERE workspace_id=$1
			AND ($2='' OR scenario=$2)
			AND ($3=0 OR message_id=$3)
		ORDER BY created_at DESC, id DESC
		LIMIT $4
	`, workspaceID, strings.TrimSpace(scenario), messageID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Action{}
	for rows.Next() {
		item, err := scanAction(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) Update(ctx context.Context, workspaceID int, actionID int64, userID int, request UpdateRequest) (Action, error) {
	status := strings.ToLower(strings.TrimSpace(request.Status))
	switch status {
	case StatusRejected:
		return scanAction(s.dbx.QueryRowContext(ctx, `
			UPDATE v2_ai_actions
			SET status=$1, rejected_by=$2, rejected_at=NOW(), updated_at=NOW()
			WHERE id=$3 AND workspace_id=$4 AND status IN ($5, $6, $7)
			RETURNING id, workspace_id, scenario, scope_type, scope_id, message_id,
				action_index, action_type, payload_json, status, entity_type, entity_id,
				proposed_by, confirmed_by, edited_by, rejected_by, error_text,
				expires_at, confirmed_at, applied_at, rejected_at, created_at, updated_at
		`, StatusRejected, userID, actionID, workspaceID, StatusProposed, StatusEdited, StatusConfirmed))
	case StatusEdited:
		if len(request.Payload) == 0 || !json.Valid(request.Payload) {
			return Action{}, fmt.Errorf("invalid_ai_action_payload")
		}
		return scanAction(s.dbx.QueryRowContext(ctx, `
			UPDATE v2_ai_actions
			SET status=$1, payload_json=$2,
				action_type=CASE WHEN BTRIM($3)='' THEN action_type ELSE BTRIM($3) END,
				edited_by=$4, updated_at=NOW()
			WHERE id=$5 AND workspace_id=$6 AND status IN ($7, $8)
			RETURNING id, workspace_id, scenario, scope_type, scope_id, message_id,
				action_index, action_type, payload_json, status, entity_type, entity_id,
				proposed_by, confirmed_by, edited_by, rejected_by, error_text,
				expires_at, confirmed_at, applied_at, rejected_at, created_at, updated_at
		`, StatusEdited, request.Payload, request.ActionType, userID, actionID, workspaceID, StatusProposed, StatusEdited))
	default:
		return Action{}, fmt.Errorf("invalid_ai_action_transition")
	}
}

type scanner interface {
	Scan(...any) error
}

func scanAction(row scanner) (Action, error) {
	var item Action
	err := row.Scan(
		&item.ID, &item.WorkspaceID, &item.Scenario, &item.ScopeType, &item.ScopeID,
		&item.MessageID, &item.ActionIndex, &item.ActionType, &item.Payload, &item.Status,
		&item.EntityType, &item.EntityID, &item.ProposedBy, &item.ConfirmedBy, &item.EditedBy,
		&item.RejectedBy, &item.Error, &item.ExpiresAt, &item.ConfirmedAt, &item.AppliedAt,
		&item.RejectedAt, &item.CreatedAt, &item.UpdatedAt,
	)
	return item, err
}

func requireOne(result sql.Result, code string) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("%s", code)
	}
	return nil
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
