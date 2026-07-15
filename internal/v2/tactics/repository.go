package tactics

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

var ErrNoActiveStrategy = errors.New("no_active_strategy")

type Store struct {
	dbx *sql.DB
}

func NewStore(dbx *sql.DB) *Store {
	return &Store{dbx: dbx}
}

func (s *Store) Current(ctx context.Context, workspaceID int, userID int) (CurrentResponse, error) {
	strategy, err := s.activeStrategy(ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return CurrentResponse{
			TacticalPlan: nil,
			Strategy:     nil,
			Workstreams:  []Workstream{},
			Uncovered:    Uncovered{Risks: []Risk{}, Opportunities: []Opportunity{}},
			Reason:       "no_active_strategy",
			Message:      "Для создания тактики нужна активная стратегия.",
		}, nil
	}
	if err != nil {
		return CurrentResponse{}, err
	}
	course, courseErr := s.activeCourse(ctx, workspaceID, strategy.ID)
	if courseErr != nil && !errors.Is(courseErr, sql.ErrNoRows) {
		return CurrentResponse{}, courseErr
	}

	plan, err := s.getOrCreatePlan(ctx, workspaceID, userID, strategy.ID)
	if err != nil {
		return CurrentResponse{}, err
	}
	if courseErr == nil && (plan.CourseID == nil || *plan.CourseID != course.ID) {
		plan, err = s.attachActiveCourse(ctx, workspaceID, plan)
		if err != nil {
			return CurrentResponse{}, err
		}
	}

	workstreams, err := s.listWorkstreams(ctx, workspaceID, plan.ID)
	if err != nil {
		return CurrentResponse{}, err
	}

	risks, err := s.listRisks(ctx, workspaceID, plan.ID)
	if err != nil {
		return CurrentResponse{}, err
	}

	opportunities, err := s.listOpportunities(ctx, workspaceID, plan.ID)
	if err != nil {
		return CurrentResponse{}, err
	}

	hydrateWorkstreams(workstreams, risks, opportunities)

	response := CurrentResponse{
		TacticalPlan: &plan,
		Strategy:     &strategy,
		Workstreams:  workstreams,
		Uncovered: Uncovered{
			Risks:         uncoveredRisks(risks),
			Opportunities: uncoveredOpportunities(opportunities),
		},
	}
	if courseErr == nil {
		response.Course = &course
	} else {
		response.Reason = "no_active_course"
		response.Message = "Для создания тактики нужен активный курс."
	}
	return response, nil
}

func (s *Store) UpdatePlan(ctx context.Context, workspaceID int, planID int, title string, summary string, status string) (TacticalPlan, error) {
	title = strings.TrimSpace(title)
	summary = strings.TrimSpace(summary)
	status = strings.TrimSpace(status)
	current, err := s.planByID(ctx, workspaceID, planID)
	if err != nil {
		return TacticalPlan{}, err
	}
	if title == "" {
		title = current.Title
	}
	if summary == "" {
		summary = current.Summary
	}
	if status == "" {
		status = current.Status
	}

	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_tactical_plans
		SET title=$1,
			summary=$2,
			status=$3,
			revision=revision + CASE WHEN title IS DISTINCT FROM $1 OR summary IS DISTINCT FROM $2 THEN 1 ELSE 0 END,
			activated_at=CASE WHEN $3=$4 THEN COALESCE(activated_at, NOW()) ELSE activated_at END,
			updated_at=NOW()
		WHERE id=$5 AND workspace_id=$6 AND archived_at IS NULL
		RETURNING id, workspace_id, strategy_id, course_id, status, revision, title, summary, source, created_by, created_at, updated_at, activated_at
	`, title, summary, status, PlanStatusActive, planID, workspaceID)

	return scanPlan(row)
}

func (s *Store) CreateWorkstream(ctx context.Context, workspaceID int, userID int, input WorkstreamInput) (Workstream, error) {
	input.normalize()
	plan, err := s.planByID(ctx, workspaceID, input.TacticalPlanID)
	if err != nil {
		return Workstream{}, err
	}

	sortOrder, err := s.nextSortOrder(ctx, "v2_tactical_workstreams", "tactical_plan_id", plan.ID)
	if err != nil {
		return Workstream{}, err
	}

	row := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_tactical_workstreams (
			workspace_id, tactical_plan_id, strategy_id, title, description, goal, ckp,
			reason, closes_risk, metric_name, metric_current, metric_target, status,
			health_status, contribution_type, source, sort_order, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		RETURNING
			id, workspace_id, tactical_plan_id, strategy_id, course_id, title, description,
			goal, ckp, reason, closes_risk, metric_name, metric_current, metric_target,
			status, health_status, contribution_type, confidence, source, sort_order, created_at, updated_at
	`, workspaceID, plan.ID, plan.StrategyID, input.Title, input.Description, input.Goal, input.CKP, input.Reason,
		input.ClosesRisk, input.MetricName, input.MetricCurrent, input.MetricTarget, input.Status,
		input.HealthStatus, input.ContributionType, SourceManual, sortOrder, userID)

	return scanWorkstream(row)
}

