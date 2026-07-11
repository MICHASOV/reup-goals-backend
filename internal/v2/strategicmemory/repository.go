package strategicmemory

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

type Store struct {
	dbx *sql.DB
}

func NewStore(dbx *sql.DB) *Store {
	return &Store{dbx: dbx}
}

func (s *Store) CreateRawSource(ctx context.Context, workspaceID int, userID *int, sourceType string, content string, metadata any) (int, error) {
	meta := mustJSON(metadata)
	var id int
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO strategic_raw_sources (workspace_id, user_id, source_type, content, metadata_json)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, workspaceID, userID, sourceType, content, meta).Scan(&id)
	return id, err
}

func (s *Store) OpenAISession(ctx context.Context, workspaceID int, compactThreshold int) (OpenAISession, error) {
	if compactThreshold <= 0 {
		compactThreshold = 120000
	}
	promptCacheKey := fmt.Sprintf("reupgoals-strategic-director-workspace-%d-v1", workspaceID)
	var item OpenAISession
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO strategic_openai_sessions (workspace_id, compact_threshold, prompt_cache_key)
		VALUES ($1, $2, $3)
		ON CONFLICT (workspace_id) DO UPDATE SET
			compact_threshold=EXCLUDED.compact_threshold,
			prompt_cache_key=EXCLUDED.prompt_cache_key,
			updated_at=NOW()
		RETURNING id, workspace_id, previous_response_id, vector_store_id,
			compact_threshold, prompt_cache_key, created_at, updated_at
	`, workspaceID, compactThreshold, promptCacheKey).Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.PreviousResponseID,
		&item.VectorStoreID,
		&item.CompactThreshold,
		&item.PromptCacheKey,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	return item, err
}

func (s *Store) UpdateOpenAIPreviousResponseID(ctx context.Context, workspaceID int, responseID string) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE strategic_openai_sessions
		SET previous_response_id=$2, updated_at=NOW()
		WHERE workspace_id=$1
	`, workspaceID, strings.TrimSpace(responseID))
	return err
}

func (s *Store) UpdateOpenAIVectorStoreID(ctx context.Context, workspaceID int, vectorStoreID string, compactThreshold int) error {
	if compactThreshold <= 0 {
		compactThreshold = 120000
	}
	promptCacheKey := fmt.Sprintf("reupgoals-strategic-director-workspace-%d-v1", workspaceID)
	_, err := s.dbx.ExecContext(ctx, `
		INSERT INTO strategic_openai_sessions (
			workspace_id, vector_store_id, compact_threshold, prompt_cache_key
		)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (workspace_id) DO UPDATE SET
			vector_store_id=EXCLUDED.vector_store_id,
			compact_threshold=EXCLUDED.compact_threshold,
			prompt_cache_key=EXCLUDED.prompt_cache_key,
			updated_at=NOW()
	`, workspaceID, strings.TrimSpace(vectorStoreID), compactThreshold, promptCacheKey)
	return err
}

