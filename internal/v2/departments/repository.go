package departments

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lib/pq"
)

var (
	ErrInvalidDepartment     = errors.New("invalid_department")
	ErrDuplicateDepartment   = errors.New("department_name_exists")
	ErrInvalidMember         = errors.New("invalid_department_member")
	ErrDepartmentInUse       = errors.New("department_in_use")
	ErrLastDepartment        = errors.New("last_department_required")
	ErrInvalidResponsibility = errors.New("invalid_responsibility")
)

type Store struct {
	dbx *sql.DB
}

func NewStore(dbx *sql.DB) *Store {
	return &Store{dbx: dbx}
}

func (s *Store) EnsureDefault(ctx context.Context, workspaceID int, ownerUserID int) error {
	_, err := s.dbx.ExecContext(ctx, `
		INSERT INTO v2_departments (
			workspace_id, name, description, responsibility, manager_user_id, status, sort_order, created_by
		)
		SELECT $1, 'Компания', 'Общая команда workspace',
			'Ответственность по умолчанию до создания функциональных отделов.',
			$2, $3, 0, $2
		WHERE NOT EXISTS (
			SELECT 1 FROM v2_departments WHERE workspace_id=$1 AND archived_at IS NULL
		)
	`, workspaceID, ownerUserID, StatusActive)
	if err != nil {
		return err
	}
	_, err = s.dbx.ExecContext(ctx, `
		INSERT INTO v2_department_members (department_id, workspace_id, user_id, role)
		SELECT id, workspace_id, $2, 'manager'
		FROM v2_departments
		WHERE workspace_id=$1 AND manager_user_id=$2 AND archived_at IS NULL
		ON CONFLICT (department_id, user_id) DO UPDATE SET role='manager', updated_at=NOW()
	`, workspaceID, ownerUserID)
	return err
}

func (s *Store) List(ctx context.Context, workspaceID int, includeArchived bool) ([]Department, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT department.id, department.workspace_id, department.name, department.description,
			department.responsibility, department.manager_user_id, department.kpis_json,
			department.status, department.sort_order, department.created_at, department.updated_at,
			(SELECT COUNT(*) FROM v2_department_members member WHERE member.department_id=department.id),
			(SELECT COUNT(*) FROM v2_workstream_departments link WHERE link.department_id=department.id),
			(SELECT COUNT(*) FROM v2_project_departments link WHERE link.department_id=department.id),
			(SELECT COUNT(*) FROM v2_tasks task WHERE task.department_id=department.id AND task.archived_at IS NULL AND task.status<>'done')
		FROM v2_departments department
		WHERE department.workspace_id=$1 AND ($2 OR department.archived_at IS NULL)
		ORDER BY department.status ASC, department.sort_order ASC, lower(department.name), department.id
	`, workspaceID, includeArchived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Department{}
	for rows.Next() {
		item, err := scanDepartment(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) Get(ctx context.Context, workspaceID int, departmentID int) (Detail, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT department.id, department.workspace_id, department.name, department.description,
			department.responsibility, department.manager_user_id, department.kpis_json,
			department.status, department.sort_order, department.created_at, department.updated_at,
			(SELECT COUNT(*) FROM v2_department_members member WHERE member.department_id=department.id),
			(SELECT COUNT(*) FROM v2_workstream_departments link WHERE link.department_id=department.id),
			(SELECT COUNT(*) FROM v2_project_departments link WHERE link.department_id=department.id),
			(SELECT COUNT(*) FROM v2_tasks task WHERE task.department_id=department.id AND task.archived_at IS NULL AND task.status<>'done')
		FROM v2_departments department
		WHERE department.id=$1 AND department.workspace_id=$2 AND department.archived_at IS NULL
	`, departmentID, workspaceID)
	department, err := scanDepartment(row)
	if err != nil {
		return Detail{}, err
	}
	members, err := s.members(ctx, workspaceID, departmentID)
	if err != nil {
		return Detail{}, err
	}
	initiatives, err := s.initiatives(ctx, workspaceID, departmentID)
	if err != nil {
		return Detail{}, err
	}
	projects, err := s.projects(ctx, workspaceID, departmentID)
	if err != nil {
		return Detail{}, err
	}
	documents, err := s.documents(ctx, workspaceID, EntityDepartment, departmentID)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Department: department, Members: members, Initiatives: initiatives, Projects: projects, Documents: documents}, nil
}

