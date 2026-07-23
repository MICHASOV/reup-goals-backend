package ai

import "testing"

func TestEstimateCostUsesCachedInput(t *testing.T) {
	usage := Usage{InputTokens: 10_000, OutputTokens: 1_000, TotalTokens: 11_000}
	usage.InputTokenDetails.CachedTokens = 8_000

	got := EstimateCost("gpt-5.4", usage)
	want := 0.022
	if got != want {
		t.Fatalf("EstimateCost() = %f, want %f", got, want)
	}
}

func TestEstimateCostUnknownModel(t *testing.T) {
	if got := EstimateCost("unknown", Usage{InputTokens: 1000, OutputTokens: 1000}); got != 0 {
		t.Fatalf("EstimateCost() = %f, want 0", got)
	}
}

func TestEstimateCostForKnowledgeExtractionModels(t *testing.T) {
	usage := Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000, TotalTokens: 2_000_000}

	if got, want := EstimateCost("gpt-5.4-nano", usage), 1.45; got != want {
		t.Fatalf("gpt-5.4-nano cost = %f, want %f", got, want)
	}
	if got, want := EstimateCost("gpt-4.1-mini", usage), 2.0; got != want {
		t.Fatalf("gpt-4.1-mini cost = %f, want %f", got, want)
	}
}

func TestMiniModelsDoNotUseLongContextPremium(t *testing.T) {
	usage := Usage{InputTokens: 300_000, OutputTokens: 10_000, TotalTokens: 310_000}
	if got, want := EstimateCost("gpt-5.4-nano", usage), 0.0725; got != want {
		t.Fatalf("gpt-5.4-nano long-context cost = %f, want %f", got, want)
	}
}