func (s *Store) CreateStrategicFile(ctx context.Context, workspaceID int, rawSourceID *int, openAIFileID string, vectorStoreID string, filename string, contentType string, sizeBytes int64, status string, errorText string) (StrategicFile, error) {
	var item StrategicFile
	var scannedRawSourceID sql.NullInt64
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO strategic_openai_files (
			workspace_id, raw_source_id, openai_file_id, vector_store_id, filename,
			content_type, size_bytes, status, error
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, workspace_id, raw_source_id, openai_file_id, vector_store_id,
			filename, content_type, size_bytes, status, error, created_at, updated_at
	`,
		workspaceID,
		rawSourceID,
		strings.TrimSpace(openAIFileID),
		strings.TrimSpace(vectorStoreID),
		strings.TrimSpace(filename),
		strings.TrimSpace(contentType),
		sizeBytes,
		defaultString(status, "uploaded"),
		strings.TrimSpace(errorText),
	).Scan(
		&item.ID,
		&item.WorkspaceID,
		&scannedRawSourceID,
		&item.OpenAIFileID,
		&item.VectorStoreID,
		&item.Filename,
		&item.ContentType,
		&item.SizeBytes,
		&item.Status,
		&item.Error,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if scannedRawSourceID.Valid {
		value := int(scannedRawSourceID.Int64)
		item.RawSourceID = &value
	}
	return item, err
}

func (s *Store) UpdateStrategicFileStatus(ctx context.Context, workspaceID int, openAIFileID string, status string, errorText string) error {
	_, err := s.dbx.ExecContext(ctx, `
		UPDATE strategic_openai_files
		SET status=$3, error=$4, updated_at=NOW()
		WHERE workspace_id=$1 AND openai_file_id=$2
	`, workspaceID, strings.TrimSpace(openAIFileID), defaultString(status, "uploaded"), strings.TrimSpace(errorText))
	return err
}

func (s *Store) RecentMessages(ctx context.Context, workspaceID int, limit int) ([]ConversationMessage, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id,
			CASE WHEN source_type=$2 THEN 'assistant' ELSE 'user' END AS role,
			content,
			created_at
		FROM strategic_raw_sources
		WHERE workspace_id=$1
			AND source_type IN ($2, $3)
		ORDER BY created_at DESC, id DESC
		LIMIT $4
	`, workspaceID, SourceTypeAssistantMessage, SourceTypeUserMessage, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := []ConversationMessage{}
	for rows.Next() {
		var item ConversationMessage
		if err := rows.Scan(&item.ID, &item.Role, &item.Content, &item.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, item)
	}
	reverseMessages(messages)
	return messages, rows.Err()
}

func (s *Store) RelevantMessages(ctx context.Context, workspaceID int, query string, limit int) ([]ConversationMessage, error) {
	terms := searchTerms(query, 8)
	if len(terms) == 0 {
		return nil, nil
	}

	args := []any{workspaceID, SourceTypeAssistantMessage, SourceTypeUserMessage}
	clauses := make([]string, 0, len(terms))
	for _, term := range terms {
		args = append(args, "%"+strings.ToLower(term)+"%")
		clauses = append(clauses, fmt.Sprintf("LOWER(content) LIKE $%d", len(args)))
	}
	args = append(args, limit)

	rows, err := s.dbx.QueryContext(ctx, fmt.Sprintf(`
		SELECT id,
			CASE WHEN source_type=$2 THEN 'assistant' ELSE 'user' END AS role,
			content,
			created_at
		FROM strategic_raw_sources
		WHERE workspace_id=$1
			AND source_type IN ($2, $3)
			AND (%s)
		ORDER BY created_at DESC, id DESC
		LIMIT $%d
	`, strings.Join(clauses, " OR "), len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := []ConversationMessage{}
	for rows.Next() {
		var item ConversationMessage
		if err := rows.Scan(&item.ID, &item.Role, &item.Content, &item.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, item)
	}
	reverseMessages(messages)
	return messages, rows.Err()
}

func (s *Store) ListClaims(ctx context.Context, workspaceID int, limit int) ([]Claim, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, workspace_id, claim_text, claim_type, topic_key, evidence_level,
			confidence, source_ids_json, status, created_at, updated_at
		FROM strategic_claims
		WHERE workspace_id=$1 AND status=$2
		ORDER BY updated_at DESC, id DESC
		LIMIT $3
	`, workspaceID, ClaimStatusActive, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	claims := []Claim{}
	for rows.Next() {
		var item Claim
		if err := rows.Scan(
			&item.ID,
			&item.WorkspaceID,
			&item.ClaimText,
			&item.ClaimType,
			&item.TopicKey,
			&item.EvidenceLevel,
			&item.Confidence,
			&item.SourceIDs,
			&item.Status,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		claims = append(claims, item)
	}
	return claims, rows.Err()
}

func (s *Store) InsertClaims(ctx context.Context, workspaceID int, sourceID int, claims []aiMemoryResponseClaim) (int, int, error) {
	existing, err := s.claimKeys(ctx, workspaceID)
	if err != nil {
		return 0, 0, err
	}

	added := 0
	skipped := 0
	sourceIDs := mustJSON([]int{sourceID})
	for _, claim := range claims {
		text := cleanText(claim.ClaimText)
		if text == "" {
			continue
		}
		key := claimKey(text)
		if existing[key] {
			skipped += 1
			continue
		}
		_, err := s.dbx.ExecContext(ctx, `
			INSERT INTO strategic_claims (
				workspace_id, claim_text, claim_type, topic_key, evidence_level,
				confidence, source_ids_json, status
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`,
			workspaceID,
			text,
			normalizeClaimType(claim.ClaimType),
			normalizeTopicKey(claim.TopicKey),
			normalizeEvidenceLevel(claim.EvidenceLevel),
			normalizeConfidence(claim.Confidence),
			sourceIDs,
			ClaimStatusActive,
		)
		if err != nil {
			return added, skipped, err
		}
		existing[key] = true
		added += 1
	}
	return added, skipped, nil
}

type aiMemoryResponseClaim struct {
	ClaimText     string
	ClaimType     string
	TopicKey      string
	EvidenceLevel string
	Confidence    string
}

func (s *Store) claimKeys(ctx context.Context, workspaceID int) (map[string]bool, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT claim_text FROM strategic_claims
		WHERE workspace_id=$1 AND status=$2
	`, workspaceID, ClaimStatusActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string]bool{}
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		result[claimKey(text)] = true
	}
	return result, rows.Err()
}

func (s *Store) LatestSnapshot(ctx context.Context, workspaceID int) (*MemorySnapshot, error) {
	var item MemorySnapshot
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, snapshot_json, business_stage, version, created_at
		FROM strategic_memory_snapshots
		WHERE workspace_id=$1
		ORDER BY version DESC, id DESC
		LIMIT 1
	`, workspaceID).Scan(&item.ID, &item.WorkspaceID, &item.Snapshot, &item.BusinessStage, &item.Version, &item.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) SaveSnapshot(ctx context.Context, workspaceID int, businessStage string, snapshot any) (*MemorySnapshot, error) {
	raw := mustJSON(snapshot)
	var item MemorySnapshot
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO strategic_memory_snapshots (workspace_id, snapshot_json, business_stage, version)
		VALUES (
			$1,
			$2,
			$3,
			COALESCE((SELECT MAX(version) + 1 FROM strategic_memory_snapshots WHERE workspace_id=$1), 1)
		)
		RETURNING id, workspace_id, snapshot_json, business_stage, version, created_at
	`, workspaceID, raw, normalizeBusinessStage(businessStage)).Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.Snapshot,
		&item.BusinessStage,
		&item.Version,
		&item.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) CommunicationProfile(ctx context.Context, workspaceID int) (CommunicationProfile, error) {
	var item CommunicationProfile
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, tone, address_style, detail_level, structure_preference,
			frustration_sensitivity, known_preferences_json, updated_at
		FROM strategic_communication_profiles
		WHERE workspace_id=$1
	`, workspaceID).Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.Tone,
		&item.AddressStyle,
		&item.DetailLevel,
		&item.StructurePreference,
		&item.FrustrationSensitivity,
		&item.KnownPreferences,
		&item.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return CommunicationProfile{
			WorkspaceID:            workspaceID,
			Tone:                   DefaultCommunicationTone,
			AddressStyle:           DefaultAddressStyle,
			DetailLevel:            DefaultDetailLevel,
			StructurePreference:    DefaultStructurePreference,
			FrustrationSensitivity: DefaultFrustrationSensitivity,
			KnownPreferences:       mustJSON(map[string]any{}),
		}, nil
	}
	return item, err
}

func (s *Store) DialogueFocus(ctx context.Context, workspaceID int) (DialogueFocus, error) {
	var item DialogueFocus
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, current_topic, research_goal, last_question,
			expected_answer_type, answer_status, do_not_repeat_json, next_angles_json, updated_at
		FROM strategic_dialogue_focus
		WHERE workspace_id=$1
	`, workspaceID).Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.CurrentTopic,
		&item.ResearchGoal,
		&item.LastQuestion,
		&item.ExpectedAnswerType,
		&item.AnswerStatus,
		&item.DoNotRepeat,
		&item.NextAngles,
		&item.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return DialogueFocus{
			WorkspaceID:  workspaceID,
			AnswerStatus: "not_started",
			DoNotRepeat:  mustJSON([]string{}),
			NextAngles:   mustJSON([]string{}),
		}, nil
	}
	return item, err
}