func (s *Store) UpdateWorkstream(ctx context.Context, workspaceID int, workstreamID int, input WorkstreamInput) (Workstream, error) {
	input.trim()
	current, err := s.workstreamByID(ctx, workspaceID, workstreamID)
	if err != nil {
		return Workstream{}, err
	}
	if input.Title == "" {
		input.Title = current.Title
	}
	if input.Description == "" {
		input.Description = current.Description
	}
	if input.Goal == "" {
		input.Goal = current.Goal
	}
	if input.CKP == "" {
		input.CKP = current.CKP
	}
	if input.Reason == "" {
		input.Reason = current.Reason
	}
	if input.ClosesRisk == "" {
		input.ClosesRisk = current.ClosesRisk
	}
	if input.MetricName == "" {
		input.MetricName = current.MetricName
	}
	if input.MetricCurrent == "" {
		input.MetricCurrent = current.MetricCurrent
	}
	if input.MetricTarget == "" {
		input.MetricTarget = current.MetricTarget
	}
	if input.HealthStatus == "" {
		input.HealthStatus = current.HealthStatus
	}
	if input.ContributionType == "" {
		input.ContributionType = current.ContributionType
	}

	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_tactical_workstreams
		SET title=$1,
			description=$2,
			goal=$3,
			ckp=$4,
			reason=$5,
			closes_risk=$6,
			metric_name=$7,
			metric_current=$8,
			metric_target=$9,
			status=$10,
			health_status=$11,
			contribution_type=$12,
			updated_at=NOW()
		WHERE id=$13 AND workspace_id=$14 AND archived_at IS NULL
		RETURNING
			id, workspace_id, tactical_plan_id, strategy_id, course_id, title, description,
			goal, ckp, reason, closes_risk, metric_name, metric_current, metric_target,
			status, health_status, contribution_type, confidence, source, sort_order, created_at, updated_at
	`, input.Title, input.Description, input.Goal, input.CKP, input.Reason, input.ClosesRisk, input.MetricName,
		input.MetricCurrent, input.MetricTarget, input.Status, input.HealthStatus, input.ContributionType,
		workstreamID, workspaceID)

	return scanWorkstream(row)
}

func (s *Store) CreateProject(ctx context.Context, workspaceID int, userID int, input ProjectInput) (Project, error) {
	input.normalize()
	workstream, err := s.workstreamByID(ctx, workspaceID, input.WorkstreamID)
	if err != nil {
		return Project{}, err
	}

	sortOrder, err := s.nextSortOrder(ctx, "v2_tactical_projects", "workstream_id", workstream.ID)
	if err != nil {
		return Project{}, err
	}

	row := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_tactical_projects (
			workspace_id, workstream_id, title, description, why_needed, success_criteria,
			failure_criteria, metric_name, status, source, sort_order, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING
			id, workspace_id, workstream_id, title, description, why_needed,
			success_criteria, failure_criteria, metric_name, status, confidence,
			source, sort_order, created_at, updated_at
	`, workspaceID, workstream.ID, input.Title, input.Description, input.WhyNeeded, input.SuccessCriteria,
		input.FailureCriteria, input.MetricName, input.Status, SourceManual, sortOrder, userID)

	return scanProject(row)
}

