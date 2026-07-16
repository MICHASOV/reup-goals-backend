package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
)

var (
	ErrNoActiveCourse = errors.New("no_active_course")
	ErrNoTacticalPlan = errors.New("no_tactical_plan")
	ErrForbidden      = errors.New("forbidden")
)

type Store struct {
	dbx *sql.DB
}

func NewStore(dbx *sql.DB) *Store {
	return &Store{dbx: dbx}
}

func (s *Store) Overview(ctx context.Context, workspaceID int) (OverviewResponse, error) {
	ctxData, err := s.currentContext(ctx, workspaceID)
	if errors.Is(err, ErrNoActiveCourse) {
		return OverviewResponse{Workstreams: []WorkstreamSummary{}, Tasks: []Task{}, Reason: "no_active_course", Message: "Сначала нужен активный курс."}, nil
	}
	if errors.Is(err, ErrNoTacticalPlan) {
		return OverviewResponse{Course: ctxData.Course, Workstreams: []WorkstreamSummary{}, Tasks: []Task{}, Reason: "no_tactical_plan", Message: "Сначала соберите тактику."}, nil
	}
	if err != nil {
		return OverviewResponse{}, err
	}

	workstreams, err := s.workstreams(ctx, workspaceID, ctxData.Plan.ID)
	if err != nil {
		return OverviewResponse{}, err
	}
	tasks, err := s.List(ctx, workspaceID, ListFilter{IncludeArchived: false})
	if err != nil {
		return OverviewResponse{}, err
	}
	attachTasks(workstreams, tasks)

	return OverviewResponse{Course: ctxData.Course, TacticalPlan: ctxData.Plan, Workstreams: workstreams, Tasks: tasks}, nil
}

func (s *Store) Workstream(ctx context.Context, workspaceID int, workstreamID int) (WorkstreamResponse, error) {
	ctxData, err := s.currentContext(ctx, workspaceID)
	if errors.Is(err, ErrNoActiveCourse) {
		return WorkstreamResponse{Reason: "no_active_course", Message: "Сначала нужен активный курс."}, nil
	}
	if errors.Is(err, ErrNoTacticalPlan) {
		return WorkstreamResponse{Course: ctxData.Course, Reason: "no_tactical_plan", Message: "Сначала соберите тактику."}, nil
	}
	if err != nil {
		return WorkstreamResponse{}, err
	}

	workstreamRef, err := s.workstreamByID(ctx, workspaceID, workstreamID)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkstreamResponse{}, ErrForbidden
	}
	if err != nil {
		return WorkstreamResponse{}, err
	}
	if workstreamRef.TacticalPlanID != ctxData.Plan.ID {
		return WorkstreamResponse{}, ErrForbidden
	}
	workstream, err := s.workstreamSummaryByID(ctx, workspaceID, workstreamID)
	if err != nil {
		return WorkstreamResponse{}, err
	}

	projects, err := s.projects(ctx, workspaceID, workstreamID)
	if err != nil {
		return WorkstreamResponse{}, err
	}
	risks, err := s.risks(ctx, workspaceID, ctxData.Plan.ID, workstreamID, projects)
	if err != nil {
		return WorkstreamResponse{}, err
	}
	opportunities, err := s.opportunities(ctx, workspaceID, ctxData.Plan.ID, workstreamID, projects)
	if err != nil {
		return WorkstreamResponse{}, err
	}
	tasks, err := s.List(ctx, workspaceID, ListFilter{WorkstreamID: &workstreamID, IncludeArchived: true})
	if err != nil {
		return WorkstreamResponse{}, err
	}

	workstream.Projects = projects
	workstream.Risks = risks
	workstream.Opportunities = opportunities
	workstream.TasksSummary = summarize(tasks)
	workstream.TopTasks = topTasks(tasks, 3)

	return WorkstreamResponse{
		Course:        ctxData.Course,
		TacticalPlan:  ctxData.Plan,
		Workstream:    &workstream,
		Projects:      projects,
		Risks:         risks,
		Opportunities: opportunities,
		Tasks:         tasks,
		TasksSummary:  summarize(tasks),
	}, nil
}

type ListFilter struct {
	Status          *string
	WorkstreamID    *int
	ProjectID       *int
	Query           string
	IncludeArchived bool
}