const EntityDepartment = "department"

func (s *Store) Create(ctx context.Context, workspaceID int, userID int, input Input) (Detail, error) {
	name := strings.TrimSpace(value(input.Name))
	if name == "" || len([]rune(name)) > 120 {
		return Detail{}, ErrInvalidDepartment
	}
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return Detail{}, err
	}
	defer tx.Rollback()
	managerID := input.ManagerUserID
	if err := validateUsers(ctx, tx, workspaceID, managerID, input.MemberUserIDs); err != nil {
		return Detail{}, err
	}
	kpis := normalizeKPIs(input.KPIs)
	kpisJSON, _ := json.Marshal(kpis)
	var id int
	err = tx.QueryRowContext(ctx, `
		INSERT INTO v2_departments (
			workspace_id, name, description, responsibility, manager_user_id, kpis_json,
			status, sort_order, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7,
			COALESCE($8, (SELECT COALESCE(MAX(sort_order), 0) + 1 FROM v2_departments WHERE workspace_id=$1)), $9)
		RETURNING id
	`, workspaceID, name, strings.TrimSpace(value(input.Description)), strings.TrimSpace(value(input.Responsibility)),
		nullableInt(managerID), kpisJSON, StatusActive, nullableInt(input.SortOrder), userID).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return Detail{}, ErrDuplicateDepartment
		}
		return Detail{}, err
	}
	if err := replaceMembers(ctx, tx, workspaceID, id, managerID, input.MemberUserIDs); err != nil {
		return Detail{}, err
	}
	if err := tx.Commit(); err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, workspaceID, id)
}

func (s *Store) Update(ctx context.Context, workspaceID int, departmentID int, input Input) (Detail, error) {
	current, err := s.Get(ctx, workspaceID, departmentID)
	if err != nil {
		return Detail{}, err
	}
	name := current.Department.Name
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
	}
	if name == "" || len([]rune(name)) > 120 {
		return Detail{}, ErrInvalidDepartment
	}
	description := current.Department.Description
	if input.Description != nil {
		description = strings.TrimSpace(*input.Description)
	}
	responsibility := current.Department.Responsibility
	if input.Responsibility != nil {
		responsibility = strings.TrimSpace(*input.Responsibility)
	}
	managerID := current.Department.ManagerUserID
	if input.ClearManager {
		managerID = nil
	} else if input.ManagerUserID != nil {
		managerID = input.ManagerUserID
	}
	kpis := current.Department.KPIs
	if input.KPIs != nil {
		kpis = normalizeKPIs(input.KPIs)
	}
	members := memberIDs(current.Members)
	if input.MemberUserIDs != nil {
		members = input.MemberUserIDs
	}

	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return Detail{}, err
	}
	defer tx.Rollback()
	if err := validateUsers(ctx, tx, workspaceID, managerID, members); err != nil {
		return Detail{}, err
	}
	kpisJSON, _ := json.Marshal(kpis)
	_, err = tx.ExecContext(ctx, `
		UPDATE v2_departments
		SET name=$1, description=$2, responsibility=$3, manager_user_id=$4,
			kpis_json=$5, sort_order=COALESCE($6, sort_order), updated_at=NOW()
		WHERE id=$7 AND workspace_id=$8 AND archived_at IS NULL
	`, name, description, responsibility, nullableInt(managerID), kpisJSON, nullableInt(input.SortOrder), departmentID, workspaceID)
	if err != nil {
		if isUniqueViolation(err) {
			return Detail{}, ErrDuplicateDepartment
		}
		return Detail{}, err
	}
	if err := replaceMembers(ctx, tx, workspaceID, departmentID, managerID, members); err != nil {
		return Detail{}, err
	}
	if err := tx.Commit(); err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, workspaceID, departmentID)
}

