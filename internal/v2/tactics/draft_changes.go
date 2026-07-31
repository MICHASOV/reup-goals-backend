package tactics

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"reup-goals-backend/internal/v2/aiactions"
	"reup-goals-backend/internal/v2/departments"
	"reup-goals-backend/internal/v2/metrics"
	"reup-goals-backend/internal/v2/strategicmemory"
	"reup-goals-backend/internal/v2/tasks"
)

const maxDraftChangesPerTurn = 16

func normalizeTacticsDraftChanges(changes []TacticsDraftChange) []TacticsDraftChange {
	result := make([]TacticsDraftChange, 0, len(changes))
	for _, change := range changes {
		change.Operation = strings.ToLower(strings.TrimSpace(change.Operation))
		change.EntityType = strings.ToLower(strings.TrimSpace(change.EntityType))
		change.DraftKey = strings.TrimSpace(change.DraftKey)
		change.ParentEntityType = strings.ToLower(strings.TrimSpace(change.ParentEntityType))
		change.ParentDraftKey = strings.TrimSpace(change.ParentDraftKey)
		change.Title = strings.TrimSpace(change.Title)
		change.Description = strings.TrimSpace(change.Description)
		change.Goal = strings.TrimSpace(change.Goal)
		change.CKP = strings.TrimSpace(change.CKP)
		change.Reason = strings.TrimSpace(change.Reason)
		change.ClosesRisk = strings.TrimSpace(change.ClosesRisk)
		change.MetricName = strings.TrimSpace(change.MetricName)
		change.MetricCurrent = strings.TrimSpace(change.MetricCurrent)
		change.MetricTarget = strings.TrimSpace(change.MetricTarget)
		var metrics []TacticMetric
		if change.Metrics != nil {
			metrics = make([]TacticMetric, 0, len(change.Metrics))
		}
		for _, metric := range change.Metrics {
			metric.Name = strings.TrimSpace(metric.Name)
			metric.Current = strings.TrimSpace(metric.Current)
			metric.Target = strings.TrimSpace(metric.Target)
			metric.Unit = strings.TrimSpace(metric.Unit)
			metric.BetterDirection = strings.ToLower(strings.TrimSpace(metric.BetterDirection))
			metric.TargetDate = strings.TrimSpace(metric.TargetDate)
			if metric.Name == "" && metric.Current == "" && metric.Target == "" {
				continue
			}
			metrics = append(metrics, metric)
			if len(metrics) == 3 {
				break
			}
		}
		change.Metrics = metrics
		change.LeadDepartmentName = strings.TrimSpace(change.LeadDepartmentName)
		change.ParticipantDepartmentIDs = normalizePositiveTacticsIDs(change.ParticipantDepartmentIDs)
		change.WhyNeeded = strings.TrimSpace(change.WhyNeeded)
		change.SuccessCriteria = strings.TrimSpace(change.SuccessCriteria)
		change.FailureCriteria = strings.TrimSpace(change.FailureCriteria)
		change.ExpectedValue = strings.TrimSpace(change.ExpectedValue)
		change.ExpectedResult = strings.TrimSpace(change.ExpectedResult)
		change.WhyNow = strings.TrimSpace(change.WhyNow)
		change.DepartmentName = strings.TrimSpace(change.DepartmentName)
		change.OwnerName = strings.TrimSpace(change.OwnerName)
		change.MemberUserIDs = normalizePositiveTacticsIDs(change.MemberUserIDs)
		change.BlockingTaskIDs = normalizePositiveTacticsIDs(change.BlockingTaskIDs)
		change.DueDate = strings.TrimSpace(change.DueDate)
		change.Severity = strings.ToLower(strings.TrimSpace(change.Severity))
		change.Probability = strings.ToLower(strings.TrimSpace(change.Probability))
		change.LeadingIndicators = strings.TrimSpace(change.LeadingIndicators)
		change.MitigationPlan = strings.TrimSpace(change.MitigationPlan)
		change.ContingencyPlan = strings.TrimSpace(change.ContingencyPlan)
		change.Statement = strings.TrimSpace(change.Statement)
		change.ExpectedEffect = strings.TrimSpace(change.ExpectedEffect)
		change.TestMethod = strings.TrimSpace(change.TestMethod)
		change.HypothesisStatus = strings.ToLower(strings.TrimSpace(change.HypothesisStatus))
		change.PotentialImpact = strings.ToLower(strings.TrimSpace(change.PotentialImpact))
		change.Urgency = strings.ToLower(strings.TrimSpace(change.Urgency))
		change.CoverageStatus = strings.ToLower(strings.TrimSpace(change.CoverageStatus))

		if !change.Apply || (change.Operation != "create" && change.Operation != "update") {
			continue
		}
		switch change.EntityType {
		case EntityWorkstream, EntityProject, EntityDepartment, EntityTask, EntityRisk, EntityHypothesis, EntityOpportunity:
		default:
			continue
		}
		if change.Operation == "create" && change.Title == "" {
			continue
		}
		if change.Operation == "update" && (change.EntityID == nil || *change.EntityID <= 0) {
			continue
		}
		if change.Operation == "create" && !tacticsDraftReadyForConfirmation(change) {
			continue
		}
		result = append(result, change)
		if len(result) >= maxDraftChangesPerTurn {
			break
		}
	}
	return result
}