func (s *Store) List(ctx context.Context, workspaceID int, filter ListFilter) ([]Task, error) {
	if err := s.archiveCompletedTasks(ctx, workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT
			id, workspace_id, course_id, tactical_plan_id, workstream_id, project_id, risk_id,
			opportunity_id, title, description, expected_result, success_criteria, why_now,
			status, blocked, backlog_category, priority_order, manual_priority_score, manual_priority_tier, owner_user_id,
			due_date::TEXT, source_type, source_id, created_by, updated_by, created_at,
			updated_at, started_at, completed_at, archived_at
		FROM v2_tasks
		WHERE workspace_id=$1
			AND ($2::TEXT IS NULL OR status=$2)
			AND ($3::INTEGER IS NULL OR workstream_id=$3)
			AND ($4::INTEGER IS NULL OR project_id=$4)
			AND ($5::BOOLEAN = TRUE OR archived_at IS NULL)
			AND ($6::TEXT = '' OR title ILIKE '%' || $6 || '%' OR description ILIKE '%' || $6 || '%'
				OR expected_result ILIKE '%' || $6 || '%' OR success_criteria ILIKE '%' || $6 || '%' OR why_now ILIKE '%' || $6 || '%')
		ORDER BY COALESCE(priority_order, 9999), updated_at DESC, id DESC
	`, workspaceID, nullableString(filter.Status), nullableInt(filter.WorkstreamID), nullableInt(filter.ProjectID), filter.IncludeArchived, strings.TrimSpace(filter.Query))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := []Task{}
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	decorated, err := s.decorateTasks(ctx, workspaceID, tasks)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(decorated, func(i, j int) bool {
		if decorated[i].EffectivePriorityScore != decorated[j].EffectivePriorityScore {
			return decorated[i].EffectivePriorityScore > decorated[j].EffectivePriorityScore
		}
		leftOrder := 9999
		rightOrder := 9999
		if decorated[i].PriorityOrder != nil {
			leftOrder = *decorated[i].PriorityOrder
		}
		if decorated[j].PriorityOrder != nil {
			rightOrder = *decorated[j].PriorityOrder
		}
		return leftOrder < rightOrder
	})
	return decorated, nil
}

func (s *Store) archiveCompletedTasks(ctx context.Context, workspaceID int) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_tasks
		SET status=$2, archived_at=NOW(), updated_at=NOW()
		WHERE workspace_id=$1 AND status=$3 AND archived_at IS NULL
			AND completed_at IS NOT NULL AND completed_at <= NOW() - INTERVAL '7 days'
	`, workspaceID, StatusArchived, StatusDone)
	return err
}

func (s *Store) Get(ctx context.Context, workspaceID int, taskID int) (Task, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT
			id, workspace_id, course_id, tactical_plan_id, workstream_id, project_id, risk_id,
			opportunity_id, title, description, expected_result, success_criteria, why_now,
			status, blocked, backlog_category, priority_order, manual_priority_score, manual_priority_tier, owner_user_id,
			due_date::TEXT, source_type, source_id, created_by, updated_by, created_at,
			updated_at, started_at, completed_at, archived_at
		FROM v2_tasks
		WHERE id=$1 AND workspace_id=$2
	`, taskID, workspaceID)
	task, err := scanTask(row)
	if err != nil {
		return Task{}, err
	}
	items, err := s.decorateTasks(ctx, workspaceID, []Task{task})
	if err != nil {
		return Task{}, err
	}
	return items[0], nil
}

func (s *Store) Create(ctx context.Context, workspaceID int, userID int, input TaskInput) (Task, error) {
	input.normalize()
	if input.WorkstreamID <= 0 || input.Title == nil || strings.TrimSpace(*input.Title) == "" {
		return Task{}, ErrForbidden
	}
	status := StatusFree
	if input.Status != nil && *input.Status != "" {
		status = strings.TrimSpace(*input.Status)
	}
	if !ValidStatus(status) {
		return Task{}, ErrForbidden
	}
	blocked := input.Blocked != nil && *input.Blocked
	backlogCategory := normalizeBacklogCategory(valueOrEmpty(input.BacklogCategory))
	if input.BacklogCategory != nil && strings.TrimSpace(*input.BacklogCategory) != "" && backlogCategory == "" {
		return Task{}, ErrForbidden
	}

	sourceType := SourceManual
	if input.SourceType != nil && strings.TrimSpace(*input.SourceType) != "" {
		sourceType = strings.TrimSpace(*input.SourceType)
	}
	if !ValidSourceType(sourceType) {
		return Task{}, ErrForbidden
	}

	ctxData, err := s.currentContext(ctx, workspaceID)
	if err != nil {
		return Task{}, err
	}
	workstream, err := s.workstreamByID(ctx, workspaceID, input.WorkstreamID)
	if err != nil {
		return Task{}, ErrForbidden
	}
	if workstream.TacticalPlanID != ctxData.Plan.ID {
		return Task{}, ErrForbidden
	}
	if err := s.validateLinks(ctx, workspaceID, workstream, input); err != nil {
		return Task{}, err
	}
	secondaryWorkstreamIDs, err := s.validateSecondaryWorkstreams(ctx, workspaceID, ctxData.Plan.ID, workstream.ID, input.SecondaryWorkstreamIDs)
	if err != nil {
		return Task{}, err
	}

	sortOrder, err := s.nextPriorityOrder(ctx, workspaceID, input.WorkstreamID, status)
	if err != nil {
		return Task{}, err
	}
	if input.PriorityOrder == nil {
		input.PriorityOrder = &sortOrder
	}

	row := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_tasks (
			workspace_id, course_id, tactical_plan_id, workstream_id, project_id, risk_id,
			opportunity_id, title, description, expected_result, success_criteria, why_now,
			status, blocked, backlog_category, priority_order, owner_user_id, due_date, source_type, source_id, created_by, updated_by,
			started_at, completed_at, archived_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18::DATE, $19, $20, $21, $21,
			CASE WHEN $13=$22 THEN NOW() ELSE NULL END,
			CASE WHEN $13=$23 THEN NOW() ELSE NULL END,
			CASE WHEN $13=$24 THEN NOW() ELSE NULL END
		)
		RETURNING
			id, workspace_id, course_id, tactical_plan_id, workstream_id, project_id, risk_id,
			opportunity_id, title, description, expected_result, success_criteria, why_now,
			status, blocked, backlog_category, priority_order, manual_priority_score, manual_priority_tier, owner_user_id,
			due_date::TEXT, source_type, source_id, created_by, updated_by, created_at,
			updated_at, started_at, completed_at, archived_at
	`, workspaceID, ctxData.Course.ID, ctxData.Plan.ID, workstream.ID, nullableInt(input.ProjectID),
		nullableInt(input.RiskID), nullableInt(input.OpportunityID), strings.TrimSpace(*input.Title),
		valueOrEmpty(input.Description), valueOrEmpty(input.ExpectedResult), valueOrEmpty(input.SuccessCriteria),
		valueOrEmpty(input.WhyNow), status, blocked, backlogCategory, nullableInt(input.PriorityOrder), nullableInt(input.OwnerUserID),
		nullableString(input.DueDate), sourceType, nullableInt(input.SourceID), userID,
		StatusInProgress, StatusDone, StatusArchived)

	task, err := scanTask(row)
	if err != nil {
		return Task{}, err
	}
	if err := s.replaceSecondaryWorkstreams(ctx, workspaceID, task.ID, secondaryWorkstreamIDs); err != nil {
		return Task{}, err
	}
	items, err := s.decorateTasks(ctx, workspaceID, []Task{task})
	if err != nil {
		return Task{}, err
	}
	return items[0], nil
}

