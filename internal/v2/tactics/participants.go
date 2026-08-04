package tactics

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	"github.com/lib/pq"
)

var ErrInvalidDirectionParticipants = errors.New("invalid_direction_participants")

type WorkstreamParticipants struct {
	OwnerUserID int   `json:"owner_user_id"`
	TeamUserIDs []int `json:"team_user_ids"`
}

func (s *Store) hydrateWorkstreamParticipants(ctx context.Context, workspaceID int, workstreams []Workstream) error {
	if len(workstreams) == 0 {
		return nil
	}
	byID := make(map[int]*Workstream, len(workstreams))
	ids := make([]int, 0, len(workstreams))
	for index := range workstreams {
		byID[workstreams[index].ID] = &workstreams[index]
		ids = append(ids, workstreams[index].ID)
	}
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT workstream_id, user_id, role
		FROM v2_workstream_participants
		WHERE workspace_id=$1 AND workstream_id = ANY($2)
		ORDER BY workstream_id, role DESC, user_id
	`, workspaceID, pq.Array(ids))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var workstreamID int
		var userID int
		var role string
		if err := rows.Scan(&workstreamID, &userID, &role); err != nil {
			return err
		}
		workstream := byID[workstreamID]
		if workstream == nil {
			continue
		}
		if role == "owner" {
			value := userID
			workstream.OwnerUserID = &value
		} else if role == "team" {
			workstream.TeamUserIDs = append(workstream.TeamUserIDs, userID)
		}
	}
	return rows.Err()
}

func (s *Store) WorkstreamParticipants(ctx context.Context, workspaceID int, workstreamID int) (WorkstreamParticipants, error) {
	workstreams := []Workstream{{ID: workstreamID}}
	var exists bool
	if err := s.dbx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM v2_tactical_workstreams WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL)`, workspaceID, workstreamID).Scan(&exists); err != nil {
		return WorkstreamParticipants{}, err
	}
	if !exists {
		return WorkstreamParticipants{}, sql.ErrNoRows
	}
	if err := s.hydrateWorkstreamParticipants(ctx, workspaceID, workstreams); err != nil {
		return WorkstreamParticipants{}, err
	}
	result := WorkstreamParticipants{TeamUserIDs: workstreams[0].TeamUserIDs}
	if workstreams[0].OwnerUserID != nil {
		result.OwnerUserID = *workstreams[0].OwnerUserID
	}
	return result, nil
}

func (s *Store) SetWorkstreamParticipants(ctx context.Context, workspaceID int, workstreamID int, input WorkstreamParticipants) (WorkstreamParticipants, error) {
	input.TeamUserIDs = normalizedParticipantIDs(input.TeamUserIDs)
	if input.OwnerUserID <= 0 {
		return WorkstreamParticipants{}, ErrInvalidDirectionParticipants
	}
	allUserIDs := normalizedParticipantIDs(append([]int{input.OwnerUserID}, input.TeamUserIDs...))
	var workstreamExists bool
	if err := s.dbx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM v2_tactical_workstreams WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL)`, workspaceID, workstreamID).Scan(&workstreamExists); err != nil {
		return WorkstreamParticipants{}, err
	}
	if !workstreamExists {
		return WorkstreamParticipants{}, sql.ErrNoRows
	}
	var memberCount int
	if err := s.dbx.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT user_id)
		FROM workspace_memberships
		WHERE workspace_id=$1 AND status='active' AND user_id = ANY($2)
	`, workspaceID, pq.Array(allUserIDs)).Scan(&memberCount); err != nil {
		return WorkstreamParticipants{}, err
	}
	if memberCount != len(allUserIDs) {
		return WorkstreamParticipants{}, ErrInvalidDirectionParticipants
	}

	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return WorkstreamParticipants{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM v2_workstream_participants WHERE workspace_id=$1 AND workstream_id=$2`, workspaceID, workstreamID); err != nil {
		return WorkstreamParticipants{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO v2_workstream_participants (workspace_id, workstream_id, user_id, role) VALUES ($1,$2,$3,'owner')`, workspaceID, workstreamID, input.OwnerUserID); err != nil {
		return WorkstreamParticipants{}, err
	}
	for _, userID := range input.TeamUserIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO v2_workstream_participants (workspace_id, workstream_id, user_id, role) VALUES ($1,$2,$3,'team')`, workspaceID, workstreamID, userID); err != nil {
			return WorkstreamParticipants{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return WorkstreamParticipants{}, err
	}
	return input, nil
}

func normalizedParticipantIDs(ids []int) []int {
	seen := make(map[int]struct{}, len(ids))
	result := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Ints(result)
	return result
}
