package metrics

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

type Store struct {
	dbx *sql.DB
}

func NewStore(dbx *sql.DB) *Store {
	return &Store{dbx: dbx}
}

func (s *Store) Definitions(ctx context.Context, workspaceID int) ([]Definition, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, workspace_id, template_key, name, description, category, unit,
			value_type, better_direction, formula, is_custom, status, created_at, updated_at, archived_at
		FROM v2_workspace_metrics
		WHERE workspace_id=$1 AND archived_at IS NULL
		ORDER BY is_custom ASC, category ASC, name ASC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []Definition{}
	for rows.Next() {
		item, err := scanDefinition(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) Targets(ctx context.Context, workspaceID int, scopeType string, scopeID int) ([]Target, error) {
	query := `
		SELECT
			target.id, target.workspace_id, target.metric_id, target.scope_type, target.scope_id,
			target.role, target.baseline_value, target.target_value, target.target_date::TEXT,
			target.display_unit, target.cadence, target.source_note, target.owner_user_id,
			target.created_at, target.updated_at,
			metric.id, metric.workspace_id, metric.template_key, metric.name, metric.description,
			metric.category, metric.unit, metric.value_type, metric.better_direction, metric.formula,
			metric.is_custom, metric.status, metric.created_at, metric.updated_at, metric.archived_at,
			latest.value, latest.measured_at::TEXT
		FROM v2_metric_targets target
		JOIN v2_workspace_metrics metric ON metric.id=target.metric_id
		LEFT JOIN LATERAL (
			SELECT observation.value, observation.measured_at
			FROM v2_metric_observations observation
			WHERE observation.workspace_id=target.workspace_id
				AND observation.metric_id=target.metric_id
				AND (observation.target_id=target.id OR observation.target_id IS NULL)
			ORDER BY observation.measured_at DESC, observation.id DESC
			LIMIT 1
		) latest ON TRUE
		WHERE target.workspace_id=$1 AND target.archived_at IS NULL AND metric.archived_at IS NULL
	`
	args := []any{workspaceID}
	if scopeType != "" {
		query += ` AND target.scope_type=$2 AND target.scope_id=$3`
		args = append(args, scopeType, scopeID)
	}
	query += ` ORDER BY CASE target.role WHEN 'primary' THEN 0 WHEN 'guardrail' THEN 1 ELSE 2 END, target.id ASC`

	rows, err := s.dbx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []Target{}
	for rows.Next() {
		item, err := scanTarget(rows)
		if err != nil {
			return nil, err
		}
		item.Observations, err = s.observations(ctx, workspaceID, item.MetricID, item.ID, 24)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) CreateTarget(ctx context.Context, workspaceID int, userID int, input TargetInput) (Target, error) {
	normalizeTargetInput(&input)
	applyTargetDefaults(&input)
	if err := s.validateScope(ctx, workspaceID, input.ScopeType, input.ScopeID); err != nil {
		return Target{}, err
	}

	metric, err := s.resolveMetric(ctx, workspaceID, userID, input)
	if err != nil {
		return Target{}, err
	}
	if input.DisplayUnit == "" {
		input.DisplayUnit = metric.Unit
	}

	var targetID int64
	err = s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_metric_targets (
			workspace_id, metric_id, scope_type, scope_id, role, baseline_value,
			target_value, target_date, display_unit, cadence, source_note, owner_user_id, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, '')::DATE, $9, $10, $11, $12, $13)
		ON CONFLICT (workspace_id, metric_id, scope_type, scope_id) WHERE archived_at IS NULL
		DO UPDATE SET
			role=EXCLUDED.role,
			baseline_value=EXCLUDED.baseline_value,
			target_value=EXCLUDED.target_value,
			target_date=EXCLUDED.target_date,
			display_unit=EXCLUDED.display_unit,
			cadence=EXCLUDED.cadence,
			source_note=EXCLUDED.source_note,
			owner_user_id=EXCLUDED.owner_user_id,
			updated_at=NOW()
		RETURNING id
	`, workspaceID, metric.ID, input.ScopeType, input.ScopeID, input.Role, input.BaselineValue,
		input.TargetValue, input.TargetDate, input.DisplayUnit, input.Cadence, input.SourceNote,
		input.OwnerUserID, userID).Scan(&targetID)
	if err != nil {
		return Target{}, err
	}
	return s.targetByID(ctx, workspaceID, targetID)
}

func (s *Store) UpdateTarget(ctx context.Context, workspaceID int, targetID int64, input TargetInput) (Target, error) {
	current, err := s.targetByID(ctx, workspaceID, targetID)
	if err != nil {
		return Target{}, err
	}
	normalizeTargetInput(&input)
	if input.Role == "" {
		input.Role = current.Role
	}
	if input.Cadence == "" {
		input.Cadence = current.Cadence
	}
	if input.TargetDate == "" && !input.ClearTargetDate {
		input.TargetDate = current.TargetDate
	}
	if input.DisplayUnit == "" {
		input.DisplayUnit = current.DisplayUnit
	}
	if input.SourceNote == "" {
		input.SourceNote = current.SourceNote
	}
	if input.BaselineValue == nil && !input.ClearBaseline {
		input.BaselineValue = current.BaselineValue
	}
	if input.TargetValue == nil && !input.ClearTarget {
		input.TargetValue = current.TargetValue
	}
	if input.OwnerUserID == nil {
		input.OwnerUserID = current.OwnerUserID
	}

	_, err = s.dbx.ExecContext(ctx, `
		UPDATE v2_metric_targets
		SET role=$1, baseline_value=$2, target_value=$3, target_date=NULLIF($4, '')::DATE,
			display_unit=$5, cadence=$6, source_note=$7, owner_user_id=$8, updated_at=NOW()
		WHERE id=$9 AND workspace_id=$10 AND archived_at IS NULL
	`, input.Role, input.BaselineValue, input.TargetValue, input.TargetDate, input.DisplayUnit,
		input.Cadence, input.SourceNote, input.OwnerUserID, targetID, workspaceID)
	if err != nil {
		return Target{}, err
	}
	return s.targetByID(ctx, workspaceID, targetID)
}

func (s *Store) ArchiveTarget(ctx context.Context, workspaceID int, targetID int64) error {
	result, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_metric_targets
		SET archived_at=NOW(), updated_at=NOW()
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, targetID, workspaceID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) AddObservation(ctx context.Context, workspaceID int, userID int, targetID int64, input ObservationInput) (Observation, error) {
	target, err := s.targetByID(ctx, workspaceID, targetID)
	if err != nil {
		return Observation{}, err
	}
	input.SourceType = strings.TrimSpace(input.SourceType)
	input.SourceNote = strings.TrimSpace(input.SourceNote)
	input.EvidenceURL = strings.TrimSpace(input.EvidenceURL)
	input.MeasuredAt = strings.TrimSpace(input.MeasuredAt)
	if input.SourceType == "" {
		input.SourceType = "manual"
	}
	if input.Confidence == 0 {
		input.Confidence = 1000
	}

	row := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_metric_observations (
			workspace_id, metric_id, target_id, value, measured_at, source_type,
			source_note, evidence_url, confidence, created_by
		)
		VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5, '')::DATE, CURRENT_DATE), $6, $7, $8, $9, $10)
		RETURNING id, workspace_id, metric_id, target_id, value, measured_at::TEXT,
			source_type, source_note, evidence_url, confidence, created_by, created_at
	`, workspaceID, target.MetricID, target.ID, input.Value, input.MeasuredAt, input.SourceType,
		input.SourceNote, input.EvidenceURL, input.Confidence, userID)
	return scanObservation(row)
}

func (s *Store) targetByID(ctx context.Context, workspaceID int, targetID int64) (Target, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT
			target.id, target.workspace_id, target.metric_id, target.scope_type, target.scope_id,
			target.role, target.baseline_value, target.target_value, target.target_date::TEXT,
			target.display_unit, target.cadence, target.source_note, target.owner_user_id,
			target.created_at, target.updated_at,
			metric.id, metric.workspace_id, metric.template_key, metric.name, metric.description,
			metric.category, metric.unit, metric.value_type, metric.better_direction, metric.formula,
			metric.is_custom, metric.status, metric.created_at, metric.updated_at, metric.archived_at,
			latest.value, latest.measured_at::TEXT
		FROM v2_metric_targets target
		JOIN v2_workspace_metrics metric ON metric.id=target.metric_id
		LEFT JOIN LATERAL (
			SELECT observation.value, observation.measured_at
			FROM v2_metric_observations observation
			WHERE observation.workspace_id=target.workspace_id
				AND observation.metric_id=target.metric_id
				AND (observation.target_id=target.id OR observation.target_id IS NULL)
			ORDER BY observation.measured_at DESC, observation.id DESC
			LIMIT 1
		) latest ON TRUE
		WHERE target.id=$1 AND target.workspace_id=$2 AND target.archived_at IS NULL
	`, targetID, workspaceID)
	if err != nil {
		return Target{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Target{}, sql.ErrNoRows
	}
	target, err := scanTarget(rows)
	if err != nil {
		return Target{}, err
	}
	target.Observations, err = s.observations(ctx, workspaceID, target.MetricID, target.ID, 24)
	return target, err
}

func (s *Store) observations(ctx context.Context, workspaceID int, metricID int64, targetID int64, limit int) ([]Observation, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, workspace_id, metric_id, target_id, value, measured_at::TEXT,
			source_type, source_note, evidence_url, confidence, created_by, created_at
		FROM v2_metric_observations
		WHERE workspace_id=$1 AND metric_id=$2 AND (target_id=$3 OR target_id IS NULL)
		ORDER BY measured_at DESC, id DESC
		LIMIT $4
	`, workspaceID, metricID, targetID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Observation{}
	for rows.Next() {
		item, err := scanObservation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) resolveMetric(ctx context.Context, workspaceID int, userID int, input TargetInput) (Definition, error) {
	if input.MetricID > 0 {
		return s.definitionByID(ctx, workspaceID, input.MetricID)
	}
	if input.TemplateKey != "" {
		template, ok := TemplateByKey(input.TemplateKey)
		if !ok {
			return Definition{}, sql.ErrNoRows
		}
		_, err := s.dbx.ExecContext(ctx, `
			INSERT INTO v2_workspace_metrics (
				workspace_id, template_key, name, description, category, unit, value_type,
				better_direction, formula, is_custom, created_by
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, false, $10)
			ON CONFLICT DO NOTHING
		`, workspaceID, template.Key, template.Name, template.Description, template.Category,
			template.Unit, template.ValueType, template.BetterDirection, template.Formula, userID)
		if err != nil {
			return Definition{}, err
		}
		return s.definitionByTemplate(ctx, workspaceID, template.Key)
	}

	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return Definition{}, errors.New("metric_name_required")
	}
	if input.Category == "" {
		input.Category = "Пользовательские"
	}
	if input.Unit == "" {
		input.Unit = "number"
	}
	if input.ValueType == "" {
		input.ValueType = "number"
	}
	if input.BetterDirection == "" {
		input.BetterDirection = "increase"
	}
	_, err := s.dbx.ExecContext(ctx, `
		INSERT INTO v2_workspace_metrics (
			workspace_id, name, description, category, unit, value_type,
			better_direction, formula, is_custom, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, true, $9)
		ON CONFLICT DO NOTHING
	`, workspaceID, input.Name, strings.TrimSpace(input.Description), strings.TrimSpace(input.Category),
		strings.TrimSpace(input.Unit), strings.TrimSpace(input.ValueType), strings.TrimSpace(input.BetterDirection),
		strings.TrimSpace(input.Formula), userID)
	if err != nil {
		return Definition{}, err
	}
	return s.definitionByName(ctx, workspaceID, input.Name)
}

func (s *Store) definitionByID(ctx context.Context, workspaceID int, metricID int64) (Definition, error) {
	return scanDefinition(s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, template_key, name, description, category, unit,
			value_type, better_direction, formula, is_custom, status, created_at, updated_at, archived_at
		FROM v2_workspace_metrics
		WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL
	`, metricID, workspaceID))
}

func (s *Store) definitionByTemplate(ctx context.Context, workspaceID int, templateKey string) (Definition, error) {
	return scanDefinition(s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, template_key, name, description, category, unit,
			value_type, better_direction, formula, is_custom, status, created_at, updated_at, archived_at
		FROM v2_workspace_metrics
		WHERE workspace_id=$1 AND template_key=$2 AND archived_at IS NULL
	`, workspaceID, templateKey))
}