func (s *Store) UpdateProject(ctx context.Context, workspaceID int, projectID int, input ProjectInput) (Project, error) {
	input.trim()
	current, err := s.projectByID(ctx, workspaceID, projectID)
	if err != nil {
		return Project{}, err
	}
	if input.Title == "" {
		input.Title = current.Title
	}
	if input.Description == "" {
		input.Description = current.Description
	}
	if input.WhyNeeded == "" {
		input.WhyNeeded = current.WhyNeeded
	}
	if input.SuccessCriteria == "" {
		input.SuccessCriteria = current.SuccessCriteria
	}
	if input.FailureCriteria == "" {
		input.FailureCriteria = current.FailureCriteria
	}
	if input.MetricName == "" {
		input.MetricName = current.MetricName
	}

	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_tactical_projects
		SET title=$1,
			description=$2,
			why_needed=$3,
			success_criteria=$4,
			failure_criteria=$5,
			metric_name=$6,
			status=$7,
			updated_at=NOW()
		WHERE id=$8 AND workspace_id=$9 AND archived_at IS NULL
		RETURNING
			id, workspace_id, workstream_id, title, description, why_needed,
			success_criteria, failure_criteria, metric_name, status, confidence,
			source, sort_order, created_at, updated_at
	`, input.Title, input.Description, input.WhyNeeded, input.SuccessCriteria, input.FailureCriteria,
		input.MetricName, input.Status, projectID, workspaceID)

	return scanProject(row)
}

func (s *Store) CreateRisk(ctx context.Context, workspaceID int, userID int, input RiskInput) (Risk, error) {
	input.normalize()
	planID, err := s.resolvePlanForEntity(ctx, workspaceID, input.EntityType, input.EntityID)
	if err != nil {
		return Risk{}, err
	}

	row := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_tactical_risks (
			workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			severity, status, coverage_status, source, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING
			id, workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			severity, status, coverage_status, source, created_at, updated_at
	`, workspaceID, planID, input.EntityType, input.EntityID, input.Title, input.Description,
		input.Severity, input.Status, input.CoverageStatus, SourceManual, userID)

	return scanRisk(row)
}

func (s *Store) UpdateRisk(ctx context.Context, workspaceID int, riskID int, input RiskInput) (Risk, error) {
	input.trim()
	current, err := s.riskByID(ctx, workspaceID, riskID)
	if err != nil {
		return Risk{}, err
	}
	if input.Title == "" {
		input.Title = current.Title
	}
	if input.Description == "" {
		input.Description = current.Description
	}
	if input.Severity == "" {
		input.Severity = current.Severity
	}
	if input.Status == "" {
		input.Status = current.Status
	}
	if input.CoverageStatus == "" {
		input.CoverageStatus = current.CoverageStatus
	}

	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_tactical_risks
		SET title=$1,
			description=$2,
			severity=$3,
			status=$4,
			coverage_status=$5,
			updated_at=NOW()
		WHERE id=$6 AND workspace_id=$7 AND archived_at IS NULL
		RETURNING
			id, workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			severity, status, coverage_status, source, created_at, updated_at
	`, input.Title, input.Description, input.Severity, input.Status, input.CoverageStatus, riskID, workspaceID)

	return scanRisk(row)
}

func (s *Store) CreateOpportunity(ctx context.Context, workspaceID int, userID int, input OpportunityInput) (Opportunity, error) {
	input.normalize()
	planID, err := s.resolvePlanForEntity(ctx, workspaceID, input.EntityType, input.EntityID)
	if err != nil {
		return Opportunity{}, err
	}

	row := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_tactical_opportunities (
			workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			potential_impact, status, coverage_status, source, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING
			id, workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			potential_impact, status, coverage_status, source, created_at, updated_at
	`, workspaceID, planID, input.EntityType, input.EntityID, input.Title, input.Description,
		input.PotentialImpact, input.Status, input.CoverageStatus, SourceManual, userID)

	return scanOpportunity(row)
}

