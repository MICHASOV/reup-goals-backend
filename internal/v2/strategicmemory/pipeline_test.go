package strategicmemory

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
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
		if got := pipelineConversationState(status); got != ConversationStateProcessingContext {
			t.Fatalf("status %q mapped to %q", status, got)
		}
	}
	if got := pipelineConversationState(KnowledgePipelineReady); got != ConversationStateReadyForStrategy {
		t.Fatalf("ready mapped to %q", got)
	}
	if got := pipelineConversationState(KnowledgePipelineNeedsMoreContext); got != ConversationStateCollectingContext {
		t.Fatalf("needs-more-context mapped to %q", got)
	}
}

func TestAuditorTurnOutputContract(t *testing.T) {
	var turn auditorTurnOutput
	err := json.Unmarshal([]byte(`{"reply":"Продолжим.","audit_candidate":true,"candidate_reason":"baseline covered"}`), &turn)
	if err != nil || turn.Reply == "" || !turn.AuditCandidate {
		t.Fatalf("structured turn contract failed: %+v, %v", turn, err)
	}
}
