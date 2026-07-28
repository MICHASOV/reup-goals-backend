package tactics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"reup-goals-backend/internal/v2/aiactions"
)

var ErrNoActiveStrategy = errors.New("no_active_strategy")

type Store struct {
	dbx       *sql.DB
	aiActions *aiactions.Store
}

func NewStore(dbx *sql.DB) *Store {
	return &Store{dbx: dbx, aiActions: aiactions.NewStore(dbx)}
}

func (s *Store) Current(ctx context.Context, workspaceID int, userID int) (CurrentResponse, error) {
	strategy, err := s.activeStrategy(ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return CurrentResponse{
			TacticalPlan: nil,
			Strategy:     nil,
			Workstreams:  []Workstream{},
			Uncovered:    emptyUncovered(),
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

	type currentPart struct {
		kind          string
		workstreams   []Workstream
		risks         []Risk
		opportunities []Opportunity
		hypotheses    []Hypothesis
		err           error
	}
	parts := make(chan currentPart, 4)
	go func() {
		items, loadErr := s.listWorkstreams(ctx, workspaceID, plan.ID)
		parts <- currentPart{kind: "workstreams", workstreams: items, err: loadErr}
	}()
	go func() {
		items, loadErr := s.listRisks(ctx, workspaceID, plan.ID)
		parts <- currentPart{kind: "risks", risks: items, err: loadErr}
	}()
	go func() {
		items, loadErr := s.listOpportunities(ctx, workspaceID, plan.ID)
		parts <- currentPart{kind: "opportunities", opportunities: items, err: loadErr}
	}()
	go func() {
		items, loadErr := s.listHypotheses(ctx, workspaceID, plan.ID)
		parts <- currentPart{kind: "hypotheses", hypotheses: items, err: loadErr}
	}()

	var workstreams []Workstream
	var risks []Risk
	var opportunities []Opportunity
	var hypotheses []Hypothesis
	for range 4 {
		part := <-parts
		if part.err != nil {
			return CurrentResponse{}, part.err
		}
		switch part.kind {
		case "workstreams":
			workstreams = part.workstreams
		case "risks":
			risks = part.risks
		case "opportunities":
			opportunities = part.opportunities
		case "hypotheses":
			hypotheses = part.hypotheses
		}
	}

	hydrateWorkstreams(workstreams, risks, opportunities, hypotheses)
	type enrichmentPart struct {
		coverage *Uncovered
		err      error
	}
	enrichment := make(chan enrichmentPart, 2)
	go func() {
		enrichment <- enrichmentPart{err: s.hydrateEntityEvaluations(ctx, workspaceID, workstreams)}
	}()
	go func() {
		coverage, loadErr := s.coverageGaps(ctx, workspaceID, plan.ID, copyWorkstreamsForCoverage(workstreams))
		enrichment <- enrichmentPart{coverage: &coverage, err: loadErr}
	}()

	var coverage Uncovered
	for range 2 {
		part := <-enrichment
		if part.err != nil {
			return CurrentResponse{}, part.err
		}
		if part.coverage != nil {
			coverage = *part.coverage
		}
	}
	coverage.Risks = uncoveredRisks(risks)
	coverage.Opportunities = uncoveredOpportunities(opportunities)

	response := CurrentResponse{
		TacticalPlan: &plan,
		Strategy:     &strategy,
		Workstreams:  workstreams,
		Uncovered:    coverage,
	}
	if courseErr == nil {
		response.Course = &course
	} else {
		response.Reason = "no_active_course"
		response.Message = "Для создания тактики нужен активный курс."
	}
	return response, nil
}

func copyWorkstreamsForCoverage(workstreams []Workstream) []Workstream {
	copied := make([]Workstream, len(workstreams))
	for index, workstream := range workstreams {
		copied[index] = Workstream{
			ID:       workstream.ID,
			Title:    workstream.Title,
			CKP:      workstream.CKP,
			Metrics:  append([]TacticMetric(nil), workstream.Metrics...),
			Projects: make([]Project, len(workstream.Projects)),
		}
		for projectIndex, project := range workstream.Projects {
			copied[index].Projects[projectIndex] = Project{
				ID:              project.ID,
				Title:           project.Title,
				SuccessCriteria: project.SuccessCriteria,
				MetricName:      project.MetricName,
			}
		}
	}
	return copied
}

func (s *Store) WorkstreamDetail(ctx context.Context, workspaceID int, workstreamID int) (Workstream, error) {
	item, err := s.workstreamByID(ctx, workspaceID, workstreamID)
	if err != nil {
		return Workstream{}, err
	}
	projects, err := s.listProjects(ctx, workspaceID, []Workstream{item})
	if err != nil {
		return Workstream{}, err
	}
	item.Projects = projects[item.ID]
	if item.Projects == nil {
		item.Projects = []Project{}
	}
	risks, err := s.listRisks(ctx, workspaceID, item.TacticalPlanID)
	if err != nil {
		return Workstream{}, err
	}
	opportunities, err := s.listOpportunities(ctx, workspaceID, item.TacticalPlanID)
	if err != nil {
		return Workstream{}, err
	}
	hypotheses, err := s.listHypotheses(ctx, workspaceID, item.TacticalPlanID)
	if err != nil {
		return Workstream{}, err
	}
	items := []Workstream{item}
	hydrateWorkstreams(items, risks, opportunities, hypotheses)
	if err := s.hydrateEntityEvaluations(ctx, workspaceID, items); err != nil {
		return Workstream{}, err
	}
	return items[0], nil
}

func (s *Store) ProjectDetail(ctx context.Context, workspaceID int, projectID int) (Project, Workstream, error) {
	project, err := s.projectByID(ctx, workspaceID, projectID)
	if err != nil {
		return Project{}, Workstream{}, err
	}
	workstream, err := s.WorkstreamDetail(ctx, workspaceID, project.WorkstreamID)
	if err != nil {
		return Project{}, Workstream{}, err
	}
	for _, candidate := range workstream.Projects {
		if candidate.ID == projectID {
			project = candidate
			break
		}
	}
	return project, workstream, nil
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
	contentChanged := title != current.Title || summary != current.Summary
	if contentChanged && status == PlanStatusActive {
		status = PlanStatusDraft
	}

	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_tactical_plans
		SET title=$1,
			summary=$2,
			status=$3,
			revision=revision + CASE WHEN title IS DISTINCT FROM $1 OR summary IS DISTINCT FROM $2 THEN 1 ELSE 0 END,
			activated_at=CASE WHEN $3=$4 THEN COALESCE(activated_at, NOW()) ELSE NULL END,
			updated_at=NOW()
		WHERE id=$5 AND workspace_id=$6 AND archived_at IS NULL
		RETURNING id, workspace_id, strategy_id, course_id, status, revision, activated_revision, activation_readiness_run_id, title, summary, source, created_by, created_at, updated_at, activated_at
	`, title, summary, status, PlanStatusActive, planID, workspaceID)

	return scanPlan(row)
}

func (s *Store) ActivatePlan(ctx context.Context, workspaceID int, userID int, planID int, readinessRunID int, expectedRevision int, snapshot any) (TacticalPlan, error) {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return TacticalPlan{}, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		UPDATE v2_tactical_plans
		SET status=$1,
			activated_revision=revision,
			activation_readiness_run_id=$2,
			activated_at=NOW(),
			updated_at=NOW()
		WHERE id=$3 AND workspace_id=$4 AND revision=$5 AND archived_at IS NULL
		RETURNING id, workspace_id, strategy_id, course_id, status, revision, activated_revision,
			activation_readiness_run_id, title, summary, source, created_by, created_at, updated_at, activated_at
	`, PlanStatusActive, readinessRunID, planID, workspaceID, expectedRevision)
	plan, err := scanPlan(row)
	if err != nil {
		return TacticalPlan{}, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO v2_tactical_plan_versions (
			workspace_id, tactical_plan_id, revision, readiness_run_id, snapshot_json, activated_by
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tactical_plan_id, revision) DO UPDATE SET
			readiness_run_id=EXCLUDED.readiness_run_id,
			snapshot_json=EXCLUDED.snapshot_json,
			activated_by=EXCLUDED.activated_by,
			activated_at=NOW()
	`, workspaceID, planID, plan.Revision, readinessRunID, tacticsJSON(snapshot), userID)
	if err != nil {
		return TacticalPlan{}, err
	}
	if err := tx.Commit(); err != nil {
		return TacticalPlan{}, err
	}
	return plan, nil
}

func (s *Store) CreateWorkstream(ctx context.Context, workspaceID int, userID int, input WorkstreamInput) (Workstream, error) {
	return s.createWorkstream(ctx, workspaceID, userID, input, SourceManual)
}

func (s *Store) createWorkstream(ctx context.Context, workspaceID int, userID int, input WorkstreamInput, source string) (Workstream, error) {
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
			reason, closes_risk, metric_name, metric_current, metric_target, metrics_json, status,
			health_status, contribution_type, source, sort_order, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		RETURNING
			id, workspace_id, tactical_plan_id, strategy_id, course_id, title, description,
			goal, ckp, reason, closes_risk, metric_name, metric_current, metric_target, metrics_json,
			status, health_status, contribution_type, confidence, source, sort_order, created_at, updated_at
	`, workspaceID, plan.ID, plan.StrategyID, input.Title, input.Description, input.Goal, input.CKP, input.Reason,
		input.ClosesRisk, input.MetricName, input.MetricCurrent, input.MetricTarget, tacticsJSON(input.Metrics), input.Status,
		input.HealthStatus, input.ContributionType, source, sortOrder, userID)

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
	if input.Metrics == nil {
		input.Metrics = current.Metrics
		if input.MetricName == "" {
			input.MetricName = current.MetricName
		}
		if input.MetricCurrent == "" {
			input.MetricCurrent = current.MetricCurrent
		}
		if input.MetricTarget == "" {
			input.MetricTarget = current.MetricTarget
		}
	} else {
		input.MetricName = ""
		input.MetricCurrent = ""
		input.MetricTarget = ""
	}
	input.syncLegacyMetric()
	if input.HealthStatus == "" {
		input.HealthStatus = current.HealthStatus
	}
	if input.ContributionType == "" {
		input.ContributionType = current.ContributionType
	}
	if input.Status == "" {
		input.Status = current.Status
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
			metrics_json=$10,
			status=$11,
			health_status=$12,
			contribution_type=$13,
			updated_at=NOW()
		WHERE id=$14 AND workspace_id=$15 AND archived_at IS NULL
		RETURNING
			id, workspace_id, tactical_plan_id, strategy_id, course_id, title, description,
			goal, ckp, reason, closes_risk, metric_name, metric_current, metric_target, metrics_json,
			status, health_status, contribution_type, confidence, source, sort_order, created_at, updated_at
	`, input.Title, input.Description, input.Goal, input.CKP, input.Reason, input.ClosesRisk, input.MetricName,
		input.MetricCurrent, input.MetricTarget, tacticsJSON(input.Metrics), input.Status, input.HealthStatus, input.ContributionType,
		workstreamID, workspaceID)

	return scanWorkstream(row)
}

func (s *Store) CreateProject(ctx context.Context, workspaceID int, userID int, input ProjectInput) (Project, error) {
	return s.createProject(ctx, workspaceID, userID, input, SourceManual)
}

func (s *Store) createProject(ctx context.Context, workspaceID int, userID int, input ProjectInput, source string) (Project, error) {
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
			failure_criteria, metric_name, expected_value, status, source, sort_order, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING
			id, workspace_id, workstream_id, title, description, why_needed,
			success_criteria, failure_criteria, metric_name, expected_value, status, confidence,
			source, sort_order, created_at, updated_at
	`, workspaceID, workstream.ID, input.Title, input.Description, input.WhyNeeded, input.SuccessCriteria,
		input.FailureCriteria, input.MetricName, input.ExpectedValue, input.Status, source, sortOrder, userID)

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
	if input.ExpectedValue == "" {
		input.ExpectedValue = current.ExpectedValue
	}
	if input.Status == "" {
		input.Status = current.Status
	}

	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_tactical_projects
		SET title=$1,
			description=$2,
			why_needed=$3,
			success_criteria=$4,
			failure_criteria=$5,
			metric_name=$6,
			expected_value=$7,
			status=$8,
			updated_at=NOW()
		WHERE id=$9 AND workspace_id=$10 AND archived_at IS NULL
		RETURNING
			id, workspace_id, workstream_id, title, description, why_needed,
			success_criteria, failure_criteria, metric_name, expected_value, status, confidence,
			source, sort_order, created_at, updated_at
	`, input.Title, input.Description, input.WhyNeeded, input.SuccessCriteria, input.FailureCriteria,
		input.MetricName, input.ExpectedValue, input.Status, projectID, workspaceID)

	return scanProject(row)
}

func (s *Store) CreateRisk(ctx context.Context, workspaceID int, userID int, input RiskInput) (Risk, error) {
	return s.createRisk(ctx, workspaceID, userID, input, SourceManual)
}

func (s *Store) createRisk(ctx context.Context, workspaceID int, userID int, input RiskInput, source string) (Risk, error) {
	input.normalize()
	planID, err := s.resolvePlanForEntity(ctx, workspaceID, input.EntityType, input.EntityID)
	if err != nil {
		return Risk{}, err
	}

	row := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_tactical_risks (
			workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			severity, probability, probability_value, impact_score, economic_exposure, currency,
			owner_user_id, leading_indicators, mitigation_plan, contingency_plan,
			status, coverage_status, source, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		RETURNING
			id, workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			severity, probability, probability_value, impact_score, economic_exposure, currency,
			owner_user_id, leading_indicators, mitigation_plan, contingency_plan,
			status, coverage_status, source, created_at, updated_at
	`, workspaceID, planID, input.EntityType, input.EntityID, input.Title, input.Description,
		input.Severity, input.Probability, input.ProbabilityValue, input.ImpactScore, input.EconomicExposure,
		input.Currency, input.OwnerUserID, input.LeadingIndicators, input.MitigationPlan, input.ContingencyPlan,
		input.Status, input.CoverageStatus, source, userID)

	risk, err := scanRisk(row)
	if err != nil {
		return Risk{}, err
	}
	if err := s.replaceRiskTaskLinks(ctx, workspaceID, risk.ID, input.LinkedTaskIDs); err != nil {
		return Risk{}, err
	}
	risk.LinkedTaskIDs = append([]int{}, input.LinkedTaskIDs...)
	return risk, nil
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
	if input.Probability == "" {
		input.Probability = current.Probability
	}
	if input.ProbabilityValue == nil {
		input.ProbabilityValue = current.ProbabilityValue
	}
	if input.ImpactScore == nil {
		input.ImpactScore = current.ImpactScore
	}
	if input.EconomicExposure == nil {
		input.EconomicExposure = current.EconomicExposure
	}
	if input.Currency == "" {
		input.Currency = current.Currency
	}
	if input.OwnerUserID == nil {
		input.OwnerUserID = current.OwnerUserID
	}
	if input.LeadingIndicators == "" {
		input.LeadingIndicators = current.LeadingIndicators
	}
	if input.MitigationPlan == "" {
		input.MitigationPlan = current.MitigationPlan
	}
	if input.ContingencyPlan == "" {
		input.ContingencyPlan = current.ContingencyPlan
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
			probability=$4,
			probability_value=$5,
			impact_score=$6,
			economic_exposure=$7,
			currency=$8,
			owner_user_id=$9,
			leading_indicators=$10,
			mitigation_plan=$11,
			contingency_plan=$12,
			status=$13,
			coverage_status=$14,
			updated_at=NOW()
		WHERE id=$15 AND workspace_id=$16 AND archived_at IS NULL
		RETURNING
			id, workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			severity, probability, probability_value, impact_score, economic_exposure, currency,
			owner_user_id, leading_indicators, mitigation_plan, contingency_plan,
			status, coverage_status, source, created_at, updated_at
	`, input.Title, input.Description, input.Severity, input.Probability, input.ProbabilityValue,
		input.ImpactScore, input.EconomicExposure, input.Currency, input.OwnerUserID,
		input.LeadingIndicators, input.MitigationPlan, input.ContingencyPlan,
		input.Status, input.CoverageStatus, riskID, workspaceID)

	risk, err := scanRisk(row)
	if err != nil {
		return Risk{}, err
	}
	if input.LinkedTaskIDs != nil {
		if err := s.replaceRiskTaskLinks(ctx, workspaceID, risk.ID, input.LinkedTaskIDs); err != nil {
			return Risk{}, err
		}
		risk.LinkedTaskIDs = append([]int{}, input.LinkedTaskIDs...)
	} else {
		risk.LinkedTaskIDs = current.LinkedTaskIDs
	}
	return risk, nil
}

func (s *Store) CreateHypothesis(ctx context.Context, workspaceID int, userID int, input HypothesisInput) (Hypothesis, error) {
	return s.createHypothesis(ctx, workspaceID, userID, input, SourceManual)
}

func (s *Store) createHypothesis(ctx context.Context, workspaceID int, userID int, input HypothesisInput, source string) (Hypothesis, error) {
	input.normalize()
	planID, err := s.resolvePlanForEntity(ctx, workspaceID, input.EntityType, input.EntityID)
	if err != nil {
		return Hypothesis{}, err
	}
	if err := s.validateMetricTargetForEntity(ctx, workspaceID, input.MetricTargetID, input.EntityType, input.EntityID); err != nil {
		return Hypothesis{}, err
	}
	row := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_tactical_hypotheses (
			workspace_id, tactical_plan_id, entity_type, entity_id, title, statement,
			expected_effect, metric_target_id, test_method, confidence, status, evidence,
			owner_user_id, source, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id, workspace_id, tactical_plan_id, entity_type, entity_id, title,
			statement, expected_effect, metric_target_id, test_method, confidence, status,
			evidence, owner_user_id, source, created_at, updated_at
	`, workspaceID, planID, input.EntityType, input.EntityID, input.Title, input.Statement,
		input.ExpectedEffect, input.MetricTargetID, input.TestMethod, *input.Confidence, input.Status,
		input.Evidence, input.OwnerUserID, source, userID)
	item, err := scanHypothesis(row)
	if err != nil {
		return Hypothesis{}, err
	}
	if err := s.replaceHypothesisTaskLinks(ctx, workspaceID, item.ID, input.LinkedTaskIDs); err != nil {
		return Hypothesis{}, err
	}
	item.LinkedTaskIDs = append([]int{}, input.LinkedTaskIDs...)
	return item, nil
}