func (s *Store) UpsertDialogueFocus(ctx context.Context, workspaceID int, focus DialogueFocus) (DialogueFocus, error) {
	var item DialogueFocus
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO strategic_dialogue_focus (
			workspace_id, current_topic, research_goal, last_question, expected_answer_type,
			answer_status, do_not_repeat_json, next_angles_json
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (workspace_id) DO UPDATE SET
			current_topic=EXCLUDED.current_topic,
			research_goal=EXCLUDED.research_goal,
			last_question=EXCLUDED.last_question,
			expected_answer_type=EXCLUDED.expected_answer_type,
			answer_status=EXCLUDED.answer_status,
			do_not_repeat_json=EXCLUDED.do_not_repeat_json,
			next_angles_json=EXCLUDED.next_angles_json,
			updated_at=NOW()
		RETURNING id, workspace_id, current_topic, research_goal, last_question,
			expected_answer_type, answer_status, do_not_repeat_json, next_angles_json, updated_at
	`,
		workspaceID,
		strings.TrimSpace(focus.CurrentTopic),
		strings.TrimSpace(focus.ResearchGoal),
		strings.TrimSpace(focus.LastQuestion),
		strings.TrimSpace(focus.ExpectedAnswerType),
		defaultString(focus.AnswerStatus, "open"),
		defaultRawJSON(focus.DoNotRepeat),
		defaultRawJSON(focus.NextAngles),
	).Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.CurrentTopic,
		&item.ResearchGoal,
		&item.LastQuestion,
		&item.ExpectedAnswerType,
		&item.AnswerStatus,
		&item.DoNotRepeat,
		&item.NextAngles,
		&item.UpdatedAt,
	)
	return item, err
}

func (s *Store) UpsertCommunicationProfile(ctx context.Context, workspaceID int, profile CommunicationProfile) (CommunicationProfile, error) {
	var item CommunicationProfile
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO strategic_communication_profiles (
			workspace_id, tone, address_style, detail_level, structure_preference,
			frustration_sensitivity, known_preferences_json
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (workspace_id) DO UPDATE SET
			tone=EXCLUDED.tone,
			address_style=EXCLUDED.address_style,
			detail_level=EXCLUDED.detail_level,
			structure_preference=EXCLUDED.structure_preference,
			frustration_sensitivity=EXCLUDED.frustration_sensitivity,
			known_preferences_json=EXCLUDED.known_preferences_json,
			updated_at=NOW()
		RETURNING id, workspace_id, tone, address_style, detail_level, structure_preference,
			frustration_sensitivity, known_preferences_json, updated_at
	`,
		workspaceID,
		defaultString(profile.Tone, DefaultCommunicationTone),
		defaultString(profile.AddressStyle, DefaultAddressStyle),
		defaultString(profile.DetailLevel, DefaultDetailLevel),
		defaultString(profile.StructurePreference, DefaultStructurePreference),
		defaultString(profile.FrustrationSensitivity, DefaultFrustrationSensitivity),
		defaultRawJSON(profile.KnownPreferences),
	).Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.Tone,
		&item.AddressStyle,
		&item.DetailLevel,
		&item.StructurePreference,
		&item.FrustrationSensitivity,
		&item.KnownPreferences,
		&item.UpdatedAt,
	)
	return item, err
}

