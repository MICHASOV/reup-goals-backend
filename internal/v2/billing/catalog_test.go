package billing

import (
	"strings"
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
		{PlanFounder, 3490, 33504, 890, 1, 1_250_000},
		{PlanTeam, 11990, 115104, 2990, 5, 3_000_000},
		{PlanCompany, 29990, 287904, 7490, 0, 9_000_000},
	}
	for _, test := range tests {
		plan, err := PlanByCode(test.code)
		if err != nil {
			t.Fatal(err)
		}
		if plan.MonthlyAmount != test.monthly || plan.AnnualAmount != test.annual ||
			plan.ResetAmount != test.reset || plan.MemberLimit != test.members ||
			plan.WeeklyTokenLimit != test.weeklyLimit {
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
		baseLimit: 1_250_000, baseUsed: 1_250_000, purchasedBalance: 25_000, timezone: "Europe/Moscow",
	}
	summary := state.summary()
	if !summary.AIAvailable || !summary.ExtraCapacityActive || summary.UsedPercent != 100 {
		t.Fatalf("unexpected quota summary: %+v", summary)
	}
	if summary.WeeklyTokenLimit != 1_250_000 || summary.WeeklyTokensUsed != 1_250_000 ||
		summary.PurchasedTokenBalance != 25_000 || summary.RemainingTokens != 25_000 {
		t.Fatalf("unexpected quota counters: %+v", summary)
	}
}

func TestSettledTokenUsageConsumesBaseThenPurchasedCapacity(t *testing.T) {
	state := quotaState{
		baseLimit: 10_000, baseUsed: 9_500, purchasedBalance: 2_000,
	}
	charge := settleReservedTokens(&state, 500, 500, 1_200)
	if state.baseUsed != 10_000 || state.purchasedBalance != 2_300 {
		t.Fatalf("unexpected settled quota: %+v", state)
	}
	if charge.actual != 1_200 || charge.charged != 1_200 || charge.base != 1_000 || charge.purchased != 200 {
		t.Fatalf("unexpected token charge: %+v", charge)
	}
}

func TestSettledTokenUsageClampsAtAvailableCapacity(t *testing.T) {
	state := quotaState{
		baseLimit: 1_000, baseUsed: 1_000, purchasedBalance: 0,
	}
	charge := settleReservedTokens(&state, 100, 100, 500)
	if state.baseUsed != 1_000 || state.purchasedBalance != 0 {
		t.Fatalf("unexpected exhausted quota: %+v", state)
	}
	if charge.actual != 500 || charge.charged != 200 {
		t.Fatalf("unexpected clamped charge: %+v", charge)
	}
}

func TestSettledTokenUsageRefundsUnusedReservation(t *testing.T) {
	state := quotaState{
		baseLimit: 100_000, baseUsed: 75_000, purchasedBalance: 0,
	}
	charge := settleReservedTokens(&state, 75_000, 0, 2_400)
	if state.baseUsed != 2_400 || state.purchasedBalance != 0 {
		t.Fatalf("expected unused reservation to be refunded: %+v", state)
	}
	if charge.actual != 2_400 || charge.charged != 2_400 || charge.base != 2_400 {
		t.Fatalf("unexpected token charge: %+v", charge)
	}
}

func TestSettlementMetadataParametersHaveExplicitPostgresTypes(t *testing.T) {
	for _, parameter := range []string{"$3::INTEGER", "$4::INTEGER", "$5::INTEGER", "$6::INTEGER", "$7::INTEGER", "$8::INTEGER"} {
		if !strings.Contains(settleReservationSQL, parameter) {
			t.Fatalf("settlement query must type %s explicitly", parameter)
		}
	}
}

func TestQuotaReservationPreventsParallelOvercommit(t *testing.T) {
	state := quotaState{
		baseLimit: 100_000, baseUsed: 92_000, purchasedBalance: 2_000,
	}
	first := reserveQuotaTokens(&state, maxChatReservationTokens)
	second := reserveQuotaTokens(&state, maxChatReservationTokens)
	if first.total != 10_000 || first.base != 8_000 || first.purchased != 2_000 {
		t.Fatalf("unexpected first reservation: %+v", first)
	}
	if second.total != 0 {
		t.Fatalf("expected parallel request to be blocked, got %+v", second)
	}
	if state.baseUsed != 100_000 || state.purchasedBalance != 0 {
		t.Fatalf("unexpected reserved state: %+v", state)
	}
}

func TestLegacyMessageQuotaPreservesUsageRatio(t *testing.T) {
	if got := scaleQuotaUnits(168, 400, 3_000_000); got != 1_260_000 {
		t.Fatalf("unexpected migrated token usage: %d", got)
	}
}
