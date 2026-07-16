package strategy

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

type StrategyResearchUpdate struct {
	Status     string `json:"status"`
	ResultText string `json:"result_text"`
}

func (s *Store) ListResearchRequests(ctx context.Context, workspaceID int, strategyID int) ([]StrategyResearchRequest, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, workspace_id, strategy_id, source_readiness_run_id, area, research_goal,
			why_it_matters, context_to_carry, priority, blocking, status, assignee_user_id,
			result_text, created_by, updated_by, created_at, updated_at, accepted_at,
			started_at, completed_at, rejected_at
		FROM v2_strategy_research_requests
		WHERE workspace_id=$1 AND strategy_id=$2
		ORDER BY
			CASE status WHEN 'in_progress' THEN 1 WHEN 'accepted' THEN 2 WHEN 'proposed' THEN 3 WHEN 'completed' THEN 4 ELSE 5 END,
			CASE priority WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END,
			updated_at DESC, id DESC
	`, workspaceID, strategyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []StrategyResearchRequest{}
	for rows.Next() {
		item, err := scanStrategyResearchRequest(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpdateResearchRequest(
	ctx context.Context,
	workspaceID int,
	userID int,
	requestID int,
	input StrategyResearchUpdate,
) (StrategyResearchRequest, error) {
	status := normalizeResearchStatus(input.Status)
	if status == "" {
		return StrategyResearchRequest{}, ErrInvalidResearchStatus
	}

	var currentStatus string
	if err := s.dbx.QueryRowContext(ctx, `
		SELECT status FROM v2_strategy_research_requests WHERE id=$1 AND workspace_id=$2
	`, requestID, workspaceID).Scan(&currentStatus); err != nil {
		return StrategyResearchRequest{}, err
	}
	if !validResearchTransition(currentStatus, status) {
		return StrategyResearchRequest{}, ErrInvalidResearchTransition
	}

	resultText := strings.TrimSpace(input.ResultText)
	if status == ResearchStatusCompleted && resultText == "" {
		return StrategyResearchRequest{}, ErrResearchResultRequired
	}
	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_strategy_research_requests
		SET status=$1,
			assignee_user_id=CASE WHEN $1 IN ($2, $3, $4) THEN COALESCE(assignee_user_id, $5) ELSE assignee_user_id END,
			result_text=CASE WHEN $1=$4 THEN $6 ELSE result_text END,
			updated_by=$5,
			updated_at=NOW(),
			accepted_at=CASE WHEN $1 IN ($2, $3, $4) THEN COALESCE(accepted_at, NOW()) ELSE accepted_at END,
			started_at=CASE WHEN $1 IN ($3, $4) THEN COALESCE(started_at, NOW()) ELSE started_at END,
			completed_at=CASE WHEN $1=$4 THEN NOW() ELSE completed_at END,
			rejected_at=CASE WHEN $1=$7 THEN NOW() ELSE rejected_at END
		WHERE id=$8 AND workspace_id=$9
		RETURNING id, workspace_id, strategy_id, source_readiness_run_id, area, research_goal,
			why_it_matters, context_to_carry, priority, blocking, status, assignee_user_id,
			result_text, created_by, updated_by, created_at, updated_at, accepted_at,
			started_at, completed_at, rejected_at
	`, status, ResearchStatusAccepted, ResearchStatusInProgress, ResearchStatusCompleted,
		userID, resultText, ResearchStatusRejected, requestID, workspaceID)
	return scanStrategyResearchRequest(row)
}

func normalizeResearchStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ResearchStatusProposed:
		return ResearchStatusProposed
	case ResearchStatusAccepted:
		return ResearchStatusAccepted
	case ResearchStatusInProgress:
		return ResearchStatusInProgress
	case ResearchStatusCompleted:
		return ResearchStatusCompleted
	case ResearchStatusRejected:
		return ResearchStatusRejected
	default:
		return ""
	}
}

func validResearchTransition(current string, next string) bool {
	if current == next {
		return true
	}
	switch current {
	case ResearchStatusProposed:
		return next == ResearchStatusAccepted || next == ResearchStatusInProgress || next == ResearchStatusRejected
	case ResearchStatusAccepted:
		return next == ResearchStatusInProgress || next == ResearchStatusCompleted || next == ResearchStatusRejected
	case ResearchStatusInProgress:
		return next == ResearchStatusCompleted || next == ResearchStatusRejected
	case ResearchStatusCompleted, ResearchStatusRejected:
		return false
	default:
		return false
	}
}

func scanStrategyResearchRequest(scanner scanner) (StrategyResearchRequest, error) {
	var item StrategyResearchRequest
	var sourceRunID sql.NullInt64
	var assigneeUserID sql.NullInt64
	var createdBy sql.NullInt64
	var updatedBy sql.NullInt64
	var acceptedAt sql.NullTime
	var startedAt sql.NullTime
	var completedAt sql.NullTime
	var rejectedAt sql.NullTime
	err := scanner.Scan(
		&item.ID, &item.WorkspaceID, &item.StrategyID, &sourceRunID, &item.Area, &item.ResearchGoal,
		&item.WhyItMatters, &item.ContextToCarry, &item.Priority, &item.Blocking, &item.Status,
		&assigneeUserID, &item.ResultText, &createdBy, &updatedBy, &item.CreatedAt, &item.UpdatedAt,
		&acceptedAt, &startedAt, &completedAt, &rejectedAt,
	)
	if err != nil {
		return StrategyResearchRequest{}, err
	}
	assignNullableInt(&item.SourceReadinessRunID, sourceRunID)
	assignNullableInt(&item.AssigneeUserID, assigneeUserID)
	assignNullableInt(&item.CreatedBy, createdBy)
	assignNullableInt(&item.UpdatedBy, updatedBy)
	if acceptedAt.Valid {
		item.AcceptedAt = &acceptedAt.Time
	}
	if startedAt.Valid {
		item.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		item.CompletedAt = &completedAt.Time
	}
	if rejectedAt.Valid {
		item.RejectedAt = &rejectedAt.Time
	}
	return item, nil
}

func assignNullableInt(target **int, value sql.NullInt64) {
	if value.Valid {
		parsed := int(value.Int64)
		*target = &parsed
	}
}

var (
	ErrInvalidResearchStatus     = errors.New("invalid_research_status")
	ErrInvalidResearchTransition = errors.New("invalid_research_transition")
	ErrResearchResultRequired    = errors.New("research_result_required")
)
