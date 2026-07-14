package course

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Store struct {
	dbx *sql.DB
}

var (
	ErrCourseIncomplete       = errors.New("course_incomplete")
	ErrCourseStrategyStale    = errors.New("course_strategy_stale")
	ErrCourseStrategyMismatch = errors.New("course_strategy_mismatch")
	ErrCourseArtifactsMissing = errors.New("course_strategy_artifacts_missing")
)

type strategySnapshot struct {
	RunID                  int
	SessionRevision        int
	CurrentSessionRevision int
	Artifacts              map[string]strategyArtifactValue
}

func (s strategySnapshot) isCurrent() bool {
	return s.RunID > 0 && s.SessionRevision == s.CurrentSessionRevision
}

func NewStore(dbx *sql.DB) *Store {
	return &Store{dbx: dbx}
}

func (s *Store) Current(ctx context.Context, workspaceID int, userID int) (CurrentResponse, error) {
	strategy, err := s.activeStrategy(ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return CurrentResponse{
			Course:  nil,
			Sources: []CourseSource{},
			Reason:  "no_active_strategy",
			Message: "Для создания курса нужна активная стратегия.",
		}, nil
	}
	if err != nil {
		return CurrentResponse{}, fmt.Errorf("load active strategy: %w", err)
	}

	knowledgeBase, err := s.knowledgeBaseSummary(ctx, workspaceID)
	if err != nil {
		return CurrentResponse{}, fmt.Errorf("load knowledge base summary: %w", err)
	}

	snapshot, snapshotErr := s.strategySnapshot(ctx, workspaceID, strategy.ID)
	course, courseErr := s.courseByStrategy(ctx, workspaceID, strategy.ID)
	if errors.Is(courseErr, sql.ErrNoRows) {
		if snapshotErr != nil {
			if errors.Is(snapshotErr, sql.ErrNoRows) {
				return CurrentResponse{
					Course:        nil,
					Strategy:      &strategy,
					Sources:       []CourseSource{},
					KnowledgeBase: knowledgeBase,
					Reason:        "strategy_artifacts_missing",
					Message:       "Активная стратегия пока не собрана в актуальные артефакты.",
				}, nil
			}
			return CurrentResponse{}, fmt.Errorf("load strategy snapshot: %w", snapshotErr)
		}
		course, err = s.createDraft(ctx, workspaceID, userID, strategy, snapshot)
		if err != nil {
			return CurrentResponse{}, fmt.Errorf("materialize course draft: %w", err)
		}
	} else if courseErr != nil {
		return CurrentResponse{}, fmt.Errorf("load course: %w", courseErr)
	}

	if snapshotErr != nil && !errors.Is(snapshotErr, sql.ErrNoRows) {
		return CurrentResponse{}, fmt.Errorf("load strategy snapshot: %w", snapshotErr)
	}

	syncState := buildCourseSync(course, snapshot, snapshotErr)
	return CurrentResponse{
		Course:        &course,
		Strategy:      &strategy,
		Sync:          &syncState,
		Sources:       buildCourseSources(snapshot.Artifacts),
		KnowledgeBase: knowledgeBase,
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
	if input.KeyMetric == "" {
		input.KeyMetric = current.KeyMetric
	}
	if input.SuccessCriterion == "" {
		input.SuccessCriterion = current.SuccessCriterion
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
			end_date=COALESCE($8::DATE, $7::DATE + ($5::INTEGER)),
			key_metric=$9,
			success_criterion=$10,
			source=$11,
			updated_at=NOW()
		WHERE id=$12 AND workspace_id=$13 AND archived_at IS NULL
		RETURNING
			id, workspace_id, strategy_id, source_synthesis_run_id, source_session_revision,
			title, direction, strategic_goal, meaning,
			horizon, horizon_unit, start_date::TEXT, end_date::TEXT, key_metric,
			success_criterion, status, source, created_by, created_at, updated_at, activated_at
	`, input.Title, input.Direction, input.StrategicGoal, input.Meaning, horizon, input.HorizonUnit,
		input.StartDate, nullableString(input.EndDate), input.KeyMetric, input.SuccessCriterion,
		SourceManual, courseID, workspaceID)

	return scanCourse(row)
}

func (s *Store) activeStrategy(ctx context.Context, workspaceID int) (StrategySummary, error) {
	var strategy StrategySummary
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id, status, version, title, summary
		FROM v2_strategies
		WHERE workspace_id=$1 AND status='active' AND archived_at IS NULL
		ORDER BY version DESC, created_at DESC
		LIMIT 1
	`, workspaceID).Scan(&strategy.ID, &strategy.Status, &strategy.Version, &strategy.Title, &strategy.Summary)
	return strategy, err
}

func (s *Store) createDraft(
	ctx context.Context,
	workspaceID int,
	userID int,
	strategy StrategySummary,
	snapshot strategySnapshot,
) (Course, error) {
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

	draft := buildDraft(strategy, snapshot.Artifacts)

	row := tx.QueryRowContext(ctx, `
		UPDATE v2_courses
		SET source_synthesis_run_id=$3,
			source_session_revision=$4,
			title=$5,
			direction=$6,
			strategic_goal=$7,
			meaning=$8,
			horizon=$9,
			horizon_unit=$10,
			start_date=CURRENT_DATE,
			end_date=CURRENT_DATE + ($9::INTEGER),
			key_metric=$11,
			success_criterion=$12,
			status=$13,
			source=$14,
			created_by=COALESCE(created_by, $15),
			activated_at=NULL,
			archived_at=NULL,
			updated_at=NOW()
		WHERE workspace_id=$1 AND strategy_id=$2
		RETURNING
			id, workspace_id, strategy_id, source_synthesis_run_id, source_session_revision,
			title, direction, strategic_goal, meaning,
			horizon, horizon_unit, start_date::TEXT, end_date::TEXT, key_metric,
			success_criterion, status, source, created_by, created_at, updated_at, activated_at
	`, workspaceID, strategy.ID, snapshot.RunID, snapshot.SessionRevision,
		draft.Title, draft.Direction, draft.StrategicGoal, draft.Meaning,
		draft.Horizon, draft.HorizonUnit, draft.KeyMetric, draft.SuccessCriterion,
		StatusDraft, SourceFromStrategy, userID)

	course, err := scanCourse(row)
	if errors.Is(err, sql.ErrNoRows) {
		row = tx.QueryRowContext(ctx, `
			INSERT INTO v2_courses (
				workspace_id, strategy_id, source_synthesis_run_id, source_session_revision,
				title, direction, strategic_goal, meaning,
				horizon, horizon_unit, start_date, end_date, key_metric, success_criterion,
				status, source, created_by
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, CURRENT_DATE, CURRENT_DATE + ($9::INTEGER), $11, $12, $13, $14, $15)
			RETURNING
				id, workspace_id, strategy_id, source_synthesis_run_id, source_session_revision,
				title, direction, strategic_goal, meaning,
				horizon, horizon_unit, start_date::TEXT, end_date::TEXT, key_metric,
				success_criterion, status, source, created_by, created_at, updated_at, activated_at
		`, workspaceID, strategy.ID, snapshot.RunID, snapshot.SessionRevision,
			draft.Title, draft.Direction, draft.StrategicGoal, draft.Meaning,
			draft.Horizon, draft.HorizonUnit, draft.KeyMetric, draft.SuccessCriterion,
			StatusDraft, SourceFromStrategy, userID)
		course, err = scanCourse(row)
	}
	if err != nil {
		return Course{}, fmt.Errorf("write generated course: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Course{}, err
	}

	return course, nil
}

func (s *Store) Refresh(ctx context.Context, workspaceID int, courseID int) (Course, error) {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return Course{}, err
	}
	defer tx.Rollback()

	current, err := courseByIDTx(ctx, tx, workspaceID, courseID)
	if err != nil {
		return Course{}, err
	}
	strategy, err := activeStrategyTx(ctx, tx, workspaceID)
	if err != nil {
		return Course{}, err
	}
	if current.StrategyID != strategy.ID {
		return Course{}, ErrCourseStrategyMismatch
	}
	snapshot, err := strategySnapshotTx(ctx, tx, workspaceID, strategy.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Course{}, ErrCourseArtifactsMissing
		}
		return Course{}, err
	}
	if !snapshot.isCurrent() {
		return Course{}, ErrCourseStrategyStale
	}
	draft := buildDraft(strategy, snapshot.Artifacts)
	row := tx.QueryRowContext(ctx, `
		UPDATE v2_courses
		SET source_synthesis_run_id=$1, source_session_revision=$2,
			title=$3, direction=$4, strategic_goal=$5, meaning=$6,
			horizon=$7, horizon_unit=$8, start_date=CURRENT_DATE,
			end_date=CURRENT_DATE + ($7::INTEGER), key_metric=$9,
			success_criterion=$10, status=$11, source=$12,
			activated_at=NULL, updated_at=NOW()
		WHERE id=$13 AND workspace_id=$14 AND archived_at IS NULL
		RETURNING
			id, workspace_id, strategy_id, source_synthesis_run_id, source_session_revision,
			title, direction, strategic_goal, meaning,
			horizon, horizon_unit, start_date::TEXT, end_date::TEXT, key_metric,
			success_criterion, status, source, created_by, created_at, updated_at, activated_at
	`, snapshot.RunID, snapshot.SessionRevision, draft.Title, draft.Direction,
		draft.StrategicGoal, draft.Meaning, *draft.Horizon, draft.HorizonUnit,
		draft.KeyMetric, draft.SuccessCriterion, StatusDraft, SourceFromStrategy,
		current.ID, workspaceID)
	refreshed, err := scanCourse(row)
	if err != nil {
		return Course{}, err
	}
	if err := tx.Commit(); err != nil {
		return Course{}, err
	}
	return refreshed, nil
}

