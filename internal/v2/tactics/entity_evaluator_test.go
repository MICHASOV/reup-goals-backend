package tactics

import "testing"

func TestCalculateTacticalEntityPriority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output tacticalEntityEvaluatorOutput
		score  int
		tier   string
	}{
		{
			name: "strong high confidence entity",
			output: tacticalEntityEvaluatorOutput{
				StrategicRelevance: 950,
				ExpectedImpact:     900,
				Clarity:            850,
				Feasibility:        800,
				Measurability:      750,
				Confidence:         900,
			},
			score: 856,
			tier:  "P1",
		},
		{
			name: "uncertainty lowers otherwise strong entity",
			output: tacticalEntityEvaluatorOutput{
				StrategicRelevance: 900,
				ExpectedImpact:     900,
				Clarity:            900,
				Feasibility:        900,
				Measurability:      900,
				Confidence:         0,
			},
			score: 630,
			tier:  "P3",
		},
		{
			name:   "empty entity",
			output: tacticalEntityEvaluatorOutput{},
			score:  0,
			tier:   "P5",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			score := calculateTacticalEntityPriority(test.output)
			if score != test.score {
				t.Fatalf("score = %d, want %d", score, test.score)
			}
			if tier := tacticalEntityPriorityTier(score); tier != test.tier {
				t.Fatalf("tier = %s, want %s", tier, test.tier)
			}
		})
	}
}

func TestParseTacticalEntityEvaluatorOutputClampsScores(t *testing.T) {
	t.Parallel()

	output, err := parseTacticalEntityEvaluatorOutput(`{
		"strategic_relevance": 1200,
		"expected_impact": -10,
		"clarity": 500,
		"feasibility": 600,
		"measurability": 700,
		"confidence": 800,
		"priority_reason": "  Strong strategic link.  ",
		"missing_information": ["", "  Budget evidence  "]
	}`)
	if err != nil {
		t.Fatalf("parse output: %v", err)
	}
	if output.StrategicRelevance != 1000 || output.ExpectedImpact != 0 {
		t.Fatalf("scores were not clamped: %+v", output)
	}
	if output.PriorityReason != "Strong strategic link." {
		t.Fatalf("reason = %q", output.PriorityReason)
	}
	if len(output.MissingInformation) != 1 || output.MissingInformation[0] != "Budget evidence" {
		t.Fatalf("missing information = %#v", output.MissingInformation)
	}
}
