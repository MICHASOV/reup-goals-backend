package billing

import (
	"errors"
	"strings"
)

const (
	PlanFounder = "founder"
	PlanTeam    = "team"
	PlanCompany = "company"

	PeriodMonthly   = "monthly"
	PeriodQuarterly = "quarterly"
	PeriodAnnual    = "annual"

	OrderSubscription = "subscription"
	OrderQuotaReset   = "quota_reset"
)

var ErrPlanNotFound = errors.New("billing_plan_not_found")

type Plan struct {
	Code              string  `json:"code"`
	Name              string  `json:"name"`
	MonthlyAmount     float64 `json:"monthly_amount"`
	QuarterlyAmount   float64 `json:"quarterly_amount"`
	AnnualAmount      float64 `json:"annual_amount"`
	Currency          string  `json:"currency"`
	MemberLimit       int     `json:"member_limit"`
	WeeklyTokenLimit  int     `json:"weekly_token_limit"`
	ResetAmount       float64 `json:"reset_amount"`
	StandardResponses int     `json:"standard_responses_month"`
	EquivalentTokens  int     `json:"equivalent_tokens_month"`
}

var plans = []Plan{
	{
		Code: PlanFounder, Name: "Founder", MonthlyAmount: 3490, QuarterlyAmount: 9423, AnnualAmount: 29316,
		Currency: "RUB", MemberLimit: 1, WeeklyTokenLimit: 1_250_000, ResetAmount: 890,
		StandardResponses: 650, EquivalentTokens: 5_000_000,
	},
	{
		Code: PlanTeam, Name: "Team", MonthlyAmount: 11990, QuarterlyAmount: 32373, AnnualAmount: 100716,
		Currency: "RUB", MemberLimit: 5, WeeklyTokenLimit: 3_000_000, ResetAmount: 2990,
		StandardResponses: 1730, EquivalentTokens: 12_000_000,
	},
	{
		Code: PlanCompany, Name: "Company", MonthlyAmount: 29990, QuarterlyAmount: 80973, AnnualAmount: 251916,
		Currency: "RUB", MemberLimit: 0, WeeklyTokenLimit: 9_000_000, ResetAmount: 7490,
		StandardResponses: 5200, EquivalentTokens: 36_000_000,
	},
}

func Plans() []Plan {
	result := make([]Plan, len(plans))
	copy(result, plans)
	return result
}

func PlanByCode(code string) (Plan, error) {
	code = strings.ToLower(strings.TrimSpace(code))
	for _, item := range plans {
		if item.Code == code {
			return item, nil
		}
	}
	return Plan{}, ErrPlanNotFound
}

func Price(plan Plan, period string) (float64, error) {
	switch strings.ToLower(strings.TrimSpace(period)) {
	case PeriodMonthly:
		return plan.MonthlyAmount, nil
	case PeriodQuarterly:
		return plan.QuarterlyAmount, nil
	case PeriodAnnual:
		return plan.AnnualAmount, nil
	default:
		return 0, errors.New("billing_period_invalid")
	}
}