func (s *Store) Activate(ctx context.Context, workspaceID int, courseID int) (Course, error) {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return Course{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, workspaceID+6000000); err != nil {
		return Course{}, err
	}
	current, err := courseByIDTx(ctx, tx, workspaceID, courseID)
	if err != nil {
		return Course{}, err
	}
	if len(missingCourseFields(current)) > 0 {
		return Course{}, ErrCourseIncomplete
	}
	strategy, err := activeStrategyTx(ctx, tx, workspaceID)
	if err != nil {
		return Course{}, err
	}
	if current.StrategyID != strategy.ID {
		return Course{}, ErrCourseStrategyMismatch
	}
	snapshot, err := strategySnapshotTx(ctx, tx, workspaceID, strategy.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Course{}, ErrCourseArtifactsMissing
		}
		return Course{}, err
	}
	if !snapshot.isCurrent() || current.SourceSynthesisRunID == nil ||
		*current.SourceSynthesisRunID != snapshot.RunID ||
		current.SourceSessionRevision != snapshot.SessionRevision {
		return Course{}, ErrCourseStrategyStale
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE v2_courses
		SET status=$1, archived_at=NOW(), updated_at=NOW()
		WHERE workspace_id=$2 AND id<>$3 AND status=$4 AND archived_at IS NULL
	`, StatusArchived, workspaceID, courseID, StatusActive); err != nil {
		return Course{}, err
	}

	row := tx.QueryRowContext(ctx, `
		UPDATE v2_courses
		SET status=$1, activated_at=NOW(), updated_at=NOW()
		WHERE id=$2 AND workspace_id=$3 AND archived_at IS NULL
		RETURNING
			id, workspace_id, strategy_id, source_synthesis_run_id, source_session_revision,
			title, direction, strategic_goal, meaning,
			horizon, horizon_unit, start_date::TEXT, end_date::TEXT, key_metric,
			success_criterion, status, source, created_by, created_at, updated_at, activated_at
	`, StatusActive, courseID, workspaceID)
	activated, err := scanCourse(row)
	if err != nil {
		return Course{}, err
	}
	if err := tx.Commit(); err != nil {
		return Course{}, err
	}
	return activated, nil
}

func (s *Store) courseByStrategy(ctx context.Context, workspaceID int, strategyID int) (Course, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT
			id, workspace_id, strategy_id, source_synthesis_run_id, source_session_revision,
			title, direction, strategic_goal, meaning,
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
			id, workspace_id, strategy_id, source_synthesis_run_id, source_session_revision,
			title, direction, strategic_goal, meaning,
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
			id, workspace_id, strategy_id, source_synthesis_run_id, source_session_revision,
			title, direction, strategic_goal, meaning,
			horizon, horizon_unit, start_date::TEXT, end_date::TEXT, key_metric,
			success_criterion, status, source, created_by, created_at, updated_at, activated_at
		FROM v2_courses
		WHERE workspace_id=$1 AND strategy_id=$2 AND archived_at IS NULL AND status IN ($3, $4)
		ORDER BY CASE status WHEN $4 THEN 1 ELSE 2 END, created_at DESC
		LIMIT 1
	`, workspaceID, strategyID, StatusDraft, StatusActive)
	return scanCourse(row)
}

