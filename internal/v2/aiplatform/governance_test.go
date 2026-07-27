package aiplatform

import (
	"testing"

	"reup-goals-backend/internal/ai"
)

func TestEstimateCost(t *testing.T) {
	usage := ai.Usage{InputTokens: 100_000, OutputTokens: 100_000, TotalTokens: 200_000}
	cost := estimateCost("gpt-5.4", usage)
	if cost != 1.75 {
		t.Fatalf("unexpected cost: %f", cost)
	}
}

func TestEstimateCostUsesCachedAndLongContextPricing(t *testing.T) {
	usage := ai.Usage{InputTokens: 300_000, OutputTokens: 10_000, TotalTokens: 310_000}
	usage.InputTokenDetails.CachedTokens = 100_000
	cost := estimateCost("gpt-5.4", usage)
	want := 1.275
	if cost != want {
		t.Fatalf("unexpected cost: got %f want %f", cost, want)
	}
}

func TestUnknownModelHasNoInventedPrice(t *testing.T) {
	if cost := estimateCost("custom-provider-model", ai.Usage{InputTokens: 1000, OutputTokens: 1000}); cost != 0 {
		t.Fatalf("unexpected fallback cost: %f", cost)
	}
}

func TestOnlyConversationalModulesConsumeWorkspaceQuota(t *testing.T) {
	chatModules := []string{
		"business_auditor_openai_native",
		"business_document_chat",
		"strategy_facilitator_openai_native",
		"tactics_advisor_openai_native",
		"task_brainstorm",
	}
	for _, module := range chatModules {
		if !consumesWorkspaceQuota(module) {
			t.Fatalf("chat module %q must consume workspace quota", module)
		}
	}

	backgroundModules := []string{
		"audio_transcription",
		"task_evaluator_v2",
		"task_completion_evaluator",
		"knowledge_base_deferred_extractor",
		"strategy_readiness_auditor",
	}
	for _, module := range backgroundModules {
		if consumesWorkspaceQuota(module) {
			t.Fatalf("background module %q must remain available after chat quota is exhausted", module)
		}
	}
}