func (s *Store) UpdateHypothesis(ctx context.Context, workspaceID int, hypothesisID int64, input HypothesisInput) (Hypothesis, error) {
	input.trim()
	current, err := s.hypothesisByID(ctx, workspaceID, hypothesisID)
	if err != nil {
		return Hypothesis{}, err
	}
	if input.Title == "" {
		input.Title = current.Title
	}
	if input.Statement == "" {
		input.Statement = current.Statement
	}
	if input.ExpectedEffect == "" {
		input.ExpectedEffect = current.ExpectedEffect
	}
	if input.ClearMetricTarget {
		input.MetricTargetID = nil
	} else if input.MetricTargetID == nil {
		input.MetricTargetID = current.MetricTargetID
	}
	if input.TestMethod == "" {
		input.TestMethod = current.TestMethod
	}
	if input.Confidence == nil {
		value := current.Confidence
		input.Confidence = &value
	}
	if input.Status == "" {
		input.Status = current.Status
	}
	if input.Evidence == "" {
		input.Evidence = current.Evidence
	}
	if input.OwnerUserID == nil {
		input.OwnerUserID = current.OwnerUserID
	}
	if err := s.validateMetricTargetForEntity(
		ctx, workspaceID, input.MetricTargetID, current.EntityType, current.EntityID,
	); err != nil {
		return Hypothesis{}, err
	}
	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_tactical_hypotheses
		SET title=$1, statement=$2, expected_effect=$3, metric_target_id=$4,
			test_method=$5, confidence=$6, status=$7, evidence=$8, owner_user_id=$9,
			updated_at=NOW()
		WHERE id=$10 AND workspace_id=$11 AND archived_at IS NULL
		RETURNING id, workspace_id, tactical_plan_id, entity_type, entity_id, title,
			statement, expected_effect, metric_target_id, test_method, confidence, status,
			evidence, owner_user_id, source, created_at, updated_at
	`, input.Title, input.Statement, input.ExpectedEffect, input.MetricTargetID, input.TestMethod,
		*input.Confidence, input.Status, input.Evidence, input.OwnerUserID, hypothesisID, workspaceID)
	item, err := scanHypothesis(row)
	if err != nil {
		return Hypothesis{}, err
	}
	if input.LinkedTaskIDs != nil {
		if err := s.replaceHypothesisTaskLinks(ctx, workspaceID, item.ID, input.LinkedTaskIDs); err != nil {
			return Hypothesis{}, err
		}
		item.LinkedTaskIDs = append([]int{}, input.LinkedTaskIDs...)
	} else {
		item.LinkedTaskIDs = current.LinkedTaskIDs
	}
	return item, nil
}

func (s *Store) CreateOpportunity(ctx context.Context, workspaceID int, userID int, input OpportunityInput) (Opportunity, error) {
	return s.createOpportunity(ctx, workspaceID, userID, input, SourceManual)
}

func (s *Store) createOpportunity(ctx context.Context, workspaceID int, userID int, input OpportunityInput, source string) (Opportunity, error) {
	input.normalize()
	planID, err := s.resolvePlanForEntity(ctx, workspaceID, input.EntityType, input.EntityID)
	if err != nil {
		return Opportunity{}, err
	}

	row := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_tactical_opportunities (
			workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			potential_impact, urgency, status, coverage_status, source, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING
			id, workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			potential_impact, urgency, status, coverage_status, source, created_at, updated_at
	`, workspaceID, planID, input.EntityType, input.EntityID, input.Title, input.Description,
		input.PotentialImpact, input.Urgency, input.Status, input.CoverageStatus, source, userID)

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
	if input.Urgency == "" {
		input.Urgency = current.Urgency
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
			urgency=$4,
			status=$5,
			coverage_status=$6,
			updated_at=NOW()
		WHERE id=$7 AND workspace_id=$8 AND archived_at IS NULL
		RETURNING
			id, workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			potential_impact, urgency, status, coverage_status, source, created_at, updated_at
	`, input.Title, input.Description, input.PotentialImpact, input.Urgency, input.Status, input.CoverageStatus, opportunityID, workspaceID)

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
		RETURNING id, workspace_id, strategy_id, course_id, status, revision, activated_revision, activation_readiness_run_id, title, summary, source, created_by, created_at, updated_at, activated_at
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
			status=CASE WHEN course_id IS DISTINCT FROM $1 THEN $5 ELSE status END,
			activated_at=CASE WHEN course_id IS DISTINCT FROM $1 THEN NULL ELSE activated_at END,
			updated_at=NOW()
		WHERE id=$2 AND workspace_id=$3 AND strategy_id=$4 AND archived_at IS NULL
		RETURNING id, workspace_id, strategy_id, course_id, status, revision, activated_revision, activation_readiness_run_id, title, summary, source, created_by, created_at, updated_at, activated_at
	`, courseID, plan.ID, workspaceID, plan.StrategyID, PlanStatusDraft)
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
		SELECT id, workspace_id, strategy_id, course_id, status, revision, activated_revision, activation_readiness_run_id, title, summary, source, created_by, created_at, updated_at, activated_at
		FROM v2_tactical_plans
		WHERE workspace_id=$1 AND strategy_id=$2 AND archived_at IS NULL
	`, workspaceID, strategyID)
	return scanPlan(row)
}

func (s *Store) planByID(ctx context.Context, workspaceID int, planID int) (TacticalPlan, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, strategy_id, course_id, status, revision, activated_revision, activation_readiness_run_id, title, summary, source, created_by, created_at, updated_at, activated_at
		FROM v2_tactical_plans
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, planID, workspaceID)
	return scanPlan(row)
}

func (s *Store) workstreamByID(ctx context.Context, workspaceID int, workstreamID int) (Workstream, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT
			id, workspace_id, tactical_plan_id, strategy_id, course_id, title, description,
			goal, ckp, reason, closes_risk, metric_name, metric_current, metric_target, metrics_json,
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
			success_criteria, failure_criteria, metric_name, expected_value, status, confidence,
			source, sort_order, created_at, updated_at
		FROM v2_tactical_projects
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, projectID, workspaceID)
	return scanProject(row)
}

func (s *Store) riskByID(ctx context.Context, workspaceID int, riskID int) (Risk, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			severity, probability, probability_value, impact_score, economic_exposure, currency,
			owner_user_id, leading_indicators, mitigation_plan, contingency_plan,
			status, coverage_status, source, created_at, updated_at
		FROM v2_tactical_risks
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, riskID, workspaceID)
	item, err := scanRisk(row)
	if err != nil {
		return Risk{}, err
	}
	item.LinkedTaskIDs, err = s.riskTaskIDs(ctx, workspaceID, item.ID)
	return item, err
}

func (s *Store) opportunityByID(ctx context.Context, workspaceID int, opportunityID int) (Opportunity, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			potential_impact, urgency, status, coverage_status, source, created_at, updated_at
		FROM v2_tactical_opportunities
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, opportunityID, workspaceID)
	return scanOpportunity(row)
}

func (s *Store) hypothesisByID(ctx context.Context, workspaceID int, hypothesisID int64) (Hypothesis, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, tactical_plan_id, entity_type, entity_id, title,
			statement, expected_effect, metric_target_id, test_method, confidence, status,
			evidence, owner_user_id, source, created_at, updated_at
		FROM v2_tactical_hypotheses
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, hypothesisID, workspaceID)
	item, err := scanHypothesis(row)
	if err != nil {
		return Hypothesis{}, err
	}
	item.LinkedTaskIDs, err = s.hypothesisTaskIDs(ctx, workspaceID, item.ID)
	return item, err
}

func (s *Store) validateMetricTargetForEntity(
	ctx context.Context,
	workspaceID int,
	targetID *int64,
	entityType string,
	entityID int,
) error {
	if targetID == nil {
		return nil
	}
	var exists bool
	if err := s.dbx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM v2_metric_targets
			WHERE id=$1 AND workspace_id=$2 AND scope_type=$3 AND scope_id=$4
				AND archived_at IS NULL
		)
	`, *targetID, workspaceID, entityType, entityID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) riskTaskIDs(ctx context.Context, workspaceID int, riskID int) ([]int, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT task_id
		FROM v2_task_risks
		WHERE workspace_id=$1 AND risk_id=$2
		ORDER BY task_id
	`, workspaceID, riskID)
	if err != nil {
		return nil, err
	}
	return scanTaskIDs(rows)
}

func (s *Store) hypothesisTaskIDs(ctx context.Context, workspaceID int, hypothesisID int64) ([]int, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT task_id
		FROM v2_task_hypotheses
		WHERE workspace_id=$1 AND hypothesis_id=$2
		ORDER BY task_id
	`, workspaceID, hypothesisID)
	if err != nil {
		return nil, err
	}
	return scanTaskIDs(rows)
}