func courseByIDTx(ctx context.Context, tx *sql.Tx, workspaceID int, courseID int) (Course, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT
			id, workspace_id, strategy_id, source_synthesis_run_id, source_session_revision,
			title, direction, strategic_goal, meaning,
			horizon, horizon_unit, start_date::TEXT, end_date::TEXT, key_metric,
			success_criterion, status, source, created_by, created_at, updated_at, activated_at
		FROM v2_courses
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
		FOR UPDATE
	`, courseID, workspaceID)
	return scanCourse(row)
}

func activeStrategyTx(ctx context.Context, tx *sql.Tx, workspaceID int) (StrategySummary, error) {
	var strategy StrategySummary
	err := tx.QueryRowContext(ctx, `
		SELECT id, status, version, title, summary
		FROM v2_strategies
		WHERE workspace_id=$1 AND status='active' AND archived_at IS NULL
		ORDER BY version DESC, created_at DESC
		LIMIT 1
	`, workspaceID).Scan(&strategy.ID, &strategy.Status, &strategy.Version, &strategy.Title, &strategy.Summary)
	return strategy, err
}

type strategyArtifactValue struct {
	FrameTitle    string
	FrameSubtitle string
	PrimarySignal string
	Content       string
}

func (s *Store) strategySnapshot(ctx context.Context, workspaceID int, strategyID int) (strategySnapshot, error) {
	tx, err := s.dbx.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return strategySnapshot{}, err
	}
	defer tx.Rollback()
	snapshot, err := strategySnapshotTx(ctx, tx, workspaceID, strategyID)
	if err != nil {
		return strategySnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return strategySnapshot{}, err
	}
	return snapshot, nil
}

func strategySnapshotTx(ctx context.Context, tx *sql.Tx, workspaceID int, strategyID int) (strategySnapshot, error) {
	var snapshot strategySnapshot
	err := tx.QueryRowContext(ctx, `
		SELECT run.id, run.session_revision,
			COALESCE(session.revision, run.session_revision)
		FROM v2_strategy_synthesis_runs run
		LEFT JOIN v2_strategy_session_state session ON session.workspace_id=run.workspace_id
		WHERE run.workspace_id=$1 AND run.strategy_id=$2 AND run.status='completed'
		ORDER BY run.created_at DESC, run.id DESC
		LIMIT 1
	`, workspaceID, strategyID).Scan(
		&snapshot.RunID,
		&snapshot.SessionRevision,
		&snapshot.CurrentSessionRevision,
	)
	if err != nil {
		return strategySnapshot{}, err
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT document_type, frame_title, frame_subtitle, primary_signal, formatted_markdown
		FROM v2_strategy_synthesis_documents
		WHERE workspace_id=$1 AND run_id=$2
		ORDER BY sort_order ASC, id ASC
	`, workspaceID, snapshot.RunID)
	if err != nil {
		return strategySnapshot{}, err
	}
	defer rows.Close()

	artifacts := map[string]strategyArtifactValue{}
	for rows.Next() {
		var artifactType string
		var value strategyArtifactValue
		if err := rows.Scan(&artifactType, &value.FrameTitle, &value.FrameSubtitle, &value.PrimarySignal, &value.Content); err != nil {
			return strategySnapshot{}, err
		}
		artifacts[artifactType] = value
	}
	if err := rows.Err(); err != nil {
		return strategySnapshot{}, err
	}
	if len(artifacts) == 0 {
		return strategySnapshot{}, sql.ErrNoRows
	}
	snapshot.Artifacts = artifacts
	return snapshot, nil
}