func (s *Store) UpdateOpportunity(ctx context.Context, workspaceID int, opportunityID int, input OpportunityInput) (Opportunity, error) {
	input.trim()
	current, err := s.opportunityByID(ctx, workspaceID, opportunityID)
	if err != nil {
		return Opportunity{}, err
	}
	if input.Title == "" {
		input.Title = current.Title
	}
	if input.Description == "" {
		input.Description = current.Description
	}
	if input.PotentialImpact == "" {
		input.PotentialImpact = current.PotentialImpact
	}
	if input.Status == "" {
		input.Status = current.Status
	}
	if input.CoverageStatus == "" {
		input.CoverageStatus = current.CoverageStatus
	}

	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_tactical_opportunities
		SET title=$1,
			description=$2,
			potential_impact=$3,
			status=$4,
			coverage_status=$5,
			updated_at=NOW()
		WHERE id=$6 AND workspace_id=$7 AND archived_at IS NULL
		RETURNING
			id, workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			potential_impact, status, coverage_status, source, created_at, updated_at
	`, input.Title, input.Description, input.PotentialImpact, input.Status, input.CoverageStatus, opportunityID, workspaceID)

	return scanOpportunity(row)
}

func (s *Store) activeStrategy(ctx context.Context, workspaceID int) (StrategySummary, error) {
	var strategy StrategySummary
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id, status, title, summary, version, updated_at::TEXT
		FROM v2_strategies
		WHERE workspace_id=$1 AND status='active' AND archived_at IS NULL
		ORDER BY version DESC, created_at DESC
		LIMIT 1
	`, workspaceID).Scan(&strategy.ID, &strategy.Status, &strategy.Title, &strategy.Summary, &strategy.Version, &strategy.UpdatedAt)
	return strategy, err
}

func (s *Store) activeCourse(ctx context.Context, workspaceID int, strategyID int) (CourseSummary, error) {
	var course CourseSummary
	var endDate sql.NullString
	var activatedAt sql.NullTime
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id, strategy_id, status, title, direction, strategic_goal, meaning,
			horizon, horizon_unit, start_date::TEXT, end_date::TEXT, key_metric,
			success_criterion, updated_at, activated_at
		FROM v2_courses
		WHERE workspace_id=$1 AND strategy_id=$2 AND status='active' AND archived_at IS NULL
		ORDER BY updated_at DESC, id DESC
		LIMIT 1
	`, workspaceID, strategyID).Scan(
		&course.ID,
		&course.StrategyID,
		&course.Status,
		&course.Title,
		&course.Direction,
		&course.StrategicGoal,
		&course.Meaning,
		&course.Horizon,
		&course.HorizonUnit,
		&course.StartDate,
		&endDate,
		&course.KeyMetric,
		&course.SuccessCriterion,
		&course.UpdatedAt,
		&activatedAt,
	)
	if err != nil {
		return CourseSummary{}, err
	}
	if endDate.Valid {
		course.EndDate = endDate.String
	}
	if activatedAt.Valid {
		course.ActivatedAt = &activatedAt.Time
	}
	return course, nil
}

func (s *Store) getOrCreatePlan(ctx context.Context, workspaceID int, userID int, strategyID int) (TacticalPlan, error) {
	plan, err := s.planByStrategy(ctx, workspaceID, strategyID)
	if err == nil {
		return plan, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return TacticalPlan{}, err
	}

	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return TacticalPlan{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, workspaceID+5000000); err != nil {
		return TacticalPlan{}, err
	}

	courseID, err := activeCourseIDTx(ctx, tx, workspaceID, strategyID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return TacticalPlan{}, err
	}

	row := tx.QueryRowContext(ctx, `
		INSERT INTO v2_tactical_plans (workspace_id, strategy_id, course_id, status, title, summary, source, created_by)
		VALUES ($1, $2, $3, $4, $5, '', $6, $7)
		ON CONFLICT (workspace_id, strategy_id) DO UPDATE SET updated_at=v2_tactical_plans.updated_at
		RETURNING id, workspace_id, strategy_id, course_id, status, revision, title, summary, source, created_by, created_at, updated_at, activated_at
	`, workspaceID, strategyID, nullableInt(courseID), PlanStatusDraft, "Тактический план", SourceManual, userID)

	plan, err = scanPlan(row)
	if err != nil {
		return TacticalPlan{}, err
	}
	if err := tx.Commit(); err != nil {
		return TacticalPlan{}, err
	}
	return plan, nil
}

func (s *Store) attachActiveCourse(ctx context.Context, workspaceID int, plan TacticalPlan) (TacticalPlan, error) {
	var courseID int
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id
		FROM v2_courses
		WHERE workspace_id=$1
			AND strategy_id=$2
			AND status='active'
			AND archived_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1
	`, workspaceID, plan.StrategyID).Scan(&courseID)
	if errors.Is(err, sql.ErrNoRows) {
		return plan, nil
	}
	if err != nil {
		return TacticalPlan{}, err
	}

	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_tactical_plans
		SET course_id=$1,
			revision=revision + CASE WHEN course_id IS DISTINCT FROM $1 THEN 1 ELSE 0 END,
			updated_at=NOW()
		WHERE id=$2 AND workspace_id=$3 AND strategy_id=$4 AND archived_at IS NULL
		RETURNING id, workspace_id, strategy_id, course_id, status, revision, title, summary, source, created_by, created_at, updated_at, activated_at
	`, courseID, plan.ID, workspaceID, plan.StrategyID)
	return scanPlan(row)
}

func activeCourseIDTx(ctx context.Context, tx *sql.Tx, workspaceID int, strategyID int) (int, error) {
	var courseID int
	err := tx.QueryRowContext(ctx, `
		SELECT id
		FROM v2_courses
		WHERE workspace_id=$1
			AND strategy_id=$2
			AND status='active'
			AND archived_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1
	`, workspaceID, strategyID).Scan(&courseID)
	return courseID, err
}

func (s *Store) planByStrategy(ctx context.Context, workspaceID int, strategyID int) (TacticalPlan, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, strategy_id, course_id, status, revision, title, summary, source, created_by, created_at, updated_at, activated_at
		FROM v2_tactical_plans
		WHERE workspace_id=$1 AND strategy_id=$2 AND archived_at IS NULL
	`, workspaceID, strategyID)
	return scanPlan(row)
}