func scanTaskIDs(rows *sql.Rows) ([]int, error) {
	defer rows.Close()
	result := []int{}
	for rows.Next() {
		var taskID int
		if err := rows.Scan(&taskID); err != nil {
			return nil, err
		}
		result = append(result, taskID)
	}
	return result, rows.Err()
}

func (s *Store) replaceRiskTaskLinks(ctx context.Context, workspaceID int, riskID int, taskIDs []int) error {
	if taskIDs == nil {
		return nil
	}
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM v2_task_risks
		WHERE workspace_id=$1 AND risk_id=$2
	`, workspaceID, riskID); err != nil {
		return err
	}
	seen := map[int]bool{}
	for _, taskID := range taskIDs {
		if taskID <= 0 || seen[taskID] {
			continue
		}
		seen[taskID] = true
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO v2_task_risks (workspace_id, task_id, risk_id)
			SELECT $1, id, $3
			FROM v2_tasks
			WHERE id=$2 AND workspace_id=$1 AND archived_at IS NULL
			ON CONFLICT DO NOTHING
		`, workspaceID, taskID, riskID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) replaceHypothesisTaskLinks(ctx context.Context, workspaceID int, hypothesisID int64, taskIDs []int) error {
	if taskIDs == nil {
		return nil
	}
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM v2_task_hypotheses
		WHERE workspace_id=$1 AND hypothesis_id=$2
	`, workspaceID, hypothesisID); err != nil {
		return err
	}
	seen := map[int]bool{}
	for _, taskID := range taskIDs {
		if taskID <= 0 || seen[taskID] {
			continue
		}
		seen[taskID] = true
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO v2_task_hypotheses (workspace_id, task_id, hypothesis_id)
			SELECT $1, id, $3
			FROM v2_tasks
			WHERE id=$2 AND workspace_id=$1 AND archived_at IS NULL
			ON CONFLICT DO NOTHING
		`, workspaceID, taskID, hypothesisID); err != nil {
			return err
		}
	}
	return tx.Commit()
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
			goal, ckp, reason, closes_risk, metric_name, metric_current, metric_target, metrics_json,
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
		workstream.Hypotheses = []Hypothesis{}
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
			success_criteria, failure_criteria, metric_name, expected_value, status, confidence,
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
			severity, probability, probability_value, impact_score, economic_exposure, currency,
			owner_user_id, leading_indicators, mitigation_plan, contingency_plan,
			status, coverage_status, source, created_at, updated_at
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	links, err := s.riskTaskIDsForPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	for index := range risks {
		risks[index].LinkedTaskIDs = links[risks[index].ID]
		if risks[index].LinkedTaskIDs == nil {
			risks[index].LinkedTaskIDs = []int{}
		}
	}
	return risks, nil
}

