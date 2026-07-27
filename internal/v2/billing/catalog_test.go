package billing

import (
	"testing"
	"time"
)

func TestPlanCatalog(t *testing.T) {
	tests := []struct {
		code        string
		monthly     float64
		annual      float64
		reset       float64
		members     int
		weeklyLimit int
	}{
		{PlanFounder, 3490, 33504, 890, 1, 150},
		{PlanTeam, 11990, 115104, 2990, 5, 400},
		{PlanCompany, 29990, 287904, 7490, 0, 1200},
	}
	for _, test := range tests {
		plan, err := PlanByCode(test.code)
		if err != nil {
			t.Fatal(err)
		}
		if plan.MonthlyAmount != test.monthly || plan.AnnualAmount != test.annual ||
			plan.ResetAmount != test.reset || plan.MemberLimit != test.members ||
			plan.WeeklyAILimit != test.weeklyLimit {
			t.Fatalf("unexpected plan: %+v", plan)
		}
	}
}

func TestQuotaWindowKeepsActivationCadence(t *testing.T) {
	anchor := time.Date(2026, time.July, 1, 15, 30, 0, 0, time.UTC)
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	start, end := quotaWindow(anchor, now)
	if want := anchor.Add(14 * 24 * time.Hour); !start.Equal(want) {
		t.Fatalf("unexpected window start: got %s want %s", start, want)
	}
	if !end.Equal(start.Add(7 * 24 * time.Hour)) {
		t.Fatalf("unexpected window end: %s", end)
	}
}

func TestQuotaSummaryUsesPurchasedCapacityAfterBaseLimit(t *testing.T) {
	state := quotaState{
		windowStartedAt: time.Now().UTC(), windowEndsAt: time.Now().UTC().Add(7 * 24 * time.Hour),
		baseLimit: 150, baseUsed: 150, purchasedBalance: 25, timezone: "Europe/Moscow",
	}
	summary := state.summary()
	if !summary.AIAvailable || !summary.ExtraCapacityActive || summary.UsedPercent != 100 {
		t.Fatalf("unexpected quota summary: %+v", summary)
	}
}