func (s *Store) planByID(ctx context.Context, workspaceID int, planID int) (TacticalPlan, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, strategy_id, course_id, status, revision, title, summary, source, created_by, created_at, updated_at, activated_at
		FROM v2_tactical_plans
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, planID, workspaceID)
	return scanPlan(row)
}

func (s *Store) workstreamByID(ctx context.Context, workspaceID int, workstreamID int) (Workstream, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT
			id, workspace_id, tactical_plan_id, strategy_id, course_id, title, description,
			goal, ckp, reason, closes_risk, metric_name, metric_current, metric_target,
			status, health_status, contribution_type, confidence, source, sort_order, created_at, updated_at
		FROM v2_tactical_workstreams
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, workstreamID, workspaceID)
	return scanWorkstream(row)
}

func (s *Store) projectByID(ctx context.Context, workspaceID int, projectID int) (Project, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT
			id, workspace_id, workstream_id, title, description, why_needed,
			success_criteria, failure_criteria, metric_name, status, confidence,
			source, sort_order, created_at, updated_at
		FROM v2_tactical_projects
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, projectID, workspaceID)
	return scanProject(row)
}

func (s *Store) riskByID(ctx context.Context, workspaceID int, riskID int) (Risk, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			severity, status, coverage_status, source, created_at, updated_at
		FROM v2_tactical_risks
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, riskID, workspaceID)
	return scanRisk(row)
}

func (s *Store) opportunityByID(ctx context.Context, workspaceID int, opportunityID int) (Opportunity, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			potential_impact, status, coverage_status, source, created_at, updated_at
		FROM v2_tactical_opportunities
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, opportunityID, workspaceID)
	return scanOpportunity(row)
}

func (s *Store) nextSortOrder(ctx context.Context, table string, column string, ownerID int) (int, error) {
	var value int
	query := "SELECT COALESCE(MAX(sort_order), 0) + 1 FROM " + table + " WHERE " + column + "=$1 AND archived_at IS NULL"
	err := s.dbx.QueryRowContext(ctx, query, ownerID).Scan(&value)
	return value, err
}

