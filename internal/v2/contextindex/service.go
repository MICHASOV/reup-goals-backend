package contextindex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"reup-goals-backend/internal/ai"
)

const RetrievalInstructions = `

The current workspace context is available through file_search. Search it before relying on company facts, documents, strategy, course, tactics, projects, risks, opportunities, or tasks. Treat the newest indexed workspace context as the source of truth for current structured data. Use the conversation itself for what the user and assistant have said. Do not ask the user to repeat information that can be found through file_search.`

type Service struct {
	dbx      *sql.DB
	provider ai.Provider
	locks    sync.Map
	pending  sync.Map
}

type contextFile struct {
	ID                int64
	ContentHash       string
	OpenAIFileID      string
	VectorStoreID     string
	VectorStoreFileID string
	Status            string
}

type snapshotSection struct {
	Title string
	Query string
}

const workspaceContextLockNamespace int64 = 0x5245555000000000

var snapshotSections = []snapshotSection{
	{Title: "Workspace", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.id), '[]'::jsonb)::text
		FROM (
			SELECT id, name, display_name, status, created_at, updated_at
			FROM workspaces WHERE id=$1
		) item`},
	{Title: "Knowledge base documents", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.document_type), '[]'::jsonb)::text
		FROM (
			SELECT document_type, title, markdown, status, version, generated_at
			FROM strategic_documents WHERE workspace_id=$1
		) item`},
	{Title: "Knowledge claims", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.id), '[]'::jsonb)::text
		FROM (
			SELECT id, claim_text, claim_type, topic_key, evidence_level, confidence,
				status, status_reason, superseded_by, created_at, updated_at
			FROM strategic_claims WHERE workspace_id=$1
		) item`},
	{Title: "Latest knowledge snapshot", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.id), '[]'::jsonb)::text
		FROM (
			SELECT id, snapshot_json, business_stage, version, created_at
			FROM strategic_memory_snapshots WHERE workspace_id=$1
			ORDER BY version DESC, id DESC LIMIT 1
		) item`},
	{Title: "Knowledge research agenda", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.id), '[]'::jsonb)::text
		FROM (
			SELECT id, topic_key, question_goal, why_it_matters, status, priority,
				linked_claim_ids_json, last_asked_at, times_asked, created_at, updated_at
			FROM strategic_research_agenda_items WHERE workspace_id=$1
		) item`},
	{Title: "Communication profile", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.id), '[]'::jsonb)::text
		FROM (
			SELECT id, tone, address_style, detail_level, structure_preference,
				frustration_sensitivity, known_preferences_json, updated_at
			FROM strategic_communication_profiles WHERE workspace_id=$1
		) item`},
	{Title: "Current dialogue focus", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.id), '[]'::jsonb)::text
		FROM (
			SELECT id, current_topic, research_goal, last_question, expected_answer_type,
				answer_status, do_not_repeat_json, next_angles_json, updated_at
			FROM strategic_dialogue_focus WHERE workspace_id=$1
		) item`},
	{Title: "Latest knowledge quality report", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.id), '[]'::jsonb)::text
		FROM (
			SELECT id, readiness_score, readiness_status, changed_document_types_json,
				report_json, created_at
			FROM strategic_quality_reports WHERE workspace_id=$1
			ORDER BY created_at DESC, id DESC LIMIT 1
		) item`},
	{Title: "Strategies", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.version), '[]'::jsonb)::text
		FROM (
			SELECT id, status, version, title, summary, source_type, created_at, updated_at,
				approved_at, activated_at
			FROM v2_strategies WHERE workspace_id=$1 AND archived_at IS NULL
		) item`},
	{Title: "Latest strategy documents", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.sort_order, item.id), '[]'::jsonb)::text
		FROM (
			SELECT document.id, document.document_type, document.title, document.display_title,
				document.status, document.content_json, document.formatted_markdown,
				document.source_refs_json, document.open_questions_json, document.sort_order
			FROM v2_strategy_synthesis_documents document
			JOIN v2_strategy_synthesis_runs run ON run.id=document.run_id
			WHERE document.workspace_id=$1 AND run.status='completed'
				AND run.id=(
					SELECT id FROM v2_strategy_synthesis_runs
					WHERE workspace_id=$1 AND status='completed'
					ORDER BY created_at DESC, id DESC LIMIT 1
				)
			) item`},
	{Title: "Strategy artifacts", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.strategy_id, item.sort_order, item.id), '[]'::jsonb)::text
		FROM (
			SELECT * FROM v2_strategy_artifacts WHERE workspace_id=$1
		) item`},
	{Title: "Latest strategy readiness", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.id), '[]'::jsonb)::text
		FROM (
			SELECT id, verdict, can_synthesize, overall_score, confidence, report_json, completed_at
			FROM v2_strategy_readiness_runs
			WHERE workspace_id=$1 AND status='completed'
			ORDER BY created_at DESC, id DESC LIMIT 1
		) item`},
	{Title: "Strategy research requests", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.id), '[]'::jsonb)::text
		FROM (
			SELECT id, strategy_id, area, research_goal, why_it_matters, context_to_carry,
				priority, blocking, status, result_text, created_at, updated_at
			FROM v2_strategy_research_requests WHERE workspace_id=$1
		) item`},
	{Title: "Courses", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.id), '[]'::jsonb)::text
		FROM (
			SELECT * FROM v2_courses WHERE workspace_id=$1 AND archived_at IS NULL
		) item`},
	{Title: "Tactical plans", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.id), '[]'::jsonb)::text
		FROM (
			SELECT * FROM v2_tactical_plans WHERE workspace_id=$1 AND archived_at IS NULL
		) item`},
	{Title: "Tactical workstreams", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.sort_order, item.id), '[]'::jsonb)::text
		FROM (
			SELECT * FROM v2_tactical_workstreams WHERE workspace_id=$1 AND archived_at IS NULL
		) item`},
	{Title: "Tactical projects", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.sort_order, item.id), '[]'::jsonb)::text
		FROM (
			SELECT * FROM v2_tactical_projects WHERE workspace_id=$1 AND archived_at IS NULL
		) item`},
	{Title: "Tactical risks", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.id), '[]'::jsonb)::text
		FROM (
			SELECT * FROM v2_tactical_risks WHERE workspace_id=$1 AND archived_at IS NULL
		) item`},
	{Title: "Tactical opportunities", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.id), '[]'::jsonb)::text
		FROM (
			SELECT * FROM v2_tactical_opportunities WHERE workspace_id=$1 AND archived_at IS NULL
		) item`},
	{Title: "Latest tactics readiness", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.id), '[]'::jsonb)::text
		FROM (
			SELECT id, tactical_plan_id, verdict, can_activate, overall_score, confidence,
				report_json, completed_at
			FROM v2_tactics_readiness_runs
			WHERE workspace_id=$1 AND status='completed'
			ORDER BY created_at DESC, id DESC LIMIT 1
		) item`},
	{Title: "Tasks", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.id), '[]'::jsonb)::text
		FROM (
			SELECT * FROM v2_tasks WHERE workspace_id=$1 AND archived_at IS NULL
		) item`},
	{Title: "Task secondary workstream links", Query: `
		SELECT COALESCE(jsonb_agg(to_jsonb(item) ORDER BY item.task_id, item.workstream_id), '[]'::jsonb)::text
		FROM (
			SELECT * FROM v2_task_secondary_workstreams WHERE workspace_id=$1
		) item`},
}

func New(dbx *sql.DB, provider ai.Provider) *Service {
	if dbx == nil || provider == nil {
		return nil
	}
	return &Service{dbx: dbx, provider: provider}
}

// Available returns the latest ready snapshot without putting file indexing on
// the user-facing response path. A refresh is deduplicated and completed in the
// background; strict one-shot workers call Ensure when they require freshness.
func (s *Service) Available(ctx context.Context, workspaceID int) ([]string, error) {
	if s == nil || workspaceID <= 0 {
		return nil, nil
	}
	active, found, err := s.activeContextFile(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if !found || strings.TrimSpace(active.VectorStoreID) == "" {
		return s.Ensure(ctx, workspaceID)
	}
	s.RefreshAsync(workspaceID)
	return []string{active.VectorStoreID}, nil
}

func (s *Service) RefreshAsync(workspaceID int) {
	if s == nil || workspaceID <= 0 {
		return
	}
	if _, loaded := s.pending.LoadOrStore(workspaceID, struct{}{}); loaded {
		return
	}
	go func() {
		defer s.pending.Delete(workspaceID)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := s.Ensure(ctx, workspaceID); err != nil {
			s.logRefreshFailure(workspaceID, err)
		}
	}()
}

func (s *Service) logRefreshFailure(workspaceID int, refreshErr error) {
	if refreshErr == nil {
		return
	}
	errorText := refreshErr.Error()
	if len(errorText) > 4000 {
		errorText = errorText[:4000]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = s.dbx.ExecContext(ctx, `
		INSERT INTO strategic_ai_runs (
			workspace_id, scenario, model, prompt_version, status, error
		) VALUES ($1, 'workspace_context_sync', $2, 'workspace_context_v1', 'failed', $3)
	`, workspaceID, s.provider.ModelName(), errorText)
}

func (s *Service) Ensure(ctx context.Context, workspaceID int) ([]string, error) {
	if s == nil || workspaceID <= 0 {
		return nil, nil
	}
	lock := s.workspaceLock(workspaceID)
	lock.Lock()
	defer lock.Unlock()
	releaseDatabaseLock, err := s.acquireDatabaseLock(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	defer releaseDatabaseLock()

	content, hash, err := s.buildSnapshot(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("build workspace context: %w", err)
	}
	active, found, err := s.activeContextFile(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if found && active.ContentHash == hash && strings.TrimSpace(active.VectorStoreID) != "" {
		return []string{active.VectorStoreID}, nil
	}

	vectorStoreID, err := s.ensureVectorStore(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	filename := fmt.Sprintf("reup-workspace-%d-context-%s.md", workspaceID, hash[:12])
	uploaded, err := s.provider.UploadFile(ctx, filename, "assistants", bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("upload workspace context: %w", err)
	}
	attached, err := s.provider.AddFileToVectorStore(ctx, vectorStoreID, uploaded.ID)
	if err != nil {
		s.deleteFileBestEffort(uploaded.ID)
		return nil, fmt.Errorf("attach workspace context: %w", err)
	}
	ready, err := s.provider.WaitVectorStoreFileReady(ctx, vectorStoreID, attached.ID, uploaded.ID, 2*time.Minute)
	if err != nil {
		s.deleteVectorStoreFileBestEffort(vectorStoreID, uploaded.ID)
		s.deleteFileBestEffort(uploaded.ID)
		return nil, fmt.Errorf("index workspace context: %w", err)
	}
	if ready.Status != "completed" {
		s.deleteVectorStoreFileBestEffort(vectorStoreID, uploaded.ID)
		s.deleteFileBestEffort(uploaded.ID)
		return nil, fmt.Errorf("workspace context is not ready: %s", ready.Status)
	}
	vectorStoreFileID := strings.TrimSpace(ready.ID)
	if vectorStoreFileID == "" {
		vectorStoreFileID = strings.TrimSpace(attached.ID)
	}
	if err := s.activateContextFile(ctx, workspaceID, hash, uploaded.ID, vectorStoreID, vectorStoreFileID); err != nil {
		s.deleteVectorStoreFileBestEffort(vectorStoreID, uploaded.ID)
		s.deleteFileBestEffort(uploaded.ID)
		return nil, err
	}
	if found && active.OpenAIFileID != uploaded.ID {
		s.deleteVectorStoreFileBestEffort(active.VectorStoreID, active.OpenAIFileID)
		s.deleteFileBestEffort(active.OpenAIFileID)
	}
	return []string{vectorStoreID}, nil
}

func (s *Service) acquireDatabaseLock(ctx context.Context, workspaceID int) (func(), error) {
	connection, err := s.dbx.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire workspace context connection: %w", err)
	}
	lockKey := workspaceContextLockNamespace + int64(workspaceID)
	if _, err := connection.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		connection.Close()
		return nil, fmt.Errorf("acquire workspace context lock: %w", err)
	}
	return func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = connection.ExecContext(unlockCtx, `SELECT pg_advisory_unlock($1)`, lockKey)
		_ = connection.Close()
	}, nil
}

func (s *Service) buildSnapshot(ctx context.Context, workspaceID int) ([]byte, string, error) {
	var builder strings.Builder
	builder.WriteString("# REUP.goals workspace context\n\n")
	builder.WriteString("This file contains the current structured state of the workspace. Empty arrays mean that the corresponding area has no structured data yet.\n")
	for _, section := range snapshotSections {
		var raw string
		if err := s.dbx.QueryRowContext(ctx, section.Query, workspaceID).Scan(&raw); err != nil {
			return nil, "", fmt.Errorf("%s: %w", section.Title, err)
		}
		builder.WriteString("\n## ")
		builder.WriteString(section.Title)
		builder.WriteString("\n\n```json\n")
		builder.WriteString(raw)
		builder.WriteString("\n```\n")
	}
	content := []byte(builder.String())
	sum := sha256.Sum256(content)
	return content, hex.EncodeToString(sum[:]), nil
}

