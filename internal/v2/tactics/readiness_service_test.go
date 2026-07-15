package tactics

import "testing"

func TestNormalizeTacticsReadinessReportCalculatesWeightedScore(t *testing.T) {
	criteria := make([]TacticsReadinessCriterion, 0, len(tacticsReadinessCriterionOrder))
	for _, code := range tacticsReadinessCriterionOrder {
		criteria = append(criteria, TacticsReadinessCriterion{
			CriterionCode: code,
			Score:         70,
			SourceKeys:    []string{"course:7", "invented:source"},
		})
	}
	run := TacticsReadinessRun{
		SessionRevision:           4,
		TacticalPlanRevision:      9,
		ValidatedThroughMessageID: 25,
	}
	report := normalizeTacticsReadinessReport(TacticsReadinessReport{
		CriteriaAssessment: criteria,
	}, run, map[string]tacticsReadinessSource{
		"course:7": {Key: "course:7"},
	})

	if report.OverallScore != 70 {
		t.Fatalf("expected weighted score 70, got %d", report.OverallScore)
	}
	if report.Verdict != TacticsReadinessVerdictConditionallyReady || !report.CanActivate {
		t.Fatalf("expected conditionally ready and activatable, got %s %v", report.Verdict, report.CanActivate)
	}
	if len(report.CriteriaAssessment[0].SourceKeys) != 1 || report.CriteriaAssessment[0].SourceKeys[0] != "course:7" {
		t.Fatalf("unexpected cleaned sources: %#v", report.CriteriaAssessment[0].SourceKeys)
	}
	if report.SessionRevision != 4 || report.TacticalPlanRevision != 9 || report.ValidatedThroughMessageID != 25 {
		t.Fatal("expected snapshot revisions to be enforced from the run")
	}
}

func TestNormalizeTacticsReadinessReportBlocksOnBlockingGap(t *testing.T) {
	criteria := make([]TacticsReadinessCriterion, 0, len(tacticsReadinessCriterionOrder))
	for _, code := range tacticsReadinessCriterionOrder {
		criteria = append(criteria, TacticsReadinessCriterion{CriterionCode: code, Score: 95})
	}
	report := normalizeTacticsReadinessReport(TacticsReadinessReport{
		CriteriaAssessment: criteria,
		BlockingGaps: []TacticsReadinessIssue{{
			Area:  "resources",
			Issue: "No accountable owner or capacity exists for the critical workstream.",
		}},
	}, TacticsReadinessRun{}, nil)

	if report.OverallScore != 95 {
		t.Fatalf("expected score to remain transparent, got %d", report.OverallScore)
	}
	if report.Verdict != TacticsReadinessVerdictNotReady || report.CanActivate {
		t.Fatalf("blocking gap must prevent activation, got %s %v", report.Verdict, report.CanActivate)
	}
}

func TestNormalizeTacticsReadinessReportPenalizesMissingCriterion(t *testing.T) {
	report := normalizeTacticsReadinessReport(TacticsReadinessReport{
		CriteriaAssessment: []TacticsReadinessCriterion{{
			CriterionCode: "course_alignment",
			Score:         100,
		}},
	}, TacticsReadinessRun{}, nil)

	if report.OverallScore != 15 {
		t.Fatalf("expected only the 15%% course-alignment weight, got %d", report.OverallScore)
	}
	if len(report.CriteriaAssessment) != len(tacticsReadinessCriterionOrder) {
		t.Fatalf("expected all required criteria, got %d", len(report.CriteriaAssessment))
	}
	if report.CanActivate {
		t.Fatal("incomplete audit must not allow activation")
	}
}
