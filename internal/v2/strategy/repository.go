package strategy

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

func (s *Store) Current(ctx context.Context, workspaceID int, userID int) (Strategy, []Artifact, KnowledgeBaseSummary, error) {
	strategy, err := s.getCurrent(ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		strategy, err = s.createDraft(ctx, workspaceID, userID, "Стратегия v1")
	}
	if err != nil {
		return Strategy{}, nil, KnowledgeBaseSummary{}, err
	}

	if err := s.ensureDefaultArtifacts(ctx, strategy.ID, workspaceID); err != nil {
		return Strategy{}, nil, KnowledgeBaseSummary{}, err
	}

	artifacts, err := s.listArtifacts(ctx, workspaceID, strategy.ID)
	if err != nil {
		return Strategy{}, nil, KnowledgeBaseSummary{}, err
	}

	summary, err := s.knowledgeBaseSummary(ctx, workspaceID)
	if err != nil {
		return Strategy{}, nil, KnowledgeBaseSummary{}, err
	}

	return strategy, artifacts, summary, nil
}

func (s *Store) Update(ctx context.Context, workspaceID int, strategyID int, title string, summary string) (Strategy, error) {
	title = strings.TrimSpace(title)
	summary = strings.TrimSpace(summary)
	if title == "" {
		title = "Стратегия v1"
	}

	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_strategies
		SET title=$1, summary=$2, updated_at=NOW()
		WHERE id=$3 AND workspace_id=$4 AND archived_at IS NULL
		RETURNING
			id, workspace_id, status, version, title, summary, source_type,
			created_by, approved_by, created_at, updated_at, approved_at, activated_at
	`, title, summary, strategyID, workspaceID)

	return scanStrategy(row)
}

func (s *Store) UpdateArtifact(ctx context.Context, workspaceID int, artifactID int, content string, status string) (Artifact, error) {
	content = strings.TrimSpace(content)
	status = strings.TrimSpace(status)
	if content == "" {
		status = ArtifactStatusEmpty
	} else if status == "" || status == ArtifactStatusEmpty {
		status = ArtifactStatusDraft
	}

	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_strategy_artifacts a
		SET content=$1, status=$2, source=$3, updated_at=NOW()
		FROM v2_strategies s
		WHERE a.id=$4
			AND a.workspace_id=$5
			AND a.strategy_id=s.id
			AND s.archived_at IS NULL
		RETURNING
			a.id, a.strategy_id, a.workspace_id, a.type, a.title, a.description,
			a.content, a.status, a.sort_order, a.confidence, a.source,
			a.created_at, a.updated_at
	`, content, status, SourceManual, artifactID, workspaceID)

	return scanArtifact(row)
}

func (s *Store) Activate(ctx context.Context, workspaceID int, strategyID int, userID int) (Strategy, error) {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return Strategy{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, workspaceID+3000000); err != nil {
		return Strategy{}, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE v2_strategies
		SET status=$1, archived_at=NOW(), updated_at=NOW()
		WHERE workspace_id=$2
			AND status=$3
			AND id<>$4
			AND archived_at IS NULL
	`, StatusArchived, workspaceID, StatusActive, strategyID); err != nil {
		return Strategy{}, err
	}

	row := tx.QueryRowContext(ctx, `
		UPDATE v2_strategies
		SET status=$1,
			approved_by=$2,
			approved_at=COALESCE(approved_at, NOW()),
			activated_at=NOW(),
			updated_at=NOW()
		WHERE id=$3 AND workspace_id=$4 AND archived_at IS NULL
		RETURNING
			id, workspace_id, status, version, title, summary, source_type,
			created_by, approved_by, created_at, updated_at, approved_at, activated_at
	`, StatusActive, userID, strategyID, workspaceID)

	strategy, err := scanStrategy(row)
	if err != nil {
		return Strategy{}, err
	}

	if err := tx.Commit(); err != nil {
		return Strategy{}, err
	}

	return strategy, nil
}

func (s *Store) getCurrent(ctx context.Context, workspaceID int) (Strategy, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT
			id, workspace_id, status, version, title, summary, source_type,
			created_by, approved_by, created_at, updated_at, approved_at, activated_at
		FROM v2_strategies
		WHERE workspace_id=$1 AND archived_at IS NULL AND status IN ($2, $3, $4)
		ORDER BY
			CASE status
				WHEN $4 THEN 1
				WHEN $3 THEN 2
				ELSE 3
			END,
			version DESC,
			created_at DESC
		LIMIT 1
	`, workspaceID, StatusDraft, StatusReadyForReview, StatusActive)

	return scanStrategy(row)
}