func (s *Store) ListAgenda(ctx context.Context, workspaceID int, limit int) ([]ResearchAgendaItem, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, workspace_id, topic_key, question_goal, why_it_matters, status,
			priority, linked_claim_ids_json, last_asked_at, times_asked, created_at, updated_at
		FROM strategic_research_agenda_items
		WHERE workspace_id=$1
		ORDER BY
			CASE priority WHEN 'critical' THEN 1 WHEN 'high' THEN 2 WHEN 'medium' THEN 3 ELSE 4 END,
			updated_at DESC,
			id DESC
		LIMIT $2
	`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []ResearchAgendaItem{}
	for rows.Next() {
		var item ResearchAgendaItem
		if err := rows.Scan(
			&item.ID,
			&item.WorkspaceID,
			&item.TopicKey,
			&item.QuestionGoal,
			&item.WhyItMatters,
			&item.Status,
			&item.Priority,
			&item.LinkedClaimIDs,
			&item.LastAskedAt,
			&item.TimesAsked,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpsertAgenda(ctx context.Context, workspaceID int, items []ResearchAgendaItem) (int, error) {
	updated := 0
	for _, item := range items {
		goal := cleanText(item.QuestionGoal)
		if goal == "" {
			continue
		}
		_, err := s.dbx.ExecContext(ctx, `
			INSERT INTO strategic_research_agenda_items (
				workspace_id, topic_key, question_goal, why_it_matters, status, priority, linked_claim_ids_json
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (workspace_id, topic_key, question_goal) DO UPDATE SET
				why_it_matters=EXCLUDED.why_it_matters,
				status=EXCLUDED.status,
				priority=EXCLUDED.priority,
				updated_at=NOW()
		`,
			workspaceID,
			normalizeTopicKey(item.TopicKey),
			goal,
			cleanText(item.WhyItMatters),
			normalizeAgendaStatus(item.Status),
			normalizePriority(item.Priority),
			defaultRawJSON(item.LinkedClaimIDs),
		)
		if err != nil {
			return updated, err
		}
		updated += 1
	}
	return updated, nil
}

func (s *Store) ListDocuments(ctx context.Context, workspaceID int) ([]StrategicDocument, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, workspace_id, document_type, title, markdown, source_claim_ids_json,
			status, version, generated_at
		FROM strategic_documents
		WHERE workspace_id=$1
		ORDER BY document_type ASC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []StrategicDocument{}
	for rows.Next() {
		var item StrategicDocument
		if err := rows.Scan(
			&item.ID,
			&item.WorkspaceID,
			&item.DocumentType,
			&item.Title,
			&item.Markdown,
			&item.SourceClaimIDs,
			&item.Status,
			&item.Version,
			&item.GeneratedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) LatestQualityReport(ctx context.Context, workspaceID int) (*QualityReport, error) {
	var id int
	var storedWorkspaceID int
	var readinessScore int
	var readinessStatus string
	var changedRaw json.RawMessage
	var reportRaw json.RawMessage
	var createdAt sql.NullTime
	err := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, readiness_score, readiness_status,
			changed_document_types_json, report_json, created_at
		FROM strategic_quality_reports
		WHERE workspace_id=$1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, workspaceID).Scan(
		&id,
		&storedWorkspaceID,
		&readinessScore,
		&readinessStatus,
		&changedRaw,
		&reportRaw,
		&createdAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return qualityReportFromStored(id, storedWorkspaceID, readinessScore, readinessStatus, changedRaw, reportRaw, createdAt), nil
}

func (s *Store) SaveQualityReport(ctx context.Context, workspaceID int, report QualityReport) (QualityReport, error) {
	readinessScore := clampScore(report.ReadinessScore)
	if readinessScore == 0 {
		readinessScore = clampScore(report.Overall.ReadinessScore)
	}
	readinessStatus := normalizeReadinessStatus(defaultString(report.ReadinessStatus, report.Overall.ReadinessStatus))
	report.ReadinessScore = readinessScore
	report.ReadinessStatus = readinessStatus
	report.Overall.ReadinessScore = readinessScore
	report.Overall.ReadinessStatus = readinessStatus

	payload := map[string]any{
		"overall":       report.Overall,
		"documents":     report.Documents,
		"chat_guidance": report.ChatGuidance,
	}

	var id int
	var storedWorkspaceID int
	var storedScore int
	var storedStatus string
	var changedRaw json.RawMessage
	var reportRaw json.RawMessage
	var createdAt sql.NullTime
	err := s.dbx.QueryRowContext(ctx, `
		INSERT INTO strategic_quality_reports (
			workspace_id, readiness_score, readiness_status,
			changed_document_types_json, report_json
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, workspace_id, readiness_score, readiness_status,
			changed_document_types_json, report_json, created_at
	`,
		workspaceID,
		readinessScore,
		readinessStatus,
		mustJSON(report.ChangedDocumentTypes),
		mustJSON(payload),
	).Scan(
		&id,
		&storedWorkspaceID,
		&storedScore,
		&storedStatus,
		&changedRaw,
		&reportRaw,
		&createdAt,
	)
	if err != nil {
		return QualityReport{}, err
	}
	stored := qualityReportFromStored(id, storedWorkspaceID, storedScore, storedStatus, changedRaw, reportRaw, createdAt)
	if stored == nil {
		return QualityReport{}, fmt.Errorf("quality_report_decode_failed")
	}
	return *stored, nil
}

func (s *Store) ListFiles(ctx context.Context, workspaceID int) ([]StrategicFile, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, workspace_id, raw_source_id, openai_file_id, vector_store_id,
			filename, content_type, size_bytes, status, error, created_at, updated_at
		FROM strategic_openai_files
		WHERE workspace_id=$1
		ORDER BY created_at DESC, id DESC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []StrategicFile{}
	for rows.Next() {
		var item StrategicFile
		var rawSourceID sql.NullInt64
		if err := rows.Scan(
			&item.ID,
			&item.WorkspaceID,
			&rawSourceID,
			&item.OpenAIFileID,
			&item.VectorStoreID,
			&item.Filename,
			&item.ContentType,
			&item.SizeBytes,
			&item.Status,
			&item.Error,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if rawSourceID.Valid {
			value := int(rawSourceID.Int64)
			item.RawSourceID = &value
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpsertDocuments(ctx context.Context, workspaceID int, docs []StrategicDocument) (int, error) {
	updated := 0
	for _, doc := range docs {
		docType := normalizeDocumentType(doc.DocumentType)
		if docType == "" || strings.TrimSpace(doc.Markdown) == "" {
			continue
		}
		_, err := s.dbx.ExecContext(ctx, `
			INSERT INTO strategic_documents (
				workspace_id, document_type, title, markdown, source_claim_ids_json, status, version
			)
			VALUES ($1, $2, $3, $4, $5, $6, 1)
			ON CONFLICT (workspace_id, document_type) DO UPDATE SET
				title=EXCLUDED.title,
				markdown=EXCLUDED.markdown,
				source_claim_ids_json=EXCLUDED.source_claim_ids_json,
				status=EXCLUDED.status,
				version=strategic_documents.version + 1,
				generated_at=NOW()
		`,
			workspaceID,
			docType,
			defaultString(doc.Title, documentTitle(docType)),
			strings.TrimSpace(doc.Markdown),
			defaultRawJSON(doc.SourceClaimIDs),
			defaultString(doc.Status, DefaultStrategicDocumentStatus),
		)
		if err != nil {
			return updated, err
		}
		updated += 1
	}
	return updated, nil
}

func (s *Store) Reset(ctx context.Context, workspaceID int) error {
	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tables := []string{
		"strategic_ai_runs",
		"strategic_quality_reports",
		"strategic_documents",
		"strategic_dialogue_focus",
		"strategic_research_agenda_items",
		"strategic_memory_snapshots",
		"strategic_claims",
		"strategic_communication_profiles",
		"strategic_openai_files",
		"strategic_raw_sources",
	}
	for _, table := range tables {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE workspace_id=$1", workspaceID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE strategic_openai_sessions
		SET previous_response_id='', vector_store_id='', updated_at=NOW()
		WHERE workspace_id=$1
	`, workspaceID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) LogAIRun(ctx context.Context, workspaceID int, scenario string, model string, promptVersion string, durationMs int64, status string, errorText string) {
	_, _ = s.dbx.ExecContext(ctx, `
		INSERT INTO strategic_ai_runs (workspace_id, scenario, model, prompt_version, duration_ms, status, error)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, workspaceID, scenario, model, promptVersion, durationMs, status, errorText)
}

func (s *Store) LogAIRunWithUsage(ctx context.Context, workspaceID int, scenario string, model string, promptVersion string, durationMs int64, inputTokens int, outputTokens int, status string, errorText string) {
	_, _ = s.dbx.ExecContext(ctx, `
		INSERT INTO strategic_ai_runs (
			workspace_id, scenario, model, prompt_version, input_tokens,
			output_tokens, duration_ms, status, error
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, workspaceID, scenario, model, promptVersion, inputTokens, outputTokens, durationMs, status, errorText)
}

func mustJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func defaultRawJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 || string(value) == "null" {
		return json.RawMessage(`[]`)
	}
	return value
}

type storedQualityReportPayload struct {
	Overall      QualityOverallAssessment    `json:"overall"`
	Documents    []QualityDocumentAssessment `json:"documents"`
	ChatGuidance QualityChatGuidance         `json:"chat_guidance"`
}

func qualityReportFromStored(
	id int,
	workspaceID int,
	readinessScore int,
	readinessStatus string,
	changedRaw json.RawMessage,
	reportRaw json.RawMessage,
	createdAt sql.NullTime,
) *QualityReport {
	var changed []string
	_ = json.Unmarshal(defaultRawJSON(changedRaw), &changed)

	var payload storedQualityReportPayload
	_ = json.Unmarshal(reportRaw, &payload)

	report := &QualityReport{
		ID:                   id,
		WorkspaceID:          workspaceID,
		ReadinessScore:       clampScore(readinessScore),
		ReadinessStatus:      normalizeReadinessStatus(readinessStatus),
		ChangedDocumentTypes: changed,
		Overall:              payload.Overall,
		Documents:            payload.Documents,
		ChatGuidance:         payload.ChatGuidance,
	}
	if createdAt.Valid {
		report.CreatedAt = createdAt.Time
	}
	if report.Overall.ReadinessScore == 0 {
		report.Overall.ReadinessScore = report.ReadinessScore
	}
	if report.Overall.ReadinessStatus == "" {
		report.Overall.ReadinessStatus = report.ReadinessStatus
	}
	return report
}

func reverseMessages(items []ConversationMessage) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func searchTerms(value string, limit int) []string {
	stop := map[string]bool{
		"что": true, "как": true, "это": true, "или": true, "для": true, "про": true,
		"там": true, "тут": true, "уже": true, "пока": true, "если": true, "надо": true,
		"нужно": true, "есть": true, "нет": true, "мне": true, "тебе": true, "меня": true,
		"your": true, "with": true, "from": true, "this": true, "that": true,
	}
	seen := map[string]bool{}
	result := []string{}
	for _, raw := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		word := strings.TrimSpace(raw)
		if len([]rune(word)) < 4 || stop[word] || seen[word] {
			continue
		}
		seen[word] = true
		result = append(result, word)
		if len(result) >= limit {
			break
		}
	}
	return result
}

func claimKey(value string) string {
	hash := sha1.Sum([]byte(normalizeForKey(value)))
	return hex.EncodeToString(hash[:])
}

func normalizeForKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "ё", "е")
	return strings.Join(strings.Fields(value), " ")
}
