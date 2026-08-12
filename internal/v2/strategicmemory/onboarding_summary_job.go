package strategicmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"reup-goals-backend/internal/v2/jobs"
)

const (
	jobTypeOnboardingSummary = "knowledge_base.onboarding_summary"
	onboardingSummaryTimeout = 6 * time.Minute
)

type onboardingSummaryJobPayload struct {
	Revision        int    `json:"revision"`
	SourceID        int    `json:"source_id"`
	UserID          int    `json:"user_id"`
	ReadinessReason string `json:"readiness_reason"`
}

func (s *Service) queueOnboardingSummary(
	ctx context.Context,
	workspaceID int,
	userID int,
	pipeline KnowledgePipelineState,
	sourceID int,
	readinessReason string,
) error {
	if err := s.store.BeginOnboardingSummary(ctx, workspaceID, pipeline.ConversationRevision, sourceID); err != nil {
		return err
	}
	payload := onboardingSummaryJobPayload{
		Revision:        pipeline.ConversationRevision,
		SourceID:        sourceID,
		UserID:          userID,
		ReadinessReason: strings.TrimSpace(readinessReason),
	}
	if s.jobs != nil {
		_, err := s.jobs.Enqueue(
			ctx,
			workspaceID,
			jobTypeOnboardingSummary,
			fmt.Sprintf("revision:%d", payload.Revision),
			payload,
			3,
			time.Now().UTC(),
		)
		return err
	}

	parentCtx := context.WithoutCancel(ctx)
	go func() {
		jobCtx, cancel := context.WithTimeout(parentCtx, onboardingSummaryTimeout)
		defer cancel()
		if err := s.runOnboardingSummary(jobCtx, workspaceID, payload); err != nil {
			_ = s.store.DeleteOnboardingSummary(context.WithoutCancel(jobCtx), workspaceID, payload.Revision, payload.SourceID)
			log.Printf("[WARN] onboarding summary failed workspace_id=%d revision=%d: %v", workspaceID, payload.Revision, err)
		}
	}()
	return nil
}

func (s *Service) runOnboardingSummary(ctx context.Context, workspaceID int, payload onboardingSummaryJobPayload) error {
	pipeline, err := s.store.KnowledgePipelineState(ctx, workspaceID)
	if err != nil {
		return err
	}
	if pipeline.ConversationRevision != payload.Revision || pipeline.LastUserSourceID != payload.SourceID {
		_ = s.store.DeleteOnboardingSummary(ctx, workspaceID, payload.Revision, payload.SourceID)
		return nil
	}

	summary, err := s.store.OnboardingSummary(ctx, workspaceID)
	if err != nil {
		return err
	}
	if summary != nil && summary.SourceRevision == payload.Revision && summary.SourceID == payload.SourceID &&
		summary.Status == "ready" && strings.TrimSpace(summary.Markdown) != "" {
		_, err = s.queueKnowledgeCandidate(ctx, workspaceID, pipeline, payload.SourceID, payload.ReadinessReason)
		return err
	}
	if summary == nil || summary.SourceRevision != payload.Revision || summary.SourceID != payload.SourceID {
		if err := s.store.BeginOnboardingSummary(ctx, workspaceID, payload.Revision, payload.SourceID); err != nil {
			return err
		}
	}

	session, err := s.store.OpenAISession(ctx, workspaceID, s.compactThreshold)
	if err != nil {
		return err
	}
	vectorStoreIDs := vectorStoreIDsFromSession(session)
	if s.contextIndex != nil {
		if indexedIDs, indexErr := s.contextIndex.Available(ctx, workspaceID); indexErr == nil && len(indexedIDs) > 0 {
			vectorStoreIDs = indexedIDs
		}
	}
	result, err := s.generateOnboardingSummary(ctx, workspaceID, payload.UserID, session.ConversationID, vectorStoreIDs, session)
	if err != nil {
		return err
	}
	if err := s.store.CompleteOnboardingSummary(ctx, workspaceID, payload.Revision, payload.SourceID, result.Markdown); err != nil {
		return err
	}
	if strings.TrimSpace(result.ConversationID) != "" && result.ConversationID != session.ConversationID {
		_ = s.store.UpdateOpenAIConversationID(ctx, workspaceID, result.ConversationID)
	}
	_, err = s.queueKnowledgeCandidate(ctx, workspaceID, pipeline, payload.SourceID, payload.ReadinessReason)
	return err
}

func registerOnboardingSummaryJob(s *Service) {
	s.jobs.Register(jobTypeOnboardingSummary, func(ctx context.Context, job jobs.Job) error {
		if job.WorkspaceID == nil {
			return fmt.Errorf("onboarding summary job has no workspace")
		}
		var payload onboardingSummaryJobPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return err
		}
		err := s.runOnboardingSummary(ctx, *job.WorkspaceID, payload)
		if err != nil && job.Attempts >= job.MaxAttempts {
			_ = s.store.DeleteOnboardingSummary(context.WithoutCancel(ctx), *job.WorkspaceID, payload.Revision, payload.SourceID)
		}
		return err
	})
}