func (s *Store) listWorkstreams(ctx context.Context, workspaceID int, planID int) ([]Workstream, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT
			id, workspace_id, tactical_plan_id, strategy_id, course_id, title, description,
			goal, ckp, reason, closes_risk, metric_name, metric_current, metric_target,
			status, health_status, contribution_type, confidence, source, sort_order, created_at, updated_at
		FROM v2_tactical_workstreams
		WHERE workspace_id=$1 AND tactical_plan_id=$2 AND archived_at IS NULL
		ORDER BY sort_order ASC, id ASC
	`, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	workstreams := []Workstream{}
	for rows.Next() {
		workstream, err := scanWorkstream(rows)
		if err != nil {
			return nil, err
		}
		workstream.Projects = []Project{}
		workstream.Risks = []Risk{}
		workstream.Opportunities = []Opportunity{}
		workstreams = append(workstreams, workstream)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	projects, err := s.listProjects(ctx, workspaceID, workstreams)
	if err != nil {
		return nil, err
	}
	for i := range workstreams {
		workstreams[i].Projects = projects[workstreams[i].ID]
	}

	return workstreams, nil
}

func (s *Store) listProjects(ctx context.Context, workspaceID int, workstreams []Workstream) (map[int][]Project, error) {
	result := map[int][]Project{}
	for _, workstream := range workstreams {
		result[workstream.ID] = []Project{}
	}
	if len(workstreams) == 0 {
		return result, nil
	}

	rows, err := s.dbx.QueryContext(ctx, `
		SELECT
			id, workspace_id, workstream_id, title, description, why_needed,
			success_criteria, failure_criteria, metric_name, status, confidence,
			source, sort_order, created_at, updated_at
		FROM v2_tactical_projects
		WHERE workspace_id=$1 AND archived_at IS NULL
		ORDER BY sort_order ASC, id ASC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	allowed := map[int]bool{}
	for _, workstream := range workstreams {
		allowed[workstream.ID] = true
	}

	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		if allowed[project.WorkstreamID] {
			result[project.WorkstreamID] = append(result[project.WorkstreamID], project)
		}
	}

	return result, rows.Err()
}

func (s *Store) listRisks(ctx context.Context, workspaceID int, planID int) ([]Risk, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			severity, status, coverage_status, source, created_at, updated_at
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
		risk, err := scanRisk(rows)
		if err != nil {
			return nil, err
		}
		risks = append(risks, risk)
	}
	return risks, rows.Err()
}

func (s *Store) listOpportunities(ctx context.Context, workspaceID int, planID int) ([]Opportunity, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			potential_impact, status, coverage_status, source, created_at, updated_at
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
		opportunity, err := scanOpportunity(rows)
		if err != nil {
			return nil, err
		}
		opportunities = append(opportunities, opportunity)
	}
	return opportunities, rows.Err()
}

func (s *Store) resolvePlanForEntity(ctx context.Context, workspaceID int, entityType string, entityID int) (int, error) {
	switch entityType {
	case EntityPlan:
		plan, err := s.planByID(ctx, workspaceID, entityID)
		return plan.ID, err
	case EntityWorkstream:
		workstream, err := s.workstreamByID(ctx, workspaceID, entityID)
		return workstream.TacticalPlanID, err
	case EntityProject:
		var planID int
		err := s.dbx.QueryRowContext(ctx, `
			SELECT w.tactical_plan_id
			FROM v2_tactical_projects p
			JOIN v2_tactical_workstreams w ON w.id=p.workstream_id
			WHERE p.id=$1 AND p.workspace_id=$2 AND p.archived_at IS NULL AND w.archived_at IS NULL
		`, entityID, workspaceID).Scan(&planID)
		return planID, err
	default:
		return 0, sql.ErrNoRows
	}
}

func hydrateWorkstreams(workstreams []Workstream, risks []Risk, opportunities []Opportunity) {
	byID := map[int]int{}
	for i := range workstreams {
		byID[workstreams[i].ID] = i
	}
	for _, risk := range risks {
		if risk.EntityType == EntityWorkstream {
			if index, ok := byID[risk.EntityID]; ok {
				workstreams[index].Risks = append(workstreams[index].Risks, risk)
			}
		}
	}
	for _, opportunity := range opportunities {
		if opportunity.EntityType == EntityWorkstream {
			if index, ok := byID[opportunity.EntityID]; ok {
				workstreams[index].Opportunities = append(workstreams[index].Opportunities, opportunity)
			}
		}
	}
}

func uncoveredRisks(risks []Risk) []Risk {
	result := []Risk{}
	for _, risk := range risks {
		if risk.CoverageStatus == CoverageUncovered || risk.CoverageStatus == CoveragePartiallyCovered {
			result = append(result, risk)
		}
	}
	return result
}

func uncoveredOpportunities(opportunities []Opportunity) []Opportunity {
	result := []Opportunity{}
	for _, opportunity := range opportunities {
		if opportunity.CoverageStatus == CoverageUncovered || opportunity.CoverageStatus == CoveragePartiallyCovered {
			result = append(result, opportunity)
		}
	}
	return result
}