func (s *Store) listOpportunities(ctx context.Context, workspaceID int, planID int) ([]Opportunity, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, workspace_id, tactical_plan_id, entity_type, entity_id, title, description,
			potential_impact, urgency, status, coverage_status, source, created_at, updated_at
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

func (s *Store) listHypotheses(ctx context.Context, workspaceID int, planID int) ([]Hypothesis, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, workspace_id, tactical_plan_id, entity_type, entity_id, title,
			statement, expected_effect, metric_target_id, test_method, confidence, status,
			evidence, owner_user_id, source, created_at, updated_at
		FROM v2_tactical_hypotheses
		WHERE workspace_id=$1 AND tactical_plan_id=$2 AND archived_at IS NULL
		ORDER BY id ASC
	`, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Hypothesis{}
	for rows.Next() {
		item, err := scanHypothesis(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	links, err := s.hypothesisTaskIDsForPlan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	for index := range result {
		result[index].LinkedTaskIDs = links[result[index].ID]
		if result[index].LinkedTaskIDs == nil {
			result[index].LinkedTaskIDs = []int{}
		}
	}
	return result, nil
}

func (s *Store) riskTaskIDsForPlan(ctx context.Context, workspaceID int, planID int) (map[int][]int, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT link.risk_id, link.task_id
		FROM v2_task_risks link
		JOIN v2_tactical_risks risk
			ON risk.id=link.risk_id AND risk.workspace_id=link.workspace_id
		WHERE link.workspace_id=$1 AND risk.tactical_plan_id=$2 AND risk.archived_at IS NULL
		ORDER BY link.risk_id, link.task_id
	`, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int][]int{}
	for rows.Next() {
		var riskID int
		var taskID int
		if err := rows.Scan(&riskID, &taskID); err != nil {
			return nil, err
		}
		result[riskID] = append(result[riskID], taskID)
	}
	return result, rows.Err()
}