func ValidateDraftChangesForConfirmation(changes []TacticsDraftChange) error {
	if len(changes) == 0 || len(changes) > maxDraftChangesPerTurn {
		return fmt.Errorf("invalid_tactics_action_count")
	}
	normalized := normalizeTacticsDraftChanges(changes)
	if len(normalized) != len(changes) {
		return fmt.Errorf("incomplete_tactics_action")
	}
	indices := make([]int, len(normalized))
	for index := range normalized {
		indices[index] = index
	}
	_, err := orderedTacticsActionIndices(normalized, indices)
	return err
}

func tacticsDraftReadyForConfirmation(change TacticsDraftChange) bool {
	switch change.EntityType {
	case EntityWorkstream:
		return change.Description != "" && change.Goal != "" && change.CKP != "" &&
			change.Reason != "" && change.LeadDepartmentID > 0 && completeDraftMetrics(change.Metrics)
	case EntityProject:
		hasParent := pointerValue(change.ParentEntityID) > 0 || change.ParentDraftKey != ""
		return hasParent && change.Description != "" && change.WhyNeeded != "" &&
			change.SuccessCriteria != "" && change.FailureCriteria != "" &&
			change.ExpectedValue != "" && change.LeadDepartmentID > 0 &&
			completeDraftMetrics(change.Metrics)
	case EntityTask:
		hasProject := pointerValue(change.ProjectID) > 0 ||
			(change.ParentEntityType == EntityProject &&
				(pointerValue(change.ParentEntityID) > 0 || change.ParentDraftKey != ""))
		hasOwnerDecision := pointerValue(change.OwnerUserID) > 0 || change.OwnerDeferred
		hasDueDateDecision := change.DueDate != "" || change.DueDateDeferred
		if change.DueDate != "" {
			if _, err := time.Parse("2006-01-02", change.DueDate); err != nil {
				return false
			}
		}
		return hasProject && change.Description != "" && change.ExpectedResult != "" &&
			pointerValue(change.DepartmentID) > 0 && hasOwnerDecision && hasDueDateDecision
	case EntityDepartment:
		return change.Description != "" && change.ExpectedResult != ""
	default:
		return true
	}
}

func completeDraftMetrics(items []TacticMetric) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if item.Name == "" || item.Target == "" {
			return false
		}
		if _, err := strconv.ParseFloat(strings.ReplaceAll(item.Target, ",", "."), 64); err != nil {
			return false
		}
	}
	return true
}

