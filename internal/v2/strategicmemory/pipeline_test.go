package strategicmemory

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPipelineCanStartCandidate(t *testing.T) {
	base := KnowledgePipelineState{
		Status: KnowledgePipelineCollecting, ConversationRevision: 4,
		LastUserSourceID: 20, LastAuditedSourceID: 10,
	}
	if !pipelineCanStartCandidate(base, 4, 20) {
		t.Fatal("current unaudited revision should start a candidate")
	}

	for _, status := range []string{
		KnowledgePipelineReady, KnowledgePipelineAuditCandidate, KnowledgePipelineExtracting,
		KnowledgePipelineReviewing, KnowledgePipelineCompiling,
	} {
		state := base
		state.Status = status
		if pipelineCanStartCandidate(state, 4, 20) {
			t.Fatalf("status %q must not start another candidate", status)
		}
	}

	state := base
	state.LastAuditedSourceID = 20
	if pipelineCanStartCandidate(state, 4, 20) {
		t.Fatal("an already audited source must not start another candidate")
	}
	if pipelineCanStartCandidate(base, 3, 20) || pipelineCanStartCandidate(base, 4, 19) {
		t.Fatal("stale revisions and source ids must be rejected")
	}
}

func TestChunkKnowledgeSourcesPreservesOrderAndOversizedSource(t *testing.T) {
	sources := []RawSource{
		{ID: 1, Content: "1234"},
		{ID: 2, Content: "5678"},
		{ID: 3, Content: strings.Repeat("x", 12)},
		{ID: 4, Content: "90"},
	}
	chunks := chunkKnowledgeSources(sources, 7)
	if len(chunks) != 4 {
		t.Fatalf("expected four chunks, got %d", len(chunks))
	}
	var ids []int
	for _, chunk := range chunks {
		for _, source := range chunk {
			ids = append(ids, source.ID)
		}
	}
	if !reflect.DeepEqual(ids, []int{1, 2, 3, 4}) {
		t.Fatalf("source order changed: %v", ids)
	}
}

func TestChunkKnowledgeSourcesSeparatesDeferredPolicies(t *testing.T) {
	factsOnly, _ := json.Marshal(map[string]any{"facts_only": true})
	financeDocument, _ := json.Marshal(map[string]any{
		"preferred_document_type": "finance_economics",
	})
	sources := []RawSource{
		{ID: 1, SourceType: SourceTypeUserMessage, Content: "base"},
		{ID: 2, SourceType: SourceTypeStrategyMessage, Content: "strategy fact", Metadata: factsOnly},
		{ID: 3, SourceType: SourceTypeStrategyMessage, Content: "another fact", Metadata: factsOnly},
		{ID: 4, SourceType: SourceTypeDocumentMessage, Content: "finance correction", Metadata: financeDocument},
	}

	chunks := chunkKnowledgeSources(sources, 1000)
	if len(chunks) != 3 {
		t.Fatalf("expected policy-isolated chunks, got %#v", chunks)
	}
	if got := knowledgeSourceChunkPolicy(chunks[1]); !got.FactsOnly || got.PreferredDocumentType != "" {
		t.Fatalf("unexpected strategy policy: %#v", got)
	}
	if got := knowledgeSourceChunkPolicy(chunks[2]); got.FactsOnly || got.PreferredDocumentType != "finance_economics" {
		t.Fatalf("unexpected document policy: %#v", got)
	}
}

func TestCompilationSourcesTreatsInterviewSourcesAsUserEvidence(t *testing.T) {
	got := compilationSources([]RawSource{
		{ID: 1, SourceType: SourceTypeStrategyMessage},
		{ID: 2, SourceType: SourceTypeDocumentMessage},
	})
	if len(got) != 2 || got[0].Role != "user" || got[1].Role != "user" {
		t.Fatalf("interview sources must remain user evidence: %#v", got)
	}
}

