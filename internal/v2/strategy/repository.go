package strategy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

// CurrentActive returns the strategy that currently drives the course and
// execution views. If a workspace has not activated a strategy yet, it falls
// back to the current working version.
func (s *Store) CurrentActive(ctx context.Context, workspaceID int, userID int) (Strategy, []Artifact, KnowledgeBaseSummary, error) {
	strategy, err := s.getActive(ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return s.Current(ctx, workspaceID, userID)
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

func (s *Store) ListVersions(ctx context.Context, workspaceID int) ([]Strategy, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, workspace_id, status, version, title, summary, source_type,
			created_by, approved_by, created_at, updated_at, approved_at, activated_at
		FROM v2_strategies
		WHERE workspace_id=$1
		ORDER BY version DESC, created_at DESC, id DESC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	versions := []Strategy{}
	for rows.Next() {
		version, err := scanStrategy(rows)
		if err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func (s *Store) CreateNextVersion(ctx context.Context, workspaceID int, userID int) (Strategy, bool, error) {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return Strategy{}, false, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, workspaceID+2000000); err != nil {
		return Strategy{}, false, err
	}

	working, err := workingStrategyTx(ctx, tx, workspaceID)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return Strategy{}, false, err
		}
		return working, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Strategy{}, false, err
	}

	var sourceSummary string
	var version int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1 FROM v2_strategies WHERE workspace_id=$1
	`, workspaceID).Scan(&version); err != nil {
		return Strategy{}, false, err
	}
	err = tx.QueryRowContext(ctx, `
		SELECT summary
		FROM v2_strategies
		WHERE workspace_id=$1
		ORDER BY version DESC, created_at DESC, id DESC
		LIMIT 1
	`, workspaceID).Scan(&sourceSummary)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Strategy{}, false, err
	}

	title := fmt.Sprintf("Стратегия v%d", version)
	row := tx.QueryRowContext(ctx, `
		INSERT INTO v2_strategies (workspace_id, status, version, title, summary, source_type, created_by)
		VALUES ($1, $2, $3, $4, $5, 'revision', $6)
		RETURNING id, workspace_id, status, version, title, summary, source_type,
			created_by, approved_by, created_at, updated_at, approved_at, activated_at
	`, workspaceID, StatusDraft, version, title, strings.TrimSpace(sourceSummary), userID)
	strategy, err := scanStrategy(row)
	if err != nil {
		return Strategy{}, false, err
	}

	for _, definition := range artifactDefinitions {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO v2_strategy_artifacts (
				strategy_id, workspace_id, type, title, description, status, sort_order, source
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, strategy.ID, workspaceID, definition.Type, definition.Title, definition.Description,
			ArtifactStatusEmpty, definition.SortOrder, SourceManual); err != nil {
			return Strategy{}, false, err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO v2_strategy_session_state (
			workspace_id, revision, facilitator_status, status_reason, remaining_uncertainties_json
		)
		VALUES ($1, 1, $2, 'A new strategy version was created.', '[]'::jsonb)
		ON CONFLICT (workspace_id) DO UPDATE SET
			revision=v2_strategy_session_state.revision + 1,
			facilitator_status=$2,
			status_reason='A new strategy version was created.',
			remaining_uncertainties_json='[]'::jsonb,
			last_audited_revision=0,
			last_readiness_run_id=NULL,
			last_synthesis_run_id=NULL,
			updated_at=NOW()
	`, workspaceID, FacilitatorStatusContinue); err != nil {
		return Strategy{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM v2_strategy_readiness_queue WHERE workspace_id=$1`, workspaceID); err != nil {
		return Strategy{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return Strategy{}, false, err
	}
	return strategy, true, nil
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

	var readinessVerdict string
	var readinessCanSynthesize bool
	var readinessRevision int
	var currentRevision int
	if err := tx.QueryRowContext(ctx, `
		SELECT r.verdict, r.can_synthesize, r.session_revision, session.revision
		FROM v2_strategy_readiness_runs r
		JOIN v2_strategy_session_state session ON session.workspace_id=r.workspace_id
		WHERE r.workspace_id=$1 AND r.strategy_id=$2 AND r.status=$3
		ORDER BY r.created_at DESC, r.id DESC
		LIMIT 1
	`, workspaceID, strategyID, ReadinessRunCompleted).Scan(
		&readinessVerdict,
		&readinessCanSynthesize,
		&readinessRevision,
		&currentRevision,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Strategy{}, ErrStrategyActivationNotReady
		}
		return Strategy{}, err
	}
	if readinessRevision != currentRevision {
		return Strategy{}, ErrStrategyActivationStale
	}
	if readinessVerdict != ReadinessVerdictReady || !readinessCanSynthesize {
		return Strategy{}, ErrStrategyActivationNotReady
	}

	var synthesisRunID int
	var synthesisRevision int
	if err := tx.QueryRowContext(ctx, `
		SELECT id, session_revision
		FROM v2_strategy_synthesis_runs
		WHERE workspace_id=$1 AND strategy_id=$2 AND status=$3
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, workspaceID, strategyID, SynthesisStatusCompleted).Scan(&synthesisRunID, &synthesisRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Strategy{}, ErrStrategyActivationArtifactsMissing
		}
		return Strategy{}, err
	}
	if synthesisRevision != currentRevision {
		return Strategy{}, ErrStrategyActivationStale
	}

	var coreArtifactsReady int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM v2_strategy_synthesis_documents
		WHERE run_id=$1 AND workspace_id=$2 AND status=$3
			AND document_type IN ('key_challenge', 'chosen_direction_and_refusals', 'goals_and_metrics', 'ninety_day_course')
	`, synthesisRunID, workspaceID, SynthesisDocumentFilled).Scan(&coreArtifactsReady); err != nil {
		return Strategy{}, err
	}
	if coreArtifactsReady != 4 {
		return Strategy{}, ErrStrategyActivationArtifactsMissing
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

func (s *Store) CreateChatMessage(ctx context.Context, workspaceID int, userID *int, role string, content string, metadata any) (int, error) {
	role = strings.TrimSpace(role)
	if role != "assistant" && role != "user" {
		role = "user"
	}

	var id int
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_strategy_chat_messages (workspace_id, user_id, role, content, metadata_json)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, workspaceID, userID, role, strings.TrimSpace(content), mustJSON(metadata)).Scan(&id)
	return id, err
}

func (s *Store) RecentChatMessages(ctx context.Context, workspaceID int, limit int) ([]StrategyChatMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 40
	}

	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, role, content, created_at
		FROM v2_strategy_chat_messages
		WHERE workspace_id=$1
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := []StrategyChatMessage{}
	for rows.Next() {
		var item StrategyChatMessage
		if err := rows.Scan(&item.ID, &item.Role, &item.Content, &item.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, item)
	}
	reverseChatMessages(messages)
	return messages, rows.Err()
}

func (s *Store) OpenAIStrategySession(ctx context.Context, workspaceID int, compactThreshold int) (StrategyOpenAISession, error) {
	if compactThreshold <= 0 {
		compactThreshold = 120000
	}
	promptCacheKey := fmt.Sprintf("reupgoals-strategy-facilitator-workspace-%d-v3", workspaceID)

	var item StrategyOpenAISession
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_strategy_openai_sessions (workspace_id, compact_threshold, prompt_cache_key)
		VALUES ($1, $2, $3)
		ON CONFLICT (workspace_id) DO UPDATE SET
			compact_threshold=EXCLUDED.compact_threshold,
			prompt_cache_key=EXCLUDED.prompt_cache_key,
			updated_at=NOW()
		RETURNING id, workspace_id, conversation_id, previous_response_id,
			compact_threshold, prompt_cache_key, created_at, updated_at
	`, workspaceID, compactThreshold, promptCacheKey).Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.ConversationID,
		&item.PreviousResponseID,
		&item.CompactThreshold,
		&item.PromptCacheKey,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	return item, err
}

