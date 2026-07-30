package tactics

import (
	"context"
	"strings"
)

func (s *Store) CreationOptions(ctx context.Context, workspaceID int) (TacticsCreationOptions, error) {
	result := TacticsCreationOptions{
		Departments: []TacticsDepartmentOption{},
		Members:     []TacticsMemberOption{},
	}

	departmentRows, err := s.dbx.QueryContext(ctx, `
		SELECT id, name
		FROM v2_departments
		WHERE workspace_id=$1 AND archived_at IS NULL AND status='active'
		ORDER BY sort_order, lower(name), id
	`, workspaceID)
	if err != nil {
		return result, err
	}
	defer departmentRows.Close()
	for departmentRows.Next() {
		var item TacticsDepartmentOption
		if err := departmentRows.Scan(&item.ID, &item.Name); err != nil {
			return result, err
		}
		item.Name = strings.TrimSpace(item.Name)
		result.Departments = append(result.Departments, item)
	}
	if err := departmentRows.Err(); err != nil {
		return result, err
	}

	memberRows, err := s.dbx.QueryContext(ctx, `
		SELECT users.id, COALESCE(users.name, ''), users.email, COALESCE(users.company_role, '')
		FROM workspace_memberships membership
		JOIN users ON users.id=membership.user_id
		WHERE membership.workspace_id=$1 AND membership.status='active'
		ORDER BY CASE WHEN membership.role='owner' THEN 0 ELSE 1 END,
			lower(COALESCE(NULLIF(users.name, ''), users.email)), users.id
	`, workspaceID)
	if err != nil {
		return result, err
	}
	defer memberRows.Close()
	for memberRows.Next() {
		var item TacticsMemberOption
		if err := memberRows.Scan(&item.UserID, &item.Name, &item.Email, &item.CompanyRole); err != nil {
			return result, err
		}
		item.Name = strings.TrimSpace(item.Name)
		item.Email = strings.TrimSpace(item.Email)
		item.CompanyRole = strings.TrimSpace(item.CompanyRole)
		result.Members = append(result.Members, item)
	}
	return result, memberRows.Err()
}

func hydrateTacticsDraftLabels(changes []TacticsDraftChange, options TacticsCreationOptions) {
	departmentsByID := make(map[int]string, len(options.Departments))
	for _, department := range options.Departments {
		departmentsByID[department.ID] = department.Name
	}
	membersByID := make(map[int]string, len(options.Members))
	for _, member := range options.Members {
		label := member.Name
		if label == "" {
			label = member.Email
		}
		membersByID[member.UserID] = label
	}

	for index := range changes {
		change := &changes[index]
		if change.LeadDepartmentID > 0 {
			if label, exists := departmentsByID[change.LeadDepartmentID]; exists {
				change.LeadDepartmentName = label
			} else {
				change.LeadDepartmentID = 0
				change.LeadDepartmentName = ""
			}
		}
		validParticipants := make([]int, 0, len(change.ParticipantDepartmentIDs))
		for _, departmentID := range change.ParticipantDepartmentIDs {
			if _, exists := departmentsByID[departmentID]; exists && departmentID != change.LeadDepartmentID {
				validParticipants = append(validParticipants, departmentID)
			}
		}
		change.ParticipantDepartmentIDs = validParticipants

		if change.DepartmentID != nil {
			if label, exists := departmentsByID[*change.DepartmentID]; exists {
				change.DepartmentName = label
			} else {
				change.DepartmentID = nil
				change.DepartmentName = ""
			}
		}
		if change.OwnerUserID != nil {
			if label, exists := membersByID[*change.OwnerUserID]; exists {
				change.OwnerName = label
			} else {
				change.OwnerUserID = nil
				change.OwnerName = ""
			}
		}
	}
}