func (s *Store) Update(ctx context.Context, workspaceID int, userID int, taskID int, input TaskInput) (Task, error) {
	current, err := s.Get(ctx, workspaceID, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrForbidden
	}
	if err != nil {
		return Task{}, err
	}

	title := current.Title
	if input.Title != nil {
		title = strings.TrimSpace(*input.Title)
	}
	if title == "" {
		return Task{}, ErrForbidden
	}
	description := current.Description
	if input.Description != nil {
		description = strings.TrimSpace(*input.Description)
	}
	expectedResult := current.ExpectedResult
	if input.ExpectedResult != nil {
		expectedResult = strings.TrimSpace(*input.ExpectedResult)
	}
	successCriteria := current.SuccessCriteria
	if input.SuccessCriteria != nil {
		successCriteria = strings.TrimSpace(*input.SuccessCriteria)
	}
	whyNow := current.WhyNow
	if input.WhyNow != nil {
		whyNow = strings.TrimSpace(*input.WhyNow)
	}
	blocked := current.Blocked
	if input.Blocked != nil {
		blocked = *input.Blocked
	}
	backlogCategory := current.BacklogCategory
	if input.BacklogCategory != nil {
		backlogCategory = normalizeBacklogCategory(*input.BacklogCategory)
		if strings.TrimSpace(*input.BacklogCategory) != "" && backlogCategory == "" {
			return Task{}, ErrForbidden
		}
	}
	projectID := current.ProjectID
	if input.ClearProject {
		projectID = nil
	} else if input.ProjectID != nil {
		projectID = input.ProjectID
	}
	ownerUserID := current.OwnerUserID
	if input.OwnerUserID != nil {
		ownerUserID = input.OwnerUserID
	}
	dueDate := current.DueDate
	if input.DueDate != nil {
		trimmed := strings.TrimSpace(*input.DueDate)
		if trimmed == "" {
			dueDate = nil
		} else {
			dueDate = &trimmed
		}
	}

	workstream, err := s.workstreamByID(ctx, workspaceID, current.WorkstreamID)
	if err != nil {
		return Task{}, ErrForbidden
	}
	input.WorkstreamID = current.WorkstreamID
	input.ProjectID = projectID
	input.RiskID = current.RiskID
	input.OpportunityID = current.OpportunityID
	if err := s.validateLinks(ctx, workspaceID, workstream, input); err != nil {
		return Task{}, err
	}
	secondaryWorkstreamIDs := current.SecondaryWorkstreamIDs
	if input.SecondaryWorkstreamIDs != nil {
		secondaryWorkstreamIDs, err = s.validateSecondaryWorkstreams(ctx, workspaceID, current.TacticalPlanID, workstream.ID, input.SecondaryWorkstreamIDs)
		if err != nil {
			return Task{}, err
		}
	}

	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_tasks
		SET title=$1,
			description=$2,
			expected_result=$3,
			success_criteria=$4,
			why_now=$5,
			blocked=$6,
			backlog_category=$7,
			project_id=$8,
			owner_user_id=$9,
			due_date=$10::DATE,
			updated_by=$11,
			updated_at=NOW()
		WHERE id=$12 AND workspace_id=$13
		RETURNING
			id, workspace_id, course_id, tactical_plan_id, workstream_id, project_id, risk_id,
			opportunity_id, title, description, expected_result, success_criteria, why_now,
			status, blocked, backlog_category, priority_order, manual_priority_score, manual_priority_tier, owner_user_id,
			due_date::TEXT, source_type, source_id, created_by, updated_by, created_at,
			updated_at, started_at, completed_at, archived_at
	`, title, description, expectedResult, successCriteria, whyNow, blocked, backlogCategory, nullableInt(projectID), nullableInt(ownerUserID), nullableString(dueDate), userID, taskID, workspaceID)

	task, err := scanTask(row)
	if err != nil {
		return Task{}, err
	}
	if input.SecondaryWorkstreamIDs != nil {
		if err := s.replaceSecondaryWorkstreams(ctx, workspaceID, task.ID, secondaryWorkstreamIDs); err != nil {
			return Task{}, err
		}
	}
	items, err := s.decorateTasks(ctx, workspaceID, []Task{task})
	if err != nil {
		return Task{}, err
	}
	return items[0], nil
}

func (s *Store) UpdateStatus(ctx context.Context, workspaceID int, userID int, taskID int, status string, priorityOrder *int) (Task, error) {
	status = strings.TrimSpace(status)
	if !ValidStatus(status) {
		return Task{}, ErrForbidden
	}
	if _, err := s.Get(ctx, workspaceID, taskID); errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrForbidden
	} else if err != nil {
		return Task{}, err
	}

	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_tasks
		SET status=$1,
			priority_order=COALESCE($2, priority_order),
			updated_by=$3,
			started_at=CASE WHEN $1=$4 THEN COALESCE(started_at, NOW()) ELSE started_at END,
			completed_at=CASE WHEN $1=$5 THEN COALESCE(completed_at, NOW()) ELSE completed_at END,
			archived_at=CASE WHEN $1=$6 THEN COALESCE(archived_at, NOW()) ELSE archived_at END,
			updated_at=NOW()
		WHERE id=$7 AND workspace_id=$8
		RETURNING
			id, workspace_id, course_id, tactical_plan_id, workstream_id, project_id, risk_id,
			opportunity_id, title, description, expected_result, success_criteria, why_now,
			status, blocked, backlog_category, priority_order, manual_priority_score, manual_priority_tier, owner_user_id,
			due_date::TEXT, source_type, source_id, created_by, updated_by, created_at,
			updated_at, started_at, completed_at, archived_at
	`, status, nullableInt(priorityOrder), userID, StatusInProgress, StatusDone, StatusArchived, taskID, workspaceID)

	task, err := scanTask(row)
	if err != nil {
		return Task{}, err
	}
	items, err := s.decorateTasks(ctx, workspaceID, []Task{task})
	if err != nil {
		return Task{}, err
	}
	return items[0], nil
}

