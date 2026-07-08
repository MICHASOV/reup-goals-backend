package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lib/pq"
)

type documentEntry struct {
	ID            int
	DocumentID    int
	DocumentType  string
	Text          string
	StatementType string
	SourceQuote   string
}

type proposedItemRecord struct {
	ID             int
	ClientItemID   string
	SourceQuote    string
	CleanText      string
	StatementType  string
	TargetDocument string
	RoutingReason  string
	Confidence     string
}

func (s *Store) CreateIntakeSession(ctx context.Context, workspaceID int, userID int, rawText string) (int, error) {
	var sessionID int
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO v2_knowledge_intake_sessions (
			workspace_id, user_id, raw_text, status, router_prompt_version, reconciler_prompt_version
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, workspaceID, userID, rawText, SessionProcessing, RouterPromptVersion, ReconcilerPromptVersion).Scan(&sessionID)
	return sessionID, err
}

func (s *Store) AttachQuestionBlockToSession(ctx context.Context, sessionID int, questionBlockID int) error {
	if questionBlockID <= 0 {
		return nil
	}
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_knowledge_intake_sessions
		SET guidance_question_block_id=$1, updated_at=NOW()
		WHERE id=$2
	`, questionBlockID, sessionID)
	return err
}

func (s *Store) IntakeSessionStatus(ctx context.Context, workspaceID int, sessionID int) (string, error) {
	var status string
	err := s.dbx.QueryRowContext(ctx, `
		SELECT status
		FROM v2_knowledge_intake_sessions
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, sessionID).Scan(&status)
	return status, err
}

func (s *Store) IntakeSessionState(ctx context.Context, workspaceID int, sessionID int) (status string, errorMessage string, result *GuidancePreviewResponse, err error) {
	var resultRaw []byte
	err = s.dbx.QueryRowContext(ctx, `
		SELECT status, error_message, COALESCE(guidance_result_json, 'null'::jsonb)
		FROM v2_knowledge_intake_sessions
		WHERE workspace_id=$1 AND id=$2
	`, workspaceID, sessionID).Scan(&status, &errorMessage, &resultRaw)
	if err != nil {
		return "", "", nil, err
	}
	if len(resultRaw) > 0 && string(resultRaw) != "null" {
		var parsed GuidancePreviewResponse
		if err := json.Unmarshal(resultRaw, &parsed); err != nil {
			return "", "", nil, err
		}
		result = &parsed
	}
	return status, errorMessage, result, nil
}

func (s *Store) SaveGuidanceResult(ctx context.Context, workspaceID int, sessionID int, response GuidancePreviewResponse) error {
	raw, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = s.dbx.ExecContext(ctx, `
		UPDATE v2_knowledge_intake_sessions
		SET guidance_result_json=$1::jsonb, updated_at=NOW()
		WHERE workspace_id=$2 AND id=$3
	`, string(raw), workspaceID, sessionID)
	return err
}

func (s *Store) AddProgressEvent(ctx context.Context, workspaceID int, sessionID int, stage string, message string, details any) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	detailsRaw := []byte("{}")
	if details != nil {
		raw, err := json.Marshal(details)
		if err != nil {
			return err
		}
		detailsRaw = raw
	}
	_, err := s.dbx.ExecContext(ctx, `
		INSERT INTO v2_knowledge_intake_progress_events (
			session_id, workspace_id, stage, message, details_json
		)
		VALUES ($1, $2, $3, $4, $5::jsonb)
	`, sessionID, workspaceID, strings.TrimSpace(stage), message, string(detailsRaw))
	return err
}

func (s *Store) ProgressEvents(ctx context.Context, workspaceID int, sessionID int) ([]IntakeProgressEvent, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, stage, message, details_json, created_at
		FROM v2_knowledge_intake_progress_events
		WHERE workspace_id=$1 AND session_id=$2
		ORDER BY id ASC
	`, workspaceID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []IntakeProgressEvent{}
	for rows.Next() {
		var event IntakeProgressEvent
		var details []byte
		if err := rows.Scan(&event.ID, &event.Stage, &event.Message, &details, &event.CreatedAt); err != nil {
			return nil, err
		}
		if len(details) > 0 && string(details) != "{}" {
			event.Details = json.RawMessage(details)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) MarkSessionFailed(ctx context.Context, sessionID int, message string, raw json.RawMessage) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_knowledge_intake_sessions
		SET status=$1, error_message=$2, router_raw_response_json=$3::jsonb, updated_at=NOW()
		WHERE id=$4
	`, SessionFailed, message, nullableJSON(raw), sessionID)
	return err
}

