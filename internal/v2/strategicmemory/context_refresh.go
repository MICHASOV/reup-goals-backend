package strategicmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const materialContextClaimLimit = 160

func (s *Service) queueImmediateKnowledgeContextRefresh(ctx context.Context, workspaceID int, throughSourceID int) error {
	if s.jobs != nil {
		_, err := s.jobs.Enqueue(
			ctx,
			workspaceID,
			jobTypeKnowledgeContextRefresh,
			"pending",
			map[string]any{"latest_source_id": throughSourceID},
			5,
			time.Now().UTC(),
		)
		return err
	}

	parentCtx := context.WithoutCancel(ctx)
	go func() {
		jobCtx, cancel := context.WithTimeout(parentCtx, knowledgeCandidateTimeout)
		defer cancel()
		if err := s.runKnowledgeContextRefresh(jobCtx, workspaceID); err != nil {
			s.store.LogAIRunWithUsage(
				jobCtx,
				workspaceID,
				"knowledge_base_context_refresh",
				knowledgeExtractionModel,
				StrategicMemoryPromptVersion,
				0,
				0,
				0,
				"failed",
				err.Error(),
			)
		}
	}()
	return nil
}

func (s *Service) runKnowledgeContextRefresh(ctx context.Context, workspaceID int) error {
	state, err := s.store.KnowledgePipelineState(ctx, workspaceID)
	if err != nil {
		return err
	}
	if state.LastExtractedSourceID >= state.LastUserSourceID {
		return nil
	}
	if knowledgePipelineBusy(state.Status) {
		if s.jobs != nil {
			_, err := s.jobs.Enqueue(
				ctx,
				workspaceID,
				jobTypeKnowledgeContextRefresh,
				"pending",
				map[string]any{"latest_source_id": state.LastUserSourceID},
				5,
				time.Now().UTC().Add(time.Minute),
			)
			return err
		}
		return nil
	}

	throughSourceID := state.LastUserSourceID
	sources, err := s.store.KnowledgeSourcesRange(ctx, workspaceID, state.LastExtractedSourceID, throughSourceID)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return s.store.MarkKnowledgeContextExtracted(ctx, workspaceID, throughSourceID)
	}

	changed := false
	for _, chunk := range chunkKnowledgeSources(sources, knowledgeSourceChunkRunes) {
		chunkChanged, err := s.extractKnowledgeSourceChunk(ctx, workspaceID, chunk)
		if err != nil {
			return err
		}
		changed = changed || chunkChanged
	}
	if changed {
		if err := s.refreshMaterialContextSnapshot(ctx, workspaceID); err != nil {
			return err
		}
	}
	if err := s.store.MarkKnowledgeContextExtracted(ctx, workspaceID, throughSourceID); err != nil {
		return err
	}
	if s.contextIndex != nil {
		s.contextIndex.RefreshAsync(workspaceID)
	}
	return nil
}

func knowledgePipelineBusy(status string) bool {
	switch status {
	case KnowledgePipelineAuditCandidate, KnowledgePipelineExtracting,
		KnowledgePipelineReviewing, KnowledgePipelineCompiling:
		return true
	default:
		return false
	}
}

func (s *Service) refreshMaterialContextSnapshot(ctx context.Context, workspaceID int) error {
	claims, err := s.store.ListClaims(ctx, workspaceID, 3000)
	if err != nil {
		return err
	}
	ordered := make([]Claim, 0, materialContextClaimLimit)
	for _, importance := range []string{"critical", "high", "medium"} {
		for _, claim := range claims {
			if claim.Importance != importance {
				continue
			}
			ordered = append(ordered, claim)
			if len(ordered) == materialContextClaimLimit {
				break
			}
		}
		if len(ordered) == materialContextClaimLimit {
			break
		}
	}

	items := make([]map[string]any, 0, len(ordered))
	for _, claim := range ordered {
		items = append(items, map[string]any{
			"claim_id": claim.ID, "statement": claim.ClaimText,
			"type": claim.ClaimType, "topic": claim.TopicKey,
			"evidence": claim.EvidenceLevel, "confidence": claim.Confidence,
			"importance": claim.Importance, "status": claim.Status,
			"source_ids": json.RawMessage(claim.SourceIDs),
		})
	}

	snapshot, err := s.store.LatestSnapshot(ctx, workspaceID)
	if err != nil {
		return err
	}
	document := map[string]any{}
	businessStage := ""
	if snapshot != nil {
		businessStage = snapshot.BusinessStage
		if len(snapshot.Snapshot) > 0 {
			if err := json.Unmarshal(snapshot.Snapshot, &document); err != nil {
				return fmt.Errorf("decode current company context: %w", err)
			}
		}
	}
	document["material_context"] = map[string]any{
		"description": "Current material business context selected from verified and user-provided sources.",
		"items":       items,
	}
	_, err = s.store.SaveSnapshot(ctx, workspaceID, businessStage, document)
	return err
}