func (s *Store) SetManualPriority(ctx context.Context, workspaceID int, userID int, taskID int, score *int) (Task, error) {
	if score != nil && (*score < 0 || *score > 100) {
		return Task{}, ErrForbidden
	}
	tier := ""
	if score != nil {
		tier = PriorityTier(*score)
	}
	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_tasks
		SET manual_priority_score=$1,
			manual_priority_tier=NULLIF($2, ''),
			updated_by=$3,
			updated_at=NOW()
		WHERE id=$4 AND workspace_id=$5
		RETURNING
			id, workspace_id, course_id, tactical_plan_id, workstream_id, project_id, risk_id,
			opportunity_id, title, description, expected_result, success_criteria, why_now,
			status, blocked, backlog_category, priority_order, manual_priority_score, manual_priority_tier, owner_user_id,
			due_date::TEXT, source_type, source_id, created_by, updated_by, created_at,
			updated_at, started_at, completed_at, archived_at
	`, nullableInt(score), tier, userID, taskID, workspaceID)
	task, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrForbidden
	}
	if err != nil {
		return Task{}, err
	}
	items, err := s.decorateTasks(ctx, workspaceID, []Task{task})
	if err != nil {
		return Task{}, err
	}
	return items[0], nil
}

type currentContextData struct {
	Course *CourseSummary
	Plan   *TacticalPlanSummary
}

func (s *Store) currentContext(ctx context.Context, workspaceID int) (currentContextData, error) {
	course, err := s.activeCourse(ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return currentContextData{}, ErrNoActiveCourse
	}
	if err != nil {
		return currentContextData{}, err
	}
	plan, err := s.currentPlan(ctx, workspaceID, course.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return currentContextData{Course: &course}, ErrNoTacticalPlan
	}
	if err != nil {
		return currentContextData{}, err
	}
	return currentContextData{Course: &course, Plan: &plan}, nil
}

func (s *Store) activeCourse(ctx context.Context, workspaceID int) (CourseSummary, error) {
	var course CourseSummary
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id, direction, strategic_goal, key_metric, success_criterion
		FROM v2_courses
		WHERE workspace_id=$1 AND status='active' AND archived_at IS NULL
		ORDER BY activated_at DESC NULLS LAST, created_at DESC
		LIMIT 1
	`, workspaceID).Scan(&course.ID, &course.Direction, &course.StrategicGoal, &course.KeyMetric, &course.SuccessCriterion)
	return course, err
}

