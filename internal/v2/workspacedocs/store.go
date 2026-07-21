package workspacedocs

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

type Store struct {
	db *sql.DB
}

func NewStore(dbx *sql.DB) *Store {
	return &Store{db: dbx}
}

func (s *Store) List(ctx context.Context, workspaceID int, includeArchived bool) ([]Document, error) {
	query := `
		SELECT id, workspace_id, parent_id, title, content, status, favorite,
			linked_department_ids, linked_workstream_ids, linked_project_ids,
			version, created_by, updated_by, created_at, updated_at, archived_at
		FROM workspace_documents
		WHERE workspace_id=$1`
	if !includeArchived {
		query += ` AND archived_at IS NULL`
	}
	query += ` ORDER BY favorite DESC, updated_at DESC, id DESC`
	rows, err := s.db.QueryContext(ctx, query, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	documents := make([]Document, 0)
	for rows.Next() {
		document, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	return documents, rows.Err()
}

func (s *Store) Get(ctx context.Context, workspaceID int, documentID int64) (Document, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, parent_id, title, content, status, favorite,
			linked_department_ids, linked_workstream_ids, linked_project_ids,
			version, created_by, updated_by, created_at, updated_at, archived_at
		FROM workspace_documents
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, documentID)
	return scanDocument(row)
}

func (s *Store) Versions(ctx context.Context, workspaceID int, documentID int64) ([]Version, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, document_id, version, title, content, status, favorite, saved_by, created_at
		FROM workspace_document_versions
		WHERE workspace_id=$1 AND document_id=$2
		ORDER BY version DESC
		LIMIT 50
	`, workspaceID, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := make([]Version, 0)
	for rows.Next() {
		var version Version
		if err := rows.Scan(&version.ID, &version.DocumentID, &version.Version, &version.Title, &version.Content, &version.Status, &version.Favorite, &version.SavedBy, &version.CreatedAt); err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func (s *Store) Create(ctx context.Context, workspaceID, userID int, input Input) (Document, error) {
	if input.Title == nil {
		return Document{}, ErrInvalidDocument
	}
	title := strings.TrimSpace(*input.Title)
	content := valueOr(input.Content, "")
	status := valueOr(input.Status, "draft")
	favorite := boolOr(input.Favorite, false)
	departmentIDs := normalizedIDs(input.DepartmentIDs)
	workstreamIDs := normalizedIDs(input.WorkstreamIDs)
	projectIDs := normalizedIDs(input.ProjectIDs)
	if !validDocument(title, content, status) {
		return Document{}, ErrInvalidDocument
	}
	if err := s.validateRelations(ctx, workspaceID, input.ParentID, departmentIDs, workstreamIDs, projectIDs); err != nil {
		return Document{}, err
	}
	departmentJSON, _ := json.Marshal(departmentIDs)
	workstreamJSON, _ := json.Marshal(workstreamIDs)
	projectJSON, _ := json.Marshal(projectIDs)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Document{}, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
		INSERT INTO workspace_documents (
			workspace_id, parent_id, title, content, status, favorite,
			linked_department_ids, linked_workstream_ids, linked_project_ids,
			created_by, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9::jsonb,$10,$10)
		RETURNING id, workspace_id, parent_id, title, content, status, favorite,
			linked_department_ids, linked_workstream_ids, linked_project_ids,
			version, created_by, updated_by, created_at, updated_at, archived_at
	`, workspaceID, input.ParentID, title, content, status, favorite, departmentJSON, workstreamJSON, projectJSON, userID)
	document, err := scanDocument(row)
	if err != nil {
		return Document{}, err
	}
	if err := saveVersion(ctx, tx, document, userID); err != nil {
		return Document{}, err
	}
	if err := tx.Commit(); err != nil {
		return Document{}, err
	}
	return document, nil
}