func (s *Store) SaveRouterResponse(ctx context.Context, sessionID int, response RouterResponse, raw json.RawMessage) error {
	intentRaw, err := json.Marshal(defaultConversationIntent(response.ConversationIntent))
	if err != nil {
		return err
	}
	_, err = s.dbx.ExecContext(ctx, `
		UPDATE v2_knowledge_intake_sessions
		SET input_summary=$1, router_raw_response_json=$2::jsonb, conversation_intent_json=$3::jsonb, updated_at=NOW()
		WHERE id=$4
	`, strings.TrimSpace(response.InputSummary), nullableJSON(raw), string(intentRaw), sessionID)
	return err
}

func (s *Store) SaveRouterItems(ctx context.Context, sessionID int, workspaceID int, items []RouterItem) error {
	for _, item := range items {
		if _, err := s.dbx.ExecContext(ctx, `
			INSERT INTO v2_proposed_knowledge_items (
				session_id, workspace_id, client_item_id, source_quote, clean_text, statement_type,
				target_document_type, routing_reason, confidence
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, sessionID, workspaceID, item.ClientItemID, item.SourceQuote, item.CleanText, item.StatementType,
			item.TargetDocument, item.RoutingReason, item.Confidence); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) EnsureDocuments(ctx context.Context, workspaceID int) (map[string]int, error) {
	if err := s.ensureDefaultBlocks(ctx, workspaceID); err != nil {
		return nil, err
	}

	existing, err := s.existingDocuments(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if documentsReady(existing) {
		return existing, nil
	}

	result := make(map[string]int, len(documentDefinitions))
	for _, definition := range documentDefinitions {
		var documentID int
		if err := s.dbx.QueryRowContext(ctx, `
			INSERT INTO v2_knowledge_documents (workspace_id, document_type, title)
			VALUES ($1, $2, $3)
			ON CONFLICT (workspace_id, document_type) DO UPDATE SET
				title=EXCLUDED.title,
				updated_at=v2_knowledge_documents.updated_at
			RETURNING id
		`, workspaceID, definition.Type, definition.Title).Scan(&documentID); err != nil {
			return nil, err
		}

		result[definition.Type] = documentID
		if err := s.ensureInitialEntryFromBlock(ctx, workspaceID, documentID, definition); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (s *Store) existingDocuments(ctx context.Context, workspaceID int) (map[string]int, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT document_type, id
		FROM v2_knowledge_documents
		WHERE workspace_id=$1
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]int, len(documentDefinitions))
	for rows.Next() {
		var documentType string
		var documentID int
		if err := rows.Scan(&documentType, &documentID); err != nil {
			return nil, err
		}
		result[documentType] = documentID
	}
	return result, rows.Err()
}

func documentsReady(documents map[string]int) bool {
	if len(documents) < len(documentDefinitions) {
		return false
	}
	for _, definition := range documentDefinitions {
		if documents[definition.Type] == 0 {
			return false
		}
	}
	return true
}

func (s *Store) ensureInitialEntryFromBlock(ctx context.Context, workspaceID int, documentID int, definition DocumentDefinition) error {
	var hasEntries bool
	if err := s.dbx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM v2_knowledge_document_entries
			WHERE workspace_id=$1 AND document_id=$2 AND status='active'
		)
	`, workspaceID, documentID).Scan(&hasEntries); err != nil {
		return err
	}
	if hasEntries {
		return nil
	}

	var content string
	if err := s.dbx.QueryRowContext(ctx, `
		SELECT content
		FROM v2_knowledge_base_blocks
		WHERE workspace_id=$1 AND type=$2 AND archived_at IS NULL
	`, workspaceID, definition.BlockType).Scan(&content); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}

	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}

	_, err := s.dbx.ExecContext(ctx, `
		INSERT INTO v2_knowledge_document_entries (
			workspace_id, document_id, document_type, text, statement_type, source_type, position, status
		)
		VALUES ($1, $2, $3, $4, $5, 'manual', 1, 'active')
	`, workspaceID, documentID, definition.Type, content, StatementTypeStatement)
	return err
}

