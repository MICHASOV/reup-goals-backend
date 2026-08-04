package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"reup-goals-backend/internal/v2/metrics"
	"reup-goals-backend/internal/v2/tactics"
)

func (s *Service) readTool(ctx context.Context, run Run, toolName string, input map[string]any) (any, error) {
	switch toolName {
	case "get_business_brief":
		brief, err := s.businessBrief(ctx, run.WorkspaceID, run.UserID)
		if err != nil {
			return nil, err
		}
		var result any
		if err := json.Unmarshal([]byte(brief), &result); err != nil {
			return map[string]any{"brief": brief}, nil
		}
		return result, nil
	case "list_entities":
		return s.listEntities(ctx, run.WorkspaceID, input)
	case "get_entity":
		return s.getEntity(ctx, run.WorkspaceID, input)
	case "list_workspace_members":
		return s.listWorkspaceMembers(ctx, run.WorkspaceID, input)
	case "get_priority_view":
		return s.priorityView(ctx, run.WorkspaceID, input)
	case "search_metric_catalog":
		query := stringValue(input, "query")
		category := stringValue(input, "category")
		limit := intValue(input, "limit")
		if limit <= 0 || limit > 20 {
			limit = 8
		}
		items := metrics.Catalog(query, category)
		if len(items) > limit {
			items = items[:limit]
		}
		return map[string]any{"metrics": items}, nil
	default:
		return nil, errors.New("agent_tool_not_found")
	}
}

func (s *Service) listWorkspaceMembers(ctx context.Context, workspaceID int, input map[string]any) (any, error) {
	query := strings.ToLower(stringValue(input, "query"))
	limit := intValue(input, "limit")
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	items, err := queryJSONRows(ctx, s.dbx, `
		SELECT jsonb_build_object(
			'user_id', users.id,
			'name', COALESCE(users.name, ''),
			'email', users.email,
			'company_role', COALESCE(users.company_role, ''),
			'workspace_role', membership.role
		)
		FROM workspace_memberships membership
		JOIN users ON users.id=membership.user_id
		WHERE membership.workspace_id=$1 AND membership.status='active'
			AND (
				$2='' OR
				lower(COALESCE(users.name, '')) LIKE '%' || $2 || '%' OR
				lower(users.email) LIKE '%' || $2 || '%' OR
				lower(COALESCE(users.company_role, '')) LIKE '%' || $2 || '%'
			)
		ORDER BY CASE WHEN membership.role='owner' THEN 0 ELSE 1 END,
			lower(COALESCE(NULLIF(users.name, ''), users.email)), users.id
		LIMIT `+fmt.Sprintf("%d", limit), workspaceID, query)
	if err != nil {
		return nil, err
	}
	return map[string]any{"members": items}, nil
}

