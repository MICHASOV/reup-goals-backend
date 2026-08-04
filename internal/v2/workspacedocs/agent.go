package workspacedocs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

type agentDocumentPayload struct {
	DocumentID       int64  `json:"document_id"`
	BaseVersion      int    `json:"base_version"`
	Title            string `json:"title"`
	Content          string `json:"content"`
	ParentDocumentID *int64 `json:"parent_document_id"`
	DepartmentIDs    []int  `json:"linked_department_ids"`
	DirectionIDs     []int  `json:"linked_direction_ids"`
	ProjectIDs       []int  `json:"linked_project_ids"`
}

func (h *Handler) ApplyAgentDocument(
	ctx context.Context,
	workspaceID int,
	userID int,
	toolName string,
	arguments map[string]any,
) (any, error) {
	payload, err := decodeAgentDocumentPayload(toolName, arguments)
	if err != nil {
		return nil, err
	}
	var document Document
	switch toolName {
	case "propose_document":
		status := "draft"
		document, err = h.store.Create(ctx, workspaceID, userID, Input{
			Title:         &payload.Title,
			Content:       &payload.Content,
			Status:        &status,
			ParentID:      payload.ParentDocumentID,
			DepartmentIDs: &payload.DepartmentIDs,
			WorkstreamIDs: &payload.DirectionIDs,
			ProjectIDs:    &payload.ProjectIDs,
		})
	case "update_document":
		document, err = h.store.Update(ctx, workspaceID, userID, payload.DocumentID, Input{
			Title:       &payload.Title,
			Content:     &payload.Content,
			BaseVersion: &payload.BaseVersion,
		})
	default:
		return nil, errors.New("invalid_agent_document_tool")
	}
	if err != nil {
		return nil, err
	}
	h.captureDocument(ctx, workspaceID, userID, document)
	h.hub.publish(documentStreamKey{workspaceID: workspaceID, documentID: document.ID}, document)
	return map[string]any{
		"ok":          true,
		"entity_type": "workspace_document",
		"entity_id":   document.ID,
		"title":       document.Title,
		"version":     document.Version,
		"status":      document.Status,
	}, nil
}

func decodeAgentDocumentPayload(toolName string, arguments map[string]any) (agentDocumentPayload, error) {
	raw, err := json.Marshal(arguments)
	if err != nil {
		return agentDocumentPayload{}, err
	}
	var payload agentDocumentPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return agentDocumentPayload{}, err
	}
	payload.Title = strings.TrimSpace(payload.Title)
	payload.Content = strings.TrimSpace(payload.Content)
	if payload.Title == "" || payload.Content == "" {
		return agentDocumentPayload{}, ErrInvalidDocument
	}
	switch toolName {
	case "propose_document":
		if payload.DocumentID != 0 || payload.BaseVersion != 0 {
			return agentDocumentPayload{}, ErrInvalidDocument
		}
	case "update_document":
		if payload.DocumentID <= 0 || payload.BaseVersion <= 0 {
			return agentDocumentPayload{}, ErrInvalidDocument
		}
	default:
		return agentDocumentPayload{}, errors.New("invalid_agent_document_tool")
	}
	return payload, nil
}
