package strategy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/v2/strategicmemory"
)

const (
	strategySynthesisMaxOutputTokens = 18000
	strategySynthesisTimeout         = 4 * time.Minute
)

var synthesisURLPattern = regexp.MustCompile(`https?://[^\s<>()\[\]{}"']+`)

type SynthesisService struct {
	store            *Store
	memoryStore      *strategicmemory.Store
	ai               *ai.OpenAIClient
	compactThreshold int
}

func NewSynthesisService(dbx *sql.DB, aiClient *ai.OpenAIClient, compactThreshold int) *SynthesisService {
	if compactThreshold <= 0 {
		compactThreshold = 120000
	}
	return &SynthesisService{
		store:            NewStore(dbx),
		memoryStore:      strategicmemory.NewStore(dbx),
		ai:               aiClient,
		compactThreshold: compactThreshold,
	}
}

func (s *SynthesisService) Start(ctx context.Context, workspaceID int, userID int) (StrategySynthesisResponse, error) {
	state, err := s.store.SessionState(ctx, workspaceID)
	if err != nil {
		return StrategySynthesisResponse{}, err
	}
	readiness, err := s.store.LatestCompletedReadinessAudit(ctx, workspaceID)
	if err != nil {
		return StrategySynthesisResponse{}, err
	}
	if readiness == nil || readiness.SessionRevision != state.Revision || !readiness.CanSynthesize {
		return StrategySynthesisResponse{}, fmt.Errorf("strategy_synthesis_not_ready")
	}
	messages, err := s.store.ChatMessages(ctx, workspaceID, 1)
	if err != nil {
		return StrategySynthesisResponse{}, err
	}
	if len(messages) == 0 {
		return StrategySynthesisResponse{}, fmt.Errorf("strategy_synthesis_no_session")
	}
	throughMessageID := state.LastUserMessageID
	if throughMessageID <= 0 {
		throughMessageID = messages[len(messages)-1].ID
	}
	return s.StartForRevision(ctx, workspaceID, userID, state.Revision, throughMessageID)
}

func (s *SynthesisService) StartForRevision(
	ctx context.Context,
	workspaceID int,
	userID int,
	sessionRevision int,
	throughMessageID int,
) (StrategySynthesisResponse, error) {
	strategy, _, _, err := s.store.Current(ctx, workspaceID, userID)
	if err != nil {
		return StrategySynthesisResponse{}, err
	}
	if throughMessageID <= 0 {
		return StrategySynthesisResponse{}, fmt.Errorf("strategy_synthesis_no_session")
	}
	state, err := s.store.SessionState(ctx, workspaceID)
	if err != nil {
		return StrategySynthesisResponse{}, err
	}
	if state.Revision != sessionRevision || state.LastUserMessageID != throughMessageID {
		return StrategySynthesisResponse{}, fmt.Errorf("strategy_synthesis_stale_revision")
	}

	run, created, err := s.store.CreateSynthesisRun(ctx, workspaceID, strategy.ID, userID, sessionRevision, throughMessageID, s.ai.Model)
	if err != nil {
		return StrategySynthesisResponse{}, err
	}
	if created {
		go s.executeDetached(workspaceID, run.ID, strategy)
	}
	return StrategySynthesisResponse{Run: &run, Documents: []StrategySynthesisDocument{}}, nil
}

func (s *SynthesisService) Latest(ctx context.Context, workspaceID int) (StrategySynthesisResponse, error) {
	return s.store.LatestSynthesis(ctx, workspaceID)
}

func (s *SynthesisService) executeDetached(workspaceID int, runID int, strategy Strategy) {
	ctx, cancel := context.WithTimeout(context.Background(), strategySynthesisTimeout)
	defer cancel()
	started := time.Now()
	failRun := func(errorText string) {
		failureCtx, failureCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer failureCancel()
		_ = s.store.FailSynthesisRun(failureCtx, workspaceID, runID, time.Since(started).Milliseconds(), errorText)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			failRun(fmt.Sprintf("strategy synthesis panic: %v", recovered))
		}
	}()
	if err := s.execute(ctx, workspaceID, runID, strategy); err != nil {
		failRun(err.Error())
	}
}

