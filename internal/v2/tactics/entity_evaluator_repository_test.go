package tactics

import "testing"

func TestAssignEntityEvaluationsUsesBatchStateAndDefaults(t *testing.T) {
	workstreams := []Workstream{
		{
			ID: 11,
			Projects: []Project{
				{ID: 21},
				{ID: 22},
			},
		},
		{ID: 12, Projects: []Project{}},
	}
	workstreamEvaluation := &TacticalEntityEvaluation{ID: 101, EntityType: EntityWorkstream, EntityID: 11}
	projectEvaluation := &TacticalEntityEvaluation{ID: 202, EntityType: EntityProject, EntityID: 21}

	assignEntityEvaluations(workstreams, map[string]entityEvaluationState{
		entityEvaluationKey(EntityWorkstream, 11): {
			Status:     entityEvaluationReady,
			Evaluation: workstreamEvaluation,
		},
		entityEvaluationKey(EntityProject, 21): {
			Status:     entityEvaluationQueued,
			Evaluation: projectEvaluation,
		},
	})

	if workstreams[0].Evaluation != workstreamEvaluation || workstreams[0].EvaluationStatus != entityEvaluationReady {
		t.Fatalf("unexpected workstream evaluation state: %+v", workstreams[0])
	}
	if workstreams[0].Projects[0].Evaluation != projectEvaluation || workstreams[0].Projects[0].EvaluationStatus != entityEvaluationQueued {
		t.Fatalf("unexpected project evaluation state: %+v", workstreams[0].Projects[0])
	}
	if workstreams[0].Projects[1].Evaluation != nil || workstreams[0].Projects[1].EvaluationStatus != "not_evaluated" {
		t.Fatalf("expected missing project state to default to not_evaluated: %+v", workstreams[0].Projects[1])
	}
	if workstreams[1].Evaluation != nil || workstreams[1].EvaluationStatus != "not_evaluated" {
		t.Fatalf("expected missing workstream state to default to not_evaluated: %+v", workstreams[1])
	}
}