func (s *Store) currentPlan(ctx context.Context, workspaceID int, courseID int) (TacticalPlanSummary, error) {
	var plan TacticalPlanSummary
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id, strategy_id, course_id
		FROM v2_tactical_plans
		WHERE workspace_id=$1
			AND archived_at IS NULL
			AND (course_id=$2 OR course_id IS NULL)
		ORDER BY CASE status WHEN 'active' THEN 1 WHEN 'draft' THEN 2 ELSE 3 END, updated_at DESC
		LIMIT 1
	`, workspaceID, courseID).Scan(&plan.ID, &plan.StrategyID, &plan.CourseID)
	return plan, err
}

func (s *Store) workstreams(ctx context.Context, workspaceID int, planID int) ([]WorkstreamSummary, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, title, description, goal, ckp, reason, closes_risk, metric_name,
			metric_current, metric_target, metrics_json, health_status
		FROM v2_tactical_workstreams
		WHERE workspace_id=$1 AND tactical_plan_id=$2 AND archived_at IS NULL
		ORDER BY sort_order ASC, id ASC
	`, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	workstreams := []WorkstreamSummary{}
	for rows.Next() {
		var item WorkstreamSummary
		var metricsJSON []byte
		if err := rows.Scan(&item.ID, &item.Title, &item.Description, &item.Goal, &item.CKP, &item.Reason,
			&item.ClosesRisk, &item.MetricName, &item.MetricCurrent, &item.MetricTarget, &metricsJSON, &item.HealthStatus); err != nil {
			return nil, err
		}
		item.Metrics = decodeTacticMetrics(metricsJSON, item.MetricName, item.MetricCurrent, item.MetricTarget)
		item.Projects = []Project{}
		item.Risks = []Risk{}
		item.Opportunities = []Opportunity{}
		item.TopTasks = []Task{}
		workstreams = append(workstreams, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range workstreams {
		projects, err := s.projects(ctx, workspaceID, workstreams[i].ID)
		if err != nil {
			return nil, err
		}
		risks, err := s.risks(ctx, workspaceID, planID, workstreams[i].ID, projects)
		if err != nil {
			return nil, err
		}
		opportunities, err := s.opportunities(ctx, workspaceID, planID, workstreams[i].ID, projects)
		if err != nil {
			return nil, err
		}
		workstreams[i].Projects = projects
		workstreams[i].Risks = risks
		workstreams[i].Opportunities = opportunities
	}

	return workstreams, nil
}

type workstreamRef struct {
	ID             int
	TacticalPlanID int
}

func (s *Store) workstreamByID(ctx context.Context, workspaceID int, workstreamID int) (workstreamRef, error) {
	var ref workstreamRef
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id, tactical_plan_id
		FROM v2_tactical_workstreams
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, workstreamID, workspaceID).Scan(&ref.ID, &ref.TacticalPlanID)
	return ref, err
}

func (s *Store) workstreamSummaryByID(ctx context.Context, workspaceID int, workstreamID int) (WorkstreamSummary, error) {
	var item WorkstreamSummary
	row := s.dbx.QueryRowContext(ctx, `
		SELECT id, title, description, goal, ckp, reason, closes_risk, metric_name,
			metric_current, metric_target, metrics_json, health_status
		FROM v2_tactical_workstreams
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, workstreamID, workspaceID)
	var metricsJSON []byte
	err := row.Scan(
		&item.ID, &item.Title, &item.Description, &item.Goal, &item.CKP, &item.Reason,
		&item.ClosesRisk, &item.MetricName, &item.MetricCurrent, &item.MetricTarget, &metricsJSON, &item.HealthStatus,
	)
	if err != nil {
		return WorkstreamSummary{}, err
	}
	item.Metrics = decodeTacticMetrics(metricsJSON, item.MetricName, item.MetricCurrent, item.MetricTarget)
	return item, nil
}

func (s *Store) projects(ctx context.Context, workspaceID int, workstreamID int) ([]Project, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, workstream_id, title, description, why_needed, success_criteria, failure_criteria, metric_name, expected_value, status
		FROM v2_tactical_projects
		WHERE workspace_id=$1 AND workstream_id=$2 AND archived_at IS NULL
		ORDER BY sort_order ASC, id ASC
	`, workspaceID, workstreamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	projects := []Project{}
	for rows.Next() {
		var item Project
		if err := rows.Scan(&item.ID, &item.WorkstreamID, &item.Title, &item.Description, &item.WhyNeeded,
			&item.SuccessCriteria, &item.FailureCriteria, &item.MetricName, &item.ExpectedValue, &item.Status); err != nil {
			return nil, err
		}
		projects = append(projects, item)
	}
	return projects, rows.Err()
}

func (s *Store) risks(ctx context.Context, workspaceID int, planID int, workstreamID int, projects []Project) ([]Risk, error) {
	projectIDs := map[int]bool{}
	for _, project := range projects {
		projectIDs[project.ID] = true
	}
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, tactical_plan_id, entity_type, entity_id, title, description, severity, probability, status, coverage_status
		FROM v2_tactical_risks
		WHERE workspace_id=$1 AND tactical_plan_id=$2 AND archived_at IS NULL
		ORDER BY id ASC
	`, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	risks := []Risk{}
	for rows.Next() {
		var item Risk
		if err := rows.Scan(&item.ID, &item.TacticalPlanID, &item.EntityType, &item.EntityID, &item.Title,
			&item.Description, &item.Severity, &item.Probability, &item.Status, &item.CoverageStatus); err != nil {
			return nil, err
		}
		if item.EntityType == "workstream" && item.EntityID == workstreamID {
			risks = append(risks, item)
		}
		if item.EntityType == "tactical_plan" && item.EntityID == planID {
			risks = append(risks, item)
		}
		if item.EntityType == "project" && projectIDs[item.EntityID] {
			risks = append(risks, item)
		}
	}
	return risks, rows.Err()
}

func (s *Store) opportunities(ctx context.Context, workspaceID int, planID int, workstreamID int, projects []Project) ([]Opportunity, error) {
	projectIDs := map[int]bool{}
	for _, project := range projects {
		projectIDs[project.ID] = true
	}
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, tactical_plan_id, entity_type, entity_id, title, description, potential_impact, urgency, status, coverage_status
		FROM v2_tactical_opportunities
		WHERE workspace_id=$1 AND tactical_plan_id=$2 AND archived_at IS NULL
		ORDER BY id ASC
	`, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	opportunities := []Opportunity{}
	for rows.Next() {
		var item Opportunity
		if err := rows.Scan(&item.ID, &item.TacticalPlanID, &item.EntityType, &item.EntityID, &item.Title,
			&item.Description, &item.PotentialImpact, &item.Urgency, &item.Status, &item.CoverageStatus); err != nil {
			return nil, err
		}
		if item.EntityType == "workstream" && item.EntityID == workstreamID {
			opportunities = append(opportunities, item)
		}
		if item.EntityType == "tactical_plan" && item.EntityID == planID {
			opportunities = append(opportunities, item)
		}
		if item.EntityType == "project" && projectIDs[item.EntityID] {
			opportunities = append(opportunities, item)
		}
	}
	return opportunities, rows.Err()
}

func decodeTacticMetrics(raw []byte, legacyName string, legacyCurrent string, legacyTarget string) []TacticMetric {
	metrics := []TacticMetric{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &metrics)
	}
	if len(metrics) == 0 && strings.TrimSpace(legacyName) != "" {
		metrics = append(metrics, TacticMetric{Name: legacyName, Current: legacyCurrent, Target: legacyTarget})
	}
	return metrics
}

func (s *Store) validateLinks(ctx context.Context, workspaceID int, workstream workstreamRef, input TaskInput) error {
	if input.ProjectID != nil {
		var exists bool
		if err := s.dbx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM v2_tactical_projects
				WHERE id=$1 AND workspace_id=$2 AND workstream_id=$3 AND archived_at IS NULL
			)
		`, *input.ProjectID, workspaceID, workstream.ID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrForbidden
		}
	}
	if input.RiskID != nil {
		var exists bool
		if err := s.dbx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM v2_tactical_risks
					WHERE id=$1 AND workspace_id=$2 AND tactical_plan_id=$3 AND archived_at IS NULL
						AND (
							(entity_type='tactical_plan' AND entity_id=$3)
							OR
							(entity_type='workstream' AND entity_id=$4)
						OR (
							entity_type='project'
							AND EXISTS (
								SELECT 1 FROM v2_tactical_projects p
								WHERE p.id=v2_tactical_risks.entity_id
									AND p.workspace_id=$2
									AND p.workstream_id=$4
									AND p.archived_at IS NULL
							)
						)
					)
			)
		`, *input.RiskID, workspaceID, workstream.TacticalPlanID, workstream.ID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrForbidden
		}
	}
	if input.OpportunityID != nil {
		var exists bool
		if err := s.dbx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM v2_tactical_opportunities
					WHERE id=$1 AND workspace_id=$2 AND tactical_plan_id=$3 AND archived_at IS NULL
						AND (
							(entity_type='tactical_plan' AND entity_id=$3)
							OR
							(entity_type='workstream' AND entity_id=$4)
						OR (
							entity_type='project'
							AND EXISTS (
								SELECT 1 FROM v2_tactical_projects p
								WHERE p.id=v2_tactical_opportunities.entity_id
									AND p.workspace_id=$2
									AND p.workstream_id=$4
									AND p.archived_at IS NULL
							)
						)
					)
			)
		`, *input.OpportunityID, workspaceID, workstream.TacticalPlanID, workstream.ID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrForbidden
		}
	}
	return nil
}

func (s *Store) validateSecondaryWorkstreams(ctx context.Context, workspaceID int, planID int, primaryID int, requested []int) ([]int, error) {
	if requested == nil {
		return nil, nil
	}
	unique := make([]int, 0, len(requested))
	seen := map[int]bool{}
	for _, id := range requested {
		if id <= 0 || id == primaryID || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return []int{}, nil
	}
	var count int
	if err := s.dbx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM v2_tactical_workstreams
		WHERE workspace_id=$1 AND tactical_plan_id=$2 AND archived_at IS NULL AND id=ANY($3)
	`, workspaceID, planID, pq.Array(unique)).Scan(&count); err != nil {
		return nil, err
	}
	if count != len(unique) {
		return nil, ErrForbidden
	}
	sort.Ints(unique)
	return unique, nil
}