func (s *Store) Archive(ctx context.Context, workspaceID int, departmentID int) error {
	var activeCount int
	if err := s.dbx.QueryRowContext(ctx, `SELECT COUNT(*) FROM v2_departments WHERE workspace_id=$1 AND archived_at IS NULL`, workspaceID).Scan(&activeCount); err != nil {
		return err
	}
	if activeCount <= 1 {
		return ErrLastDepartment
	}
	var usageCount int
	if err := s.dbx.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM v2_tasks WHERE workspace_id=$1 AND department_id=$2 AND archived_at IS NULL) +
			(SELECT COUNT(*) FROM v2_workstream_departments WHERE workspace_id=$1 AND department_id=$2) +
			(SELECT COUNT(*) FROM v2_project_departments WHERE workspace_id=$1 AND department_id=$2)
	`, workspaceID, departmentID).Scan(&usageCount); err != nil {
		return err
	}
	if usageCount > 0 {
		return ErrDepartmentInUse
	}
	result, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_departments SET status=$1, archived_at=NOW(), updated_at=NOW()
		WHERE id=$2 AND workspace_id=$3 AND archived_at IS NULL
	`, StatusArchived, departmentID, workspaceID)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ListResponsibilities(ctx context.Context, workspaceID int) ([]ResponsibilityView, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT 'workstream', link.workstream_id, link.role,
			department.id, department.workspace_id, department.name, department.description,
			department.responsibility, department.manager_user_id, department.kpis_json,
			department.status, department.sort_order, department.created_at, department.updated_at
		FROM v2_workstream_departments link
		JOIN v2_departments department ON department.id=link.department_id AND department.archived_at IS NULL
		WHERE link.workspace_id=$1
		UNION ALL
		SELECT 'project', link.project_id, link.role,
			department.id, department.workspace_id, department.name, department.description,
			department.responsibility, department.manager_user_id, department.kpis_json,
			department.status, department.sort_order, department.created_at, department.updated_at
		FROM v2_project_departments link
		JOIN v2_departments department ON department.id=link.department_id AND department.archived_at IS NULL
		WHERE link.workspace_id=$1
		ORDER BY 1, 2, 3
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byKey := map[string]*ResponsibilityView{}
	keys := []string{}
	for rows.Next() {
		var entityType, role string
		var entityID int
		var item Department
		var manager sql.NullInt64
		var raw []byte
		if err := rows.Scan(&entityType, &entityID, &role, &item.ID, &item.WorkspaceID, &item.Name,
			&item.Description, &item.Responsibility, &manager, &raw, &item.Status, &item.SortOrder,
			&item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if manager.Valid {
			value := int(manager.Int64)
			item.ManagerUserID = &value
		}
		_ = json.Unmarshal(raw, &item.KPIs)
		if item.KPIs == nil {
			item.KPIs = []KPI{}
		}
		key := fmt.Sprintf("%s:%d", entityType, entityID)
		view := byKey[key]
		if view == nil {
			view = &ResponsibilityView{EntityType: entityType, EntityID: entityID, Participants: []Department{}}
			byKey[key] = view
			keys = append(keys, key)
		}
		if role == ResponsibilityLead {
			copy := item
			view.Lead = &copy
		} else {
			view.Participants = append(view.Participants, item)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(keys)
	result := make([]ResponsibilityView, 0, len(keys))
	for _, key := range keys {
		result = append(result, *byKey[key])
	}
	return result, nil
}

func (s *Store) SetResponsibility(ctx context.Context, workspaceID int, input Responsibility) (ResponsibilityView, error) {
	if input.EntityID <= 0 || input.LeadDepartmentID <= 0 || (input.EntityType != EntityWorkstream && input.EntityType != EntityProject) {
		return ResponsibilityView{}, ErrInvalidResponsibility
	}
	ids := dedupePositive(append([]int{input.LeadDepartmentID}, input.ParticipantDepartmentIDs...))
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return ResponsibilityView{}, err
	}
	defer tx.Rollback()
	if err := validateEntity(ctx, tx, workspaceID, input.EntityType, input.EntityID); err != nil {
		return ResponsibilityView{}, err
	}
	if err := validateDepartments(ctx, tx, workspaceID, ids); err != nil {
		return ResponsibilityView{}, err
	}
	if err := clearResponsibility(ctx, tx, workspaceID, input.EntityType, input.EntityID); err != nil {
		return ResponsibilityView{}, err
	}
	for _, id := range ids {
		role := ResponsibilityParticipant
		if id == input.LeadDepartmentID {
			role = ResponsibilityLead
		}
		if err := insertResponsibility(ctx, tx, workspaceID, input.EntityType, input.EntityID, id, role); err != nil {
			return ResponsibilityView{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ResponsibilityView{}, err
	}
	items, err := s.ListResponsibilities(ctx, workspaceID)
	if err != nil {
		return ResponsibilityView{}, err
	}
	for _, item := range items {
		if item.EntityType == input.EntityType && item.EntityID == input.EntityID {
			return item, nil
		}
	}
	return ResponsibilityView{}, sql.ErrNoRows
}

type scanner interface {
	Scan(dest ...any) error
}

func scanDepartment(row scanner) (Department, error) {
	var item Department
	var manager sql.NullInt64
	var raw []byte
	err := row.Scan(&item.ID, &item.WorkspaceID, &item.Name, &item.Description, &item.Responsibility,
		&manager, &raw, &item.Status, &item.SortOrder, &item.CreatedAt, &item.UpdatedAt,
		&item.MemberCount, &item.InitiativeCount, &item.ProjectCount, &item.ActiveTaskCount)
	if manager.Valid {
		value := int(manager.Int64)
		item.ManagerUserID = &value
	}
	_ = json.Unmarshal(raw, &item.KPIs)
	if item.KPIs == nil {
		item.KPIs = []KPI{}
	}
	return item, err
}

func (s *Store) members(ctx context.Context, workspaceID int, departmentID int) ([]Member, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT users.id, COALESCE(users.name, ''), users.email, COALESCE(users.avatar_url, ''), member.role
		FROM v2_department_members member
		JOIN users ON users.id=member.user_id
		WHERE member.workspace_id=$1 AND member.department_id=$2
		ORDER BY CASE member.role WHEN 'manager' THEN 0 ELSE 1 END, lower(COALESCE(users.name, users.email))
	`, workspaceID, departmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Member{}
	for rows.Next() {
		var item Member
		if err := rows.Scan(&item.UserID, &item.Name, &item.Email, &item.AvatarURL, &item.Role); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) initiatives(ctx context.Context, workspaceID int, departmentID int) ([]EntitySummary, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT initiative.id, initiative.title, initiative.description, initiative.status, link.role
		FROM v2_workstream_departments link
		JOIN v2_tactical_workstreams initiative ON initiative.id=link.workstream_id AND initiative.archived_at IS NULL
		WHERE link.workspace_id=$1 AND link.department_id=$2
		ORDER BY CASE link.role WHEN 'lead' THEN 0 ELSE 1 END, initiative.sort_order, initiative.id
	`, workspaceID, departmentID)
	return scanEntities(rows, err)
}

func (s *Store) projects(ctx context.Context, workspaceID int, departmentID int) ([]EntitySummary, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT project.id, project.title, project.description, project.status, link.role
		FROM v2_project_departments link
		JOIN v2_tactical_projects project ON project.id=link.project_id AND project.archived_at IS NULL
		WHERE link.workspace_id=$1 AND link.department_id=$2
		ORDER BY CASE link.role WHEN 'lead' THEN 0 ELSE 1 END, project.sort_order, project.id
	`, workspaceID, departmentID)
	return scanEntities(rows, err)
}

func scanEntities(rows *sql.Rows, err error) ([]EntitySummary, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []EntitySummary{}
	for rows.Next() {
		var item EntitySummary
		if err := rows.Scan(&item.ID, &item.Title, &item.Description, &item.Status, &item.Role); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) documents(ctx context.Context, workspaceID int, entityType string, entityID int) ([]DocumentLink, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT link.id, document.id, document.document_type, document.title, document.generated_at
		FROM v2_entity_document_links link
		JOIN strategic_documents document ON document.id=link.document_id AND document.workspace_id=link.workspace_id
		WHERE link.workspace_id=$1 AND link.entity_type=$2 AND link.entity_id=$3
		ORDER BY document.updated_at DESC, document.id DESC
	`, workspaceID, entityType, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []DocumentLink{}
	for rows.Next() {
		var item DocumentLink
		if err := rows.Scan(&item.ID, &item.DocumentID, &item.DocumentType, &item.Title, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func validateUsers(ctx context.Context, tx *sql.Tx, workspaceID int, managerID *int, members []int) error {
	ids := dedupePositive(members)
	if managerID != nil && *managerID > 0 {
		ids = dedupePositive(append(ids, *managerID))
	}
	if len(ids) == 0 {
		return nil
	}
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM workspace_memberships
		WHERE workspace_id=$1 AND status='active' AND user_id=ANY($2)
	`, workspaceID, pq.Array(ids)).Scan(&count); err != nil {
		return err
	}
	if count != len(ids) {
		return ErrInvalidMember
	}
	return nil
}

func replaceMembers(ctx context.Context, tx *sql.Tx, workspaceID int, departmentID int, managerID *int, members []int) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM v2_department_members WHERE department_id=$1 AND workspace_id=$2`, departmentID, workspaceID); err != nil {
		return err
	}
	ids := dedupePositive(members)
	if managerID != nil && *managerID > 0 {
		ids = dedupePositive(append(ids, *managerID))
	}
	for _, userID := range ids {
		role := "member"
		if managerID != nil && userID == *managerID {
			role = "manager"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO v2_department_members (department_id, workspace_id, user_id, role)
			VALUES ($1, $2, $3, $4)
		`, departmentID, workspaceID, userID, role); err != nil {
			return err
		}
	}
	return nil
}

func validateDepartments(ctx context.Context, tx *sql.Tx, workspaceID int, ids []int) error {
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM v2_departments
		WHERE workspace_id=$1 AND archived_at IS NULL AND id=ANY($2)
	`, workspaceID, pq.Array(ids)).Scan(&count); err != nil {
		return err
	}
	if count != len(ids) {
		return ErrInvalidResponsibility
	}
	return nil
}

func validateEntity(ctx context.Context, tx *sql.Tx, workspaceID int, entityType string, entityID int) error {
	var exists bool
	switch entityType {
	case EntityWorkstream:
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM v2_tactical_workstreams WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL)`, entityID, workspaceID).Scan(&exists); err != nil {
			return err
		}
	case EntityProject:
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM v2_tactical_projects WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL)`, entityID, workspaceID).Scan(&exists); err != nil {
			return err
		}
	default:
		return ErrInvalidResponsibility
	}
	if !exists {
		return ErrInvalidResponsibility
	}
	return nil
}

func clearResponsibility(ctx context.Context, tx *sql.Tx, workspaceID int, entityType string, entityID int) error {
	if entityType == EntityProject {
		_, err := tx.ExecContext(ctx, `DELETE FROM v2_project_departments WHERE workspace_id=$1 AND project_id=$2`, workspaceID, entityID)
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM v2_workstream_departments WHERE workspace_id=$1 AND workstream_id=$2`, workspaceID, entityID)
	return err
}

func insertResponsibility(ctx context.Context, tx *sql.Tx, workspaceID int, entityType string, entityID int, departmentID int, role string) error {
	if entityType == EntityProject {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO v2_project_departments (workspace_id, project_id, department_id, role)
			VALUES ($1, $2, $3, $4)
		`, workspaceID, entityID, departmentID, role)
		return err
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO v2_workstream_departments (workspace_id, workstream_id, department_id, role)
		VALUES ($1, $2, $3, $4)
	`, workspaceID, entityID, departmentID, role)
	return err
}

func normalizeKPIs(items []KPI) []KPI {
	result := make([]KPI, 0, len(items))
	for _, item := range items {
		item.Name = strings.TrimSpace(item.Name)
		item.Current = strings.TrimSpace(item.Current)
		item.Target = strings.TrimSpace(item.Target)
		if item.Name != "" {
			result = append(result, item)
		}
	}
	return result
}

func memberIDs(items []Member) []int {
	result := make([]int, 0, len(items))
	for _, item := range items {
		result = append(result, item.UserID)
	}
	return result
}

func dedupePositive(items []int) []int {
	seen := map[int]struct{}{}
	result := []int{}
	for _, item := range items {
		if item <= 0 {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

func value(input *string) string {
	if input == nil {
		return ""
	}
	return *input
}

func nullableInt(input *int) any {
	if input == nil || *input <= 0 {
		return nil
	}
	return *input
}

func isUniqueViolation(err error) bool {
	var pqError *pq.Error
	return errors.As(err, &pqError) && pqError.Code == "23505"
}