type WorkstreamInput struct {
	TacticalPlanID   int    `json:"tactical_plan_id"`
	Title            string `json:"title"`
	Description      string `json:"description"`
	Goal             string `json:"goal"`
	CKP              string `json:"ckp"`
	Reason           string `json:"reason"`
	ClosesRisk       string `json:"closes_risk"`
	MetricName       string `json:"metric_name"`
	MetricCurrent    string `json:"metric_current"`
	MetricTarget     string `json:"metric_target"`
	Status           string `json:"status"`
	HealthStatus     string `json:"health_status"`
	ContributionType string `json:"contribution_type"`
}

func (i *WorkstreamInput) normalize() {
	i.trim()
	if i.Status == "" {
		i.Status = WorkstreamStatusActive
	}
	if i.HealthStatus == "" {
		i.HealthStatus = "В работе"
	}
}

func (i *WorkstreamInput) trim() {
	i.Title = strings.TrimSpace(i.Title)
	i.Description = strings.TrimSpace(i.Description)
	i.Goal = strings.TrimSpace(i.Goal)
	i.CKP = strings.TrimSpace(i.CKP)
	i.Reason = strings.TrimSpace(i.Reason)
	i.ClosesRisk = strings.TrimSpace(i.ClosesRisk)
	i.MetricName = strings.TrimSpace(i.MetricName)
	i.MetricCurrent = strings.TrimSpace(i.MetricCurrent)
	i.MetricTarget = strings.TrimSpace(i.MetricTarget)
	i.Status = strings.TrimSpace(i.Status)
	i.HealthStatus = strings.TrimSpace(i.HealthStatus)
	i.ContributionType = strings.TrimSpace(i.ContributionType)
}

type ProjectInput struct {
	WorkstreamID    int    `json:"workstream_id"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	WhyNeeded       string `json:"why_needed"`
	SuccessCriteria string `json:"success_criteria"`
	FailureCriteria string `json:"failure_criteria"`
	MetricName      string `json:"metric_name"`
	Status          string `json:"status"`
}

func (i *ProjectInput) normalize() {
	i.trim()
	if i.Status == "" {
		i.Status = ProjectStatusActive
	}
}

func (i *ProjectInput) trim() {
	i.Title = strings.TrimSpace(i.Title)
	i.Description = strings.TrimSpace(i.Description)
	i.WhyNeeded = strings.TrimSpace(i.WhyNeeded)
	i.SuccessCriteria = strings.TrimSpace(i.SuccessCriteria)
	i.FailureCriteria = strings.TrimSpace(i.FailureCriteria)
	i.MetricName = strings.TrimSpace(i.MetricName)
	i.Status = strings.TrimSpace(i.Status)
}

type RiskInput struct {
	EntityType     string `json:"entity_type"`
	EntityID       int    `json:"entity_id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	Severity       string `json:"severity"`
	Status         string `json:"status"`
	CoverageStatus string `json:"coverage_status"`
}

func (i *RiskInput) normalize() {
	i.trim()
	if i.Severity == "" {
		i.Severity = "medium"
	}
	if i.Status == "" {
		i.Status = "active"
	}
	if i.CoverageStatus == "" {
		i.CoverageStatus = CoverageUncovered
	}
}

func (i *RiskInput) trim() {
	i.EntityType = strings.TrimSpace(i.EntityType)
	i.Title = strings.TrimSpace(i.Title)
	i.Description = strings.TrimSpace(i.Description)
	i.Severity = strings.TrimSpace(i.Severity)
	i.Status = strings.TrimSpace(i.Status)
	i.CoverageStatus = strings.TrimSpace(i.CoverageStatus)
}