func (s *Store) replaceSecondaryWorkstreams(ctx context.Context, workspaceID int, taskID int, workstreamIDs []int) error {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM v2_task_secondary_workstreams WHERE workspace_id=$1 AND task_id=$2`, workspaceID, taskID); err != nil {
		return err
	}
	for _, workstreamID := range workstreamIDs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO v2_task_secondary_workstreams (task_id, workspace_id, workstream_id)
			VALUES ($1, $2, $3)
		`, taskID, workspaceID, workstreamID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) nextPriorityOrder(ctx context.Context, workspaceID int, workstreamID int, status string) (int, error) {
	var value int
	err := s.dbx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(priority_order), 0) + 1
		FROM v2_tasks
		WHERE workspace_id=$1 AND workstream_id=$2 AND status=$3 AND archived_at IS NULL
	`, workspaceID, workstreamID, status).Scan(&value)
	return value, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(scanner scanner) (Task, error) {
	var task Task
	var projectID sql.NullInt64
	var riskID sql.NullInt64
	var opportunityID sql.NullInt64
	var priorityOrder sql.NullInt64
	var manualPriorityScore sql.NullInt64
	var manualPriorityTier sql.NullString
	var ownerUserID sql.NullInt64
	var dueDate sql.NullString
	var sourceID sql.NullInt64
	var createdBy sql.NullInt64
	var updatedBy sql.NullInt64
	var startedAt sql.NullTime
	var completedAt sql.NullTime
	var archivedAt sql.NullTime

	err := scanner.Scan(
		&task.ID, &task.WorkspaceID, &task.CourseID, &task.TacticalPlanID, &task.WorkstreamID,
		&projectID, &riskID, &opportunityID, &task.Title, &task.Description, &task.ExpectedResult,
		&task.SuccessCriteria, &task.WhyNow, &task.Status, &task.Blocked, &task.BacklogCategory, &priorityOrder, &manualPriorityScore,
		&manualPriorityTier, &ownerUserID, &dueDate, &task.SourceType, &sourceID, &createdBy,
		&updatedBy, &task.CreatedAt, &task.UpdatedAt, &startedAt, &completedAt, &archivedAt,
	)
	if err != nil {
		return Task{}, err
	}

	task.ProjectID = intPtr(projectID)
	task.RiskID = intPtr(riskID)
	task.OpportunityID = intPtr(opportunityID)
	task.PriorityOrder = intPtr(priorityOrder)
	task.ManualPriorityScore = intPtr(manualPriorityScore)
	if manualPriorityTier.Valid {
		task.ManualPriorityTier = manualPriorityTier.String
	}
	task.OwnerUserID = intPtr(ownerUserID)
	task.DueDate = stringPtr(dueDate)
	task.SourceID = intPtr(sourceID)
	task.CreatedBy = intPtr(createdBy)
	task.UpdatedBy = intPtr(updatedBy)
	task.StartedAt = timePtr(startedAt)
	task.CompletedAt = timePtr(completedAt)
	task.ArchivedAt = timePtr(archivedAt)

	return task, nil
}

func (s *Store) decorateTasks(ctx context.Context, workspaceID int, tasks []Task) ([]Task, error) {
	if len(tasks) == 0 {
		return tasks, nil
	}
	targets := make(map[int]bool, len(tasks))
	taskIDs := make([]int, 0, len(tasks))
	for i := range tasks {
		targets[tasks[i].ID] = true
		taskIDs = append(taskIDs, tasks[i].ID)
		tasks[i].Flags = []string{}
		tasks[i].SecondaryWorkstreamIDs = []int{}
		tasks[i].EvaluationStatus = "not_evaluated"
		tasks[i].PrioritySource = "none"
		if tasks[i].ManualPriorityScore != nil {
			tasks[i].EffectivePriorityScore = *tasks[i].ManualPriorityScore
			tasks[i].EffectivePriorityTier = tasks[i].ManualPriorityTier
			if tasks[i].EffectivePriorityTier == "" {
				tasks[i].EffectivePriorityTier = PriorityTier(*tasks[i].ManualPriorityScore)
			}
			tasks[i].PrioritySource = "manual"
		}
	}
	secondaryRows, err := s.dbx.QueryContext(ctx, `
		SELECT task_id, workstream_id
		FROM v2_task_secondary_workstreams
		WHERE workspace_id=$1 AND task_id=ANY($2)
		ORDER BY task_id, workstream_id
	`, workspaceID, pq.Array(taskIDs))
	if err != nil {
		return nil, err
	}
	secondaryByTask := map[int][]int{}
	for secondaryRows.Next() {
		var taskID int
		var workstreamID int
		if err := secondaryRows.Scan(&taskID, &workstreamID); err != nil {
			secondaryRows.Close()
			return nil, err
		}
		secondaryByTask[taskID] = append(secondaryByTask[taskID], workstreamID)
	}
	if err := secondaryRows.Close(); err != nil {
		return nil, err
	}

	evaluations := map[int]TaskEvaluation{}
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT DISTINCT ON (task_id)
			id, task_id, strategic_relevance, course_alignment, tactical_alignment,
			expected_impact, urgency, effort, confidence, priority_score, priority_tier,
			recommendation, priority_reason, clarification_question,
			missing_information_json, flags_json, backlog_category, created_at
		FROM v2_task_evaluations
		WHERE workspace_id=$1
		ORDER BY task_id, created_at DESC, id DESC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item TaskEvaluation
		var missingRaw json.RawMessage
		var flagsRaw json.RawMessage
		if err := rows.Scan(
			&item.ID, &item.TaskID, &item.StrategicRelevance, &item.CourseAlignment,
			&item.TacticalAlignment, &item.ExpectedImpact, &item.Urgency, &item.Effort,
			&item.Confidence, &item.PriorityScore, &item.PriorityTier, &item.Recommendation,
			&item.PriorityReason, &item.ClarificationQuestion, &missingRaw, &flagsRaw, &item.BacklogCategory, &item.CreatedAt,
		); err != nil {
			rows.Close()
			return nil, err
		}
		if targets[item.TaskID] {
			_ = json.Unmarshal(missingRaw, &item.MissingInformation)
			_ = json.Unmarshal(flagsRaw, &item.Flags)
			evaluations[item.TaskID] = item
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	jobStatuses := map[int]string{}
	jobRows, err := s.dbx.QueryContext(ctx, `
		SELECT task_id, status
		FROM v2_task_evaluation_jobs
		WHERE workspace_id=$1
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	for jobRows.Next() {
		var taskID int
		var status string
		if err := jobRows.Scan(&taskID, &status); err != nil {
			jobRows.Close()
			return nil, err
		}
		if targets[taskID] {
			jobStatuses[taskID] = status
		}
	}
	if err := jobRows.Close(); err != nil {
		return nil, err
	}

	for i := range tasks {
		if linked, ok := secondaryByTask[tasks[i].ID]; ok {
			tasks[i].SecondaryWorkstreamIDs = linked
		}
		if tasks[i].Blocked {
			tasks[i].Flags = append(tasks[i].Flags, TaskFlagBlocked)
		}
		if evaluation, ok := evaluations[tasks[i].ID]; ok {
			tasks[i].Evaluation = &evaluation
			tasks[i].Flags = appendUniqueStrings(tasks[i].Flags, evaluation.Flags...)
			if tasks[i].BacklogCategory == "" {
				tasks[i].BacklogCategory = evaluation.BacklogCategory
			}
			tasks[i].EvaluationStatus = EvaluationReady
			if tasks[i].ManualPriorityScore == nil {
				tasks[i].EffectivePriorityScore = evaluation.PriorityScore
				tasks[i].EffectivePriorityTier = evaluation.PriorityTier
				tasks[i].PrioritySource = "ai"
			}
		}
		if status := jobStatuses[tasks[i].ID]; status != "" && status != EvaluationReady {
			tasks[i].EvaluationStatus = status
		}
	}
	return tasks, nil
}

