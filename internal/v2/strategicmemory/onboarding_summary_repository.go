package strategicmemory

import (
	"context"
	"database/sql"
	"strings"
)

func (s *Store) BeginOnboardingSummary(ctx context.Context, workspaceID int, revision int, sourceID int) error {
	result, err := s.dbx.ExecContext(ctx, `
		INSERT INTO strategic_onboarding_summaries (
			workspace_id, source_revision, source_id, status, markdown
		)
		SELECT workspace_id, $2, $3, 'generating', ''
		FROM strategic_knowledge_pipeline_state
		WHERE workspace_id=$1 AND conversation_revision=$2 AND last_user_source_id=$3
		ON CONFLICT (workspace_id) DO UPDATE SET
			source_revision=EXCLUDED.source_revision,
			source_id=EXCLUDED.source_id,
			status='generating',
			markdown='',
			updated_at=NOW()
	`, workspaceID, revision, sourceID)
	if err != nil {
		return err
	}
	return requirePipelineUpdate(result)
}

func (s *Store) CompleteOnboardingSummary(ctx context.Context, workspaceID int, revision int, sourceID int, markdown string) error {
	markdown = strings.TrimSpace(markdown)
	if markdown == "" {
		return sql.ErrNoRows
	}
	result, err := s.dbx.ExecContext(ctx, `
		UPDATE strategic_onboarding_summaries
		SET status='ready', markdown=$4, updated_at=NOW()
		WHERE workspace_id=$1 AND source_revision=$2 AND source_id=$3
	`, workspaceID, revision, sourceID, markdown)
	if err != nil {
		return err
	}
	return requirePipelineUpdate(result)
}

func (s *Store) DeleteOnboardingSummary(ctx context.Context, workspaceID int, revision int, sourceID int) error {
	_, err := s.dbx.ExecContext(ctx, `
		DELETE FROM strategic_onboarding_summaries
		WHERE workspace_id=$1 AND source_revision=$2 AND source_id=$3 AND status='generating'
	`, workspaceID, revision, sourceID)
	return err
}

func (s *Store) OnboardingSummary(ctx context.Context, workspaceID int) (*OnboardingSummary, error) {
	var item OnboardingSummary
	err := s.dbx.QueryRowContext(ctx, `
		SELECT workspace_id, source_revision, source_id, status, markdown, updated_at
		FROM strategic_onboarding_summaries
		WHERE workspace_id=$1
	`, workspaceID).Scan(
		&item.WorkspaceID, &item.SourceRevision, &item.SourceID,
		&item.Status, &item.Markdown, &item.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func strategicDocumentFromOnboardingSummary(summary *OnboardingSummary) (StrategicDocument, bool) {
	if summary == nil || summary.Status != "ready" || strings.TrimSpace(summary.Markdown) == "" {
		return StrategicDocument{}, false
	}
	return StrategicDocument{
		WorkspaceID:  summary.WorkspaceID,
		DocumentType: "company_overview",
		Title:        "О компании",
		Markdown:     strings.TrimSpace(summary.Markdown),
		Status:       "strong",
		Version:      1,
		GeneratedAt:  summary.UpdatedAt,
	}, true
}