func (s *SynthesisService) execute(ctx context.Context, workspaceID int, runID int, strategy Strategy) error {
	if err := s.store.MarkSynthesisRunRunning(ctx, workspaceID, runID); err != nil {
		return err
	}

	documents, err := s.memoryStore.ListDocuments(ctx, workspaceID)
	if err != nil {
		return err
	}
	qualityReport, err := s.memoryStore.LatestQualityReport(ctx, workspaceID)
	if err != nil {
		return err
	}
	files, err := s.memoryStore.ListFiles(ctx, workspaceID)
	if err != nil {
		return err
	}
	messages, err := s.store.ChatMessages(ctx, workspaceID, 500)
	if err != nil {
		return err
	}
	runState, err := s.store.SynthesisRun(ctx, workspaceID, runID)
	if err != nil {
		return err
	}
	messages = messagesThroughID(messages, runState.ThroughMessageID)
	session, err := s.memoryStore.OpenAISession(ctx, workspaceID, s.compactThreshold)
	if err != nil {
		return err
	}

	catalog, sourceIndex := buildSynthesisSourceCatalog(documents, messages, files)
	input := buildStrategySynthesisInput(strategy, documents, qualityReport, messages, files, catalog)
	rawInput, err := json.Marshal(input)
	if err != nil {
		return err
	}

	started := time.Now()
	result, err := s.ai.GenerateJSONNative(ctx, strategySynthesizerPrompt, string(rawInput), ai.ResponseContextOptions{
		VectorStoreIDs:       synthesisVectorStoreIDs(session),
		CompactThreshold:     session.CompactThreshold,
		PromptCacheKey:       fmt.Sprintf("reupgoals-strategy-synthesizer-workspace-%d-v1", workspaceID),
		MaxFileSearchResults: 20,
		MaxOutputTokens:      strategySynthesisMaxOutputTokens,
		RequestTimeout:       strategySynthesisTimeout - 15*time.Second,
	})
	duration := time.Since(started).Milliseconds()
	if err != nil {
		s.memoryStore.LogAIRunWithUsage(ctx, workspaceID, "strategy_synthesizer", s.ai.Model, StrategySynthesizerPromptVersion, duration, 0, 0, "failed", err.Error())
		return err
	}

	var output strategySynthesisModelOutput
	if err := json.Unmarshal([]byte(result.Text), &output); err != nil {
		s.memoryStore.LogAIRunWithUsage(ctx, workspaceID, "strategy_synthesizer", s.ai.Model, StrategySynthesizerPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "failed", err.Error())
		return fmt.Errorf("strategy synthesis json decode error: %w", err)
	}
	if len(output.Documents) == 0 {
		return fmt.Errorf("strategy synthesis returned no documents")
	}

	normalized := normalizeSynthesisOutput(workspaceID, runID, output, sourceIndex)
	if err := s.store.CompleteSynthesisRun(
		ctx,
		workspaceID,
		runID,
		output,
		normalized,
		result.ResponseID,
		result.Usage.InputTokens,
		result.Usage.OutputTokens,
		duration,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_ = s.store.SupersedeSynthesisRun(ctx, workspaceID, runID)
			return nil
		}
		return err
	}
	s.memoryStore.LogAIRunWithUsage(ctx, workspaceID, "strategy_synthesizer", s.ai.Model, StrategySynthesizerPromptVersion, duration, result.Usage.InputTokens, result.Usage.OutputTokens, "success", "")
	return nil
}

