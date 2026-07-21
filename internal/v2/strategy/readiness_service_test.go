package strategy

import (
	"encoding/json"
	"testing"
)

func TestStrategyConversationInputsStayCompactAfterInitialization(t *testing.T) {
	state := StrategyFacilitatorState{Strategy: Strategy{ID: 17, Status: StatusDraft, Title: "Основная стратегия"}}
	var initial map[string]any
	if err := json.Unmarshal([]byte(buildStrategyFacilitatorInitialInput("Начнем", state)), &initial); err != nil {
		t.Fatal(err)
	}
	if initial["active_strategy"] == nil || initial["knowledge_base"] != nil || initial["recent_dialogue"] != nil {
		t.Fatalf("unexpected initial context: %#v", initial)
	}
	var turn map[string]any
	if err := json.Unmarshal([]byte(buildStrategyFacilitatorTurnInput("Продолжим")), &turn); err != nil {
		t.Fatal(err)
	}
	if len(turn) != 1 || turn["latest_user_message"] != "Продолжим" {
		t.Fatalf("conversation turn repeated context: %#v", turn)
	}
}

func TestNormalizeReadinessReportAlwaysAddsFeedbackForReady(t *testing.T) {
	run := StrategyReadinessRun{SessionRevision: 7, ValidatedThroughMessageID: 42}
	report := StrategyReadinessReport{
		Verdict:          ReadinessVerdictReady,
		CanSynthesize:    false,
		Confidence:       "HIGH",
		ExecutiveSummary: "The core strategic logic is coherent.",
		CriteriaAssessment: []StrategyReadinessCriterion{{
			Area:       "choice",
			Status:     "strong",
			SourceKeys: []string{"strategy_message:42", "invented:9"},
		}},
	}
	sources := map[string]strategySynthesisSourceCatalogItem{
		"strategy_message:42": {Key: "strategy_message:42"},
	}

	normalized := normalizeReadinessReport(report, run, sources)

	if normalized.Verdict != ReadinessVerdictReady || !normalized.CanSynthesize {
		t.Fatalf("expected ready report to synthesize, got verdict=%q can_synthesize=%v", normalized.Verdict, normalized.CanSynthesize)
	}
	if normalized.SessionRevision != 7 || normalized.ValidatedThroughMessageID != 42 {
		t.Fatalf("expected server revision identifiers to win: %+v", normalized)
	}
	if normalized.Confidence != "high" {
		t.Fatalf("expected normalized confidence, got %q", normalized.Confidence)
	}
	if len(normalized.FacilitatorGuidance) == 0 {
		t.Fatal("ready reports must always contain facilitator feedback")
	}
	if normalized.FacilitatorGuidance[0].Blocking {
		t.Fatal("default feedback for a ready strategy must be non-blocking")
	}
	if got := normalized.CriteriaAssessment[0].SourceKeys; len(got) != 1 || got[0] != "strategy_message:42" {
		t.Fatalf("expected invented source key to be removed, got %#v", got)
	}
}

func TestNormalizeReadinessReportBlockingGapPreventsSynthesis(t *testing.T) {
	report := StrategyReadinessReport{
		Verdict:       ReadinessVerdictReady,
		CanSynthesize: true,
		BlockingGaps: []StrategyReadinessIssue{{
			Area:        "economics",
			Issue:       "No viable economic logic",
			WhyItBlocks: "The selected direction cannot be evaluated.",
		}},
	}

	normalized := normalizeReadinessReport(report, StrategyReadinessRun{}, map[string]strategySynthesisSourceCatalogItem{})

	if normalized.Verdict != ReadinessVerdictNotReady || normalized.CanSynthesize {
		t.Fatalf("blocking gaps must stop synthesis, got verdict=%q can_synthesize=%v", normalized.Verdict, normalized.CanSynthesize)
	}
	if len(normalized.FacilitatorGuidance) == 0 || !normalized.FacilitatorGuidance[0].Blocking {
		t.Fatal("not-ready reports must provide blocking facilitator guidance")
	}
}

func TestParseStrategyFacilitatorOutputKeepsNaturalMessageAndNormalizesStatus(t *testing.T) {
	raw := `{
		"message":"Понял. Тогда давай проверим экономику этого выбора.",
		"session_status":"CANDIDATE_READY",
		"status_reason":" Core choice is explicit. ",
		"remaining_uncertainties":[" Economics ","Economics",""]
	}`

	output, err := parseStrategyFacilitatorOutput(raw)
	if err != nil {
		t.Fatal(err)
	}
	if output.SessionStatus != FacilitatorStatusCandidateReady {
		t.Fatalf("unexpected status %q", output.SessionStatus)
	}
	if output.Message != "Понял. Тогда давай проверим экономику этого выбора." {
		t.Fatalf("natural message changed: %q", output.Message)
	}
	if len(output.RemainingUncertainties) != 1 || output.RemainingUncertainties[0] != "Economics" {
		t.Fatalf("unexpected uncertainties %#v", output.RemainingUncertainties)
	}
}

func TestParseStrategyFacilitatorOutputRejectsReplacementCharacters(t *testing.T) {
	_, err := parseStrategyFacilitatorOutput(`{
		"message":"Кажется, но здесь символ повре�дён.",
		"session_status":"continue",
		"status_reason":"",
		"remaining_uncertainties":[]
	}`)
	if err == nil {
		t.Fatal("expected invalid UTF-8 replacement character to trigger a retry")
	}
}

func TestParseStrategyFacilitatorOutputRejectsSerializedMessage(t *testing.T) {
	_, err := parseStrategyFacilitatorOutput(`{
		"message":"{\"next_question\":\"Что проверяем?\"}",
		"session_status":"continue",
		"status_reason":"",
		"remaining_uncertainties":[]
	}`)
	if err == nil {
		t.Fatal("serialized JSON must not reach the strategy chat")
	}
}

func TestMessagesThroughIDExcludesNewerTurns(t *testing.T) {
	messages := []StrategyChatMessage{{ID: 10}, {ID: 11}, {ID: 12}}
	filtered := messagesThroughID(messages, 11)
	if len(filtered) != 2 || filtered[1].ID != 11 {
		t.Fatalf("unexpected filtered messages %#v", filtered)
	}
}