func (s *Store) hypothesisTaskIDsForPlan(ctx context.Context, workspaceID int, planID int) (map[int64][]int, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT link.hypothesis_id, link.task_id
		FROM v2_task_hypotheses link
		JOIN v2_tactical_hypotheses hypothesis
			ON hypothesis.id=link.hypothesis_id AND hypothesis.workspace_id=link.workspace_id
		WHERE link.workspace_id=$1 AND hypothesis.tactical_plan_id=$2
			AND hypothesis.archived_at IS NULL
		ORDER BY link.hypothesis_id, link.task_id
	`, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int64][]int{}
	for rows.Next() {
		var hypothesisID int64
		var taskID int
		if err := rows.Scan(&hypothesisID, &taskID); err != nil {
			return nil, err
		}
		result[hypothesisID] = append(result[hypothesisID], taskID)
	}
	return result, rows.Err()
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

func hydrateWorkstreams(workstreams []Workstream, risks []Risk, opportunities []Opportunity, hypotheses []Hypothesis) {
	byID := map[int]int{}
	projectOwner := map[int]int{}
	for i := range workstreams {
		byID[workstreams[i].ID] = i
		for _, project := range workstreams[i].Projects {
			projectOwner[project.ID] = i
		}
	}
	for _, risk := range risks {
		if risk.EntityType == EntityWorkstream {
			if index, ok := byID[risk.EntityID]; ok {
				workstreams[index].Risks = append(workstreams[index].Risks, risk)
			}
		} else if risk.EntityType == EntityProject {
			if index, ok := projectOwner[risk.EntityID]; ok {
				workstreams[index].Risks = append(workstreams[index].Risks, risk)
			}
		}
	}
	for _, opportunity := range opportunities {
		if opportunity.EntityType == EntityWorkstream {
			if index, ok := byID[opportunity.EntityID]; ok {
				workstreams[index].Opportunities = append(workstreams[index].Opportunities, opportunity)
			}
		} else if opportunity.EntityType == EntityProject {
			if index, ok := projectOwner[opportunity.EntityID]; ok {
				workstreams[index].Opportunities = append(workstreams[index].Opportunities, opportunity)
			}
		}
	}
	for _, hypothesis := range hypotheses {
		if hypothesis.EntityType == EntityWorkstream {
			if index, ok := byID[hypothesis.EntityID]; ok {
				workstreams[index].Hypotheses = append(workstreams[index].Hypotheses, hypothesis)
			}
		} else if hypothesis.EntityType == EntityProject {
			if index, ok := projectOwner[hypothesis.EntityID]; ok {
				workstreams[index].Hypotheses = append(workstreams[index].Hypotheses, hypothesis)
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

func emptyUncovered() Uncovered {
	return Uncovered{
		Risks:                   []Risk{},
		Opportunities:           []Opportunity{},
		WorkstreamsWithoutTasks: []TacticsCoverageGap{},
		ProjectsWithoutTasks:    []TacticsCoverageGap{},
		MissingMetrics:          []TacticsCoverageGap{},
		MissingCKP:              []TacticsCoverageGap{},
		MissingSuccessCriteria:  []TacticsCoverageGap{},
	}
}

func (s *Store) coverageGaps(ctx context.Context, workspaceID int, planID int, workstreams []Workstream) (Uncovered, error) {
	result := emptyUncovered()
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT 'task', 'workstream', workstream_id, COUNT(*)
		FROM v2_tasks
		WHERE workspace_id=$1 AND tactical_plan_id=$2 AND archived_at IS NULL AND status <> 'archived'
		GROUP BY workstream_id
		UNION ALL
		SELECT 'task', 'project', project_id, COUNT(*)
		FROM v2_tasks
		WHERE workspace_id=$1 AND tactical_plan_id=$2 AND archived_at IS NULL
			AND status <> 'archived' AND project_id IS NOT NULL
		GROUP BY project_id
		UNION ALL
		SELECT 'metric', scope_type, scope_id, COUNT(*)
		FROM v2_metric_targets
		WHERE workspace_id=$1 AND archived_at IS NULL
			AND scope_type IN ('workstream', 'project')
		GROUP BY scope_type, scope_id
	`, workspaceID, planID)
	if err != nil {
		return Uncovered{}, err
	}
	defer rows.Close()

	workstreamTasks := map[int]int{}
	projectTasks := map[int]int{}
	metricTargets := map[string]int{}
	for rows.Next() {
		var category string
		var entityType string
		var entityID int
		var count int
		if err := rows.Scan(&category, &entityType, &entityID, &count); err != nil {
			return Uncovered{}, err
		}
		switch category + ":" + entityType {
		case "task:workstream":
			workstreamTasks[entityID] = count
		case "task:project":
			projectTasks[entityID] = count
		case "metric:workstream", "metric:project":
			metricTargets[fmt.Sprintf("%s:%d", entityType, entityID)] = count
		}
	}
	if err := rows.Err(); err != nil {
		return Uncovered{}, err
	}

	for _, workstream := range workstreams {
		if workstreamTasks[workstream.ID] == 0 {
			result.WorkstreamsWithoutTasks = append(result.WorkstreamsWithoutTasks, TacticsCoverageGap{
				EntityType: EntityWorkstream, EntityID: workstream.ID, Title: workstream.Title,
				Reason: "У направления нет активных задач.",
			})
		}
		if strings.TrimSpace(workstream.CKP) == "" {
			result.MissingCKP = append(result.MissingCKP, TacticsCoverageGap{
				EntityType: EntityWorkstream, EntityID: workstream.ID, Title: workstream.Title,
				Reason: "Не определён ценный конечный продукт направления.",
			})
		}
		if len(workstream.Metrics) == 0 && metricTargets[fmt.Sprintf("%s:%d", EntityWorkstream, workstream.ID)] == 0 {
			result.MissingMetrics = append(result.MissingMetrics, TacticsCoverageGap{
				EntityType: EntityWorkstream, EntityID: workstream.ID, Title: workstream.Title,
				Reason: "Не определена измеримая метрика направления.",
			})
		}
		for _, project := range workstream.Projects {
			if projectTasks[project.ID] == 0 {
				result.ProjectsWithoutTasks = append(result.ProjectsWithoutTasks, TacticsCoverageGap{
					EntityType: EntityProject, EntityID: project.ID, Title: project.Title,
					Reason: "У проекта нет активных задач.",
				})
			}
			if strings.TrimSpace(project.SuccessCriteria) == "" {
				result.MissingSuccessCriteria = append(result.MissingSuccessCriteria, TacticsCoverageGap{
					EntityType: EntityProject, EntityID: project.ID, Title: project.Title,
					Reason: "Не определён критерий успеха проекта.",
				})
			}
			if strings.TrimSpace(project.MetricName) == "" && metricTargets[fmt.Sprintf("%s:%d", EntityProject, project.ID)] == 0 {
				result.MissingMetrics = append(result.MissingMetrics, TacticsCoverageGap{
					EntityType: EntityProject, EntityID: project.ID, Title: project.Title,
					Reason: "Не определена измеримая метрика проекта.",
				})
			}
		}
	}
	return result, nil
}