func buildStrategySynthesisInput(
	strategy Strategy,
	documents []strategicmemory.StrategicDocument,
	qualityReport *strategicmemory.QualityReport,
	messages []StrategyChatMessage,
	files []strategicmemory.StrategicFile,
	catalog []strategySynthesisSourceCatalogItem,
) map[string]any {
	knowledgeDocuments := make([]map[string]any, 0, len(documents))
	for _, document := range documents {
		knowledgeDocuments = append(knowledgeDocuments, map[string]any{
			"source_key":    synthesisKnowledgeDocumentKey(document.ID),
			"document_id":   document.ID,
			"document_type": document.DocumentType,
			"title":         document.Title,
			"status":        document.Status,
			"version":       document.Version,
			"content":       document.Markdown,
		})
	}

	transcript := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		transcript = append(transcript, map[string]any{
			"source_key": synthesisStrategyMessageKey(message.ID),
			"message_id": message.ID,
			"role":       message.Role,
			"content":    message.Content,
			"created_at": message.CreatedAt,
		})
	}

	uploadedFiles := make([]map[string]any, 0, len(files))
	for _, file := range files {
		uploadedFiles = append(uploadedFiles, map[string]any{
			"source_key":   synthesisUploadedFileKey(file.ID),
			"file_id":      file.ID,
			"filename":     file.Filename,
			"content_type": file.ContentType,
			"size_bytes":   file.SizeBytes,
			"status":       file.Status,
			"created_at":   file.CreatedAt,
		})
	}

	return map[string]any{
		"strategy": map[string]any{
			"id":      strategy.ID,
			"version": strategy.Version,
			"title":   strategy.Title,
			"status":  strategy.Status,
		},
		"requested_document_catalog":  synthesisDocumentCatalogJSON(),
		"knowledge_base_documents":    knowledgeDocuments,
		"knowledge_base_quality":      qualityReport,
		"strategy_session_transcript": transcript,
		"uploaded_files":              uploadedFiles,
		"source_catalog":              catalog,
		"output_stage":                "semantic_synthesis_only; visual design will be performed by a separate system",
	}
}

func buildSynthesisSourceCatalog(
	documents []strategicmemory.StrategicDocument,
	messages []StrategyChatMessage,
	files []strategicmemory.StrategicFile,
) ([]strategySynthesisSourceCatalogItem, map[string]strategySynthesisSourceCatalogItem) {
	items := make([]strategySynthesisSourceCatalogItem, 0, len(documents)+len(messages)+len(files))
	seen := map[string]bool{}
	add := func(item strategySynthesisSourceCatalogItem) {
		if strings.TrimSpace(item.Key) == "" || seen[item.Key] {
			return
		}
		seen[item.Key] = true
		items = append(items, item)
	}
	textsForURLs := make([]string, 0, len(documents)+len(messages))

	for _, document := range documents {
		add(strategySynthesisSourceCatalogItem{
			Key:        synthesisKnowledgeDocumentKey(document.ID),
			SourceType: "knowledge_document",
			SourceID:   strconv.Itoa(document.ID),
			Label:      document.Title,
			Href:       "/knowledge-base?document=" + url.QueryEscape(document.DocumentType),
		})
		textsForURLs = append(textsForURLs, document.Markdown)
	}
	for _, message := range messages {
		roleLabel := "Сообщение участника стратегической сессии"
		if message.Role == "assistant" {
			roleLabel = "Сообщение фасилитатора"
		}
		add(strategySynthesisSourceCatalogItem{
			Key:        synthesisStrategyMessageKey(message.ID),
			SourceType: "strategy_message",
			SourceID:   strconv.Itoa(message.ID),
			Label:      fmt.Sprintf("%s от %s", roleLabel, message.CreatedAt.Format("02.01.2006 15:04")),
			Href:       fmt.Sprintf("/strategy?message=%d", message.ID),
		})
		if message.Role == "user" {
			textsForURLs = append(textsForURLs, message.Content)
		}
	}
	for _, file := range files {
		add(strategySynthesisSourceCatalogItem{
			Key:        synthesisUploadedFileKey(file.ID),
			SourceType: "uploaded_file",
			SourceID:   strconv.Itoa(file.ID),
			Label:      file.Filename,
			Href:       fmt.Sprintf("/knowledge-base?file=%d", file.ID),
		})
	}
	for _, externalURL := range extractSynthesisURLs(textsForURLs) {
		add(strategySynthesisSourceCatalogItem{
			Key:        synthesisExternalLinkKey(externalURL),
			SourceType: "external_link",
			SourceID:   externalURL,
			Label:      externalURL,
			Href:       externalURL,
		})
	}

	index := make(map[string]strategySynthesisSourceCatalogItem, len(items))
	for _, item := range items {
		index[item.Key] = item
	}
	return items, index
}