func (s *Store) DocumentEntries(ctx context.Context, workspaceID int, documentID int) ([]documentEntry, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, document_id, document_type, text, statement_type, source_quote
		FROM v2_knowledge_document_entries
		WHERE workspace_id=$1 AND document_id=$2 AND status='active'
		ORDER BY position ASC, id ASC
	`, workspaceID, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []documentEntry{}
	for rows.Next() {
		var entry documentEntry
		if err := rows.Scan(&entry.ID, &entry.DocumentID, &entry.DocumentType, &entry.Text, &entry.StatementType, &entry.SourceQuote); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}

	return entries, rows.Err()
}

func (s *Store) ProposedItemsByDocument(ctx context.Context, sessionID int) (map[string][]proposedItemRecord, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, client_item_id, source_quote, clean_text, statement_type, target_document_type, routing_reason, confidence
		FROM v2_proposed_knowledge_items
		WHERE session_id=$1
		ORDER BY id ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string][]proposedItemRecord{}
	for rows.Next() {
		var item proposedItemRecord
		if err := rows.Scan(&item.ID, &item.ClientItemID, &item.SourceQuote, &item.CleanText, &item.StatementType,
			&item.TargetDocument, &item.RoutingReason, &item.Confidence); err != nil {
			return nil, err
		}
		result[item.TargetDocument] = append(result[item.TargetDocument], item)
	}

	return result, rows.Err()
}

func (s *Store) SaveReconcilerResponse(ctx context.Context, sessionID int, workspaceID int, documentID int, documentType string, response ReconcilerResponse) error {
	for _, patch := range response.Patches {
		sourceIDs, err := json.Marshal(patch.SourceItemIDs)
		if err != nil {
			return err
		}
		targetID, err := nullableEntryID(patch.TargetEntryID)
		if err != nil {
			return err
		}
		if _, err := s.dbx.ExecContext(ctx, `
			INSERT INTO v2_proposed_document_patches (
				session_id, workspace_id, document_id, document_type, patch_type, target_entry_id,
				source_item_ids, existing_text, new_text, reason, status
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10, $11)
		`, sessionID, workspaceID, documentID, documentType, patch.PatchType, targetID, string(sourceIDs),
			patch.ExistingText, patch.NewText, patch.Reason, PatchStatusSuggested); err != nil {
			return err
		}
	}

	for _, conflict := range response.Conflicts {
		sourceIDs, err := json.Marshal(conflict.SourceItemIDs)
		if err != nil {
			return err
		}
		entryID, err := nullableEntryID(conflict.ExistingEntryID)
		if err != nil {
			return err
		}
		if _, err := s.dbx.ExecContext(ctx, `
			INSERT INTO v2_proposed_document_conflicts (
				session_id, workspace_id, document_id, document_type, existing_entry_id, source_item_ids,
				existing_text, new_text, question, option_a_text, option_b_text, status
			)
			VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $11, $12)
		`, sessionID, workspaceID, documentID, documentType, entryID, string(sourceIDs), conflict.ExistingText,
			conflict.NewText, conflict.Question, conflict.OptionAText, conflict.OptionBText, ConflictStatusActive); err != nil {
			return err
		}
	}

	for _, ignored := range response.IgnoredItems {
		sourceIDs, err := json.Marshal(ignored.SourceItemIDs)
		if err != nil {
			return err
		}
		if _, err := s.dbx.ExecContext(ctx, `
			INSERT INTO v2_ignored_knowledge_items (
				session_id, workspace_id, document_id, document_type, source_item_ids, clean_text, reason
			)
			VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7)
		`, sessionID, workspaceID, documentID, documentType, string(sourceIDs), ignored.CleanText, ignored.Reason); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) SaveDirectAddPatches(ctx context.Context, sessionID int, workspaceID int, documentID int, documentType string, items []proposedItemRecord) error {
	for _, item := range items {
		sourceIDs, err := json.Marshal([]string{item.ClientItemID})
		if err != nil {
			return err
		}
		if _, err := s.dbx.ExecContext(ctx, `
			INSERT INTO v2_proposed_document_patches (
				session_id, workspace_id, document_id, document_type, patch_type, target_entry_id,
				source_item_ids, existing_text, new_text, reason, status
			)
			VALUES ($1, $2, $3, $4, $5, NULL, $6::jsonb, '', $7, $8, $9)
		`, sessionID, workspaceID, documentID, documentType, PatchTypeAdd, string(sourceIDs),
			item.CleanText, "Документ пустой, информация добавляется как новая запись.", PatchStatusSuggested); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) MarkSessionPreviewReady(ctx context.Context, sessionID int) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE v2_knowledge_intake_sessions
		SET status=$1, updated_at=NOW()
		WHERE id=$2
	`, SessionPreviewReady, sessionID)
	return err
}