func (s *Service) listEntities(ctx context.Context, workspaceID int, input map[string]any) (any, error) {
	entityType := stringValue(input, "entity_type")
	parentType := stringValue(input, "parent_type")
	parentID := intValue(input, "parent_id")
	status := stringValue(input, "status")
	query := strings.ToLower(stringValue(input, "query"))
	limit := intValue(input, "limit")
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	type entityQuery struct {
		sql  string
		args []any
	}
	var selected entityQuery
	searchColumn := "title"
	switch entityType {
	case "strategy":
		selected.sql = `SELECT jsonb_build_object('id', id, 'title', title, 'summary', summary, 'status', status, 'version', version)
			FROM v2_strategies WHERE workspace_id=$1 AND archived_at IS NULL`
	case "workstream":
		selected.sql = `SELECT jsonb_build_object('id', id, 'title', title, 'description', description, 'goal', goal, 'ckp', ckp, 'status', status)
			FROM v2_tactical_workstreams WHERE workspace_id=$1 AND archived_at IS NULL`
	case "project":
		selected.sql = `SELECT jsonb_build_object('id', id, 'workstream_id', workstream_id, 'title', title, 'description', description, 'expected_result', expected_result, 'status', status, 'expected_value', expected_value)
			FROM v2_tactical_projects WHERE workspace_id=$1 AND archived_at IS NULL`
		if parentType == "workstream" && parentID > 0 {
			selected.sql += ` AND workstream_id=$4`
			selected.args = append(selected.args, parentID)
		}
	case "department":
		searchColumn = "name"
		selected.sql = `SELECT jsonb_build_object('id', id, 'name', name, 'description', description, 'responsibility', responsibility, 'manager_user_id', manager_user_id, 'kpis', kpis_json, 'status', status)
			FROM v2_departments WHERE workspace_id=$1 AND archived_at IS NULL`
	case "task":
		selected.sql = `SELECT jsonb_build_object('id', task.id,
				'department_id', task.department_id, 'direction_name', COALESCE(direction.name, ''),
				'title', task.title, 'description', task.description,
				'expected_result', task.expected_result, 'status', task.status, 'blocked', task.blocked,
				'owner_user_id', task.owner_user_id, 'due_date', task.due_date,
				'priority_score', COALESCE(evaluation.priority_score, 0), 'priority_tier', COALESCE(evaluation.priority_tier, ''))
			FROM v2_tasks task
			LEFT JOIN v2_departments direction
				ON direction.workspace_id=task.workspace_id AND direction.id=task.department_id
			LEFT JOIN LATERAL (
				SELECT priority_score, priority_tier FROM v2_task_evaluations
				WHERE workspace_id=task.workspace_id AND task_id=task.id
				ORDER BY created_at DESC, id DESC LIMIT 1
			) evaluation ON TRUE
			WHERE task.workspace_id=$1 AND task.archived_at IS NULL`
		switch parentType {
		case "project":
			if parentID > 0 {
				selected.sql += ` AND task.project_id=$4`
				selected.args = append(selected.args, parentID)
			}
		case "workstream":
			if parentID > 0 {
				selected.sql += ` AND task.workstream_id=$4`
				selected.args = append(selected.args, parentID)
			}
		case "department":
			if parentID > 0 {
				selected.sql += ` AND task.department_id=$4`
				selected.args = append(selected.args, parentID)
			}
		}
	case "risk":
		selected.sql = `SELECT jsonb_build_object('id', id, 'entity_type', entity_type, 'entity_id', entity_id, 'title', title,
				'description', description, 'severity', severity, 'probability', probability, 'status', status, 'mitigation_plan', mitigation_plan)
			FROM v2_tactical_risks WHERE workspace_id=$1 AND archived_at IS NULL`
		if (parentType == "workstream" || parentType == "project") && parentID > 0 {
			selected.sql += ` AND entity_type=$4 AND entity_id=$5`
			selected.args = append(selected.args, parentType, parentID)
		}
	case "hypothesis":
		selected.sql = `SELECT jsonb_build_object('id', id, 'entity_type', entity_type, 'entity_id', entity_id, 'title', title,
				'statement', statement, 'expected_effect', expected_effect, 'test_method', test_method, 'status', status)
			FROM v2_tactical_hypotheses WHERE workspace_id=$1 AND archived_at IS NULL`
		if (parentType == "workstream" || parentType == "project") && parentID > 0 {
			selected.sql += ` AND entity_type=$4 AND entity_id=$5`
			selected.args = append(selected.args, parentType, parentID)
		}
	case "document":
		selected.sql = `SELECT jsonb_build_object('id', id, 'title', title, 'document_type', document_type, 'status', status, 'version', version)
			FROM strategic_documents WHERE workspace_id=$1`
	case "workspace_document":
		selected.sql = `SELECT jsonb_build_object(
				'id', id, 'parent_id', parent_id, 'title', title, 'status', status,
				'version', version, 'favorite', favorite,
				'linked_direction_ids', linked_department_ids
			)
			FROM workspace_documents WHERE workspace_id=$1 AND archived_at IS NULL`
	default:
		return nil, errors.New("invalid_agent_entity_type")
	}
	args := []any{workspaceID, status, query}
	args = append(args, selected.args...)
	orderColumn := "updated_at"
	if entityType == "document" {
		orderColumn = "generated_at"
	}
	selected.sql += `
		AND ($2='' OR status=$2)
		AND ($3='' OR lower(COALESCE(` + searchColumn + `, '')) LIKE '%' || $3 || '%')
		ORDER BY ` + orderColumn + ` DESC, id DESC
		LIMIT ` + fmt.Sprintf("%d", limit)
	items, err := queryJSONRows(ctx, s.dbx, selected.sql, args...)
	if err != nil {
		return nil, err
	}
	return map[string]any{"entity_type": entityType, "items": items}, nil
}