func buildCourseSync(course Course, snapshot strategySnapshot, snapshotErr error) CourseSync {
	syncState := CourseSync{
		SourceSessionRevision:  course.SourceSessionRevision,
		SourceSynthesisRunID:   course.SourceSynthesisRunID,
		CurrentSessionRevision: snapshot.CurrentSessionRevision,
	}
	if snapshot.RunID > 0 {
		runID := snapshot.RunID
		syncState.CurrentSynthesisRunID = &runID
		syncState.CurrentSynthesisIsCurrent = snapshot.isCurrent()
	}

	if errors.Is(snapshotErr, sql.ErrNoRows) || snapshot.RunID == 0 {
		syncState.State = SyncUnavailable
		syncState.NeedsReview = true
		syncState.Message = "Не удалось подтвердить актуальную сборку стратегии для этого курса."
		return syncState
	}
	if course.SourceSynthesisRunID == nil || course.SourceSessionRevision == 0 {
		syncState.State = SyncLegacy
		syncState.NeedsReview = true
		syncState.Message = "Курс создан до появления версий стратегии и требует повторной проверки."
		return syncState
	}
	if !snapshot.isCurrent() || *course.SourceSynthesisRunID != snapshot.RunID ||
		course.SourceSessionRevision != snapshot.SessionRevision {
		syncState.State = SyncStrategyUpdated
		syncState.NeedsReview = true
		syncState.Message = "После создания курса стратегия изменилась. Обновите курс и подтвердите его заново."
		return syncState
	}
	if course.Status == StatusDraft {
		syncState.State = SyncDraft
		syncState.NeedsReview = true
		syncState.Message = "Курс собран из актуальной стратегии и ждёт подтверждения."
		return syncState
	}

	syncState.State = SyncCurrent
	syncState.Message = "Курс подтверждён и соответствует активной версии стратегии."
	return syncState
}