func (s *Store) Preview(ctx context.Context, workspaceID int, sessionID int) (IntakePreviewResponse, error) {
	var response IntakePreviewResponse
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id, status, input_summary
		FROM v2_knowledge_intake_sessions
		WHERE id=$1 AND workspace_id=$2
	`, sessionID, workspaceID).Scan(&response.SessionID, &response.Status, &response.InputSummary)
	if err != nil {
		return response, err
	}

	patches, err := s.previewPatches(ctx, workspaceID, sessionID)
	if err != nil {
		return response, err
	}
	response.UpdatedDocuments = patches

	conflicts, err := s.previewConflicts(ctx, workspaceID, sessionID)
	if err != nil {
		return response, err
	}
	response.Conflicts = conflicts

	ignored, err := s.previewIgnored(ctx, workspaceID, sessionID)
	if err != nil {
		return response, err
	}
	response.IgnoredItems = ignored

	return response, nil
}

func (s *Store) previewPatches(ctx context.Context, workspaceID int, sessionID int) ([]IntakeDocumentPreview, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT p.id, p.document_id, p.document_type, d.title, p.patch_type, p.source_item_ids,
			p.existing_text, p.new_text, p.reason, p.status
		FROM v2_proposed_document_patches p
		JOIN v2_knowledge_documents d ON d.id=p.document_id
		WHERE p.workspace_id=$1 AND p.session_id=$2
		ORDER BY d.id ASC, p.id ASC
	`, workspaceID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	documents := []IntakeDocumentPreview{}
	byID := map[int]int{}
	for rows.Next() {
		var patch IntakePatch
		var title string
		var sourceJSON []byte
		if err := rows.Scan(&patch.ID, &patch.DocumentID, &patch.DocumentType, &title, &patch.PatchType, &sourceJSON,
			&patch.ExistingText, &patch.NewText, &patch.Reason, &patch.Status); err != nil {
			return nil, err
		}
		patch.SourceItemIDs = decodeStringArray(sourceJSON)
		index, ok := byID[patch.DocumentID]
		if !ok {
			documents = append(documents, IntakeDocumentPreview{
				DocumentID:   patch.DocumentID,
				DocumentType: patch.DocumentType,
				Title:        title,
				Patches:      []IntakePatch{},
			})
			index = len(documents) - 1
			byID[patch.DocumentID] = index
		}
		documents[index].Patches = append(documents[index].Patches, patch)
	}

	return documents, rows.Err()
}

func (s *Store) previewConflicts(ctx context.Context, workspaceID int, sessionID int) ([]IntakeConflict, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT c.id, c.document_id, c.document_type, d.title, c.source_item_ids, c.existing_text,
			c.new_text, c.question, c.option_a_text, c.option_b_text, c.status,
			COALESCE(c.selected_option, '')
		FROM v2_proposed_document_conflicts c
		JOIN v2_knowledge_documents d ON d.id=c.document_id
		WHERE c.workspace_id=$1 AND c.session_id=$2
		ORDER BY c.id ASC
	`, workspaceID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	conflicts := []IntakeConflict{}
	for rows.Next() {
		var conflict IntakeConflict
		var sourceJSON []byte
		if err := rows.Scan(&conflict.ID, &conflict.DocumentID, &conflict.DocumentType, &conflict.DocumentTitle,
			&sourceJSON, &conflict.ExistingText, &conflict.NewText, &conflict.Question, &conflict.OptionAText,
			&conflict.OptionBText, &conflict.Status, &conflict.SelectedOption); err != nil {
			return nil, err
		}
		conflict.SourceItemIDs = decodeStringArray(sourceJSON)
		conflicts = append(conflicts, conflict)
	}

	return conflicts, rows.Err()
}

func (s *Store) previewIgnored(ctx context.Context, workspaceID int, sessionID int) ([]IntakeIgnoredItem, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT i.document_id, i.document_type, d.title, i.source_item_ids, i.clean_text, i.reason
		FROM v2_ignored_knowledge_items i
		JOIN v2_knowledge_documents d ON d.id=i.document_id
		WHERE i.workspace_id=$1 AND i.session_id=$2
		ORDER BY i.id ASC
	`, workspaceID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []IntakeIgnoredItem{}
	for rows.Next() {
		var item IntakeIgnoredItem
		var sourceJSON []byte
		if err := rows.Scan(&item.DocumentID, &item.DocumentType, &item.DocumentTitle, &sourceJSON,
			&item.CleanText, &item.Reason); err != nil {
			return nil, err
		}
		item.SourceItemIDs = decodeStringArray(sourceJSON)
		items = append(items, item)
	}

	return items, rows.Err()
}

