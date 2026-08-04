package tasks

import (
	"context"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

const maxTaskAttachments = 20

func (s *Store) CreateTaskAttachment(
	ctx context.Context,
	workspaceID int,
	userID int,
	filename string,
	contentType string,
	content []byte,
) (TaskAttachment, error) {
	filename = strings.TrimSpace(filename)
	contentType = strings.TrimSpace(contentType)
	if filename == "" || len(content) == 0 {
		return TaskAttachment{}, ErrInvalidCompletionFile
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	var item TaskAttachment
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_task_attachments (
			workspace_id, uploaded_by, filename, content_type, size_bytes, content
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, filename, content_type, size_bytes, created_at
	`, workspaceID, userID, filename, contentType, len(content), content).Scan(
		&item.ID, &item.Filename, &item.ContentType, &item.SizeBytes, &item.CreatedAt,
	)
	if err != nil {
		return TaskAttachment{}, err
	}
	item.DownloadURL = taskAttachmentDownloadURL(item.ID)
	return item, nil
}

func (s *Store) TaskAttachmentContent(ctx context.Context, workspaceID int, attachmentID int64) (TaskAttachment, []byte, error) {
	var item TaskAttachment
	var content []byte
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id, filename, content_type, size_bytes, content, created_at
		FROM v2_task_attachments
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, attachmentID).Scan(
		&item.ID, &item.Filename, &item.ContentType, &item.SizeBytes, &content, &item.CreatedAt,
	)
	if err != nil {
		return TaskAttachment{}, nil, err
	}
	item.DownloadURL = taskAttachmentDownloadURL(item.ID)
	return item, content, nil
}

func (s *Store) ReplaceTaskAttachments(ctx context.Context, workspaceID int, taskID int, attachmentIDs []int64) error {
	if len(attachmentIDs) > maxTaskAttachments {
		return ErrInvalidCompletionFile
	}
	seen := map[int64]bool{}
	cleaned := make([]int64, 0, len(attachmentIDs))
	for _, attachmentID := range attachmentIDs {
		if attachmentID <= 0 || seen[attachmentID] {
			return ErrInvalidCompletionFile
		}
		seen[attachmentID] = true
		cleaned = append(cleaned, attachmentID)
	}
	if len(cleaned) > 0 {
		var count int
		if err := s.dbx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM v2_task_attachments
			WHERE workspace_id=$1 AND id=ANY($2)
		`, workspaceID, pq.Array(cleaned)).Scan(&count); err != nil {
			return err
		}
		if count != len(cleaned) {
			return ErrInvalidCompletionFile
		}
	}

	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM v2_task_attachment_links WHERE workspace_id=$1 AND task_id=$2
	`, workspaceID, taskID); err != nil {
		return err
	}
	for _, attachmentID := range cleaned {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO v2_task_attachment_links (workspace_id, task_id, attachment_id)
			VALUES ($1, $2, $3)
		`, workspaceID, taskID, attachmentID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) taskAttachments(ctx context.Context, workspaceID int, taskIDs []int) (map[int][]TaskAttachment, error) {
	if len(taskIDs) == 0 {
		return map[int][]TaskAttachment{}, nil
	}
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT link.task_id, attachment.id, attachment.filename, attachment.content_type,
			attachment.size_bytes, attachment.created_at
		FROM v2_task_attachment_links link
		JOIN v2_task_attachments attachment
			ON attachment.id=link.attachment_id AND attachment.workspace_id=link.workspace_id
		WHERE link.workspace_id=$1 AND link.task_id=ANY($2)
		ORDER BY link.task_id, link.created_at, attachment.id
	`, workspaceID, pq.Array(taskIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := map[int][]TaskAttachment{}
	for rows.Next() {
		var taskID int
		var item TaskAttachment
		if err := rows.Scan(&taskID, &item.ID, &item.Filename, &item.ContentType, &item.SizeBytes, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.DownloadURL = taskAttachmentDownloadURL(item.ID)
		items[taskID] = append(items[taskID], item)
	}
	return items, rows.Err()
}

func taskAttachmentDownloadURL(id int64) string {
	return fmt.Sprintf("/api/v2/tasks/files/%d/download", id)
}