func buildCourseSources(artifacts map[string]strategyArtifactValue) []CourseSource {
	definitions := []struct {
		artifactType string
		fields       []string
	}{
		{artifactType: "chosen_direction_and_refusals", fields: []string{"direction"}},
		{artifactType: "key_challenge", fields: []string{"meaning"}},
		{artifactType: "goals_and_metrics", fields: []string{"strategic_goal", "key_metric"}},
		{artifactType: "ninety_day_course", fields: []string{"title", "success_criterion", "horizon"}},
	}
	sources := make([]CourseSource, 0, len(definitions))
	for _, definition := range definitions {
		value, ok := artifacts[definition.artifactType]
		if !ok {
			continue
		}
		title := firstNonEmpty(value.FrameTitle, value.PrimarySignal, definition.artifactType)
		summary := firstNonEmpty(value.FrameSubtitle, value.PrimarySignal)
		sources = append(sources, CourseSource{
			ArtifactType: definition.artifactType,
			Title:        title,
			Summary:      summary,
			Fields:       append([]string(nil), definition.fields...),
		})
	}
	return sources
}

func (s *Store) knowledgeBaseSummary(ctx context.Context, workspaceID int) (KnowledgeBaseSummary, error) {
	var summary KnowledgeBaseSummary
	var updatedAt sql.NullTime
	err := s.dbx.QueryRowContext(ctx, `
		SELECT COUNT(*),
			COUNT(*) FILTER (WHERE BTRIM(markdown)<>''),
			MAX(generated_at)
		FROM strategic_documents
		WHERE workspace_id=$1
	`, workspaceID).Scan(&summary.DocumentsTotal, &summary.DocumentsFilled, &updatedAt)
	if err != nil {
		return KnowledgeBaseSummary{}, err
	}

	var qualityUpdatedAt sql.NullTime
	err = s.dbx.QueryRowContext(ctx, `
		SELECT readiness_score, readiness_status, created_at
		FROM strategic_quality_reports
		WHERE workspace_id=$1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, workspaceID).Scan(&summary.ReadinessScore, &summary.ReadinessStatus, &qualityUpdatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return KnowledgeBaseSummary{}, err
	}
	if qualityUpdatedAt.Valid {
		summary.UpdatedAt = qualityUpdatedAt.Time.UTC().Format(time.RFC3339)
	} else if updatedAt.Valid {
		summary.UpdatedAt = updatedAt.Time.UTC().Format(time.RFC3339)
	}
	return summary, nil
}

func buildDraft(strategy StrategySummary, artifacts map[string]strategyArtifactValue) CourseInput {
	direction := firstNonEmpty(
		artifactValue(artifacts["chosen_direction_and_refusals"]),
		artifactValue(artifacts["strategic_direction"]),
	)
	goal := firstNonEmpty(
		artifacts["goals_and_metrics"].PrimarySignal,
		artifactValue(artifacts["goals_and_metrics"]),
		artifactValue(artifacts["local_goal"]),
		artifactValue(artifacts["global_goal"]),
	)
	meaning := firstNonEmpty(
		artifactValue(artifacts["key_challenge"]),
		artifactValue(artifacts["strategic_diagnosis"]),
		strategy.Summary,
		artifactValue(artifacts["current_challenge"]),
	)
	keyMetric := firstNonEmpty(artifacts["goals_and_metrics"].PrimarySignal, artifacts["goals_and_metrics"].FrameTitle, artifactValue(artifacts["key_metric"]))
	successCriterion := firstNonEmpty(artifacts["ninety_day_course"].FrameSubtitle, artifacts["goals_and_metrics"].FrameSubtitle, artifactValue(artifacts["validation_plan"]))

	title := "Курс компании"
	if courseTitle := strings.TrimSpace(artifacts["ninety_day_course"].PrimarySignal); courseTitle != "" {
		title = courseTitle
	} else if courseTitle := strings.TrimSpace(artifacts["ninety_day_course"].FrameTitle); courseTitle != "" {
		title = courseTitle
	} else if direction != "" {
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
		Status:           StatusDraft,
	}
}

func artifactValue(value strategyArtifactValue) string {
	return firstNonEmpty(value.PrimarySignal, value.FrameTitle, value.FrameSubtitle, value.Content)
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

func missingCourseFields(course Course) []string {
	values := map[string]string{
		"title":             course.Title,
		"direction":         course.Direction,
		"strategic_goal":    course.StrategicGoal,
		"key_metric":        course.KeyMetric,
		"success_criterion": course.SuccessCriterion,
	}
	missing := []string{}
	for _, field := range requiredCourseFields {
		if strings.TrimSpace(values[field]) == "" {
			missing = append(missing, field)
		}
	}
	return missing
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
	var sourceSynthesisRunID sql.NullInt64
	var endDate sql.NullString
	var createdBy sql.NullInt64
	var activatedAt sql.NullTime
	err := scanner.Scan(
		&course.ID,
		&course.WorkspaceID,
		&course.StrategyID,
		&sourceSynthesisRunID,
		&course.SourceSessionRevision,
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
	if sourceSynthesisRunID.Valid {
		value := int(sourceSynthesisRunID.Int64)
		course.SourceSynthesisRunID = &value
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