func (s *Store) RejectIntake(ctx context.Context, workspaceID int, sessionID int) error {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	status, err := sessionStatusForUpdate(ctx, tx, workspaceID, sessionID)
	if err != nil {
		return err
	}
	if status == SessionConfirmed {
		return errors.New("session_already_confirmed")
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE v2_proposed_document_patches
		SET status=$1
		WHERE workspace_id=$2 AND session_id=$3 AND status=$4
	`, PatchStatusRejected, workspaceID, sessionID, PatchStatusSuggested); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE v2_proposed_document_conflicts
		SET status=$1
		WHERE workspace_id=$2 AND session_id=$3 AND status=$4
	`, ConflictStatusDismissed, workspaceID, sessionID, ConflictStatusActive); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE v2_knowledge_intake_sessions
		SET status=$1, updated_at=NOW()
		WHERE workspace_id=$2 AND id=$3
	`, SessionRejected, workspaceID, sessionID); err != nil {
		return err
	}

	return tx.Commit()
}

type conflictResolution struct {
	ConflictID     int
	SelectedOption string
}

func (s *Store) ConfirmIntake(ctx context.Context, workspaceID int, userID int, sessionID int, acceptedPatchIDs []int, resolutions []conflictResolution) (IntakeConfirmResponse, error) {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return IntakeConfirmResponse{}, err
	}
	defer tx.Rollback()

	status, err := sessionStatusForUpdate(ctx, tx, workspaceID, sessionID)
	if err != nil {
		return IntakeConfirmResponse{}, err
	}
	if status == SessionConfirmed {
		return IntakeConfirmResponse{SessionID: sessionID}, nil
	}
	if status != SessionPreviewReady {
		return IntakeConfirmResponse{}, errors.New("session_not_ready")
	}

	resolutionByID := map[int]string{}
	for _, resolution := range resolutions {
		if resolution.SelectedOption != ConflictOptionExisting && resolution.SelectedOption != ConflictOptionNew {
			return IntakeConfirmResponse{}, errors.New("invalid_conflict_resolution")
		}
		resolutionByID[resolution.ConflictID] = resolution.SelectedOption
	}

	activeConflictIDs, err := activeConflictIDs(ctx, tx, workspaceID, sessionID)
	if err != nil {
		return IntakeConfirmResponse{}, err
	}
	for _, conflictID := range activeConflictIDs {
		if _, ok := resolutionByID[conflictID]; !ok {
			return IntakeConfirmResponse{}, errors.New("unresolved_conflicts")
		}
	}

	acceptedSet := map[int]bool{}
	for _, id := range acceptedPatchIDs {
		if id > 0 {
			acceptedSet[id] = true
		}
	}
	if len(acceptedSet) == 0 {
		if err := acceptAllSuggestedPatches(ctx, tx, workspaceID, sessionID, acceptedSet); err != nil {
			return IntakeConfirmResponse{}, err
		}
	}

	changedDocuments := map[int]bool{}
	appliedChanges := 0
	if err := applyPatches(ctx, tx, workspaceID, userID, sessionID, acceptedSet, changedDocuments, &appliedChanges); err != nil {
		return IntakeConfirmResponse{}, err
	}
	if err := applyConflictResolutions(ctx, tx, workspaceID, userID, sessionID, resolutionByID, changedDocuments, &appliedChanges); err != nil {
		return IntakeConfirmResponse{}, err
	}
	for documentID := range changedDocuments {
		if err := syncBlockFromDocumentTx(ctx, tx, workspaceID, documentID); err != nil {
			return IntakeConfirmResponse{}, err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE v2_knowledge_intake_sessions
		SET status=$1, updated_at=NOW()
		WHERE workspace_id=$2 AND id=$3
	`, SessionConfirmed, workspaceID, sessionID); err != nil {
		return IntakeConfirmResponse{}, err
	}

	if err := tx.Commit(); err != nil {
		return IntakeConfirmResponse{}, err
	}

	return IntakeConfirmResponse{
		SessionID:        sessionID,
		UpdatedDocuments: len(changedDocuments),
		AppliedChanges:   appliedChanges,
	}, nil
}

