package strategicmemory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"reup-goals-backend/internal/v2/jobs"
)

// Conversation state covers immediate turns. Cross-product company context is
// intentionally compiled in a wider window to avoid one extraction call per
// message while keeping other chats reasonably fresh.
const contextRefreshDelay = 5 * time.Minute

type SourceCapture struct {
	SourceType            string
	EntityKey             string
	Content               string
	FactsOnly             bool
	PreferredDocumentType string
	Metadata              map[string]any
}

type SourceRecorder struct {
	store *Store
	jobs  *jobs.Manager
}

func NewSourceRecorder(dbx *sql.DB, manager *jobs.Manager) *SourceRecorder {
	if dbx == nil {
		return nil
	}
	return &SourceRecorder{store: NewStore(dbx), jobs: manager}
}

func (s *Service) captureDeferredSource(
	ctx context.Context,
	workspaceID int,
	userID int,
	input SourceCapture,
) (int, bool, error) {
	if s == nil {
		return 0, false, nil
	}
	recorder := &SourceRecorder{store: s.store, jobs: s.jobs}
	return recorder.Capture(ctx, workspaceID, userID, input)
}

func (s *Service) CaptureSource(
	ctx context.Context,
	workspaceID int,
	userID int,
	input SourceCapture,
) (int, bool, error) {
	return s.captureDeferredSource(ctx, workspaceID, userID, input)
}

func (r *SourceRecorder) Capture(
	ctx context.Context,
	workspaceID int,
	userID int,
	input SourceCapture,
) (int, bool, error) {
	if r == nil || r.store == nil {
		return 0, false, nil
	}
	input.SourceType = strings.TrimSpace(input.SourceType)
	input.EntityKey = strings.TrimSpace(input.EntityKey)
	input.Content = strings.TrimSpace(input.Content)
	if workspaceID <= 0 || input.EntityKey == "" || input.Content == "" || !isDeferredSourceType(input.SourceType) {
		return 0, false, fmt.Errorf("invalid_strategic_source")
	}

	sum := sha256.Sum256([]byte(input.Content))
	contentHash := hex.EncodeToString(sum[:])
	metadata := cloneMetadata(input.Metadata)
	metadata["entity_key"] = input.EntityKey
	metadata["content_hash"] = contentHash
	metadata["facts_only"] = input.FactsOnly
	if preferred := strings.TrimSpace(input.PreferredDocumentType); preferred != "" {
		metadata["preferred_document_type"] = preferred
	}

	var sourceUserID *int
	if userID > 0 {
		sourceUserID = &userID
	}
	sourceID, created, err := r.store.CreateRawSourceOnce(
		ctx, workspaceID, sourceUserID, input.SourceType, input.EntityKey,
		contentHash, input.Content, metadata,
	)
	if err != nil {
		return sourceID, false, err
	}
	if created {
		if _, err := r.store.RecordDeferredKnowledgeSource(ctx, workspaceID, sourceID); err != nil {
			return sourceID, true, err
		}
	}
	if r.jobs != nil {
		_, err = r.jobs.Enqueue(
			ctx,
			workspaceID,
			jobTypeKnowledgeContextRefresh,
			"pending",
			map[string]any{"latest_source_id": sourceID},
			5,
			time.Now().UTC().Add(contextRefreshDelay),
		)
		if err != nil {
			return sourceID, created, err
		}
	}
	return sourceID, created, nil
}

func JSONSourceContent(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
}

func cloneMetadata(value map[string]any) map[string]any {
	result := make(map[string]any, len(value)+4)
	for key, item := range value {
		result[key] = item
	}
	return result
}

func isDeferredSourceType(value string) bool {
	switch value {
	case SourceTypeStrategyMessage, SourceTypeDocumentMessage, SourceTypeTacticsMessage,
		SourceTypeTaskDiscussion, SourceTypeWorkspaceDoc, SourceTypeTaskCompletion,
		SourceTypeDepartment, SourceTypeTacticalPlan, SourceTypeWorkstream,
		SourceTypeProject, SourceTypeRisk, SourceTypeOpportunity, SourceTypeHypothesis, SourceTypeResearchResult:
		return true
	default:
		return false
	}
}
