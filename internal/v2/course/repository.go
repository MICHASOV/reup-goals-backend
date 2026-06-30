package course

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

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
			Course:  nil,
			Reason:  "no_active_strategy",
			Message: "Для создания курса нужна активная стратегия.",
		}, nil
	}
	if err != nil {
		return CurrentResponse{}, err
	}

	course, err := s.getOrCreate(ctx, workspaceID, userID, strategy)
	if err != nil {
		return CurrentResponse{}, err
	}

	return CurrentResponse{
		Course:   &course,
		Strategy: &strategy,
	}, nil
}

func (s *Store) Update(ctx context.Context, workspaceID int, courseID int, input CourseInput) (Course, error) {
	current, err := s.courseByID(ctx, workspaceID, courseID)
	if err != nil {
		return Course{}, err
	}

	input.trim()
	if input.Title == "" {
		input.Title = current.Title
	}
	if input.Direction == "" {
		input.Direction = current.Direction
	}
	if input.StrategicGoal == "" {
		input.StrategicGoal = current.StrategicGoal
	}
	if input.Meaning == "" {
		input.Meaning = current.Meaning
	}
	horizon := current.Horizon
	if input.Horizon != nil && *input.Horizon > 0 {
		horizon = *input.Horizon
	}
	if input.HorizonUnit == "" {
		input.HorizonUnit = current.HorizonUnit
	}
	if input.StartDate == "" {
		input.StartDate = current.StartDate
	}
	if input.EndDate == nil {
		input.EndDate = current.EndDate
	}
	if input.KeyMetric == "" {
		input.KeyMetric = current.KeyMetric
	}
	if input.SuccessCriterion == "" {
		input.SuccessCriterion = current.SuccessCriterion
	}
	if input.Status == "" {
		input.Status = current.Status
	}

	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_courses
		SET title=$1,
			direction=$2,
			strategic_goal=$3,
			meaning=$4,
			horizon=$5,
			horizon_unit=$6,
			start_date=$7::DATE,
			end_date=$8::DATE,
			key_metric=$9,
			success_criterion=$10,
			status=$11,
			source=$12,
			activated_at=CASE WHEN $11=$13 THEN COALESCE(activated_at, NOW()) ELSE activated_at END,
			updated_at=NOW()
		WHERE id=$14 AND workspace_id=$15 AND archived_at IS NULL
		RETURNING
			id, workspace_id, strategy_id, title, direction, strategic_goal, meaning,
			horizon, horizon_unit, start_date::TEXT, end_date::TEXT, key_metric,
			success_criterion, status, source, created_by, created_at, updated_at, activated_at
	`, input.Title, input.Direction, input.StrategicGoal, input.Meaning, horizon, input.HorizonUnit,
		input.StartDate, nullableString(input.EndDate), input.KeyMetric, input.SuccessCriterion,
		input.Status, SourceManual, StatusActive, courseID, workspaceID)

	return scanCourse(row)
}

func (s *Store) activeStrategy(ctx context.Context, workspaceID int) (StrategySummary, error) {
	var strategy StrategySummary
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id, status, title, summary
		FROM v2_strategies
		WHERE workspace_id=$1 AND status='active' AND archived_at IS NULL
		ORDER BY version DESC, created_at DESC
		LIMIT 1
	`, workspaceID).Scan(&strategy.ID, &strategy.Status, &strategy.Title, &strategy.Summary)
	return strategy, err
}

func (s *Store) getOrCreate(ctx context.Context, workspaceID int, userID int, strategy StrategySummary) (Course, error) {
	course, err := s.courseByStrategy(ctx, workspaceID, strategy.ID)
	if err == nil {
		return course, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Course{}, err
	}

	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return Course{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, workspaceID+6000000); err != nil {
		return Course{}, err
	}

	if course, err := courseByStrategyTx(ctx, tx, workspaceID, strategy.ID); err == nil {
		if err := tx.Commit(); err != nil {
			return Course{}, err
		}
		return course, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Course{}, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE v2_courses
		SET status=$1, archived_at=NOW(), updated_at=NOW()
		WHERE workspace_id=$2
			AND strategy_id<>$3
			AND status=$4
			AND archived_at IS NULL
	`, StatusArchived, workspaceID, strategy.ID, StatusActive); err != nil {
		return Course{}, err
	}

	artifacts, err := strategyArtifactsTx(ctx, tx, workspaceID, strategy.ID)
	if err != nil {
		return Course{}, err
	}
	draft := buildDraft(strategy, artifacts)

	row := tx.QueryRowContext(ctx, `
		INSERT INTO v2_courses (
			workspace_id, strategy_id, title, direction, strategic_goal, meaning,
			horizon, horizon_unit, start_date, end_date, key_metric, success_criterion,
			status, source, created_by, activated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, CURRENT_DATE, CURRENT_DATE + $7, $9, $10, $11, $12, $13, NOW())
		ON CONFLICT (workspace_id, strategy_id) DO UPDATE SET updated_at=v2_courses.updated_at
		RETURNING
			id, workspace_id, strategy_id, title, direction, strategic_goal, meaning,
			horizon, horizon_unit, start_date::TEXT, end_date::TEXT, key_metric,
			success_criterion, status, source, created_by, created_at, updated_at, activated_at
	`, workspaceID, strategy.ID, draft.Title, draft.Direction, draft.StrategicGoal, draft.Meaning,
		draft.Horizon, draft.HorizonUnit, draft.KeyMetric, draft.SuccessCriterion, StatusActive, SourceFromStrategy, userID)

	course, err = scanCourse(row)
	if err != nil {
		return Course{}, err
	}

	if err := tx.Commit(); err != nil {
		return Course{}, err
	}

	return course, nil
}

func (s *Store) courseByStrategy(ctx context.Context, workspaceID int, strategyID int) (Course, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT
			id, workspace_id, strategy_id, title, direction, strategic_goal, meaning,
			horizon, horizon_unit, start_date::TEXT, end_date::TEXT, key_metric,
			success_criterion, status, source, created_by, created_at, updated_at, activated_at
		FROM v2_courses
		WHERE workspace_id=$1 AND strategy_id=$2 AND archived_at IS NULL AND status IN ($3, $4)
		ORDER BY CASE status WHEN $4 THEN 1 ELSE 2 END, created_at DESC
		LIMIT 1
	`, workspaceID, strategyID, StatusDraft, StatusActive)
	return scanCourse(row)
}