func (s *Store) definitionByName(ctx context.Context, workspaceID int, name string) (Definition, error) {
	return scanDefinition(s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, template_key, name, description, category, unit,
			value_type, better_direction, formula, is_custom, status, created_at, updated_at, archived_at
		FROM v2_workspace_metrics
		WHERE workspace_id=$1 AND LOWER(name)=LOWER($2) AND archived_at IS NULL
	`, workspaceID, name))
}

func (s *Store) validateScope(ctx context.Context, workspaceID int, scopeType string, scopeID int) error {
	var exists bool
	var query string
	var args []any
	switch scopeType {
	case ScopeWorkspace:
		return nil
	case ScopeStrategy:
		query = `SELECT EXISTS(SELECT 1 FROM v2_strategies WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL)`
		args = []any{scopeID, workspaceID}
	case ScopeWorkstream:
		query = `SELECT EXISTS(SELECT 1 FROM v2_tactical_workstreams WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL)`
		args = []any{scopeID, workspaceID}
	case ScopeProject:
		query = `SELECT EXISTS(SELECT 1 FROM v2_tactical_projects WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL)`
		args = []any{scopeID, workspaceID}
	default:
		return sql.ErrNoRows
	}
	if err := s.dbx.QueryRowContext(ctx, query, args...).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return sql.ErrNoRows
	}
	return nil
}

func normalizeTargetInput(input *TargetInput) {
	input.TemplateKey = strings.TrimSpace(input.TemplateKey)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Category = strings.TrimSpace(input.Category)
	input.Unit = strings.TrimSpace(input.Unit)
	input.ValueType = strings.TrimSpace(input.ValueType)
	input.BetterDirection = strings.TrimSpace(input.BetterDirection)
	input.Formula = strings.TrimSpace(input.Formula)
	input.ScopeType = strings.TrimSpace(input.ScopeType)
	input.Role = strings.TrimSpace(input.Role)
	input.TargetDate = strings.TrimSpace(input.TargetDate)
	input.DisplayUnit = strings.TrimSpace(input.DisplayUnit)
	input.Cadence = strings.TrimSpace(input.Cadence)
	input.SourceNote = strings.TrimSpace(input.SourceNote)
}

func applyTargetDefaults(input *TargetInput) {
	if input.Role == "" {
		input.Role = RoleSupporting
	}
	if input.Cadence == "" {
		input.Cadence = "monthly"
	}
}

type scanner interface {
	Scan(dest ...any) error
}

func scanDefinition(row scanner) (Definition, error) {
	var item Definition
	var archivedAt sql.NullTime
	err := row.Scan(
		&item.ID, &item.WorkspaceID, &item.TemplateKey, &item.Name, &item.Description,
		&item.Category, &item.Unit, &item.ValueType, &item.BetterDirection, &item.Formula,
		&item.IsCustom, &item.Status, &item.CreatedAt, &item.UpdatedAt, &archivedAt,
	)
	if archivedAt.Valid {
		item.ArchivedAt = &archivedAt.Time
	}
	return item, err
}

func scanTarget(row scanner) (Target, error) {
	var item Target
	var baseline sql.NullFloat64
	var target sql.NullFloat64
	var targetDate sql.NullString
	var ownerID sql.NullInt64
	var latest sql.NullFloat64
	var latestAt sql.NullString
	var archivedAt sql.NullTime
	err := row.Scan(
		&item.ID, &item.WorkspaceID, &item.MetricID, &item.ScopeType, &item.ScopeID,
		&item.Role, &baseline, &target, &targetDate, &item.DisplayUnit, &item.Cadence, &item.SourceNote,
		&ownerID, &item.CreatedAt, &item.UpdatedAt,
		&item.Metric.ID, &item.Metric.WorkspaceID, &item.Metric.TemplateKey, &item.Metric.Name,
		&item.Metric.Description, &item.Metric.Category, &item.Metric.Unit, &item.Metric.ValueType,
		&item.Metric.BetterDirection, &item.Metric.Formula, &item.Metric.IsCustom, &item.Metric.Status,
		&item.Metric.CreatedAt, &item.Metric.UpdatedAt, &archivedAt,
		&latest, &latestAt,
	)
	if err != nil {
		return Target{}, err
	}
	if baseline.Valid {
		value := baseline.Float64
		item.BaselineValue = &value
	}
	if target.Valid {
		value := target.Float64
		item.TargetValue = &value
	}
	if targetDate.Valid {
		item.TargetDate = targetDate.String
	}
	if ownerID.Valid {
		value := int(ownerID.Int64)
		item.OwnerUserID = &value
	}
	if latest.Valid {
		value := latest.Float64
		item.LatestValue = &value
	}
	if latestAt.Valid {
		item.LatestAt = latestAt.String
	}
	item.Observations = []Observation{}
	return item, nil
}

func scanObservation(row scanner) (Observation, error) {
	var item Observation
	var targetID sql.NullInt64
	var createdBy sql.NullInt64
	err := row.Scan(
		&item.ID, &item.WorkspaceID, &item.MetricID, &targetID, &item.Value,
		&item.MeasuredAt, &item.SourceType, &item.SourceNote, &item.EvidenceURL,
		&item.Confidence, &createdBy, &item.CreatedAt,
	)
	if targetID.Valid {
		value := targetID.Int64
		item.TargetID = &value
	}
	if createdBy.Valid {
		value := int(createdBy.Int64)
		item.CreatedBy = &value
	}
	return item, err
}
