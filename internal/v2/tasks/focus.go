package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	FocusScopeWorkspace  = "workspace"
	FocusScopeWorkstream = "workstream"
	FocusScopeProject    = "project"
)

type FocusTask struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	PriorityScore int    `json:"priority_score"`
	PriorityTier  string `json:"priority_tier"`
}

type FocusDecision struct {
	TaskID       int       `json:"task_id"`
	TaskTitle    string    `json:"task_title"`
	TopTaskID    *int      `json:"top_task_id,omitempty"`
	TopTaskTitle string    `json:"top_task_title"`
	ChosenScore  int       `json:"chosen_score"`
	TopScore     int       `json:"top_score"`
	ChosenRank   int       `json:"chosen_rank"`
	Aligned      bool      `json:"aligned"`
	CreatedAt    time.Time `json:"created_at"`
}

type FocusResult struct {
	TaskID           int        `json:"task_id"`
	Title            string     `json:"title"`
	CompletionResult string     `json:"completion_result"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
}

type FocusSummary struct {
	ScopeType       string         `json:"scope_type"`
	ScopeID         int            `json:"scope_id"`
	StrategyID      *int           `json:"strategy_id,omitempty"`
	SelectionCount  int            `json:"selection_count"`
	AlignedCount    int            `json:"aligned_count"`
	Score           *int           `json:"score,omitempty"`
	RecommendedTask *FocusTask     `json:"recommended_task,omitempty"`
	LatestDecision  *FocusDecision `json:"latest_decision,omitempty"`
	LatestResults   []FocusResult  `json:"latest_results"`
}

type focusCandidate struct {
	ID    int
	Title string
	Score int
	Tier  string
}

func ValidFocusScope(scopeType string) bool {
	switch scopeType {
	case FocusScopeWorkspace, FocusScopeWorkstream, FocusScopeProject:
		return true
	default:
		return false
	}
}

func focusScore(aligned, total int) *int {
	if total <= 0 {
		return nil
	}
	value := (aligned*100 + total/2) / total
	return &value
}

func (s *Store) recordFocusDecisions(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID int,
	userID int,
	task Task,
) error {
	strategyID, err := focusStrategyID(ctx, tx, workspaceID, task.TacticalPlanID)
	if err != nil {
		return err
	}
	scopes := []struct {
		scopeType string
		scopeID   int
	}{
		{scopeType: FocusScopeWorkspace, scopeID: 0},
		{scopeType: FocusScopeWorkstream, scopeID: task.WorkstreamID},
	}
	if task.ProjectID != nil {
		scopes = append(scopes, struct {
			scopeType string
			scopeID   int
		}{scopeType: FocusScopeProject, scopeID: *task.ProjectID})
	}

	for _, scope := range scopes {
		candidates, err := focusCandidates(
			ctx,
			tx,
			workspaceID,
			task.TacticalPlanID,
			scope.scopeType,
			scope.scopeID,
			&task.ID,
		)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			continue
		}
		chosen := candidates[0]
		top := candidates[0]
		for _, candidate := range candidates {
			if candidate.ID == task.ID {
				chosen = candidate
			}
			if candidate.Score > top.Score || (candidate.Score == top.Score && candidate.ID < top.ID) {
				top = candidate
			}
		}
		rank := 1
		seenScores := map[int]struct{}{}
		for _, candidate := range candidates {
			if candidate.Score > chosen.Score {
				seenScores[candidate.Score] = struct{}{}
			}
		}
		rank += len(seenScores)
		_, err = tx.ExecContext(ctx, `
			INSERT INTO v2_task_focus_decisions (
				workspace_id, strategy_id, user_id, task_id, scope_type, scope_id,
				chosen_score, top_score, chosen_rank, aligned, top_task_id
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		`,
			workspaceID, nullableInt(strategyID), userID, task.ID, scope.scopeType, scope.scopeID,
			chosen.Score, top.Score, rank, chosen.Score >= top.Score, top.ID,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) RecordFocusStart(
	ctx context.Context,
	workspaceID int,
	userID int,
	task Task,
) error {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.recordFocusDecisions(ctx, tx, workspaceID, userID, task); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) FocusSummary(
	ctx context.Context,
	workspaceID int,
	scopeType string,
	scopeID int,
) (FocusSummary, error) {
	if !ValidFocusScope(scopeType) || scopeID < 0 || (scopeType != FocusScopeWorkspace && scopeID == 0) {
		return FocusSummary{}, ErrForbidden
	}
	result := FocusSummary{
		ScopeType:     scopeType,
		ScopeID:       scopeID,
		LatestResults: []FocusResult{},
	}
	strategyID, err := currentFocusStrategyID(ctx, s.dbx, workspaceID)
	if err != nil {
		return FocusSummary{}, err
	}
	result.StrategyID = strategyID

	err = s.dbx.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE aligned)
		FROM v2_task_focus_decisions
		WHERE workspace_id=$1 AND strategy_id IS NOT DISTINCT FROM $2
			AND scope_type=$3 AND scope_id=$4
	`, workspaceID, nullableInt(strategyID), scopeType, scopeID).Scan(
		&result.SelectionCount,
		&result.AlignedCount,
	)
	if err != nil {
		return FocusSummary{}, err
	}
	result.Score = focusScore(result.AlignedCount, result.SelectionCount)

	planID, planErr := currentFocusPlanID(ctx, s.dbx, workspaceID, strategyID)
	if planErr != nil && !errors.Is(planErr, sql.ErrNoRows) {
		return FocusSummary{}, planErr
	}
	if planErr == nil {
		candidates, candidateErr := focusCandidates(
			ctx,
			s.dbx,
			workspaceID,
			planID,
			scopeType,
			scopeID,
			nil,
		)
		if candidateErr != nil {
			return FocusSummary{}, candidateErr
		}
		if len(candidates) > 0 {
			top := candidates[0]
			for _, candidate := range candidates[1:] {
				if candidate.Score > top.Score || (candidate.Score == top.Score && candidate.ID < top.ID) {
					top = candidate
				}
			}
			result.RecommendedTask = &FocusTask{
				ID: top.ID, Title: top.Title, PriorityScore: top.Score, PriorityTier: top.Tier,
			}
		}
	}

	var latest FocusDecision
	var topTaskID sql.NullInt64
	latestErr := s.dbx.QueryRowContext(ctx, `
		SELECT decision.task_id, task.title, decision.top_task_id, COALESCE(top_task.title, ''),
			decision.chosen_score, decision.top_score, decision.chosen_rank,
			decision.aligned, decision.created_at
		FROM v2_task_focus_decisions decision
		JOIN v2_tasks task ON task.id=decision.task_id
		LEFT JOIN v2_tasks top_task ON top_task.id=decision.top_task_id
		WHERE decision.workspace_id=$1 AND decision.strategy_id IS NOT DISTINCT FROM $2
			AND decision.scope_type=$3 AND decision.scope_id=$4
		ORDER BY decision.created_at DESC, decision.id DESC
		LIMIT 1
	`, workspaceID, nullableInt(strategyID), scopeType, scopeID).Scan(
		&latest.TaskID,
		&latest.TaskTitle,
		&topTaskID,
		&latest.TopTaskTitle,
		&latest.ChosenScore,
		&latest.TopScore,
		&latest.ChosenRank,
		&latest.Aligned,
		&latest.CreatedAt,
	)
	if latestErr != nil && !errors.Is(latestErr, sql.ErrNoRows) {
		return FocusSummary{}, latestErr
	}
	if latestErr == nil {
		if topTaskID.Valid {
			value := int(topTaskID.Int64)
			latest.TopTaskID = &value
		}
		result.LatestDecision = &latest
	}

	results, err := latestFocusResults(ctx, s.dbx, workspaceID, scopeType, scopeID)
	if err != nil {
		return FocusSummary{}, err
	}
	result.LatestResults = results
	return result, nil
}

type focusQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func focusCandidates(
	ctx context.Context,
	queryer focusQueryer,
	workspaceID int,
	planID int,
	scopeType string,
	scopeID int,
	chosenTaskID *int,
) ([]focusCandidate, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT task.id, task.title,
			COALESCE(latest_evaluation.priority_score, 0) AS effective_score,
			COALESCE(latest_evaluation.priority_tier, '') AS effective_tier
		FROM v2_tasks task
		LEFT JOIN LATERAL (
			SELECT evaluation.priority_score, evaluation.priority_tier
			FROM v2_task_evaluations evaluation
			WHERE evaluation.workspace_id=task.workspace_id AND evaluation.task_id=task.id
			ORDER BY evaluation.created_at DESC, evaluation.id DESC
			LIMIT 1
		) latest_evaluation ON TRUE
		WHERE task.workspace_id=$1
			AND task.tactical_plan_id=$2
			AND task.archived_at IS NULL
			AND (
				task.id=$5
				OR (
					$5::INTEGER IS NULL
					AND task.status='free'
					AND task.blocked=FALSE
					AND COALESCE(task.backlog_category, '') <> 'recommended_delete'
					AND NOT EXISTS (
						SELECT 1
						FROM v2_task_dependencies dependency
						JOIN v2_tasks blocker ON blocker.id=dependency.blocker_task_id
						WHERE dependency.workspace_id=task.workspace_id
							AND dependency.task_id=task.id
							AND blocker.status NOT IN ('done', 'archived')
					)
				)
				OR (
					$5::INTEGER IS NOT NULL
					AND task.status='free'
					AND task.blocked=FALSE
					AND COALESCE(task.backlog_category, '') <> 'recommended_delete'
					AND NOT EXISTS (
						SELECT 1
						FROM v2_task_dependencies dependency
						JOIN v2_tasks blocker ON blocker.id=dependency.blocker_task_id
						WHERE dependency.workspace_id=task.workspace_id
							AND dependency.task_id=task.id
							AND blocker.status NOT IN ('done', 'archived')
					)
				)
			)
			AND (
				$3='workspace'
				OR ($3='workstream' AND task.workstream_id=$4)
				OR ($3='project' AND task.project_id=$4)
			)
	`, workspaceID, planID, scopeType, scopeID, nullableInt(chosenTaskID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := []focusCandidate{}
	for rows.Next() {
		var item focusCandidate
		if err := rows.Scan(&item.ID, &item.Title, &item.Score, &item.Tier); err != nil {
			return nil, err
		}
		candidates = append(candidates, item)
	}
	return candidates, rows.Err()
}

func latestFocusResults(
	ctx context.Context,
	queryer focusQueryer,
	workspaceID int,
	scopeType string,
	scopeID int,
) ([]FocusResult, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT task.id, task.title, task.completion_result, task.completed_at
		FROM v2_tasks task
		WHERE task.workspace_id=$1
			AND task.status='done'
			AND BTRIM(task.completion_result) <> ''
			AND (
				$2='workspace'
				OR ($2='workstream' AND task.workstream_id=$3)
				OR ($2='project' AND task.project_id=$3)
			)
		ORDER BY task.completed_at DESC NULLS LAST, task.updated_at DESC, task.id DESC
		LIMIT 3
	`, workspaceID, scopeType, scopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []FocusResult{}
	for rows.Next() {
		var item FocusResult
		if err := rows.Scan(&item.TaskID, &item.Title, &item.CompletionResult, &item.CompletedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func focusStrategyID(
	ctx context.Context,
	queryer focusQueryer,
	workspaceID int,
	planID int,
) (*int, error) {
	var strategyID sql.NullInt64
	err := queryer.QueryRowContext(ctx, `
		SELECT strategy_id
		FROM v2_tactical_plans
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, planID, workspaceID).Scan(&strategyID)
	if err != nil {
		return nil, err
	}
	if !strategyID.Valid {
		return nil, nil
	}
	value := int(strategyID.Int64)
	return &value, nil
}

func currentFocusStrategyID(
	ctx context.Context,
	queryer focusQueryer,
	workspaceID int,
) (*int, error) {
	var strategyID int
	err := queryer.QueryRowContext(ctx, `
		SELECT id
		FROM v2_strategies
		WHERE workspace_id=$1 AND status='active' AND archived_at IS NULL
		ORDER BY version DESC, id DESC
		LIMIT 1
	`, workspaceID).Scan(&strategyID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &strategyID, nil
}

func currentFocusPlanID(
	ctx context.Context,
	queryer focusQueryer,
	workspaceID int,
	strategyID *int,
) (int, error) {
	var planID int
	err := queryer.QueryRowContext(ctx, `
		SELECT id
		FROM v2_tactical_plans
		WHERE workspace_id=$1 AND strategy_id IS NOT DISTINCT FROM $2 AND archived_at IS NULL
		ORDER BY (status='active') DESC, updated_at DESC, id DESC
		LIMIT 1
	`, workspaceID, nullableInt(strategyID)).Scan(&planID)
	return planID, err
}

func focusScopeFromQuery(scopeType string, rawScopeID string) (string, int, error) {
	scopeType = strings.TrimSpace(strings.ToLower(scopeType))
	if !ValidFocusScope(scopeType) {
		return "", 0, ErrForbidden
	}
	if scopeType == FocusScopeWorkspace {
		return scopeType, 0, nil
	}
	var scopeID int
	if _, err := fmt.Sscanf(strings.TrimSpace(rawScopeID), "%d", &scopeID); err != nil || scopeID <= 0 {
		return "", 0, ErrForbidden
	}
	return scopeType, scopeID, nil
}
