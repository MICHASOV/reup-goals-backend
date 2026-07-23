package tactics

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"reup-goals-backend/internal/v2/aiactions"
	"reup-goals-backend/internal/v2/strategicmemory"
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
		metrics := make([]TacticMetric, 0, len(change.Metrics))
		for _, metric := range change.Metrics {
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
		change.Metrics = metrics
		change.WhyNeeded = strings.TrimSpace(change.WhyNeeded)
		change.SuccessCriteria = strings.TrimSpace(change.SuccessCriteria)
		change.FailureCriteria = strings.TrimSpace(change.FailureCriteria)
		change.ExpectedValue = strings.TrimSpace(change.ExpectedValue)
		change.Severity = strings.ToLower(strings.TrimSpace(change.Severity))
		change.Probability = strings.ToLower(strings.TrimSpace(change.Probability))
		change.PotentialImpact = strings.ToLower(strings.TrimSpace(change.PotentialImpact))
		change.Urgency = strings.ToLower(strings.TrimSpace(change.Urgency))
		change.CoverageStatus = strings.ToLower(strings.TrimSpace(change.CoverageStatus))

		if !change.Apply || (change.Operation != "create" && change.Operation != "update") {
			continue
		}
		switch change.EntityType {
		case EntityWorkstream, EntityProject, "risk", "opportunity":
		default:
			continue
		}
		if change.Operation == "create" && change.Title == "" {
			continue
		}
		if change.Operation == "update" && (change.EntityID == nil || *change.EntityID <= 0) {
			continue
		}
		result = append(result, change)
		if len(result) >= maxDraftChangesPerTurn {
			break
		}
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

	indices := append([]int{}, request.ActionIndices...)
	sort.Ints(indices)
	seen := map[int]bool{}
	createdByKey := map[string]int{}
	response := ApplyTacticsChangesResponse{
		WorkspaceID:    workspaceID,
		AppliedIndices: []int{},
		AppliedChanges: []AppliedTacticsChange{},
	}
	for _, index := range indices {
		if seen[index] {
			continue
		}
		seen[index] = true
		if index < 0 || index >= len(changes) {
			return ApplyTacticsChangesResponse{}, fmt.Errorf("invalid_tactics_action_index")
		}
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
	case "risk":
		return s.applyRiskDraft(ctx, workspaceID, userID, plan, parentID, change)
	case "opportunity":
		return s.applyOpportunityDraft(ctx, workspaceID, userID, plan, parentID, change)
	default:
		return AppliedTacticsChange{}, false
	}
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
	return appliedChange(operation, EntityWorkstream, item.ID, item.Title, change), true
}

func (s *Store) applyProjectDraft(ctx context.Context, workspaceID int, userID int, plan TacticalPlan, parentID int, change TacticsDraftChange) (AppliedTacticsChange, bool) {
	operation := change.Operation
	entityID := pointerValue(change.EntityID)
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
	input := ProjectInput{
		WorkstreamID: parentID, Title: change.Title, Description: change.Description,
		WhyNeeded: change.WhyNeeded, SuccessCriteria: change.SuccessCriteria,
		FailureCriteria: change.FailureCriteria, MetricName: change.MetricName, ExpectedValue: change.ExpectedValue,
	}
	var item Project
	var err error
	if operation == "create" {
		input.Status = ProjectStatusActive
		item, err = s.createProject(ctx, workspaceID, userID, input, SourceAISuggestion)
	} else {
		current, lookupErr := s.projectByID(ctx, workspaceID, entityID)
		if lookupErr != nil {
			return AppliedTacticsChange{}, false
		}
		parent, parentErr := s.workstreamByID(ctx, workspaceID, current.WorkstreamID)
		if parentErr != nil || parent.TacticalPlanID != plan.ID {
			return AppliedTacticsChange{}, false
		}
		item, err = s.UpdateProject(ctx, workspaceID, entityID, input)
	}
	if err != nil {
		return AppliedTacticsChange{}, false
	}
	return appliedChange(operation, EntityProject, item.ID, item.Title, change), true
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
	input := RiskInput{EntityType: entityType, EntityID: parentID, Title: change.Title, Description: change.Description, Severity: change.Severity, Probability: change.Probability, CoverageStatus: change.CoverageStatus}
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

func pointerValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