func normalizeSynthesisOutput(
	workspaceID int,
	runID int,
	output strategySynthesisModelOutput,
	sourceIndex map[string]strategySynthesisSourceCatalogItem,
) []StrategySynthesisDocument {
	byType := map[string]strategySynthesisModelDocument{}
	for _, document := range output.Documents {
		docType := strings.TrimSpace(strings.ToLower(document.DocumentType))
		if _, exists := byType[docType]; !exists {
			byType[docType] = document
		}
	}

	result := make([]StrategySynthesisDocument, 0, len(strategySynthesisDocumentDefinitions))
	for _, definition := range strategySynthesisDocumentDefinitions {
		modelDocument, exists := byType[definition.Type]
		status := SynthesisDocumentInsufficientData
		title := definition.Title
		blocks := []StrategySynthesisContentBlock{}
		refs := []StrategySynthesisSourceRef{}
		seenRefs := map[string]bool{}

		if exists {
			title = strings.TrimSpace(modelDocument.Title)
			if title == "" {
				title = definition.Title
			}
			status = normalizeSynthesisDocumentStatus(modelDocument.Status)
			for _, rawBlock := range modelDocument.ContentBlocks {
				text := strings.TrimSpace(rawBlock.Text)
				if text == "" {
					continue
				}
				block := StrategySynthesisContentBlock{
					Text:       text,
					SourceKeys: []string{},
					SourceNote: strings.TrimSpace(rawBlock.SourceNote),
				}
				for _, sourceKey := range rawBlock.SourceKeys {
					sourceKey = strings.TrimSpace(sourceKey)
					catalogItem, ok := sourceIndex[sourceKey]
					if !ok || containsString(block.SourceKeys, sourceKey) {
						continue
					}
					block.SourceKeys = append(block.SourceKeys, sourceKey)
					if !seenRefs[sourceKey] {
						seenRefs[sourceKey] = true
						refs = append(refs, StrategySynthesisSourceRef{
							Key:        catalogItem.Key,
							SourceType: catalogItem.SourceType,
							SourceID:   catalogItem.SourceID,
							Label:      catalogItem.Label,
							Href:       catalogItem.Href,
							Supports:   block.SourceNote,
						})
					}
				}
				blocks = append(blocks, block)
			}
		}
		if len(blocks) == 0 && status == SynthesisDocumentFilled {
			status = SynthesisDocumentInsufficientData
		}
		result = append(result, StrategySynthesisDocument{
			RunID:         runID,
			WorkspaceID:   workspaceID,
			DocumentType:  definition.Type,
			Title:         title,
			Status:        status,
			ContentBlocks: blocks,
			SourceRefs:    refs,
			SortOrder:     definition.SortOrder,
		})
	}
	return result
}

func normalizeSynthesisDocumentStatus(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case SynthesisDocumentFilled:
		return SynthesisDocumentFilled
	case SynthesisDocumentNotApplicable:
		return SynthesisDocumentNotApplicable
	default:
		return SynthesisDocumentInsufficientData
	}
}

func synthesisVectorStoreIDs(session strategicmemory.OpenAISession) []string {
	if strings.TrimSpace(session.VectorStoreID) == "" {
		return nil
	}
	return []string{strings.TrimSpace(session.VectorStoreID)}
}

func synthesisKnowledgeDocumentKey(id int) string {
	return fmt.Sprintf("knowledge_document:%d", id)
}

func synthesisStrategyMessageKey(id int) string {
	return fmt.Sprintf("strategy_message:%d", id)
}

func synthesisUploadedFileKey(id int) string {
	return fmt.Sprintf("uploaded_file:%d", id)
}

func synthesisExternalLinkKey(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "external_link:" + hex.EncodeToString(sum[:8])
}

func extractSynthesisURLs(texts []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, text := range texts {
		for _, match := range synthesisURLPattern.FindAllString(text, -1) {
			match = strings.TrimRight(strings.TrimSpace(match), ".,;:!?)]}")
			parsed, err := url.Parse(match)
			if err != nil || parsed.Scheme == "" || parsed.Host == "" || seen[match] {
				continue
			}
			seen[match] = true
			result = append(result, match)
		}
	}
	return result
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