func (s *Store) courseByID(ctx context.Context, workspaceID int, courseID int) (Course, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT
			id, workspace_id, strategy_id, title, direction, strategic_goal, meaning,
			horizon, horizon_unit, start_date::TEXT, end_date::TEXT, key_metric,
			success_criterion, status, source, created_by, created_at, updated_at, activated_at
		FROM v2_courses
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, courseID, workspaceID)
	return scanCourse(row)
}

func courseByStrategyTx(ctx context.Context, tx *sql.Tx, workspaceID int, strategyID int) (Course, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT
			id, workspace_id, strategy_id, title, direction, strategic_goal, meaning,
			horizon, horizon_unit, start_date::TEXT, end_date::TEXT, key_metric,
			success_criterion, status, source, created_by, created_at, updated_at, activated_at
		FROM v2_courses
		WHERE workspace_id=$1 AND strategy_id=$2 AND archived_at IS NULL AND status IN ($3, $4)
		ORDER BY CASE status WHEN $4 THEN 1 ELSE 2 END, created_at DESC
		LIMIT 1
	`, workspaceID, strategyID, StatusDraft, StatusActive)
	return scanCourse(row)
}

func strategyArtifactsTx(ctx context.Context, tx *sql.Tx, workspaceID int, strategyID int) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT type, content
		FROM v2_strategy_artifacts
		WHERE workspace_id=$1 AND strategy_id=$2
	`, workspaceID, strategyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	artifacts := map[string]string{}
	for rows.Next() {
		var artifactType string
		var content string
		if err := rows.Scan(&artifactType, &content); err != nil {
			return nil, err
		}
		artifacts[artifactType] = strings.TrimSpace(content)
	}
	return artifacts, rows.Err()
}

func buildDraft(strategy StrategySummary, artifacts map[string]string) CourseInput {
	direction := firstNonEmpty(artifacts["strategic_direction"])
	goal := firstNonEmpty(artifacts["local_goal"], artifacts["global_goal"])
	meaning := firstNonEmpty(artifacts["current_challenge"], strategy.Summary, artifacts["strategy_verdict"])
	keyMetric := firstNonEmpty(artifacts["key_metric"])
	successCriterion := firstNonEmpty(artifacts["validation_plan"], artifacts["strategy_verdict"])

	title := "Курс компании"
	if direction != "" {
		title = direction
	}

	horizon := 90
	return CourseInput{
		Title:            title,
		Direction:        direction,
		StrategicGoal:    goal,
		Meaning:          meaning,
		Horizon:          &horizon,
		HorizonUnit:      "days",
		KeyMetric:        keyMetric,
		SuccessCriterion: successCriterion,
		Status:           StatusActive,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func (i *CourseInput) trim() {
	i.Title = strings.TrimSpace(i.Title)
	i.Direction = strings.TrimSpace(i.Direction)
	i.StrategicGoal = strings.TrimSpace(i.StrategicGoal)
	i.Meaning = strings.TrimSpace(i.Meaning)
	i.HorizonUnit = strings.TrimSpace(i.HorizonUnit)
	i.StartDate = strings.TrimSpace(i.StartDate)
	i.KeyMetric = strings.TrimSpace(i.KeyMetric)
	i.SuccessCriterion = strings.TrimSpace(i.SuccessCriterion)
	i.Status = strings.TrimSpace(i.Status)
	if i.EndDate != nil {
		value := strings.TrimSpace(*i.EndDate)
		i.EndDate = &value
	}
}

func nullableString(value *string) any {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	return *value
}

type scanner interface {
	Scan(dest ...any) error
}

func scanCourse(scanner scanner) (Course, error) {
	var course Course
	var endDate sql.NullString
	var createdBy sql.NullInt64
	var activatedAt sql.NullTime
	err := scanner.Scan(
		&course.ID,
		&course.WorkspaceID,
		&course.StrategyID,
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
		&course.Status,
		&course.Source,
		&createdBy,
		&course.CreatedAt,
		&course.UpdatedAt,
		&activatedAt,
	)
	if err != nil {
		return Course{}, err
	}
	if endDate.Valid {
		course.EndDate = &endDate.String
	}
	if createdBy.Valid {
		value := int(createdBy.Int64)
		course.CreatedBy = &value
	}
	if activatedAt.Valid {
		course.ActivatedAt = &activatedAt.Time
	}

	return course, nil
}

func TodayString() string {
	return time.Now().Format("2006-01-02")
}