func normalizePositiveTacticsIDs(items []int) []int {
	if items == nil {
		return nil
	}
	seen := map[int]struct{}{}
	result := make([]int, 0, len(items))
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

func (s *FacilitatorService) ApplyConfirmedChanges(
	ctx context.Context,
	workspaceID int,
	userID int,
	request ApplyTacticsChangesRequest,
) (ApplyTacticsChangesResponse, error) {
	if request.MessageID <= 0 || len(request.ActionIndices) == 0 {
		return ApplyTacticsChangesResponse{}, fmt.Errorf("invalid_tactics_actions")
	}
	state, err := s.store.Current(ctx, workspaceID, userID)
	if err != nil {
		return ApplyTacticsChangesResponse{}, err
	}
	if state.TacticalPlan == nil {
		return ApplyTacticsChangesResponse{}, fmt.Errorf("tactics_plan_required")
	}
	changes, err := s.store.AssistantDraftChanges(ctx, workspaceID, request.MessageID)
	if err != nil {
		return ApplyTacticsChangesResponse{}, err
	}

	indices, err := orderedTacticsActionIndices(changes, request.ActionIndices)
	if err != nil {
		return ApplyTacticsChangesResponse{}, err
	}
	selectedChanges := make([]TacticsDraftChange, 0, len(indices))
	for _, index := range indices {
		selectedChanges = append(selectedChanges, changes[index])
	}
	if err := ValidateDraftChangesForConfirmation(selectedChanges); err != nil {
		return ApplyTacticsChangesResponse{}, err
	}
	createdByKey := map[string]int{}
	response := ApplyTacticsChangesResponse{
		WorkspaceID:    workspaceID,
		AppliedIndices: []int{},
		AppliedChanges: []AppliedTacticsChange{},
	}
	for _, index := range indices {
		change := changes[index]
		action, confirmed, err := s.store.aiActions.Confirm(
			ctx,
			workspaceID,
			aiactions.ScenarioTacticsFacilitator,
			request.MessageID,
			index,
			userID,
		)
		if err != nil {
			return ApplyTacticsChangesResponse{}, err
		}
		if !confirmed {
			if action.Status == aiactions.StatusApplied {
				if change.DraftKey != "" && action.EntityID != nil {
					createdByKey[change.DraftKey] = *action.EntityID
				}
				continue
			}
			return ApplyTacticsChangesResponse{}, fmt.Errorf("tactics_action_not_confirmable")
		}
		if len(action.Payload) > 0 && string(action.Payload) != "{}" {
			if err := json.Unmarshal(action.Payload, &change); err != nil {
				_ = s.store.aiActions.MarkFailed(ctx, workspaceID, aiactions.ScenarioTacticsFacilitator, request.MessageID, index, err.Error())
				return ApplyTacticsChangesResponse{}, err
			}
		}
		claimed, err := s.store.ClaimTacticsActionApplication(
			ctx, workspaceID, state.TacticalPlan.ID, request.MessageID, index, change, userID,
		)
		if err != nil {
			_ = s.store.aiActions.MarkFailed(ctx, workspaceID, aiactions.ScenarioTacticsFacilitator, request.MessageID, index, err.Error())
			return ApplyTacticsChangesResponse{}, err
		}
		if !claimed {
			continue
		}

		parentID := pointerValue(change.ParentEntityID)
		if parentID <= 0 && change.ParentDraftKey != "" {
			parentID = createdByKey[change.ParentDraftKey]
		}
		item, ok := s.store.applyFacilitatorDraftChange(ctx, workspaceID, userID, *state.TacticalPlan, parentID, change)
		if !ok {
			_ = s.store.aiActions.MarkFailed(ctx, workspaceID, aiactions.ScenarioTacticsFacilitator, request.MessageID, index, "change_not_applicable")
			_ = s.store.FailTacticsActionApplication(ctx, workspaceID, request.MessageID, index, "change_not_applicable")
			return ApplyTacticsChangesResponse{}, fmt.Errorf("tactics_action_not_applicable")
		}
		if change.DraftKey != "" {
			createdByKey[change.DraftKey] = item.EntityID
		}
		item.ID = s.store.recordAppliedTacticsChange(ctx, workspaceID, state.TacticalPlan.ID, request.MessageID, userID, item)
		if err := s.store.aiActions.MarkApplied(
			ctx,
			workspaceID,
			aiactions.ScenarioTacticsFacilitator,
			request.MessageID,
			index,
			change.EntityType,
			item.EntityID,
		); err != nil {
			return ApplyTacticsChangesResponse{}, err
		}
		if err := s.store.CompleteTacticsActionApplication(ctx, workspaceID, request.MessageID, index, item.EntityID); err != nil {
			_ = s.store.FailTacticsActionApplication(ctx, workspaceID, request.MessageID, index, err.Error())
			return ApplyTacticsChangesResponse{}, err
		}
		response.AppliedIndices = append(response.AppliedIndices, index)
		response.AppliedChanges = append(response.AppliedChanges, item)
		s.captureAppliedTacticsEntity(ctx, workspaceID, userID, item)
	}
	if len(response.AppliedChanges) > 0 && s.contextIndex != nil {
		s.contextIndex.RefreshAsync(workspaceID)
	}
	return response, nil
}

func orderedTacticsActionIndices(changes []TacticsDraftChange, requested []int) ([]int, error) {
	indices := append([]int(nil), requested...)
	sort.Ints(indices)
	selected := make(map[int]bool, len(indices))
	byDraftKey := make(map[string]int, len(changes))
	for index, change := range changes {
		if change.DraftKey == "" {
			continue
		}
		if _, exists := byDraftKey[change.DraftKey]; exists {
			return nil, fmt.Errorf("duplicate_tactics_draft_key")
		}
		byDraftKey[change.DraftKey] = index
	}
	for _, index := range indices {
		if index < 0 || index >= len(changes) {
			return nil, fmt.Errorf("invalid_tactics_action_index")
		}
		if !changes[index].Apply {
			return nil, fmt.Errorf("tactics_action_not_confirmable")
		}
		selected[index] = true
	}

	ordered := make([]int, 0, len(selected))
	state := make(map[int]uint8, len(selected))
	var visit func(int) error
	visit = func(index int) error {
		switch state[index] {
		case 1:
			return fmt.Errorf("cyclic_tactics_draft_dependency")
		case 2:
			return nil
		}
		state[index] = 1
		change := changes[index]
		if pointerValue(change.ParentEntityID) <= 0 && change.ParentDraftKey != "" {
			parentIndex, exists := byDraftKey[change.ParentDraftKey]
			if !exists || !selected[parentIndex] {
				return fmt.Errorf("tactics_parent_action_required")
			}
			if err := visit(parentIndex); err != nil {
				return err
			}
		}
		state[index] = 2
		ordered = append(ordered, index)
		return nil
	}
	for _, index := range indices {
		if err := visit(index); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func (s *FacilitatorService) captureAppliedTacticsEntity(
	ctx context.Context,
	workspaceID int,
	userID int,
	change AppliedTacticsChange,
) {
	var sourceType string
	var value any
	switch change.EntityType {
	case EntityWorkstream:
		item, err := s.store.workstreamByID(ctx, workspaceID, change.EntityID)
		if err != nil {
			return
		}
		sourceType = strategicmemory.SourceTypeWorkstream
		value = item
	case EntityProject:
		item, err := s.store.projectByID(ctx, workspaceID, change.EntityID)
		if err != nil {
			return
		}
		sourceType = strategicmemory.SourceTypeProject
		value = item
	case EntityDepartment:
		item, err := departments.NewStore(s.store.dbx).Get(ctx, workspaceID, change.EntityID)
		if err != nil {
			return
		}
		sourceType = strategicmemory.SourceTypeDepartment
		value = item
	case EntityRisk:
		item, err := s.store.riskByID(ctx, workspaceID, change.EntityID)
		if err != nil {
			return
		}
		sourceType = strategicmemory.SourceTypeRisk
		value = item
	case EntityOpportunity:
		item, err := s.store.opportunityByID(ctx, workspaceID, change.EntityID)
		if err != nil {
			return
		}
		sourceType = strategicmemory.SourceTypeOpportunity
		value = item
	case EntityHypothesis:
		item, err := s.store.hypothesisByID(ctx, workspaceID, int64(change.EntityID))
		if err != nil {
			return
		}
		sourceType = strategicmemory.SourceTypeHypothesis
		value = item
	default:
		return
	}
	content := strategicmemory.JSONSourceContent(value)
	if content == "" {
		return
	}
	if _, _, err := s.memoryService.CaptureSource(ctx, workspaceID, userID, strategicmemory.SourceCapture{
		SourceType: sourceType,
		EntityKey:  fmt.Sprintf("%s:%d", sourceType, change.EntityID),
		Content:    content,
		Metadata:   map[string]any{"entity_id": change.EntityID, "source": "confirmed_ai_action"},
	}); err != nil {
		log.Printf("[WARN] capture applied tactics entity workspace_id=%d type=%s id=%d: %v", workspaceID, sourceType, change.EntityID, err)
	}
}

func (s *Store) applyFacilitatorDraftChange(
	ctx context.Context,
	workspaceID int,
	userID int,
	plan TacticalPlan,
	parentID int,
	change TacticsDraftChange,
) (AppliedTacticsChange, bool) {
	switch change.EntityType {
	case EntityWorkstream:
		return s.applyWorkstreamDraft(ctx, workspaceID, userID, plan, change)
	case EntityProject:
		return s.applyProjectDraft(ctx, workspaceID, userID, plan, parentID, change)
	case EntityDepartment:
		return s.applyDepartmentDraft(ctx, workspaceID, userID, change)
	case EntityTask:
		return s.applyTaskDraft(ctx, workspaceID, userID, plan, parentID, change)
	case EntityRisk:
		return s.applyRiskDraft(ctx, workspaceID, userID, plan, parentID, change)
	case EntityHypothesis:
		return s.applyHypothesisDraft(ctx, workspaceID, userID, plan, parentID, change)
	case EntityOpportunity:
		return s.applyOpportunityDraft(ctx, workspaceID, userID, plan, parentID, change)
	default:
		return AppliedTacticsChange{}, false
	}
}

func (s *Store) applyDepartmentDraft(
	ctx context.Context,
	workspaceID int,
	userID int,
	change TacticsDraftChange,
) (AppliedTacticsChange, bool) {
	store := departments.NewStore(s.dbx)
	operation := change.Operation
	name := change.Title
	description := change.Description
	responsibility := change.ExpectedResult
	var kpis []departments.KPI
	if change.Metrics != nil {
		kpis = make([]departments.KPI, 0, len(change.Metrics))
		for _, metric := range change.Metrics {
			kpis = append(kpis, departments.KPI{
				Name: metric.Name, Current: metric.Current, Target: metric.Target,
			})
		}
	}
	input := departments.Input{
		Name: &name, Description: &description, Responsibility: &responsibility,
		ManagerUserID: change.OwnerUserID, MemberUserIDs: change.MemberUserIDs, KPIs: kpis,
	}
	var item departments.Detail
	var err error
	if operation == "create" {
		item, err = store.Create(ctx, workspaceID, userID, input)
	} else {
		entityID := pointerValue(change.EntityID)
		if entityID <= 0 {
			return AppliedTacticsChange{}, false
		}
		item, err = store.Update(ctx, workspaceID, entityID, input)
	}
	if err != nil {
		return AppliedTacticsChange{}, false
	}
	return appliedChange(operation, EntityDepartment, item.Department.ID, item.Department.Name, change), true
}

func (s *Store) applyWorkstreamDraft(ctx context.Context, workspaceID int, userID int, plan TacticalPlan, change TacticsDraftChange) (AppliedTacticsChange, bool) {
	operation := change.Operation
	entityID := pointerValue(change.EntityID)
	if operation == "create" {
		if existingID, err := s.workstreamIDByTitle(ctx, workspaceID, plan.ID, change.Title); err == nil {
			operation = "update"
			entityID = existingID
		}
	}
	input := WorkstreamInput{
		TacticalPlanID: plan.ID,
		Title:          change.Title, Description: change.Description, Goal: change.Goal, CKP: change.CKP,
		Reason: change.Reason, ClosesRisk: change.ClosesRisk, MetricName: change.MetricName,
		MetricCurrent: change.MetricCurrent, MetricTarget: change.MetricTarget, Metrics: change.Metrics,
	}
	var item Workstream
	var err error
	if operation == "create" {
		input.Status = WorkstreamStatusActive
		input.HealthStatus = "В работе"
		item, err = s.createWorkstream(ctx, workspaceID, userID, input, SourceAISuggestion)
	} else {
		current, lookupErr := s.workstreamByID(ctx, workspaceID, entityID)
		if lookupErr != nil || current.TacticalPlanID != plan.ID {
			return AppliedTacticsChange{}, false
		}
		item, err = s.UpdateWorkstream(ctx, workspaceID, entityID, input)
	}
	if err != nil {
		return AppliedTacticsChange{}, false
	}
	if !s.applyDraftResponsibility(ctx, workspaceID, departments.EntityWorkstream, item.ID, change) {
		return AppliedTacticsChange{}, false
	}
	if !s.applyDraftMetrics(ctx, workspaceID, userID, metrics.ScopeWorkstream, item.ID, change.Metrics) {
		return AppliedTacticsChange{}, false
	}
	return appliedChange(operation, EntityWorkstream, item.ID, item.Title, change), true
}

func (s *Store) applyProjectDraft(ctx context.Context, workspaceID int, userID int, plan TacticalPlan, parentID int, change TacticsDraftChange) (AppliedTacticsChange, bool) {
	operation := change.Operation
	entityID := pointerValue(change.EntityID)
	var current Project
	if operation == "create" {
		if parentID <= 0 {
			return AppliedTacticsChange{}, false
		}
		parent, err := s.workstreamByID(ctx, workspaceID, parentID)
		if err != nil || parent.TacticalPlanID != plan.ID {
			return AppliedTacticsChange{}, false
		}
		if existingID, err := s.projectIDByTitle(ctx, workspaceID, parentID, change.Title); err == nil {
			operation = "update"
			entityID = existingID
		}
	}
	if !draftMetricsParsable(change.Metrics) {
		return AppliedTacticsChange{}, false
	}
	if operation == "update" {
		var lookupErr error
		current, lookupErr = s.projectByID(ctx, workspaceID, entityID)
		if lookupErr != nil {
			return AppliedTacticsChange{}, false
		}
		if parentID <= 0 {
			parentID = current.WorkstreamID
		}
	}
	input := ProjectInput{
		WorkstreamID: parentID, Title: change.Title, Description: change.Description,
		ExpectedResult: change.ExpectedResult, WhyNeeded: change.WhyNeeded, SuccessCriteria: change.SuccessCriteria,
		FailureCriteria: change.FailureCriteria, MetricName: change.MetricName, ExpectedValue: change.ExpectedValue,
	}
	if input.MetricName == "" && len(change.Metrics) > 0 {
		input.MetricName = change.Metrics[0].Name
	}
	var item Project
	var err error
	if operation == "create" {
		input.Status = ProjectStatusActive
		item, err = s.createProject(ctx, workspaceID, userID, input, SourceAISuggestion)
	} else {
		parent, parentErr := s.workstreamByID(ctx, workspaceID, parentID)
		if parentErr != nil || parent.TacticalPlanID != plan.ID {
			return AppliedTacticsChange{}, false
		}
		item, err = s.UpdateProject(ctx, workspaceID, entityID, input)
	}
	if err != nil {
		return AppliedTacticsChange{}, false
	}
	if !s.applyDraftResponsibility(ctx, workspaceID, departments.EntityProject, item.ID, change) {
		return AppliedTacticsChange{}, false
	}
	if !s.applyDraftMetrics(ctx, workspaceID, userID, metrics.ScopeProject, item.ID, change.Metrics) {
		return AppliedTacticsChange{}, false
	}
	return appliedChange(operation, EntityProject, item.ID, item.Title, change), true
}

func (s *Store) applyTaskDraft(
	ctx context.Context,
	workspaceID int,
	userID int,
	plan TacticalPlan,
	parentID int,
	change TacticsDraftChange,
) (AppliedTacticsChange, bool) {
	taskStore := tasks.NewStore(s.dbx)
	operation := change.Operation
	entityID := pointerValue(change.EntityID)

	if operation == "update" {
		current, err := taskStore.Get(ctx, workspaceID, entityID)
		if err != nil || current.TacticalPlanID != plan.ID {
			return AppliedTacticsChange{}, false
		}
		input := tasks.TaskInput{}
		if change.Title != "" {
			input.Title = &change.Title
		}
		if change.Description != "" {
			input.Description = &change.Description
		}
		if change.ExpectedResult != "" {
			input.ExpectedResult = &change.ExpectedResult
		}
		if change.WhyNow != "" {
			input.WhyNow = &change.WhyNow
		}
		if change.ProjectID != nil {
			input.ProjectID = change.ProjectID
			projectWorkstreamID, valid := s.taskDraftProjectWorkstream(ctx, workspaceID, plan.ID, *change.ProjectID)
			if !valid {
				return AppliedTacticsChange{}, false
			}
			input.WorkstreamID = projectWorkstreamID
		}
		if change.DepartmentID != nil {
			input.DepartmentID = change.DepartmentID
		}
		if change.OwnerUserID != nil {
			input.OwnerUserID = change.OwnerUserID
		} else if change.OwnerDeferred {
			input.ClearOwner = true
		}
		if change.DueDate != "" {
			input.DueDate = &change.DueDate
		} else if change.DueDateDeferred {
			empty := ""
			input.DueDate = &empty
		}
		input.BlockingTaskIDs = change.BlockingTaskIDs
		item, err := taskStore.Update(ctx, workspaceID, userID, current.ID, input)
		if err != nil {
			return AppliedTacticsChange{}, false
		}
		if err := taskStore.QueueTaskEvaluation(ctx, workspaceID, userID, item.ID, true); err != nil {
			return AppliedTacticsChange{}, false
		}
		return appliedChange(operation, EntityTask, item.ID, item.Title, change), true
	}

	projectID := pointerValue(change.ProjectID)
	if projectID <= 0 && change.ParentEntityType == EntityProject {
		projectID = parentID
	}
	workstreamID, valid := s.taskDraftProjectWorkstream(ctx, workspaceID, plan.ID, projectID)
	if !valid {
		return AppliedTacticsChange{}, false
	}
	if change.WorkstreamID != nil && *change.WorkstreamID != workstreamID {
		return AppliedTacticsChange{}, false
	}

	source := tasks.SourceAISuggestion
	projectIDValue := projectID
	input := tasks.TaskInput{
		WorkstreamID:    workstreamID,
		ProjectID:       &projectIDValue,
		DepartmentID:    change.DepartmentID,
		Title:           &change.Title,
		Description:     &change.Description,
		ExpectedResult:  &change.ExpectedResult,
		WhyNow:          &change.WhyNow,
		OwnerUserID:     change.OwnerUserID,
		BlockingTaskIDs: change.BlockingTaskIDs,
		SourceType:      &source,
	}
	if change.DueDate != "" {
		input.DueDate = &change.DueDate
	}
	item, err := taskStore.Create(ctx, workspaceID, userID, input)
	if err != nil {
		return AppliedTacticsChange{}, false
	}
	if err := taskStore.QueueTaskEvaluation(ctx, workspaceID, userID, item.ID, true); err != nil {
		return AppliedTacticsChange{}, false
	}
	return appliedChange(operation, EntityTask, item.ID, item.Title, change), true
}

func (s *Store) taskDraftProjectWorkstream(
	ctx context.Context,
	workspaceID int,
	planID int,
	projectID int,
) (int, bool) {
	if projectID <= 0 {
		return 0, false
	}
	var workstreamID int
	var tacticalPlanID int
	err := s.dbx.QueryRowContext(ctx, `
		SELECT project.workstream_id, workstream.tactical_plan_id
		FROM v2_tactical_projects project
		JOIN v2_tactical_workstreams workstream ON workstream.id=project.workstream_id
		WHERE project.id=$1 AND project.workspace_id=$2
			AND project.archived_at IS NULL AND workstream.archived_at IS NULL
	`, projectID, workspaceID).Scan(&workstreamID, &tacticalPlanID)
	return workstreamID, err == nil && tacticalPlanID == planID
}

func (s *Store) applyDraftResponsibility(
	ctx context.Context,
	workspaceID int,
	entityType string,
	entityID int,
	change TacticsDraftChange,
) bool {
	if change.LeadDepartmentID <= 0 {
		return change.Operation == "update"
	}
	_, err := departments.NewStore(s.dbx).SetResponsibility(ctx, workspaceID, departments.Responsibility{
		EntityType:               entityType,
		EntityID:                 entityID,
		LeadDepartmentID:         change.LeadDepartmentID,
		ParticipantDepartmentIDs: change.ParticipantDepartmentIDs,
	})
	return err == nil
}

func (s *Store) applyDraftMetrics(
	ctx context.Context,
	workspaceID int,
	userID int,
	scopeType string,
	scopeID int,
	items []TacticMetric,
) bool {
	if len(items) == 0 {
		return true
	}
	metricStore := metrics.NewStore(s.dbx)
	for index, item := range items {
		target, err := parseDraftMetricNumber(item.Target)
		if err != nil {
			return false
		}
		var baseline *float64
		if strings.TrimSpace(item.Current) != "" {
			value, parseErr := parseDraftMetricNumber(item.Current)
			if parseErr != nil {
				return false
			}
			baseline = &value
		}
		unit := strings.TrimSpace(item.Unit)
		if unit == "" {
			unit = "number"
		}
		betterDirection := strings.ToLower(strings.TrimSpace(item.BetterDirection))
		switch betterDirection {
		case "increase", "decrease", "range":
		case "target":
			// Older agent drafts used "target" for a metric that should stay
			// close to a chosen value. Preserve them as a target range.
			betterDirection = "range"
		default:
			betterDirection = "increase"
		}
		role := metrics.RoleSupporting
		if index == 0 {
			role = metrics.RolePrimary
		}
		if _, err := metricStore.CreateTarget(ctx, workspaceID, userID, metrics.TargetInput{
			Name:            item.Name,
			Description:     "Метрика, подтвержденная при создании через AI-советника.",
			Category:        "Пользовательские",
			Unit:            unit,
			ValueType:       "number",
			BetterDirection: betterDirection,
			ScopeType:       scopeType,
			ScopeID:         scopeID,
			Role:            role,
			BaselineValue:   baseline,
			TargetValue:     &target,
			TargetDate:      item.TargetDate,
			DisplayUnit:     unit,
			Cadence:         "monthly",
			SourceNote:      "Подтверждено в AI-советнике",
		}); err != nil {
			return false
		}
	}
	return true
}

func parseDraftMetricNumber(value string) (float64, error) {
	normalized := strings.NewReplacer(
		"\u00a0", " ",
		"\u202f", " ",
	).Replace(strings.TrimSpace(value))
	normalized = draftMetricRange.ReplaceAllString(normalized, "$1|$2")
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, ",", ".")
	if parsed, err := strconv.ParseFloat(normalized, 64); err == nil {
		return parsed, nil
	}
	matches := draftMetricNumber.FindAllString(normalized, -1)
	if len(matches) == 0 {
		return 0, strconv.ErrSyntax
	}
	return strconv.ParseFloat(strings.ReplaceAll(matches[len(matches)-1], ",", "."), 64)
}

var (
	draftMetricRange  = regexp.MustCompile(`(\d)\s*[-–—]\s*(\d)`)
	draftMetricNumber = regexp.MustCompile(`[-+]?\d+(?:[.,]\d+)?`)
)

func draftMetricsParsable(items []TacticMetric) bool {
	for _, item := range items {
		if _, err := parseDraftMetricNumber(item.Target); err != nil {
			return false
		}
		if strings.TrimSpace(item.Current) != "" {
			if _, err := parseDraftMetricNumber(item.Current); err != nil {
				return false
			}
		}
	}
	return true
}

func (s *Store) applyRiskDraft(ctx context.Context, workspaceID int, userID int, plan TacticalPlan, parentID int, change TacticsDraftChange) (AppliedTacticsChange, bool) {
	operation := change.Operation
	entityID := pointerValue(change.EntityID)
	entityType := change.ParentEntityType
	if entityType == "" {
		entityType = EntityPlan
	}
	if entityType == EntityPlan && parentID <= 0 {
		parentID = plan.ID
	}
	if operation == "create" {
		if existingID, err := s.riskIDByTitle(ctx, workspaceID, plan.ID, entityType, parentID, change.Title); err == nil {
			operation = "update"
			entityID = existingID
		}
	}
	input := RiskInput{
		EntityType: entityType, EntityID: parentID, Title: change.Title,
		Description: change.Description, Severity: change.Severity, Probability: change.Probability,
		ProbabilityValue: change.ProbabilityValue, ImpactScore: change.ImpactScore,
		OwnerUserID: change.OwnerUserID, LeadingIndicators: change.LeadingIndicators,
		MitigationPlan: change.MitigationPlan, ContingencyPlan: change.ContingencyPlan,
		CoverageStatus: change.CoverageStatus,
	}
	var item Risk
	var err error
	if operation == "create" {
		item, err = s.createRisk(ctx, workspaceID, userID, input, SourceAISuggestion)
	} else {
		current, lookupErr := s.riskByID(ctx, workspaceID, entityID)
		if lookupErr != nil || current.TacticalPlanID != plan.ID {
			return AppliedTacticsChange{}, false
		}
		item, err = s.UpdateRisk(ctx, workspaceID, entityID, input)
	}
	if err != nil {
		return AppliedTacticsChange{}, false
	}
	return appliedChange(operation, "risk", item.ID, item.Title, change), true
}

func (s *Store) applyHypothesisDraft(ctx context.Context, workspaceID int, userID int, plan TacticalPlan, parentID int, change TacticsDraftChange) (AppliedTacticsChange, bool) {
	operation := change.Operation
	entityID := pointerValue(change.EntityID)
	entityType := change.ParentEntityType
	if !ValidHypothesisEntityType(entityType) || parentID <= 0 {
		return AppliedTacticsChange{}, false
	}
	if operation == "create" {
		if existingID, err := s.hypothesisIDByTitle(ctx, workspaceID, plan.ID, entityType, parentID, change.Title); err == nil {
			operation = "update"
			entityID = int(existingID)
		}
	}
	input := HypothesisInput{
		EntityType: entityType, EntityID: parentID, Title: change.Title,
		Statement: change.Statement, ExpectedEffect: change.ExpectedEffect,
		TestMethod: change.TestMethod, Status: change.HypothesisStatus, OwnerUserID: change.OwnerUserID,
	}
	var item Hypothesis
	var err error
	if operation == "create" {
		item, err = s.createHypothesis(ctx, workspaceID, userID, input, SourceAISuggestion)
	} else {
		current, lookupErr := s.hypothesisByID(ctx, workspaceID, int64(entityID))
		if lookupErr != nil || current.TacticalPlanID != plan.ID {
			return AppliedTacticsChange{}, false
		}
		item, err = s.UpdateHypothesis(ctx, workspaceID, int64(entityID), input)
	}
	if err != nil {
		return AppliedTacticsChange{}, false
	}
	return appliedChange(operation, EntityHypothesis, int(item.ID), item.Title, change), true
}

func (s *Store) applyOpportunityDraft(ctx context.Context, workspaceID int, userID int, plan TacticalPlan, parentID int, change TacticsDraftChange) (AppliedTacticsChange, bool) {
	operation := change.Operation
	entityID := pointerValue(change.EntityID)
	entityType := change.ParentEntityType
	if entityType == "" {
		entityType = EntityPlan
	}
	if entityType == EntityPlan && parentID <= 0 {
		parentID = plan.ID
	}
	if operation == "create" {
		if existingID, err := s.opportunityIDByTitle(ctx, workspaceID, plan.ID, entityType, parentID, change.Title); err == nil {
			operation = "update"
			entityID = existingID
		}
	}
	input := OpportunityInput{EntityType: entityType, EntityID: parentID, Title: change.Title, Description: change.Description, PotentialImpact: change.PotentialImpact, Urgency: change.Urgency, CoverageStatus: change.CoverageStatus}
	var item Opportunity
	var err error
	if operation == "create" {
		item, err = s.createOpportunity(ctx, workspaceID, userID, input, SourceAISuggestion)
	} else {
		current, lookupErr := s.opportunityByID(ctx, workspaceID, entityID)
		if lookupErr != nil || current.TacticalPlanID != plan.ID {
			return AppliedTacticsChange{}, false
		}
		item, err = s.UpdateOpportunity(ctx, workspaceID, entityID, input)
	}
	if err != nil {
		return AppliedTacticsChange{}, false
	}
	return appliedChange(operation, "opportunity", item.ID, item.Title, change), true
}

func appliedChange(operation string, entityType string, entityID int, title string, change TacticsDraftChange) AppliedTacticsChange {
	fields := map[string]any{}
	raw, _ := json.Marshal(change)
	_ = json.Unmarshal(raw, &fields)
	delete(fields, "apply")
	return AppliedTacticsChange{Operation: operation, EntityType: entityType, EntityID: entityID, Title: title, Status: "draft", Fields: fields}
}

func (s *Store) recordAppliedTacticsChange(ctx context.Context, workspaceID int, planID int, messageID int, userID int, item AppliedTacticsChange) int {
	var id int
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_tactics_applied_changes (
			workspace_id, tactical_plan_id, source_message_id, operation, entity_type,
			entity_id, title, change_json, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`, workspaceID, planID, messageID, item.Operation, item.EntityType, item.EntityID, item.Title, tacticsJSON(item.Fields), userID).Scan(&id)
	if err != nil {
		return 0
	}
	return id
}

func (s *Store) workstreamIDByTitle(ctx context.Context, workspaceID int, planID int, title string) (int, error) {
	var id int
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id FROM v2_tactical_workstreams
		WHERE workspace_id=$1 AND tactical_plan_id=$2 AND archived_at IS NULL
			AND LOWER(BTRIM(title))=LOWER(BTRIM($3))
		ORDER BY id DESC LIMIT 1
	`, workspaceID, planID, title).Scan(&id)
	return id, err
}

func (s *Store) projectIDByTitle(ctx context.Context, workspaceID int, workstreamID int, title string) (int, error) {
	var id int
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id FROM v2_tactical_projects
		WHERE workspace_id=$1 AND workstream_id=$2 AND archived_at IS NULL
			AND LOWER(BTRIM(title))=LOWER(BTRIM($3))
		ORDER BY id DESC LIMIT 1
	`, workspaceID, workstreamID, title).Scan(&id)
	return id, err
}

func (s *Store) riskIDByTitle(ctx context.Context, workspaceID int, planID int, entityType string, entityID int, title string) (int, error) {
	var id int
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id FROM v2_tactical_risks
		WHERE workspace_id=$1 AND tactical_plan_id=$2 AND entity_type=$3 AND entity_id=$4
			AND archived_at IS NULL AND LOWER(BTRIM(title))=LOWER(BTRIM($5))
		ORDER BY id DESC LIMIT 1
	`, workspaceID, planID, entityType, entityID, title).Scan(&id)
	return id, err
}

func (s *Store) opportunityIDByTitle(ctx context.Context, workspaceID int, planID int, entityType string, entityID int, title string) (int, error) {
	var id int
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id FROM v2_tactical_opportunities
		WHERE workspace_id=$1 AND tactical_plan_id=$2 AND entity_type=$3 AND entity_id=$4
			AND archived_at IS NULL AND LOWER(BTRIM(title))=LOWER(BTRIM($5))
		ORDER BY id DESC LIMIT 1
	`, workspaceID, planID, entityType, entityID, title).Scan(&id)
	return id, err
}

func (s *Store) hypothesisIDByTitle(ctx context.Context, workspaceID int, planID int, entityType string, entityID int, title string) (int64, error) {
	var id int64
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id FROM v2_tactical_hypotheses
		WHERE workspace_id=$1 AND tactical_plan_id=$2 AND entity_type=$3 AND entity_id=$4
			AND archived_at IS NULL AND LOWER(BTRIM(title))=LOWER(BTRIM($5))
		ORDER BY id DESC LIMIT 1
	`, workspaceID, planID, entityType, entityID, title).Scan(&id)
	return id, err
}

func pointerValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