func appendUniqueStrings(target []string, values ...string) []string {
	seen := make(map[string]bool, len(target)+len(values))
	for _, value := range target {
		seen[value] = true
	}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			target = append(target, value)
		}
	}
	return target
}

func (i *TaskInput) normalize() {
	if i.Title != nil {
		trimmed := strings.TrimSpace(*i.Title)
		i.Title = &trimmed
	}
	if i.Description != nil {
		trimmed := strings.TrimSpace(*i.Description)
		i.Description = &trimmed
	}
	if i.ExpectedResult != nil {
		trimmed := strings.TrimSpace(*i.ExpectedResult)
		i.ExpectedResult = &trimmed
	}
	if i.SuccessCriteria != nil {
		trimmed := strings.TrimSpace(*i.SuccessCriteria)
		i.SuccessCriteria = &trimmed
	}
	if i.WhyNow != nil {
		trimmed := strings.TrimSpace(*i.WhyNow)
		i.WhyNow = &trimmed
	}
	if i.Status != nil {
		trimmed := strings.TrimSpace(*i.Status)
		i.Status = &trimmed
	}
	if i.DueDate != nil {
		trimmed := strings.TrimSpace(*i.DueDate)
		i.DueDate = &trimmed
	}
	if i.SourceType != nil {
		trimmed := strings.TrimSpace(*i.SourceType)
		i.SourceType = &trimmed
	}
}