func (s *Service) ensureVectorStore(ctx context.Context, workspaceID int) (string, error) {
	var vectorStoreID string
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO strategic_openai_sessions (workspace_id, compact_threshold, prompt_cache_key)
		VALUES ($1, 120000, $2)
		ON CONFLICT (workspace_id) DO UPDATE SET updated_at=NOW()
		RETURNING vector_store_id
	`, workspaceID, fmt.Sprintf("reupgoals-strategic-director-workspace-%d-v1", workspaceID)).Scan(&vectorStoreID)
	if err != nil {
		return "", fmt.Errorf("load workspace vector store: %w", err)
	}
	if strings.TrimSpace(vectorStoreID) != "" {
		return strings.TrimSpace(vectorStoreID), nil
	}
	created, err := s.provider.CreateVectorStore(ctx, fmt.Sprintf("reup-workspace-%d", workspaceID))
	if err != nil {
		return "", fmt.Errorf("create workspace vector store: %w", err)
	}
	if _, err := s.dbx.ExecContext(ctx, `
		UPDATE strategic_openai_sessions SET vector_store_id=$2, updated_at=NOW() WHERE workspace_id=$1
	`, workspaceID, created.ID); err != nil {
		return "", fmt.Errorf("save workspace vector store: %w", err)
	}
	return created.ID, nil
}

func (s *Service) activeContextFile(ctx context.Context, workspaceID int) (contextFile, bool, error) {
	var item contextFile
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id, content_hash, openai_file_id, vector_store_id, vector_store_file_id, status
		FROM v2_ai_workspace_context_files
		WHERE workspace_id=$1 AND status='active'
		ORDER BY updated_at DESC, id DESC LIMIT 1
	`, workspaceID).Scan(
		&item.ID, &item.ContentHash, &item.OpenAIFileID, &item.VectorStoreID,
		&item.VectorStoreFileID, &item.Status,
	)
	if err == sql.ErrNoRows {
		return contextFile{}, false, nil
	}
	if err != nil {
		return contextFile{}, false, fmt.Errorf("load active workspace context: %w", err)
	}
	return item, true, nil
}