func (s *Store) AutoApplyIntake(ctx context.Context, workspaceID int, userID int, sessionID int) (IntakeConfirmResponse, error) {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return IntakeConfirmResponse{}, err
	}
	defer tx.Rollback()

	status, err := sessionStatusForUpdate(ctx, tx, workspaceID, sessionID)
	if err != nil {
		return IntakeConfirmResponse{}, err
	}
	if status == SessionConfirmed {
		return IntakeConfirmResponse{SessionID: sessionID}, nil
	}
	if status != SessionPreviewReady {
		return IntakeConfirmResponse{}, errors.New("session_not_ready")
	}

	acceptedSet := map[int]bool{}
	if err := acceptAllSuggestedPatches(ctx, tx, workspaceID, sessionID, acceptedSet); err != nil {
		return IntakeConfirmResponse{}, err
	}

	changedDocuments := map[int]bool{}
	appliedChanges := 0
	if err := applyPatches(ctx, tx, workspaceID, userID, sessionID, acceptedSet, changedDocuments, &appliedChanges); err != nil {
		return IntakeConfirmResponse{}, err
	}

	for documentID := range changedDocuments {
		if err := syncBlockFromDocumentTx(ctx, tx, workspaceID, documentID); err != nil {
			return IntakeConfirmResponse{}, err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE v2_proposed_document_conflicts
		SET status=$1, resolved_at=NOW()
		WHERE workspace_id=$2 AND session_id=$3 AND status=$4
	`, ConflictStatusDismissed, workspaceID, sessionID, ConflictStatusActive); err != nil {
		return IntakeConfirmResponse{}, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE v2_knowledge_intake_sessions
		SET status=$1, updated_at=NOW()
		WHERE workspace_id=$2 AND id=$3
	`, SessionConfirmed, workspaceID, sessionID); err != nil {
		return IntakeConfirmResponse{}, err
	}

	if err := tx.Commit(); err != nil {
		return IntakeConfirmResponse{}, err
	}

	return IntakeConfirmResponse{
		SessionID:        sessionID,
		UpdatedDocuments: len(changedDocuments),
		AppliedChanges:   appliedChanges,
	}, nil
}

func sessionStatusForUpdate(ctx context.Context, tx *sql.Tx, workspaceID int, sessionID int) (string, error) {
	var status string
	err := tx.QueryRowContext(ctx, `
		SELECT status
		FROM v2_knowledge_intake_sessions
		WHERE workspace_id=$1 AND id=$2
		FOR UPDATE
	`, workspaceID, sessionID).Scan(&status)
	return status, err
}

func activeConflictIDs(ctx context.Context, tx *sql.Tx, workspaceID int, sessionID int) ([]int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM v2_proposed_document_conflicts
		WHERE workspace_id=$1 AND session_id=$2 AND status=$3
	`, workspaceID, sessionID, ConflictStatusActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := []int{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func acceptAllSuggestedPatches(ctx context.Context, tx *sql.Tx, workspaceID int, sessionID int, acceptedSet map[int]bool) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM v2_proposed_document_patches
		WHERE workspace_id=$1 AND session_id=$2 AND status=$3
	`, workspaceID, sessionID, PatchStatusSuggested)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return err
		}
		acceptedSet[id] = true
	}
	return rows.Err()
}