func attachTasks(workstreams []WorkstreamSummary, tasks []Task) {
	byID := map[int]int{}
	for i := range workstreams {
		byID[workstreams[i].ID] = i
	}
	grouped := map[int][]Task{}
	for _, task := range tasks {
		grouped[task.WorkstreamID] = append(grouped[task.WorkstreamID], task)
	}
	for workstreamID, items := range grouped {
		index, ok := byID[workstreamID]
		if !ok {
			continue
		}
		workstreams[index].TasksSummary = summarize(items)
		workstreams[index].TopTasks = topTasks(items, 3)
	}
}

func summarize(tasks []Task) TasksSummary {
	var summary TasksSummary
	for _, task := range tasks {
		summary.Total++
		switch task.Status {
		case StatusFree:
			summary.Free++
		case StatusInProgress:
			summary.InProgress++
		case StatusDone:
			summary.Done++
		case StatusArchived:
			summary.Archived++
		}
	}
	return summary
}

func topTasks(tasks []Task, limit int) []Task {
	result := []Task{}
	for _, task := range tasks {
		if task.Status == StatusArchived {
			continue
		}
		result = append(result, task)
		if len(result) >= limit {
			break
		}
	}
	return result
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	if strings.TrimSpace(*value) == "" {
		return nil
	}
	return strings.TrimSpace(*value)
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func intPtr(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int64)
	return &result
}

func stringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func timePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}