func TestCompilationSourcesClassifiesProductInputs(t *testing.T) {
	got := compilationSources([]RawSource{
		{ID: 1, SourceType: SourceTypeTacticsMessage},
		{ID: 2, SourceType: SourceTypeTaskDiscussion},
		{ID: 3, SourceType: SourceTypeWorkspaceDoc},
		{ID: 4, SourceType: SourceTypeTaskCompletion},
		{ID: 5, SourceType: SourceTypeDepartment},
		{ID: 6, SourceType: SourceTypeTacticalPlan},
		{ID: 7, SourceType: SourceTypeWorkstream},
		{ID: 8, SourceType: SourceTypeProject},
		{ID: 9, SourceType: SourceTypeRisk},
		{ID: 10, SourceType: SourceTypeOpportunity},
		{ID: 11, SourceType: SourceTypeHypothesis},
		{ID: 12, SourceType: SourceTypeResearchResult},
	})
	for index, source := range got {
		want := "business_record"
		if index < 2 {
			want = "user"
		}
		if source.Role != want {
			t.Fatalf("source %d has role %q, want %q", source.SourceID, source.Role, want)
		}
	}
}

func TestIndexedCompilationSourcesAvoidDuplicatingLargeRecords(t *testing.T) {
	sources := []RawSource{
		{ID: 1, SourceType: SourceTypeWorkspaceDoc, Content: strings.Repeat("document", 1000)},
		{ID: 2, SourceType: SourceTypeStrategyMessage, Content: "New user fact"},
	}
	got := compilationSourcesForIndexedContext(sources)
	if strings.Contains(got[0].Content, "documentdocument") {
		t.Fatal("a structured record already available via file_search was duplicated in the prompt")
	}
	if got[1].Content != sources[1].Content {
		t.Fatal("conversation input not yet indexed must remain in the extraction request")
	}
}

func TestDeferredSourceTypesCoverBusinessInputs(t *testing.T) {
	for _, sourceType := range []string{
		SourceTypeStrategyMessage, SourceTypeDocumentMessage, SourceTypeTacticsMessage,
		SourceTypeTaskDiscussion, SourceTypeWorkspaceDoc, SourceTypeTaskCompletion,
		SourceTypeDepartment, SourceTypeTacticalPlan, SourceTypeWorkstream,
		SourceTypeProject, SourceTypeRisk, SourceTypeOpportunity, SourceTypeHypothesis, SourceTypeResearchResult,
	} {
		if !isDeferredSourceType(sourceType) {
			t.Fatalf("source type %q is not accepted by the recorder", sourceType)
		}
	}
	if isDeferredSourceType(SourceTypeAssistantMessage) {
		t.Fatal("assistant messages must not enter the deferred business-input pipeline")
	}
}

func TestKnowledgePipelineBusy(t *testing.T) {
	for _, status := range []string{
		KnowledgePipelineAuditCandidate, KnowledgePipelineExtracting,
		KnowledgePipelineReviewing, KnowledgePipelineCompiling,
	} {
		if !knowledgePipelineBusy(status) {
			t.Fatalf("status %q should postpone background context extraction", status)
		}
	}
	for _, status := range []string{
		KnowledgePipelineCollecting, KnowledgePipelineNeedsMoreContext, KnowledgePipelineReady,
	} {
		if knowledgePipelineBusy(status) {
			t.Fatalf("status %q should allow background context extraction", status)
		}
	}
}

func TestValidClaimSourceIDsFiltersForeignAndDuplicateIDs(t *testing.T) {
	valid := map[int]bool{2: true, 3: true, 7: true}
	got := validClaimSourceIDs([]int{3, 999, 3, 2}, 7, valid)
	if !reflect.DeepEqual(got, []int{3, 2}) {
		t.Fatalf("unexpected source ids: %v", got)
	}
	got = validClaimSourceIDs([]int{999}, 7, valid)
	if !reflect.DeepEqual(got, []int{7}) {
		t.Fatalf("expected valid fallback source, got %v", got)
	}
}