func (s *Store) Update(ctx context.Context, workspaceID, userID int, documentID int64, input Input) (Document, error) {
	current, err := s.Get(ctx, workspaceID, documentID)
	if err != nil {
		return Document{}, err
	}
	title := current.Title
	if input.Title != nil {
		title = strings.TrimSpace(*input.Title)
	}
	content := current.Content
	if input.Content != nil {
		content = *input.Content
	}
	status := current.Status
	if input.Status != nil {
		status = *input.Status
	}
	favorite := current.Favorite
	if input.Favorite != nil {
		favorite = *input.Favorite
	}
	parentID := current.ParentID
	if input.ClearParent {
		parentID = nil
	} else if input.ParentID != nil {
		parentID = input.ParentID
	}
	departmentIDs := current.LinkedDepartmentIDs
	if input.DepartmentIDs != nil {
		departmentIDs = normalizedIDs(input.DepartmentIDs)
	}
	workstreamIDs := current.LinkedWorkstreamIDs
	if input.WorkstreamIDs != nil {
		workstreamIDs = normalizedIDs(input.WorkstreamIDs)
	}
	projectIDs := current.LinkedProjectIDs
	if input.ProjectIDs != nil {
		projectIDs = normalizedIDs(input.ProjectIDs)
	}
	if !validDocument(title, content, status) {
		return Document{}, ErrInvalidDocument
	}
	if parentID != nil && *parentID == documentID {
		return Document{}, ErrInvalidParent
	}
	if err := s.validateRelations(ctx, workspaceID, parentID, departmentIDs, workstreamIDs, projectIDs); err != nil {
		return Document{}, err
	}
	departmentJSON, _ := json.Marshal(departmentIDs)
	workstreamJSON, _ := json.Marshal(workstreamIDs)
	projectJSON, _ := json.Marshal(projectIDs)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Document{}, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
		UPDATE workspace_documents
		SET parent_id=$3, title=$4, content=$5, status=$6, favorite=$7,
			linked_department_ids=$8::jsonb, linked_workstream_ids=$9::jsonb, linked_project_ids=$10::jsonb,
			version=version+1, updated_by=$11, updated_at=NOW(),
			archived_at=CASE WHEN $6='archived' THEN COALESCE(archived_at, NOW()) ELSE NULL END
		WHERE workspace_id=$1 AND id=$2
		RETURNING id, workspace_id, parent_id, title, content, status, favorite,
			linked_department_ids, linked_workstream_ids, linked_project_ids,
			version, created_by, updated_by, created_at, updated_at, archived_at
	`, workspaceID, documentID, parentID, title, content, status, favorite, departmentJSON, workstreamJSON, projectJSON, userID)
	document, err := scanDocument(row)
	if err != nil {
		return Document{}, err
	}
	if err := saveVersion(ctx, tx, document, userID); err != nil {
		return Document{}, err
	}
	if err := tx.Commit(); err != nil {
		return Document{}, err
	}
	return document, nil
}

func (s *Store) Archive(ctx context.Context, workspaceID, userID int, documentID int64) (Document, error) {
	status := "archived"
	return s.Update(ctx, workspaceID, userID, documentID, Input{Status: &status})
}

func (s *Store) validateRelations(ctx context.Context, workspaceID int, parentID *int64, departmentIDs, workstreamIDs, projectIDs []int) error {
	if parentID != nil {
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_documents WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL)`, workspaceID, *parentID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrInvalidParent
		}
	}
	checks := []struct {
		table string
		ids   []int
	}{
		{table: "v2_departments", ids: departmentIDs},
		{table: "v2_tactical_workstreams", ids: workstreamIDs},
		{table: "v2_tactical_projects", ids: projectIDs},
	}
	for _, check := range checks {
		for _, id := range check.ids {
			var exists bool
			query := `SELECT EXISTS(SELECT 1 FROM ` + check.table + ` WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL)`
			if err := s.db.QueryRowContext(ctx, query, workspaceID, id).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrInvalidLink
			}
		}
	}
	return nil
}

func saveVersion(ctx context.Context, tx *sql.Tx, document Document, userID int) error {
	departmentJSON, _ := json.Marshal(document.LinkedDepartmentIDs)
	workstreamJSON, _ := json.Marshal(document.LinkedWorkstreamIDs)
	projectJSON, _ := json.Marshal(document.LinkedProjectIDs)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_document_versions (
			document_id, workspace_id, version, title, content, status, favorite,
			linked_department_ids, linked_workstream_ids, linked_project_ids, saved_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9::jsonb,$10::jsonb,$11)
	`, document.ID, document.WorkspaceID, document.Version, document.Title, document.Content, document.Status, document.Favorite, departmentJSON, workstreamJSON, projectJSON, userID)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanDocument(row scanner) (Document, error) {
	var document Document
	var departmentJSON, workstreamJSON, projectJSON []byte
	err := row.Scan(
		&document.ID, &document.WorkspaceID, &document.ParentID, &document.Title, &document.Content,
		&document.Status, &document.Favorite, &departmentJSON, &workstreamJSON, &projectJSON,
		&document.Version, &document.CreatedBy, &document.UpdatedBy, &document.CreatedAt, &document.UpdatedAt,
		&document.ArchivedAt,
	)
	if err != nil {
		return Document{}, err
	}
	_ = json.Unmarshal(departmentJSON, &document.LinkedDepartmentIDs)
	_ = json.Unmarshal(workstreamJSON, &document.LinkedWorkstreamIDs)
	_ = json.Unmarshal(projectJSON, &document.LinkedProjectIDs)
	if document.LinkedDepartmentIDs == nil {
		document.LinkedDepartmentIDs = []int{}
	}
	if document.LinkedWorkstreamIDs == nil {
		document.LinkedWorkstreamIDs = []int{}
	}
	if document.LinkedProjectIDs == nil {
		document.LinkedProjectIDs = []int{}
	}
	return document, nil
}

func validDocument(title, content, status string) bool {
	return title != "" && len(title) <= 240 && len(content) <= 1_000_000 && (status == "draft" || status == "published" || status == "archived")
}

func valueOr(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return *value
}

func boolOr(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func normalizedIDs(value *[]int) []int {
	if value == nil {
		return []int{}
	}
	seen := make(map[int]struct{}, len(*value))
	result := make([]int, 0, len(*value))
	for _, id := range *value {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}