type WorkstreamInput struct {
	TacticalPlanID   int            `json:"tactical_plan_id"`
	Title            string         `json:"title"`
	Description      string         `json:"description"`
	Goal             string         `json:"goal"`
	CKP              string         `json:"ckp"`
	Reason           string         `json:"reason"`
	ClosesRisk       string         `json:"closes_risk"`
	MetricName       string         `json:"metric_name"`
	MetricCurrent    string         `json:"metric_current"`
	MetricTarget     string         `json:"metric_target"`
	Metrics          []TacticMetric `json:"metrics"`
	Status           string         `json:"status"`
	HealthStatus     string         `json:"health_status"`
	ContributionType string         `json:"contribution_type"`
}

func (i *WorkstreamInput) normalize() {
	i.trim()
	if i.Metrics == nil && i.MetricName != "" {
		i.Metrics = []TacticMetric{{Name: i.MetricName, Current: i.MetricCurrent, Target: i.MetricTarget}}
	}
	i.syncLegacyMetric()
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
	if i.Metrics != nil {
		metrics := make([]TacticMetric, 0, len(i.Metrics))
		for _, metric := range i.Metrics {
			metric.Name = strings.TrimSpace(metric.Name)
			metric.Current = strings.TrimSpace(metric.Current)
			metric.Target = strings.TrimSpace(metric.Target)
			if metric.Name == "" && metric.Current == "" && metric.Target == "" {
				continue
			}
			metrics = append(metrics, metric)
			if len(metrics) == 3 {
				break
			}
		}
		i.Metrics = metrics
	}
	i.Status = strings.TrimSpace(i.Status)
	i.HealthStatus = strings.TrimSpace(i.HealthStatus)
	i.ContributionType = strings.TrimSpace(i.ContributionType)
}