func (s *Store) UpdateOpenAIStrategyConversationID(ctx context.Context, workspaceID int, conversationID string) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_strategy_openai_sessions
		SET conversation_id=$2, previous_response_id='', updated_at=NOW()
		WHERE workspace_id=$1
	`, workspaceID, strings.TrimSpace(conversationID))
	return err
}

func (s *Store) UpdateOpenAIStrategyPreviousResponseID(ctx context.Context, workspaceID int, responseID string) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_strategy_openai_sessions
		SET previous_response_id=$2, updated_at=NOW()
		WHERE workspace_id=$1
	`, workspaceID, strings.TrimSpace(responseID))
	return err
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
				WHEN $2 THEN 1
				WHEN $3 THEN 1
				ELSE 3
			END,
			version DESC,
			created_at DESC
		LIMIT 1
	`, workspaceID, StatusDraft, StatusReadyForReview, StatusActive)

	return scanStrategy(row)
}

func (s *Store) getActive(ctx context.Context, workspaceID int) (Strategy, error) {
	row := s.dbx.QueryRowContext(ctx, `
		SELECT
			id, workspace_id, status, version, title, summary, source_type,
			created_by, approved_by, created_at, updated_at, approved_at, activated_at
		FROM v2_strategies
		WHERE workspace_id=$1 AND archived_at IS NULL AND status=$2
		ORDER BY version DESC, activated_at DESC NULLS LAST, id DESC
		LIMIT 1
	`, workspaceID, StatusActive)
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
				WHEN $2 THEN 1
				WHEN $3 THEN 1
				ELSE 3
			END,
			version DESC,
			created_at DESC
		LIMIT 1
	`, workspaceID, StatusDraft, StatusReadyForReview, StatusActive)

	return scanStrategy(row)
}

func workingStrategyTx(ctx context.Context, tx *sql.Tx, workspaceID int) (Strategy, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, status, version, title, summary, source_type,
			created_by, approved_by, created_at, updated_at, approved_at, activated_at
		FROM v2_strategies
		WHERE workspace_id=$1 AND archived_at IS NULL AND status IN ($2, $3)
		ORDER BY version DESC, created_at DESC, id DESC
		LIMIT 1
	`, workspaceID, StatusDraft, StatusReadyForReview)
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
			COUNT(*) FILTER (WHERE BTRIM(markdown)<>''),
			COUNT(*) FILTER (WHERE BTRIM(markdown)='')
		FROM strategic_documents
		WHERE workspace_id=$1
	`, workspaceID).Scan(&summary.BlocksTotal, &summary.BlocksReady, &summary.BlocksFilled, &summary.BlocksEmpty)
	if err != nil {
		return KnowledgeBaseSummary{}, err
	}

	_ = s.dbx.QueryRowContext(ctx, `
		SELECT readiness_score, readiness_status
		FROM strategic_quality_reports
		WHERE workspace_id=$1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, workspaceID).Scan(&summary.ReadinessScore, &summary.ReadinessStatus)

	return summary, nil
}

var (
	ErrStrategyActivationNotReady         = errors.New("strategy_activation_not_ready")
	ErrStrategyActivationStale            = errors.New("strategy_activation_stale")
	ErrStrategyActivationArtifactsMissing = errors.New("strategy_activation_artifacts_missing")
)

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

func mustJSON(value any) []byte {
	if value == nil {
		return []byte("{}")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte("{}")
	}
	return raw
}

func reverseChatMessages(messages []StrategyChatMessage) {
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
}