func (s *Store) createDraft(ctx context.Context, workspaceID int, userID int, title string) (Strategy, error) {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return Strategy{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, workspaceID+2000000); err != nil {
		return Strategy{}, err
	}

	if strategy, err := currentTx(ctx, tx, workspaceID); err == nil {
		if err := tx.Commit(); err != nil {
			return Strategy{}, err
		}
		return strategy, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Strategy{}, err
	}

	var version int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1 FROM v2_strategies WHERE workspace_id=$1
	`, workspaceID).Scan(&version); err != nil {
		return Strategy{}, err
	}

	row := tx.QueryRowContext(ctx, `
		INSERT INTO v2_strategies (workspace_id, status, version, title, summary, source_type, created_by)
		VALUES ($1, $2, $3, $4, '', $5, $6)
		RETURNING
			id, workspace_id, status, version, title, summary, source_type,
			created_by, approved_by, created_at, updated_at, approved_at, activated_at
	`, workspaceID, StatusDraft, version, title, SourceManual, userID)

	strategy, err := scanStrategy(row)
	if err != nil {
		return Strategy{}, err
	}

	for _, definition := range artifactDefinitions {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO v2_strategy_artifacts (
				strategy_id, workspace_id, type, title, description, status, sort_order, source
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (strategy_id, type) DO NOTHING
		`, strategy.ID, workspaceID, definition.Type, definition.Title, definition.Description, ArtifactStatusEmpty, definition.SortOrder, SourceManual); err != nil {
			return Strategy{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return Strategy{}, err
	}

	return strategy, nil
}

func currentTx(ctx context.Context, tx *sql.Tx, workspaceID int) (Strategy, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT
			id, workspace_id, status, version, title, summary, source_type,
			created_by, approved_by, created_at, updated_at, approved_at, activated_at
		FROM v2_strategies
		WHERE workspace_id=$1 AND archived_at IS NULL AND status IN ($2, $3, $4)
		ORDER BY
			CASE status
				WHEN $4 THEN 1
				WHEN $3 THEN 2
				ELSE 3
			END,
			version DESC,
			created_at DESC
		LIMIT 1
	`, workspaceID, StatusDraft, StatusReadyForReview, StatusActive)

	return scanStrategy(row)
}

func (s *Store) ensureDefaultArtifacts(ctx context.Context, strategyID int, workspaceID int) error {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, strategyID+4000000); err != nil {
		return err
	}

	for _, definition := range artifactDefinitions {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO v2_strategy_artifacts (
				strategy_id, workspace_id, type, title, description, status, sort_order, source
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (strategy_id, type) DO UPDATE SET
				title=EXCLUDED.title,
				description=EXCLUDED.description,
				sort_order=EXCLUDED.sort_order,
				updated_at=v2_strategy_artifacts.updated_at
		`, strategyID, workspaceID, definition.Type, definition.Title, definition.Description, ArtifactStatusEmpty, definition.SortOrder, SourceManual); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) listArtifacts(ctx context.Context, workspaceID int, strategyID int) ([]Artifact, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT
			id, strategy_id, workspace_id, type, title, description,
			content, status, sort_order, confidence, source,
			created_at, updated_at
		FROM v2_strategy_artifacts
		WHERE workspace_id=$1 AND strategy_id=$2
		ORDER BY sort_order ASC, id ASC
	`, workspaceID, strategyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	artifacts := []Artifact{}
	for rows.Next() {
		artifact, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}

	return artifacts, rows.Err()
}

func (s *Store) knowledgeBaseSummary(ctx context.Context, workspaceID int) (KnowledgeBaseSummary, error) {
	var summary KnowledgeBaseSummary
	err := s.dbx.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status='ready'),
			COUNT(*) FILTER (WHERE status<>'empty'),
			COUNT(*) FILTER (WHERE status='empty')
		FROM v2_knowledge_base_blocks
		WHERE workspace_id=$1 AND archived_at IS NULL
	`, workspaceID).Scan(&summary.BlocksTotal, &summary.BlocksReady, &summary.BlocksFilled, &summary.BlocksEmpty)
	if err != nil {
		return KnowledgeBaseSummary{}, err
	}

	return summary, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanStrategy(scanner scanner) (Strategy, error) {
	var strategy Strategy
	var createdBy sql.NullInt64
	var approvedBy sql.NullInt64
	var approvedAt sql.NullTime
	var activatedAt sql.NullTime

	err := scanner.Scan(
		&strategy.ID,
		&strategy.WorkspaceID,
		&strategy.Status,
		&strategy.Version,
		&strategy.Title,
		&strategy.Summary,
		&strategy.SourceType,
		&createdBy,
		&approvedBy,
		&strategy.CreatedAt,
		&strategy.UpdatedAt,
		&approvedAt,
		&activatedAt,
	)
	if err != nil {
		return Strategy{}, err
	}

	if createdBy.Valid {
		value := int(createdBy.Int64)
		strategy.CreatedBy = &value
	}
	if approvedBy.Valid {
		value := int(approvedBy.Int64)
		strategy.ApprovedBy = &value
	}
	if approvedAt.Valid {
		strategy.ApprovedAt = &approvedAt.Time
	}
	if activatedAt.Valid {
		strategy.ActivatedAt = &activatedAt.Time
	}

	return strategy, nil
}

func scanArtifact(scanner scanner) (Artifact, error) {
	var artifact Artifact
	var confidence sql.NullFloat64

	err := scanner.Scan(
		&artifact.ID,
		&artifact.StrategyID,
		&artifact.WorkspaceID,
		&artifact.Type,
		&artifact.Title,
		&artifact.Description,
		&artifact.Content,
		&artifact.Status,
		&artifact.SortOrder,
		&confidence,
		&artifact.Source,
		&artifact.CreatedAt,
		&artifact.UpdatedAt,
	)
	if err != nil {
		return Artifact{}, err
	}

	if confidence.Valid {
		artifact.Confidence = &confidence.Float64
	}

	return artifact, nil
}
