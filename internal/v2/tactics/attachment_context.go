package tactics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	attachmentKnowledgeDocument = "knowledge_document"
	attachmentWorkspaceDocument = "workspace_document"
	attachmentWorkstream        = "workstream"
	attachmentProject           = "project"
	attachmentUploadedFile      = "uploaded_file"
	maxContextAttachments       = 5
	maxAttachmentRunes          = 12000
)

var ErrInvalidContextAttachment = errors.New("invalid_context_attachment")

func (s *FacilitatorService) resolveContextAttachments(
	ctx context.Context,
	workspaceID int,
	requested []TacticsContextAttachment,
) ([]TacticsResolvedAttachment, error) {
	if len(requested) > maxContextAttachments {
		return nil, fmt.Errorf("too_many_context_attachments")
	}
	result := make([]TacticsResolvedAttachment, 0, len(requested))
	seen := map[string]bool{}
	for _, attachment := range requested {
		attachment.Type = strings.TrimSpace(attachment.Type)
		attachment.Key = strings.TrimSpace(attachment.Key)
		lookupKey := fmt.Sprintf("%s:%d:%s", attachment.Type, attachment.ID, attachment.Key)
		if seen[lookupKey] {
			continue
		}
		seen[lookupKey] = true

		var resolved TacticsResolvedAttachment
		var err error
		switch attachment.Type {
		case attachmentKnowledgeDocument:
			resolved, err = s.resolveKnowledgeDocument(ctx, workspaceID, attachment)
		case attachmentWorkspaceDocument:
			resolved, err = s.resolveWorkspaceDocument(ctx, workspaceID, attachment)
		case attachmentWorkstream:
			resolved, err = s.resolveWorkstreamAttachment(ctx, workspaceID, attachment)
		case attachmentProject:
			resolved, err = s.resolveProjectAttachment(ctx, workspaceID, attachment)
		case attachmentUploadedFile:
			resolved, err = s.resolveUploadedFile(ctx, workspaceID, attachment)
		default:
			return nil, ErrInvalidContextAttachment
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidContextAttachment, err)
		}
		result = append(result, resolved)
	}
	return result, nil
}

func (s *FacilitatorService) resolveUploadedFile(
	ctx context.Context,
	workspaceID int,
	attachment TacticsContextAttachment,
) (TacticsResolvedAttachment, error) {
	if attachment.ID <= 0 {
		return TacticsResolvedAttachment{}, ErrInvalidContextAttachment
	}
	var filename, status string
	err := s.store.dbx.QueryRowContext(ctx, `
		SELECT filename, status
		FROM strategic_openai_files
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, attachment.ID).Scan(&filename, &status)
	if err != nil {
		return TacticsResolvedAttachment{}, err
	}
	if status == "failed" {
		return TacticsResolvedAttachment{}, ErrInvalidContextAttachment
	}
	return TacticsResolvedAttachment{
		Type:  attachmentUploadedFile,
		ID:    attachment.ID,
		Label: firstNonEmpty(attachment.Label, filename),
		Content: fmt.Sprintf(
			"Attached file %q is available through file_search. Inspect it when it is relevant to the user's request.",
			filename,
		),
	}, nil
}

func (s *FacilitatorService) resolveKnowledgeDocument(
	ctx context.Context,
	workspaceID int,
	attachment TacticsContextAttachment,
) (TacticsResolvedAttachment, error) {
	if attachment.Key == "" {
		return TacticsResolvedAttachment{}, ErrInvalidContextAttachment
	}
	document, err := s.memoryStore.DocumentByType(ctx, workspaceID, attachment.Key)
	if err != nil {
		return TacticsResolvedAttachment{}, err
	}
	if document.Status == "empty" && strings.TrimSpace(document.Markdown) == "" {
		return TacticsResolvedAttachment{}, sql.ErrNoRows
	}
	return TacticsResolvedAttachment{
		Type: attachmentKnowledgeDocument, Key: document.DocumentType,
		Label:   firstNonEmpty(attachment.Label, document.Title),
		Content: truncateAttachment(document.Markdown),
	}, nil
}

func (s *FacilitatorService) resolveWorkspaceDocument(
	ctx context.Context,
	workspaceID int,
	attachment TacticsContextAttachment,
) (TacticsResolvedAttachment, error) {
	if attachment.ID <= 0 {
		return TacticsResolvedAttachment{}, ErrInvalidContextAttachment
	}
	var title, content string
	err := s.store.dbx.QueryRowContext(ctx, `
		SELECT title, content
		FROM workspace_documents
		WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL
	`, workspaceID, attachment.ID).Scan(&title, &content)
	if err != nil {
		return TacticsResolvedAttachment{}, err
	}
	return TacticsResolvedAttachment{
		Type: attachmentWorkspaceDocument, ID: attachment.ID,
		Label:   firstNonEmpty(attachment.Label, title),
		Content: truncateAttachment(content),
	}, nil
}

func (s *FacilitatorService) resolveWorkstreamAttachment(
	ctx context.Context,
	workspaceID int,
	attachment TacticsContextAttachment,
) (TacticsResolvedAttachment, error) {
	if attachment.ID <= 0 {
		return TacticsResolvedAttachment{}, ErrInvalidContextAttachment
	}
	item, err := s.store.workstreamByID(ctx, workspaceID, int(attachment.ID))
	if err != nil {
		return TacticsResolvedAttachment{}, err
	}
	content, _ := json.Marshal(map[string]any{
		"title": item.Title, "description": item.Description, "goal": item.Goal,
		"valuable_final_product": item.CKP, "reason": item.Reason,
		"strategy_link": item.ContributionType, "metrics": item.Metrics,
		"status": item.Status, "health_status": item.HealthStatus,
	})
	return TacticsResolvedAttachment{
		Type: attachmentWorkstream, ID: attachment.ID,
		Label:   firstNonEmpty(attachment.Label, item.Title),
		Content: truncateAttachment(string(content)),
	}, nil
}

func (s *FacilitatorService) resolveProjectAttachment(
	ctx context.Context,
	workspaceID int,
	attachment TacticsContextAttachment,
) (TacticsResolvedAttachment, error) {
	if attachment.ID <= 0 {
		return TacticsResolvedAttachment{}, ErrInvalidContextAttachment
	}
	item, err := s.store.projectByID(ctx, workspaceID, int(attachment.ID))
	if err != nil {
		return TacticsResolvedAttachment{}, err
	}
	content, _ := json.Marshal(map[string]any{
		"title": item.Title, "description": item.Description, "why_needed": item.WhyNeeded,
		"expected_result":  item.ExpectedResult,
		"success_criteria": item.SuccessCriteria, "failure_criteria": item.FailureCriteria,
		"metric_name": item.MetricName, "expected_value": item.ExpectedValue,
		"status": item.Status,
	})
	return TacticsResolvedAttachment{
		Type: attachmentProject, ID: attachment.ID,
		Label:   firstNonEmpty(attachment.Label, item.Title),
		Content: truncateAttachment(string(content)),
	}, nil
}

func truncateAttachment(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= maxAttachmentRunes {
		return value
	}
	return string(runes[:maxAttachmentRunes]) + "\n[content truncated]"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if clean := strings.TrimSpace(value); clean != "" {
			return clean
		}
	}
	return "Контекст"
}