func (s *Service) getEntity(ctx context.Context, workspaceID int, input map[string]any) (any, error) {
	entityType := stringValue(input, "entity_type")
	entityID := intValue(input, "entity_id")
	if entityID <= 0 {
		return nil, errors.New("invalid_agent_entity_id")
	}
	queries := map[string]string{
		"strategy":   `SELECT jsonb_build_object('id', id, 'title', title, 'summary', summary, 'status', status, 'version', version) FROM v2_strategies WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL`,
		"workstream": `SELECT to_jsonb(item) FROM (SELECT id, title, description, goal, ckp, reason, status, health_status, metric_name, metric_current, metric_target FROM v2_tactical_workstreams WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL) item`,
		"project":    `SELECT to_jsonb(item) FROM (SELECT id, workstream_id, title, description, expected_result, why_needed, success_criteria, failure_criteria, metric_name, expected_value, status FROM v2_tactical_projects WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL) item`,
		"department": `SELECT to_jsonb(item) FROM (SELECT id, name, description, responsibility, manager_user_id, kpis_json, status FROM v2_departments WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL) item`,
		"task": `SELECT to_jsonb(item) FROM (
			SELECT task.id, task.department_id, COALESCE(direction.name, '') AS direction_name,
				task.title, task.description, task.expected_result, task.why_now, task.status,
				task.blocked, task.owner_user_id, task.due_date, task.completion_result
			FROM v2_tasks task
			LEFT JOIN v2_departments direction
				ON direction.workspace_id=task.workspace_id AND direction.id=task.department_id
			WHERE task.workspace_id=$1 AND task.id=$2 AND task.archived_at IS NULL
		) item`,
		"risk":       `SELECT to_jsonb(item) FROM (SELECT id, entity_type, entity_id, title, description, severity, probability, probability_value, impact_score, mitigation_plan, status FROM v2_tactical_risks WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL) item`,
		"hypothesis": `SELECT to_jsonb(item) FROM (SELECT id, entity_type, entity_id, title, statement, expected_effect, test_method, status, confidence FROM v2_tactical_hypotheses WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL) item`,
		"document": `SELECT to_jsonb(item) FROM (
			SELECT id, title, document_type, LEFT(markdown, 12000) AS markdown,
				(char_length(markdown) > 12000) AS markdown_truncated,
				status, version, generated_at
			FROM strategic_documents WHERE workspace_id=$1 AND id=$2
		) item`,
		"workspace_document": `SELECT to_jsonb(item) FROM (
			SELECT id, parent_id, title, LEFT(content, 20000) AS content,
				(char_length(content) > 20000) AS content_truncated,
				status, favorite, linked_department_ids AS linked_direction_ids,
				version, created_by, updated_by, updated_at
			FROM workspace_documents
			WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL
		) item`,
	}
	query, ok := queries[entityType]
	if !ok {
		return nil, errors.New("invalid_agent_entity_type")
	}
	var raw json.RawMessage
	if err := s.dbx.QueryRowContext(ctx, query, workspaceID, entityID).Scan(&raw); err != nil {
		return nil, err
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) priorityView(ctx context.Context, workspaceID int, input map[string]any) (any, error) {
	scopeType := stringValue(input, "scope_type")
	scopeID := intValue(input, "scope_id")
	limit := intValue(input, "limit")
	if limit <= 0 || limit > 30 {
		limit = 10
	}
	filter := ""
	args := []any{workspaceID}
	switch scopeType {
	case "workspace", "strategy":
	case "workstream":
		if scopeID <= 0 {
			return nil, errors.New("invalid_agent_scope")
		}
		filter = ` AND task.workstream_id=$2`
		args = append(args, scopeID)
	case "project":
		if scopeID <= 0 {
			return nil, errors.New("invalid_agent_scope")
		}
		filter = ` AND task.project_id=$2`
		args = append(args, scopeID)
	case "department":
		if scopeID <= 0 {
			return nil, errors.New("invalid_agent_scope")
		}
		filter = ` AND task.department_id=$2`
		args = append(args, scopeID)
	case "task":
		if scopeID <= 0 {
			return nil, errors.New("invalid_agent_scope")
		}
		filter = ` AND task.id=$2`
		args = append(args, scopeID)
	default:
		return nil, errors.New("invalid_agent_scope")
	}
	query := `
		SELECT jsonb_build_object(
			'id', task.id, 'title', task.title, 'status', task.status, 'blocked', task.blocked,
			'department_id', task.department_id, 'direction_name', COALESCE(direction.name, ''),
			'owner_user_id', task.owner_user_id, 'due_date', task.due_date,
			'priority_score', COALESCE(evaluation.priority_score, 0),
			'priority_tier', COALESCE(evaluation.priority_tier, ''),
			'priority_reason', COALESCE(evaluation.priority_reason, '')
		)
		FROM v2_tasks task
		LEFT JOIN v2_departments direction
			ON direction.workspace_id=task.workspace_id AND direction.id=task.department_id
		LEFT JOIN LATERAL (
			SELECT priority_score, priority_tier, priority_reason
			FROM v2_task_evaluations
			WHERE workspace_id=task.workspace_id AND task_id=task.id
			ORDER BY created_at DESC, id DESC LIMIT 1
		) evaluation ON TRUE
		WHERE task.workspace_id=$1 AND task.archived_at IS NULL
			AND task.status NOT IN ('done', 'archived', 'canceled')` + filter + `
		ORDER BY task.blocked ASC, COALESCE(evaluation.priority_score, 0) DESC, task.updated_at DESC
		LIMIT ` + fmt.Sprintf("%d", limit)
	items, err := queryJSONRows(ctx, s.dbx, query, args...)
	if err != nil {
		return nil, err
	}
	directions, err := s.priorityDepartments(ctx, workspaceID, scopeType, scopeID, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"scope_type":        scopeType,
		"scope_id":          scopeID,
		"ranked_tasks":      items,
		"ranked_directions": directions,
	}, nil
}

func (s *Service) priorityDepartments(
	ctx context.Context,
	workspaceID int,
	scopeType string,
	scopeID int,
	limit int,
) ([]any, error) {
	filter := ""
	args := []any{workspaceID}
	switch scopeType {
	case "workspace", "strategy":
	case "department":
		filter = ` AND direction.id=$2`
		args = append(args, scopeID)
	case "task":
		filter = ` AND direction.id=(
			SELECT task.department_id FROM v2_tasks task
			WHERE task.workspace_id=direction.workspace_id AND task.id=$2
		)`
		args = append(args, scopeID)
	case "workstream":
		// Compatibility for runs created before departments became the public direction model.
		filter = ` AND EXISTS (
			SELECT 1 FROM v2_workstream_departments link
			WHERE link.workspace_id=direction.workspace_id
				AND link.department_id=direction.id AND link.workstream_id=$2
		)`
		args = append(args, scopeID)
	case "project":
		// Compatibility for runs created before projects were removed from the public model.
		filter = ` AND EXISTS (
			SELECT 1 FROM v2_project_departments link
			WHERE link.workspace_id=direction.workspace_id
				AND link.department_id=direction.id AND link.project_id=$2
		)`
		args = append(args, scopeID)
	default:
		return nil, errors.New("invalid_agent_scope")
	}
	return queryJSONRows(ctx, s.dbx, `
		SELECT jsonb_build_object(
			'id', direction.id,
			'title', direction.name,
			'main_value', direction.responsibility,
			'description', direction.description,
			'manager_user_id', direction.manager_user_id,
			'kpis', direction.kpis_json,
			'status', direction.status,
			'priority_score', COALESCE(task_stats.priority_score, 0),
			'active_tasks', COALESCE(task_stats.active_tasks, 0),
			'blocked_tasks', COALESCE(task_stats.blocked_tasks, 0)
		)
		FROM v2_departments direction
		LEFT JOIN LATERAL (
			SELECT
				COALESCE(MAX(COALESCE(evaluation.priority_score, 0)), 0) AS priority_score,
				COUNT(*) AS active_tasks,
				COUNT(*) FILTER (WHERE task.blocked) AS blocked_tasks
			FROM v2_tasks task
			LEFT JOIN LATERAL (
				SELECT priority_score
				FROM v2_task_evaluations
				WHERE workspace_id=task.workspace_id AND task_id=task.id
				ORDER BY created_at DESC, id DESC LIMIT 1
			) evaluation ON TRUE
			WHERE task.workspace_id=direction.workspace_id
				AND task.department_id=direction.id
				AND task.archived_at IS NULL
				AND task.status IN ('free', 'in_progress')
		) task_stats ON TRUE
		WHERE direction.workspace_id=$1 AND direction.archived_at IS NULL`+filter+`
		ORDER BY COALESCE(task_stats.priority_score, 0) DESC,
			COALESCE(task_stats.active_tasks, 0) DESC,
			direction.sort_order ASC, direction.updated_at DESC
		LIMIT `+fmt.Sprintf("%d", limit), args...)
}

// Legacy project ranking remains available only for old persisted runs. New tool schemas
// never request projects and priorityView intentionally excludes this data.
func (s *Service) priorityProjects(
	ctx context.Context,
	workspaceID int,
	scopeType string,
	scopeID int,
	limit int,
) ([]any, error) {
	filter := ""
	args := []any{workspaceID}
	switch scopeType {
	case "workspace", "strategy":
	case "workstream":
		filter = ` AND project.workstream_id=$2`
		args = append(args, scopeID)
	case "project":
		filter = ` AND project.id=$2`
		args = append(args, scopeID)
	case "department":
		filter = ` AND EXISTS (
			SELECT 1 FROM v2_project_departments link
			WHERE link.workspace_id=project.workspace_id
				AND link.project_id=project.id AND link.department_id=$2
		)`
		args = append(args, scopeID)
	case "task":
		filter = ` AND project.id=(
			SELECT task.project_id FROM v2_tasks task
			WHERE task.workspace_id=project.workspace_id AND task.id=$2
		)`
		args = append(args, scopeID)
	default:
		return nil, errors.New("invalid_agent_scope")
	}
	return queryJSONRows(ctx, s.dbx, `
		SELECT jsonb_build_object(
			'id', project.id, 'title', project.title, 'status', project.status,
			'workstream_id', project.workstream_id,
			'priority_score', COALESCE(evaluation.priority_score, 0),
			'priority_tier', COALESCE(evaluation.priority_tier, ''),
			'priority_reason', COALESCE(evaluation.priority_reason, ''),
			'strategic_relevance', COALESCE(evaluation.strategic_relevance, 0),
			'expected_impact', COALESCE(evaluation.expected_impact, 0),
			'confidence', COALESCE(evaluation.confidence, 0)
		)
		FROM v2_tactical_projects project
		LEFT JOIN LATERAL (
			SELECT priority_score, priority_tier, priority_reason,
				strategic_relevance, expected_impact, confidence
			FROM v2_tactical_entity_evaluations
			WHERE workspace_id=project.workspace_id
				AND entity_type='project' AND entity_id=project.id
			ORDER BY created_at DESC, id DESC LIMIT 1
		) evaluation ON TRUE
		WHERE project.workspace_id=$1 AND project.archived_at IS NULL
			AND project.status NOT IN ('completed', 'archived', 'canceled')`+filter+`
		ORDER BY COALESCE(evaluation.priority_score, 0) DESC, project.updated_at DESC
		LIMIT `+fmt.Sprintf("%d", limit), args...)
}

// Legacy tactical-workstream ranking remains isolated for old persisted runs. Public
// directions are departments and are ranked by priorityDepartments above.
func (s *Service) priorityDirections(
	ctx context.Context,
	workspaceID int,
	scopeType string,
	scopeID int,
	limit int,
) ([]any, error) {
	filter := ""
	args := []any{workspaceID}
	switch scopeType {
	case "workspace", "strategy":
	case "workstream":
		filter = ` AND direction.id=$2`
		args = append(args, scopeID)
	case "project":
		filter = ` AND direction.id=(
			SELECT project.workstream_id FROM v2_tactical_projects project
			WHERE project.workspace_id=direction.workspace_id AND project.id=$2
		)`
		args = append(args, scopeID)
	case "department":
		filter = ` AND EXISTS (
			SELECT 1 FROM v2_workstream_departments link
			WHERE link.workspace_id=direction.workspace_id
				AND link.workstream_id=direction.id AND link.department_id=$2
		)`
		args = append(args, scopeID)
	case "task":
		filter = ` AND direction.id=(
			SELECT task.workstream_id FROM v2_tasks task
			WHERE task.workspace_id=direction.workspace_id AND task.id=$2
		)`
		args = append(args, scopeID)
	default:
		return nil, errors.New("invalid_agent_scope")
	}
	return queryJSONRows(ctx, s.dbx, `
		SELECT jsonb_build_object(
			'id', direction.id, 'title', direction.title, 'status', direction.status,
			'priority_score', COALESCE(evaluation.priority_score, 0),
			'priority_tier', COALESCE(evaluation.priority_tier, ''),
			'priority_reason', COALESCE(evaluation.priority_reason, ''),
			'strategic_relevance', COALESCE(evaluation.strategic_relevance, 0),
			'expected_impact', COALESCE(evaluation.expected_impact, 0),
			'confidence', COALESCE(evaluation.confidence, 0)
		)
		FROM v2_tactical_workstreams direction
		LEFT JOIN LATERAL (
			SELECT priority_score, priority_tier, priority_reason,
				strategic_relevance, expected_impact, confidence
			FROM v2_tactical_entity_evaluations
			WHERE workspace_id=direction.workspace_id
				AND entity_type='workstream' AND entity_id=direction.id
			ORDER BY created_at DESC, id DESC LIMIT 1
		) evaluation ON TRUE
		WHERE direction.workspace_id=$1 AND direction.archived_at IS NULL
			AND direction.status NOT IN ('completed', 'archived', 'canceled')`+filter+`
		ORDER BY COALESCE(evaluation.priority_score, 0) DESC, direction.updated_at DESC
		LIMIT `+fmt.Sprintf("%d", limit), args...)
}

func queryJSONRows(ctx context.Context, dbx *sql.DB, query string, args ...any) ([]any, error) {
	rows, err := dbx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []any{}
	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item any
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func draftChangesFromInterruptions(items []RuntimeInterruption) []tactics.TacticsDraftChange {
	result := make([]tactics.TacticsDraftChange, 0, len(items))
	for _, item := range items {
		change, ok := draftChange(item.ToolName, item.Arguments)
		if ok {
			result = append(result, change)
		}
	}
	return result
}

func draftChangesFromApprovals(items []Approval) []tactics.TacticsDraftChange {
	result := []tactics.TacticsDraftChange{}
	for _, item := range items {
		if item.Status != "pending" && item.Status != "approved" {
			continue
		}
		var arguments map[string]any
		if json.Unmarshal(item.Arguments, &arguments) != nil {
			continue
		}
		change, ok := draftChange(item.ToolName, arguments)
		if ok {
			result = append(result, change)
		}
	}
	return result
}

func draftChange(toolName string, input map[string]any) (tactics.TacticsDraftChange, bool) {
	change := tactics.TacticsDraftChange{
		Apply: true, Operation: "create", Title: stringValue(input, "title"),
		Description: stringValue(input, "description"), DraftKey: stringValue(input, "draft_key"),
	}
	if entityID := intValue(input, "existing_entity_id"); entityID > 0 {
		change.Operation = "update"
		change.EntityID = intPointer(entityID)
	}
	switch toolName {
	case "propose_direction":
		change.EntityType = tactics.EntityWorkstream
		change.Goal = stringValue(input, "expected_result")
		change.ExpectedResult = change.Goal
		change.CKP = stringValue(input, "ckp")
		change.Reason = stringValue(input, "rationale")
		change.LeadDepartmentID = intValue(input, "lead_department_id")
		change.ParticipantDepartmentIDs = intSlice(input["participant_department_ids"])
		change.Metrics = tacticMetrics(input["metrics"])
	case "propose_project":
		change.EntityType = tactics.EntityProject
		change.ParentEntityType = tactics.EntityWorkstream
		change.ParentEntityID = intPointer(intValue(input, "direction_id"))
		change.ParentDraftKey = stringValue(input, "parent_draft_key")
		change.WorkstreamID = change.ParentEntityID
		change.ExpectedResult = stringValue(input, "expected_result")
		change.WhyNeeded = stringValue(input, "why_needed")
		change.SuccessCriteria = stringValue(input, "success_criteria")
		change.FailureCriteria = stringValue(input, "failure_criteria")
		change.ExpectedValue = stringValue(input, "expected_value")
		change.LeadDepartmentID = intValue(input, "department_id")
		change.DepartmentID = intPointer(change.LeadDepartmentID)
		change.Metrics = tacticMetrics([]any{input["metric"]})
		if len(change.Metrics) > 0 {
			change.MetricName = change.Metrics[0].Name
		}
	case "propose_task":
		change.EntityType = tactics.EntityTask
		change.ParentEntityType = tactics.EntityDepartment
		change.ParentEntityID = intPointer(intValue(input, "direction_id"))
		change.ParentDraftKey = stringValue(input, "direction_draft_key")
		change.DepartmentID = change.ParentEntityID
		change.ExpectedResult = stringValue(input, "expected_result")
		change.WhyNow = stringValue(input, "why_now")
		change.OwnerUserID = intPointer(intValue(input, "owner_user_id"))
		change.OwnerDeferred = boolValue(input, "owner_deferred")
		change.DueDate = stringValue(input, "due_date")
		change.DueDateDeferred = boolValue(input, "due_date_deferred")
		change.BlockingTaskIDs = intSlice(input["blocker_task_ids"])
	case "propose_risk":
		change.EntityType = tactics.EntityRisk
		change.ParentEntityType = stringValue(input, "entity_type")
		change.ParentEntityID = intPointer(intValue(input, "entity_id"))
		change.Severity = stringValue(input, "severity")
		change.Probability = stringValue(input, "probability")
		change.LeadingIndicators = stringValue(input, "leading_indicators")
		change.MitigationPlan = stringValue(input, "mitigation_plan")
		change.ContingencyPlan = stringValue(input, "contingency_plan")
		change.OwnerUserID = intPointer(intValue(input, "owner_user_id"))
		change.CoverageStatus = tactics.CoverageUncovered
	case "propose_hypothesis":
		change.EntityType = tactics.EntityHypothesis
		change.ParentEntityType = stringValue(input, "entity_type")
		change.ParentEntityID = intPointer(intValue(input, "entity_id"))
		change.Statement = stringValue(input, "statement")
		change.ExpectedEffect = stringValue(input, "expected_effect")
		change.TestMethod = stringValue(input, "test_method")
		if successSignal := stringValue(input, "success_signal"); successSignal != "" {
			change.TestMethod += "\n\nКритерий решения: " + successSignal
		}
		change.OwnerUserID = intPointer(intValue(input, "owner_user_id"))
		change.HypothesisStatus = "draft"
	case "propose_department":
		change.EntityType = tactics.EntityDepartment
		change.Title = stringValue(input, "name")
		change.ExpectedResult = stringValue(input, "responsibility")
		change.OwnerUserID = intPointer(intValue(input, "manager_user_id"))
		change.MemberUserIDs = intSlice(input["member_user_ids"])
		change.Metrics = tacticMetrics(input["kpis"])
	case "propose_strategy_review":
		change.Operation = "submit"
		change.EntityType = "strategy_review"
		change.Title = stringValue(input, "strategic_goal")
		change.Description = stringValue(input, "strategic_logic")
		change.CurrentState = stringValue(input, "current_state")
		change.TargetState = stringValue(input, "target_state")
		change.EconomicEngine = stringValue(input, "economic_engine")
		change.KeyMetric = stringValue(input, "key_metric")
		change.StrategicLogic = stringValue(input, "strategic_logic")
		change.DeliberateNonPriorities = stringValue(input, "deliberate_non_priorities")
		change.RisksAndAssumptions = stringValue(input, "risks_and_assumptions")
	case "propose_document":
		change.EntityType = "workspace_document"
		change.Description = stringValue(input, "content")
	case "update_document":
		change.Operation = "update"
		change.EntityType = "workspace_document"
		change.EntityID = intPointer(intValue(input, "document_id"))
		change.Description = stringValue(input, "content")
	case "complete_task":
		change.Operation = "complete"
		change.EntityType = "task_completion"
		change.EntityID = intPointer(intValue(input, "task_id"))
		change.Title = stringValue(input, "task_title")
		change.Description = stringValue(input, "result")
	default:
		return tactics.TacticsDraftChange{}, false
	}
	return change, change.Title != ""
}

func isApprovalTool(toolName string) bool {
	return strings.HasPrefix(toolName, "propose_") || toolName == "update_document" || toolName == "complete_task"
}

func tacticMetrics(value any) []tactics.TacticMetric {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := []tactics.TacticMetric{}
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		metric := tactics.TacticMetric{
			Name: stringValue(item, "name"), Current: stringValue(item, "current"),
			Target: stringValue(item, "target"), Unit: stringValue(item, "unit"),
			BetterDirection: stringValue(item, "better_direction"),
			TargetDate:      stringValue(item, "target_date"),
		}
		if metric.Name != "" && metric.Target != "" {
			result = append(result, metric)
		}
	}
	return result
}

func stringValue(input map[string]any, key string) string {
	value, _ := input[key].(string)
	return strings.TrimSpace(value)
}

func boolValue(input map[string]any, key string) bool {
	value, _ := input[key].(bool)
	return value
}

func intPointer(value int) *int {
	if value <= 0 {
		return nil
	}
	return &value
}

func intSlice(value any) []int {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]int, 0, len(items))
	for _, item := range items {
		switch value := item.(type) {
		case float64:
			if value > 0 {
				result = append(result, int(value))
			}
		case int:
			if value > 0 {
				result = append(result, value)
			}
		case json.Number:
			if parsed, err := value.Int64(); err == nil && parsed > 0 {
				result = append(result, int(parsed))
			}
		case string:
			if parsed := intValue(map[string]any{"value": value}, "value"); parsed > 0 {
				result = append(result, parsed)
			}
		}
	}
	return result
}