func applyPatches(ctx context.Context, tx *sql.Tx, workspaceID int, userID int, sessionID int, acceptedSet map[int]bool, changedDocuments map[int]bool, appliedChanges *int) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, document_id, document_type, patch_type, target_entry_id, source_item_ids,
			existing_text, new_text
		FROM v2_proposed_document_patches
		WHERE workspace_id=$1 AND session_id=$2 AND status=$3
		ORDER BY id ASC
	`, workspaceID, sessionID, PatchStatusSuggested)
	if err != nil {
		return err
	}
	defer rows.Close()

	type patchRow struct {
		id            int
		documentID    int
		documentType  string
		patchType     string
		targetEntryID sql.NullInt64
		sourceJSON    []byte
		existingText  string
		newText       string
	}
	patches := []patchRow{}
	for rows.Next() {
		var patch patchRow
		if err := rows.Scan(&patch.id, &patch.documentID, &patch.documentType, &patch.patchType, &patch.targetEntryID,
			&patch.sourceJSON, &patch.existingText, &patch.newText); err != nil {
			return err
		}
		patches = append(patches, patch)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, patch := range patches {
		if !acceptedSet[patch.id] {
			if _, err := tx.ExecContext(ctx, `
				UPDATE v2_proposed_document_patches
				SET status=$1
				WHERE id=$2 AND workspace_id=$3
			`, PatchStatusRejected, patch.id, workspaceID); err != nil {
				return err
			}
			continue
		}

		switch patch.patchType {
		case PatchTypeAdd:
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO v2_knowledge_document_entries (
					workspace_id, document_id, document_type, text, statement_type, source_type,
					source_session_id, source_quote, position, status
				)
				VALUES ($1, $2, $3, $4, $5, 'ai_intake', $6, $7, (
					SELECT COALESCE(MAX(position), 0) + 1
					FROM v2_knowledge_document_entries
					WHERE workspace_id=$1 AND document_id=$2
				), 'active')
			`, workspaceID, patch.documentID, patch.documentType, patch.newText,
				statementTypeForSourceItems(ctx, tx, sessionID, decodeStringArray(patch.sourceJSON)),
				sessionID, sourceQuoteForSourceItems(ctx, tx, sessionID, decodeStringArray(patch.sourceJSON))); err != nil {
				return err
			}
		case PatchTypeUpdate:
			if !patch.targetEntryID.Valid {
				return fmt.Errorf("missing update target for patch %d", patch.id)
			}
			oldText, err := updateEntryText(ctx, tx, workspaceID, int(patch.targetEntryID.Int64), userID, sessionID, patch.newText)
			if err != nil {
				return err
			}
			_ = oldText
		default:
			return fmt.Errorf("invalid patch type %s", patch.patchType)
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE v2_proposed_document_patches
			SET status=$1, applied_at=NOW()
			WHERE id=$2 AND workspace_id=$3
		`, PatchStatusApplied, patch.id, workspaceID); err != nil {
			return err
		}

		changedDocuments[patch.documentID] = true
		(*appliedChanges)++
	}

	return nil
}

func applyConflictResolutions(ctx context.Context, tx *sql.Tx, workspaceID int, userID int, sessionID int, resolutionByID map[int]string, changedDocuments map[int]bool, appliedChanges *int) error {
	for conflictID, selected := range resolutionByID {
		var documentID int
		var documentType string
		var existingEntryID sql.NullInt64
		var newText string
		err := tx.QueryRowContext(ctx, `
			SELECT document_id, document_type, existing_entry_id, new_text
			FROM v2_proposed_document_conflicts
			WHERE id=$1 AND workspace_id=$2 AND session_id=$3 AND status=$4
			FOR UPDATE
		`, conflictID, workspaceID, sessionID, ConflictStatusActive).Scan(&documentID, &documentType, &existingEntryID, &newText)
		if err != nil {
			return err
		}

		if selected == ConflictOptionNew {
			if existingEntryID.Valid {
				if _, err := updateEntryText(ctx, tx, workspaceID, int(existingEntryID.Int64), userID, sessionID, newText); err != nil {
					return err
				}
			} else {
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO v2_knowledge_document_entries (
						workspace_id, document_id, document_type, text, statement_type, source_type,
						source_session_id, position, status
					)
					VALUES ($1, $2, $3, $4, $5, 'ai_intake', $6, (
						SELECT COALESCE(MAX(position), 0) + 1
						FROM v2_knowledge_document_entries
						WHERE workspace_id=$1 AND document_id=$2
					), 'active')
				`, workspaceID, documentID, documentType, newText, StatementTypeStatement, sessionID); err != nil {
					return err
				}
			}
			changedDocuments[documentID] = true
			(*appliedChanges)++
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE v2_proposed_document_conflicts
			SET status=$1, selected_option=$2, resolved_at=NOW()
			WHERE id=$3 AND workspace_id=$4
		`, ConflictStatusResolved, selected, conflictID, workspaceID); err != nil {
			return err
		}
	}

	return nil
}

func updateEntryText(ctx context.Context, tx *sql.Tx, workspaceID int, entryID int, userID int, sessionID int, newText string) (string, error) {
	var oldText string
	if err := tx.QueryRowContext(ctx, `
		SELECT text
		FROM v2_knowledge_document_entries
		WHERE id=$1 AND workspace_id=$2 AND status='active'
		FOR UPDATE
	`, entryID, workspaceID).Scan(&oldText); err != nil {
		return "", err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO v2_knowledge_document_entry_versions (
			entry_id, old_text, new_text, changed_by_user_id, source_session_id
		)
		VALUES ($1, $2, $3, $4, $5)
	`, entryID, oldText, newText, userID, sessionID); err != nil {
		return "", err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE v2_knowledge_document_entries
		SET text=$1, updated_at=NOW()
		WHERE id=$2 AND workspace_id=$3
	`, newText, entryID, workspaceID); err != nil {
		return "", err
	}

	return oldText, nil
}

func syncBlockFromDocumentTx(ctx context.Context, tx *sql.Tx, workspaceID int, documentID int) error {
	var documentType string
	if err := tx.QueryRowContext(ctx, `
		SELECT document_type
		FROM v2_knowledge_documents
		WHERE id=$1 AND workspace_id=$2
	`, documentID, workspaceID).Scan(&documentType); err != nil {
		return err
	}
	definition, ok := documentDefinitionByType(documentType)
	if !ok {
		return fmt.Errorf("unknown document type %s", documentType)
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT text
		FROM v2_knowledge_document_entries
		WHERE workspace_id=$1 AND document_id=$2 AND status='active'
		ORDER BY position ASC, id ASC
	`, workspaceID, documentID)
	if err != nil {
		return err
	}
	defer rows.Close()

	parts := []string{}
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return err
		}
		text = strings.TrimSpace(text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	content := strings.Join(parts, "\n\n")
	status := StatusEmpty
	if content != "" {
		status = StatusDraft
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE v2_knowledge_base_blocks
		SET content=$1, status=$2, updated_at=NOW()
		WHERE workspace_id=$3 AND type=$4 AND archived_at IS NULL
	`, content, status, workspaceID, definition.BlockType)
	return err
}

func statementTypeForSourceItems(ctx context.Context, tx *sql.Tx, sessionID int, itemIDs []string) string {
	if len(itemIDs) == 0 {
		return StatementTypeStatement
	}
	var statementType string
	if err := tx.QueryRowContext(ctx, `
		SELECT statement_type
		FROM v2_proposed_knowledge_items
		WHERE session_id=$1 AND client_item_id=$2
	`, sessionID, itemIDs[0]).Scan(&statementType); err != nil {
		return StatementTypeStatement
	}
	if ValidStatementType(statementType) {
		return statementType
	}
	return StatementTypeStatement
}

func sourceQuoteForSourceItems(ctx context.Context, tx *sql.Tx, sessionID int, itemIDs []string) string {
	if len(itemIDs) == 0 {
		return ""
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT source_quote
		FROM v2_proposed_knowledge_items
		WHERE session_id=$1 AND client_item_id = ANY($2::text[])
		ORDER BY id ASC
	`, sessionID, pq.Array(itemIDs))
	if err != nil {
		return ""
	}
	defer rows.Close()

	quotes := []string{}
	for rows.Next() {
		var quote string
		if err := rows.Scan(&quote); err != nil {
			return ""
		}
		if strings.TrimSpace(quote) != "" {
			quotes = append(quotes, quote)
		}
	}

	return strings.Join(quotes, " / ")
}

func nullableEntryID(value string) (sql.NullInt64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return sql.NullInt64{}, nil
	}
	value = strings.TrimPrefix(value, "entry_")
	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
		return sql.NullInt64{}, fmt.Errorf("invalid entry id %q", value)
	}
	return sql.NullInt64{Int64: int64(id), Valid: true}, nil
}

func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

func decodeStringArray(raw []byte) []string {
	values := []string{}
	_ = json.Unmarshal(raw, &values)
	return values
}
