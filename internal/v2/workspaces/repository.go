package workspaces

import (
	"context"
	"database/sql"
	"errors"
)

type Store struct {
	dbx *sql.DB
}

func NewStore(dbx *sql.DB) *Store {
	return &Store{dbx: dbx}
}

func (s *Store) GetOrCreateDefault(ctx context.Context, userID int) (Workspace, Membership, error) {
	if workspace, membership, err := s.current(ctx, s.dbx, userID); err == nil {
		return workspace, membership, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, Membership{}, err
	}

	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return Workspace{}, Membership{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, userID); err != nil {
		return Workspace{}, Membership{}, err
	}

	if workspace, membership, err := s.current(ctx, tx, userID); err == nil {
		if err := tx.Commit(); err != nil {
			return Workspace{}, Membership{}, err
		}
		return workspace, membership, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, Membership{}, err
	}

	workspace, err := createWorkspace(ctx, tx, userID)
	if err != nil {
		return Workspace{}, Membership{}, err
	}

	membership, err := createMembership(ctx, tx, userID, workspace.ID)
	if err != nil {
		return Workspace{}, Membership{}, err
	}

	if err := tx.Commit(); err != nil {
		return Workspace{}, Membership{}, err
	}

	return workspace, membership, nil
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) current(ctx context.Context, q queryer, userID int) (Workspace, Membership, error) {
	var workspace Workspace
	var membership Membership
	var displayName sql.NullString

	err := q.QueryRowContext(ctx, `
		SELECT
			w.id,
			w.name,
			w.display_name,
			w.owner_user_id,
			w.status,
			w.created_at,
			w.updated_at,
			m.id,
			m.workspace_id,
			m.user_id,
			m.role,
			m.status,
			m.created_at,
			m.updated_at
		FROM workspace_memberships m
		JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.user_id=$1
			AND m.status=$2
			AND w.status=$3
		ORDER BY m.is_default DESC, m.created_at ASC
		LIMIT 1
	`, userID, MembershipStatusActive, WorkspaceStatusActive).Scan(
		&workspace.ID,
		&workspace.Name,
		&displayName,
		&workspace.OwnerUserID,
		&workspace.Status,
		&workspace.CreatedAt,
		&workspace.UpdatedAt,
		&membership.ID,
		&membership.WorkspaceID,
		&membership.UserID,
		&membership.Role,
		&membership.Status,
		&membership.CreatedAt,
		&membership.UpdatedAt,
	)
	if err != nil {
		return Workspace{}, Membership{}, err
	}

	if displayName.Valid {
		workspace.DisplayName = &displayName.String
	}

	return workspace, membership, nil
}

func createWorkspace(ctx context.Context, tx *sql.Tx, userID int) (Workspace, error) {
	var workspace Workspace
	var displayName sql.NullString

	err := tx.QueryRowContext(ctx, `
		INSERT INTO workspaces (name, display_name, owner_user_id, status)
		VALUES ($1, $1, $2, $3)
		RETURNING id, name, display_name, owner_user_id, status, created_at, updated_at
	`, DefaultWorkspaceName, userID, WorkspaceStatusActive).Scan(
		&workspace.ID,
		&workspace.Name,
		&displayName,
		&workspace.OwnerUserID,
		&workspace.Status,
		&workspace.CreatedAt,
		&workspace.UpdatedAt,
	)
	if err != nil {
		return Workspace{}, err
	}

	if displayName.Valid {
		workspace.DisplayName = &displayName.String
	}

	return workspace, nil
}

func createMembership(ctx context.Context, tx *sql.Tx, userID int, workspaceID int) (Membership, error) {
	var membership Membership

	err := tx.QueryRowContext(ctx, `
		INSERT INTO workspace_memberships (workspace_id, user_id, role, status, is_default)
		VALUES ($1, $2, $3, $4, TRUE)
		RETURNING id, workspace_id, user_id, role, status, created_at, updated_at
	`, workspaceID, userID, MembershipRoleOwner, MembershipStatusActive).Scan(
		&membership.ID,
		&membership.WorkspaceID,
		&membership.UserID,
		&membership.Role,
		&membership.Status,
		&membership.CreatedAt,
		&membership.UpdatedAt,
	)
	if err != nil {
		return Membership{}, err
	}

	return membership, nil
}
