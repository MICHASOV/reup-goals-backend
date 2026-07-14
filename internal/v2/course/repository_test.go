package course

import (
	"database/sql"
	"testing"
)

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
	if draft.Status != StatusDraft {
		t.Fatalf("generated course must start as draft, got %q", draft.Status)
	}
}

func TestBuildCourseSyncRequiresCurrentStrategyRevision(t *testing.T) {
	runID := 17
	course := Course{
		Status:                StatusActive,
		SourceSynthesisRunID:  &runID,
		SourceSessionRevision: 4,
	}

	current := buildCourseSync(course, strategySnapshot{
		RunID:                  17,
		SessionRevision:        4,
		CurrentSessionRevision: 4,
	}, nil)
	if current.State != SyncCurrent || current.NeedsReview {
		t.Fatalf("expected current course, got %+v", current)
	}

	stale := buildCourseSync(course, strategySnapshot{
		RunID:                  17,
		SessionRevision:        4,
		CurrentSessionRevision: 5,
	}, nil)
	if stale.State != SyncStrategyUpdated || !stale.NeedsReview {
		t.Fatalf("expected stale course, got %+v", stale)
	}

	unavailable := buildCourseSync(course, strategySnapshot{}, sql.ErrNoRows)
	if unavailable.State != SyncUnavailable || !unavailable.NeedsReview {
		t.Fatalf("expected unavailable provenance, got %+v", unavailable)
	}
}

func TestMissingCourseFieldsChecksActivationContract(t *testing.T) {
	complete := Course{
		Title:            "Проверить экономику B2B",
		Direction:        "Сфокусироваться на B2B",
		StrategicGoal:    "100 платящих клиентов",
		KeyMetric:        "LTV/CAC",
		SuccessCriterion: "LTV/CAC не ниже 3",
	}
	if missing := missingCourseFields(complete); len(missing) != 0 {
		t.Fatalf("complete course reported missing fields: %v", missing)
	}

	complete.KeyMetric = ""
	missing := missingCourseFields(complete)
	if len(missing) != 1 || missing[0] != "key_metric" {
		t.Fatalf("unexpected missing fields: %v", missing)
	}
}

func TestBuildCourseSourcesKeepsFieldProvenance(t *testing.T) {
	sources := buildCourseSources(map[string]strategyArtifactValue{
		"chosen_direction_and_refusals": {FrameTitle: "Фокус на B2B"},
		"goals_and_metrics":             {PrimarySignal: "100 клиентов"},
	})
	if len(sources) != 2 {
		t.Fatalf("expected two sources, got %d", len(sources))
	}
	if sources[1].ArtifactType != "goals_and_metrics" || len(sources[1].Fields) != 2 {
		t.Fatalf("unexpected goals source: %+v", sources[1])
	}
}
