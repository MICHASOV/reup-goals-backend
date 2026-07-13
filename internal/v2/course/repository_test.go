package course

import "testing"

func TestBuildDraftUsesCurrentSynthesisArtifacts(t *testing.T) {
	artifacts := map[string]strategyArtifactValue{
		"chosen_direction_and_refusals": {
			FrameTitle: "Фокус на прибыльном B2B",
		},
		"key_challenge": {
			PrimarySignal: "Перейти от убыточного роста к устойчивой модели",
		},
		"goals_and_metrics": {
			FrameTitle:    "100 платящих клиентов",
			FrameSubtitle: "Достичь цели при LTV/CAC не ниже 3",
			PrimarySignal: "100 платящих клиентов",
		},
		"ninety_day_course": {
			FrameTitle:    "Доказать экономику B2B за 90 дней",
			FrameSubtitle: "Проверить продажи и удержание на реальных клиентах",
		},
	}

	draft := buildDraft(StrategySummary{Summary: "fallback"}, artifacts)
	if draft.Title != "Доказать экономику B2B за 90 дней" {
		t.Fatalf("unexpected title %q", draft.Title)
	}
	if draft.Direction != "Фокус на прибыльном B2B" {
		t.Fatalf("unexpected direction %q", draft.Direction)
	}
	if draft.StrategicGoal != "100 платящих клиентов" || draft.KeyMetric != "100 платящих клиентов" {
		t.Fatalf("unexpected goal/metric: %#v", draft)
	}
	if draft.Meaning != "Перейти от убыточного роста к устойчивой модели" {
		t.Fatalf("unexpected meaning %q", draft.Meaning)
	}
	if draft.SuccessCriterion != "Проверить продажи и удержание на реальных клиентах" {
		t.Fatalf("unexpected success criterion %q", draft.SuccessCriterion)
	}
}
