package strategicmemory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func (s *Store) DocumentByType(ctx context.Context, workspaceID int, documentType string) (StrategicDocument, error) {
	documentType = strings.TrimSpace(documentType)
	var item StrategicDocument
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, document_type, title, markdown, source_claim_ids_json,
			status, version, generated_at
		FROM strategic_documents
		WHERE workspace_id=$1 AND document_type=$2
	`, workspaceID, documentType).Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.DocumentType,
		&item.Title,
		&item.Markdown,
		&item.SourceClaimIDs,
		&item.Status,
		&item.Version,
		&item.GeneratedAt,
	)
	if err == sql.ErrNoRows {
		return StrategicDocument{
			WorkspaceID:  workspaceID,
			DocumentType: documentType,
			Title:        documentTitle(documentType),
			Status:       "empty",
		}, nil
	}
	return item, err
}

func (s *Store) CreateDocumentChatMessage(
	ctx context.Context,
	workspaceID int,
	documentType string,
	userID *int,
	role string,
	content string,
	metadata any,
) (DocumentChatMessage, error) {
	role = strings.TrimSpace(role)
	if role != "assistant" && role != "user" {
		role = "user"
	}
	var item DocumentChatMessage
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO strategic_document_chat_messages (
			workspace_id, document_type, user_id, role, content, metadata_json
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, role, content, created_at
	`, workspaceID, strings.TrimSpace(documentType), userID, role, strings.TrimSpace(content), mustJSON(metadata)).Scan(
		&item.ID, &item.Role, &item.Content, &item.CreatedAt,
	)
	return item, err
}

func (s *Store) DocumentChatMessages(
	ctx context.Context,
	workspaceID int,
	documentType string,
	limit int,
) ([]DocumentChatMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 300
	}
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, role, content, created_at
		FROM strategic_document_chat_messages
		WHERE workspace_id=$1 AND document_type=$2
		ORDER BY created_at DESC, id DESC
		LIMIT $3
	`, workspaceID, strings.TrimSpace(documentType), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []DocumentChatMessage{}
	for rows.Next() {
		var item DocumentChatMessage
		if err := rows.Scan(&item.ID, &item.Role, &item.Content, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	return items, nil
}

func (s *Store) DocumentChatSession(
	ctx context.Context,
	workspaceID int,
	documentType string,
	compactThreshold int,
	contextFingerprint string,
) (DocumentChatSession, error) {
	if compactThreshold <= 0 {
		compactThreshold = 120000
	}
	promptCacheKey := fmt.Sprintf("reupgoals-document-chat-workspace-%d-document-%s-v1", workspaceID, documentType)
	var item DocumentChatSession
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO strategic_document_chat_sessions (
			workspace_id, document_type, compact_threshold, prompt_cache_key, context_fingerprint
		)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (workspace_id, document_type) DO UPDATE SET
			compact_threshold=EXCLUDED.compact_threshold,
			prompt_cache_key=EXCLUDED.prompt_cache_key,
			context_fingerprint=EXCLUDED.context_fingerprint,
			updated_at=NOW()
		RETURNING conversation_id, previous_response_id, compact_threshold, prompt_cache_key, context_fingerprint
	`, workspaceID, strings.TrimSpace(documentType), compactThreshold, promptCacheKey, contextFingerprint).Scan(
		&item.ConversationID,
		&item.PreviousResponseID,
		&item.CompactThreshold,
		&item.PromptCacheKey,
		&item.ContextFingerprint,
	)
	return item, err
}

func (s *Store) UpdateDocumentChatConversationID(
	ctx context.Context,
	workspaceID int,
	documentType string,
	conversationID string,
) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE strategic_document_chat_sessions
		SET conversation_id=$3, previous_response_id='', updated_at=NOW()
		WHERE workspace_id=$1 AND document_type=$2
	`, workspaceID, strings.TrimSpace(documentType), strings.TrimSpace(conversationID))
	return err
}

func (s *Store) UpdateDocumentChatPreviousResponseID(
	ctx context.Context,
	workspaceID int,
	documentType string,
	responseID string,
) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE strategic_document_chat_sessions
		SET previous_response_id=$3, updated_at=NOW()
		WHERE workspace_id=$1 AND document_type=$2
	`, workspaceID, strings.TrimSpace(documentType), strings.TrimSpace(responseID))
	return err
}