func TestValidDocumentClaimIDsFiltersUnknownIDs(t *testing.T) {
	got := validDocumentClaimIDs([]int{5, 2, 5, 99}, map[int]bool{2: true, 5: true})
	if !reflect.DeepEqual(got, []int{5, 2}) {
		t.Fatalf("unexpected claim ids: %v", got)
	}
}

func TestPipelineConversationState(t *testing.T) {
	processing := []string{
		KnowledgePipelineAuditCandidate, KnowledgePipelineExtracting,
		KnowledgePipelineReviewing, KnowledgePipelineCompiling,
	}
	for _, status := range processing {
		if got := pipelineConversationState(KnowledgePipelineState{Status: status}); got != ConversationStateProcessingContext {
			t.Fatalf("status %q mapped to %q", status, got)
		}
	}
	if got := pipelineConversationState(KnowledgePipelineState{Status: KnowledgePipelineReady}); got != ConversationStateAwaitingConfirmation {
		t.Fatalf("unconfirmed ready state mapped to %q", got)
	}
	now := time.Now()
	if got := pipelineConversationState(KnowledgePipelineState{Status: KnowledgePipelineReady, OnboardingConfirmedAt: &now}); got != ConversationStateReadyForStrategy {
		t.Fatalf("confirmed ready state mapped to %q", got)
	}
	if got := pipelineConversationState(KnowledgePipelineState{Status: KnowledgePipelineNeedsMoreContext}); got != ConversationStateCollectingContext {
		t.Fatalf("needs-more-context mapped to %q", got)
	}
}

func TestAuditorTurnOutputContract(t *testing.T) {
	var turn auditorTurnOutput
	err := json.Unmarshal([]byte(`{"reply":"Продолжим【】.","context_ready":true,"readiness_reason":"baseline covered"}`), &turn)
	ready, reason := turn.contextReadinessDecision()
	if err != nil || turn.Reply == "" || !ready || reason != "baseline covered" {
		t.Fatalf("structured turn contract failed: %+v, %v", turn, err)
	}
	if got := cleanAssistantMessage(turn.Reply); got != "Продолжим." {
		t.Fatalf("citation marker was not removed: %q", got)
	}
}

func TestAuditorTurnOutputAcceptsLegacyReadinessContract(t *testing.T) {
	var turn auditorTurnOutput
	err := json.Unmarshal([]byte(`{"reply":"Контекст собран.","audit_candidate":true,"candidate_reason":"legacy conversation"}`), &turn)
	ready, reason := turn.contextReadinessDecision()
	if err != nil || !ready || reason != "legacy conversation" {
		t.Fatalf("legacy structured turn contract failed: %+v, %v", turn, err)
	}
}

func TestParseAuditorTurnRejectsSerializedReply(t *testing.T) {
	_, err := parseAuditorTurn(`{
		"reply":"{\"next_question\":\"Кто клиент?\"}",
		"audit_candidate":false,
		"candidate_reason":""
	}`)
	if err == nil {
		t.Fatal("serialized JSON must not reach the knowledge chat")
	}
}

func TestParseAuditorTurnUnwrapsNestedContractFromVisibleReply(t *testing.T) {
	nestedReply := "**Советник**\n**Копировать**\n```json\n{\n\"reply\":\"Ответьте по пунктам.\",\n\"context_ready\":false,\n\"readiness_reason\":\"\"\n}\n```"
	raw, err := json.Marshal(map[string]any{
		"reply":            nestedReply,
		"context_ready":    false,
		"readiness_reason": "",
	})
	if err != nil {
		t.Fatal(err)
	}

	turn, err := parseAuditorTurn(string(raw))
	if err != nil {
		t.Fatalf("nested auditor contract should be recovered: %v", err)
	}
	if turn.Reply != "Ответьте по пунктам." {
		t.Fatalf("unexpected visible reply: %q", turn.Reply)
	}
	if got := cleanAssistantMessage(nestedReply); got != "Ответьте по пунктам." {
		t.Fatalf("stored assistant message was not cleaned: %q", got)
	}
}