type OpportunityInput struct {
	EntityType      string `json:"entity_type"`
	EntityID        int    `json:"entity_id"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	PotentialImpact string `json:"potential_impact"`
	Status          string `json:"status"`
	CoverageStatus  string `json:"coverage_status"`
}

func (i *OpportunityInput) normalize() {
	i.trim()
	if i.PotentialImpact == "" {
		i.PotentialImpact = "medium"
	}
	if i.Status == "" {
		i.Status = "active"
	}
	if i.CoverageStatus == "" {
		i.CoverageStatus = CoverageUncovered
	}
}

func (i *OpportunityInput) trim() {
	i.EntityType = strings.TrimSpace(i.EntityType)
	i.Title = strings.TrimSpace(i.Title)
	i.Description = strings.TrimSpace(i.Description)
	i.PotentialImpact = strings.TrimSpace(i.PotentialImpact)
	i.Status = strings.TrimSpace(i.Status)
	i.CoverageStatus = strings.TrimSpace(i.CoverageStatus)
}

func nullableInt(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}

type scanner interface {
	Scan(dest ...any) error
}

func scanPlan(scanner scanner) (TacticalPlan, error) {
	var plan TacticalPlan
	var courseID sql.NullInt64
	var createdBy sql.NullInt64
	var activatedAt sql.NullTime
	err := scanner.Scan(
		&plan.ID, &plan.WorkspaceID, &plan.StrategyID, &courseID, &plan.Status, &plan.Revision, &plan.Title,
		&plan.Summary, &plan.Source, &createdBy, &plan.CreatedAt, &plan.UpdatedAt, &activatedAt,
	)
	if err != nil {
		return TacticalPlan{}, err
	}
	if courseID.Valid {
		value := int(courseID.Int64)
		plan.CourseID = &value
	}
	if createdBy.Valid {
		value := int(createdBy.Int64)
		plan.CreatedBy = &value
	}
	if activatedAt.Valid {
		plan.ActivatedAt = &activatedAt.Time
	}
	return plan, nil
}

func scanWorkstream(scanner scanner) (Workstream, error) {
	var workstream Workstream
	var courseID sql.NullInt64
	var confidence sql.NullFloat64
	err := scanner.Scan(
		&workstream.ID, &workstream.WorkspaceID, &workstream.TacticalPlanID, &workstream.StrategyID,
		&courseID, &workstream.Title, &workstream.Description, &workstream.Goal, &workstream.CKP,
		&workstream.Reason, &workstream.ClosesRisk, &workstream.MetricName, &workstream.MetricCurrent,
		&workstream.MetricTarget, &workstream.Status, &workstream.HealthStatus, &workstream.ContributionType,
		&confidence, &workstream.Source, &workstream.SortOrder, &workstream.CreatedAt, &workstream.UpdatedAt,
	)
	if err != nil {
		return Workstream{}, err
	}
	if courseID.Valid {
		value := int(courseID.Int64)
		workstream.CourseID = &value
	}
	if confidence.Valid {
		value := confidence.Float64
		workstream.Confidence = &value
	}
	workstream.Projects = []Project{}
	workstream.Risks = []Risk{}
	workstream.Opportunities = []Opportunity{}
	return workstream, nil
}

func scanProject(scanner scanner) (Project, error) {
	var project Project
	var confidence sql.NullFloat64
	err := scanner.Scan(
		&project.ID, &project.WorkspaceID, &project.WorkstreamID, &project.Title, &project.Description,
		&project.WhyNeeded, &project.SuccessCriteria, &project.FailureCriteria, &project.MetricName,
		&project.Status, &confidence, &project.Source, &project.SortOrder, &project.CreatedAt, &project.UpdatedAt,
	)
	if err != nil {
		return Project{}, err
	}
	if confidence.Valid {
		value := confidence.Float64
		project.Confidence = &value
	}
	return project, nil
}

func scanRisk(scanner scanner) (Risk, error) {
	var risk Risk
	err := scanner.Scan(
		&risk.ID, &risk.WorkspaceID, &risk.TacticalPlanID, &risk.EntityType, &risk.EntityID,
		&risk.Title, &risk.Description, &risk.Severity, &risk.Status, &risk.CoverageStatus,
		&risk.Source, &risk.CreatedAt, &risk.UpdatedAt,
	)
	return risk, err
}

func scanOpportunity(scanner scanner) (Opportunity, error) {
	var opportunity Opportunity
	err := scanner.Scan(
		&opportunity.ID, &opportunity.WorkspaceID, &opportunity.TacticalPlanID, &opportunity.EntityType,
		&opportunity.EntityID, &opportunity.Title, &opportunity.Description, &opportunity.PotentialImpact,
		&opportunity.Status, &opportunity.CoverageStatus, &opportunity.Source, &opportunity.CreatedAt,
		&opportunity.UpdatedAt,
	)
	return opportunity, err
}
