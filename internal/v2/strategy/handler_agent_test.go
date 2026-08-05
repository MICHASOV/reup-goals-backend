package strategy

import (
	"strings"
	"testing"
)

func TestAgentStrategyArtifactsKeepConfirmedCore(t *testing.T) {
	input := map[string]any{
		"strategic_goal":            "Доказать устойчивую экономику продукта",
		"current_state":             "Первые продажи без подтверждённой повторяемости",
		"target_state":              "Стабильная прибыльная модель",
		"economic_engine":           "Повторные продажи финансируют привлечение",
		"key_metric":                "Contribution margin",
		"strategic_logic":           "Сначала подтверждаем ядро, затем масштабируем",
		"deliberate_non_priorities": "Новые географии",
		"risks_and_assumptions":     "Повторный спрос может быть слабым",
	}

	artifacts := agentStrategyArtifacts(input)
	if artifacts["business_stage"] != input["current_state"] {
		t.Fatalf("current state was lost: %q", artifacts["business_stage"])
	}
	if !strings.Contains(artifacts["global_goal"], input["strategic_goal"].(string)) ||
		!strings.Contains(artifacts["global_goal"], input["target_state"].(string)) {
		t.Fatalf("global goal is incomplete: %q", artifacts["global_goal"])
	}
	if artifacts["key_metric"] != input["key_metric"] {
		t.Fatalf("key metric was lost: %q", artifacts["key_metric"])
	}
	if !strings.Contains(artifacts["strategic_direction"], input["deliberate_non_priorities"].(string)) {
		t.Fatalf("deliberate non-priorities were lost: %q", artifacts["strategic_direction"])
	}
}