func (i *WorkstreamInput) syncLegacyMetric() {
	if len(i.Metrics) == 0 {
		return
	}
	i.MetricName = i.Metrics[0].Name
	i.MetricCurrent = i.Metrics[0].Current
	i.MetricTarget = i.Metrics[0].Target
}

type ProjectInput struct {
	WorkstreamID    int    `json:"workstream_id"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	WhyNeeded       string `json:"why_needed"`
	SuccessCriteria string `json:"success_criteria"`
	FailureCriteria string `json:"failure_criteria"`
	MetricName      string `json:"metric_name"`
	ExpectedValue   string `json:"expected_value"`
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
	i.ExpectedValue = strings.TrimSpace(i.ExpectedValue)
	i.Status = strings.TrimSpace(i.Status)
}

type RiskInput struct {
	EntityType        string   `json:"entity_type"`
	EntityID          int      `json:"entity_id"`
	Title             string   `json:"title"`
	Description       string   `json:"description"`
	Severity          string   `json:"severity"`
	Probability       string   `json:"probability"`
	ProbabilityValue  *int     `json:"probability_value"`
	ImpactScore       *int     `json:"impact_score"`
	EconomicExposure  *float64 `json:"economic_exposure"`
	Currency          string   `json:"currency"`
	OwnerUserID       *int     `json:"owner_user_id"`
	LeadingIndicators string   `json:"leading_indicators"`
	MitigationPlan    string   `json:"mitigation_plan"`
	ContingencyPlan   string   `json:"contingency_plan"`
	Status            string   `json:"status"`
	CoverageStatus    string   `json:"coverage_status"`
	LinkedTaskIDs     []int    `json:"linked_task_ids"`
}

func (i *RiskInput) normalize() {
	i.trim()
	if i.Severity == "" {
		i.Severity = "medium"
	}
	if i.Probability == "" {
		i.Probability = "medium"
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
	i.Probability = strings.TrimSpace(i.Probability)
	i.Currency = strings.TrimSpace(i.Currency)
	i.LeadingIndicators = strings.TrimSpace(i.LeadingIndicators)
	i.MitigationPlan = strings.TrimSpace(i.MitigationPlan)
	i.ContingencyPlan = strings.TrimSpace(i.ContingencyPlan)
	i.Status = strings.TrimSpace(i.Status)
	i.CoverageStatus = strings.TrimSpace(i.CoverageStatus)
}

type HypothesisInput struct {
	EntityType        string `json:"entity_type"`
	EntityID          int    `json:"entity_id"`
	Title             string `json:"title"`
	Statement         string `json:"statement"`
	ExpectedEffect    string `json:"expected_effect"`
	MetricTargetID    *int64 `json:"metric_target_id"`
	ClearMetricTarget bool   `json:"clear_metric_target"`
	TestMethod        string `json:"test_method"`
	Confidence        *int   `json:"confidence"`
	Status            string `json:"status"`
	Evidence          string `json:"evidence"`
	OwnerUserID       *int   `json:"owner_user_id"`
	LinkedTaskIDs     []int  `json:"linked_task_ids"`
}

func (i *HypothesisInput) normalize() {
	i.trim()
	if i.Confidence == nil {
		value := 500
		i.Confidence = &value
	}
	if i.Status == "" {
		i.Status = "draft"
	}
}

func (i *HypothesisInput) trim() {
	i.EntityType = strings.TrimSpace(i.EntityType)
	i.Title = strings.TrimSpace(i.Title)
	i.Statement = strings.TrimSpace(i.Statement)
	i.ExpectedEffect = strings.TrimSpace(i.ExpectedEffect)
	i.TestMethod = strings.TrimSpace(i.TestMethod)
	i.Status = strings.TrimSpace(i.Status)
	i.Evidence = strings.TrimSpace(i.Evidence)
}

type OpportunityInput struct {
	EntityType      string `json:"entity_type"`
	EntityID        int    `json:"entity_id"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	PotentialImpact string `json:"potential_impact"`
	Urgency         string `json:"urgency"`
	Status          string `json:"status"`
	CoverageStatus  string `json:"coverage_status"`
}

