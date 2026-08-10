package navigation

import "testing"

func TestDeriveOnboardingProgress(t *testing.T) {
	tests := []struct {
		name        string
		context     bool
		strategy    *strategy
		departments []department
		task        bool
		want        onboardingProgress
	}{
		{
			name:    "context only",
			context: true,
			want:    onboardingProgress{ContextComplete: true},
		},
		{
			name:     "goal requires meaningful content",
			context:  true,
			strategy: &strategy{Status: "draft"},
			want:     onboardingProgress{ContextComplete: true},
		},
		{
			name:        "default company direction does not complete decomposition",
			context:     true,
			strategy:    &strategy{Summary: "Выйти на устойчивую прибыль"},
			departments: []department{{ID: 1, Name: "Компания"}},
			want: onboardingProgress{
				ContextComplete: true, GoalComplete: true,
			},
		},
		{
			name:        "workspace onboarding complete",
			context:     true,
			strategy:    &strategy{Status: "active", Summary: "Выйти на устойчивую прибыль"},
			departments: []department{{ID: 1, Name: "Продажи"}},
			task:        true,
			want: onboardingProgress{
				ContextComplete: true, GoalComplete: true, DirectionsComplete: true,
				TasksComplete: true, Complete: true,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := deriveOnboardingProgress(test.context, test.strategy, test.departments, test.task)
			if got != test.want {
				t.Fatalf("deriveOnboardingProgress() = %+v, want %+v", got, test.want)
			}
		})
	}
}