func (s *Service) activateContextFile(ctx context.Context, workspaceID int, hash string, fileID string, vectorStoreID string, vectorStoreFileID string) error {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE v2_ai_workspace_context_files
		SET status='superseded', updated_at=NOW()
		WHERE workspace_id=$1 AND status='active'
	`, workspaceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO v2_ai_workspace_context_files (
			workspace_id, content_hash, openai_file_id, vector_store_id, vector_store_file_id, status
		) VALUES ($1, $2, $3, $4, $5, 'active')
		ON CONFLICT (workspace_id, content_hash) DO UPDATE SET
			openai_file_id=EXCLUDED.openai_file_id,
			vector_store_id=EXCLUDED.vector_store_id,
			vector_store_file_id=EXCLUDED.vector_store_file_id,
			status='active', error='', updated_at=NOW()
	`, workspaceID, hash, fileID, vectorStoreID, vectorStoreFileID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) workspaceLock(workspaceID int) *sync.Mutex {
	value, _ := s.locks.LoadOrStore(workspaceID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (s *Service) deleteVectorStoreFileBestEffort(vectorStoreID string, fileID string) {
	cleaner, ok := s.provider.(ai.VectorStoreFileCleaner)
	if !ok {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = cleaner.DeleteVectorStoreFile(cleanupCtx, vectorStoreID, fileID)
}

func (s *Service) deleteFileBestEffort(fileID string) {
	cleaner, ok := s.provider.(ai.ResourceCleaner)
	if !ok {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = cleaner.DeleteFile(cleanupCtx, fileID)
}