func (i *OpportunityInput) normalize() {
	i.trim()
	if i.PotentialImpact == "" {
		i.PotentialImpact = "medium"
	}
	if i.Urgency == "" {
		i.Urgency = "medium"
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
	i.Urgency = strings.TrimSpace(i.Urgency)
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
	var activatedRevision sql.NullInt64
	var readinessRunID sql.NullInt64
	var activatedAt sql.NullTime
	err := scanner.Scan(
		&plan.ID, &plan.WorkspaceID, &plan.StrategyID, &courseID, &plan.Status, &plan.Revision, &activatedRevision, &readinessRunID, &plan.Title,
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
	if activatedRevision.Valid {
		value := int(activatedRevision.Int64)
		plan.ActivatedRevision = &value
	}
	if readinessRunID.Valid {
		value := int(readinessRunID.Int64)
		plan.ActivationReadinessRunID = &value
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
	var metricsJSON []byte
	err := scanner.Scan(
		&workstream.ID, &workstream.WorkspaceID, &workstream.TacticalPlanID, &workstream.StrategyID,
		&courseID, &workstream.Title, &workstream.Description, &workstream.Goal, &workstream.CKP,
		&workstream.Reason, &workstream.ClosesRisk, &workstream.MetricName, &workstream.MetricCurrent,
		&workstream.MetricTarget, &metricsJSON, &workstream.Status, &workstream.HealthStatus, &workstream.ContributionType,
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
	workstream.Metrics = []TacticMetric{}
	if len(metricsJSON) > 0 {
		_ = json.Unmarshal(metricsJSON, &workstream.Metrics)
	}
	if len(workstream.Metrics) == 0 && workstream.MetricName != "" {
		workstream.Metrics = []TacticMetric{{Name: workstream.MetricName, Current: workstream.MetricCurrent, Target: workstream.MetricTarget}}
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
		&project.ExpectedValue, &project.Status, &confidence, &project.Source, &project.SortOrder, &project.CreatedAt, &project.UpdatedAt,
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
	var probabilityValue sql.NullInt64
	var impactScore sql.NullInt64
	var economicExposure sql.NullFloat64
	var ownerUserID sql.NullInt64
	err := scanner.Scan(
		&risk.ID, &risk.WorkspaceID, &risk.TacticalPlanID, &risk.EntityType, &risk.EntityID,
		&risk.Title, &risk.Description, &risk.Severity, &risk.Probability, &probabilityValue,
		&impactScore, &economicExposure, &risk.Currency, &ownerUserID, &risk.LeadingIndicators,
		&risk.MitigationPlan, &risk.ContingencyPlan, &risk.Status, &risk.CoverageStatus,
		&risk.Source, &risk.CreatedAt, &risk.UpdatedAt,
	)
	if probabilityValue.Valid {
		value := int(probabilityValue.Int64)
		risk.ProbabilityValue = &value
	}
	if impactScore.Valid {
		value := int(impactScore.Int64)
		risk.ImpactScore = &value
	}
	if economicExposure.Valid {
		value := economicExposure.Float64
		risk.EconomicExposure = &value
	}
	if ownerUserID.Valid {
		value := int(ownerUserID.Int64)
		risk.OwnerUserID = &value
	}
	risk.LinkedTaskIDs = []int{}
	return risk, err
}

func scanOpportunity(scanner scanner) (Opportunity, error) {
	var opportunity Opportunity
	err := scanner.Scan(
		&opportunity.ID, &opportunity.WorkspaceID, &opportunity.TacticalPlanID, &opportunity.EntityType,
		&opportunity.EntityID, &opportunity.Title, &opportunity.Description, &opportunity.PotentialImpact,
		&opportunity.Urgency, &opportunity.Status, &opportunity.CoverageStatus, &opportunity.Source, &opportunity.CreatedAt,
		&opportunity.UpdatedAt,
	)
	return opportunity, err
}

func scanHypothesis(scanner scanner) (Hypothesis, error) {
	var item Hypothesis
	var metricTargetID sql.NullInt64
	var ownerUserID sql.NullInt64
	err := scanner.Scan(
		&item.ID, &item.WorkspaceID, &item.TacticalPlanID, &item.EntityType, &item.EntityID,
		&item.Title, &item.Statement, &item.ExpectedEffect, &metricTargetID, &item.TestMethod,
		&item.Confidence, &item.Status, &item.Evidence, &ownerUserID, &item.Source,
		&item.CreatedAt, &item.UpdatedAt,
	)
	if metricTargetID.Valid {
		value := metricTargetID.Int64
		item.MetricTargetID = &value
	}
	if ownerUserID.Valid {
		value := int(ownerUserID.Int64)
		item.OwnerUserID = &value
	}
	item.LinkedTaskIDs = []int{}
	return item, err
}
