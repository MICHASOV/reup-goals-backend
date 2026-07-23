package ai

import "strings"

// EstimateCost returns the estimated OpenAI cost in US dollars for one call.
// Unknown models intentionally return zero instead of inventing a price.
func EstimateCost(model string, usage Usage) float64 {
	inputPrice, cachedInputPrice, outputPrice := modelPrices(model)
	cachedTokens := usage.CachedInputTokens()
	if cachedTokens < 0 || cachedTokens > usage.InputTokens {
		cachedTokens = 0
	}
	uncachedTokens := usage.InputTokens - cachedTokens
	inputMultiplier, outputMultiplier := 1.0, 1.0
	if usage.InputTokens > 272_000 && usesLongContextPremium(model) {
		inputMultiplier, outputMultiplier = 2, 1.5
	}
	return (float64(uncachedTokens)*inputPrice*inputMultiplier +
		float64(cachedTokens)*cachedInputPrice*inputMultiplier +
		float64(usage.OutputTokens)*outputPrice*outputMultiplier) / 1_000_000
}

func modelPrices(model string) (float64, float64, float64) {
	value := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(value, "gpt-5.6"):
		return 5, 0.5, 30
	case strings.HasPrefix(value, "gpt-5.5"):
		return 5, 0.5, 30
	case strings.HasPrefix(value, "gpt-5.4-pro"):
		return 30, 30, 180
	case strings.HasPrefix(value, "gpt-5.4-mini"):
		return 0.75, 0.075, 4.5
	case strings.HasPrefix(value, "gpt-5.4-nano"):
		return 0.2, 0.02, 1.25
	case strings.HasPrefix(value, "gpt-5.4"):
		return 2.5, 0.25, 15
	case strings.HasPrefix(value, "gpt-5.2"):
		return 1.75, 0.175, 14
	case strings.HasPrefix(value, "gpt-5.1"):
		return 1.25, 0.125, 10
	case strings.HasPrefix(value, "gpt-5-mini"):
		return 0.25, 0.025, 2
	case strings.HasPrefix(value, "gpt-5-nano"):
		return 0.05, 0.005, 0.4
	case strings.HasPrefix(value, "gpt-5"):
		return 1.25, 0.125, 10
	case strings.HasPrefix(value, "gpt-4.1-mini"):
		return 0.4, 0.1, 1.6
	case strings.HasPrefix(value, "gpt-4.1-nano"):
		return 0.1, 0.025, 0.4
	case strings.HasPrefix(value, "gpt-4.1"):
		return 2, 0.5, 8
	case strings.HasPrefix(value, "gpt-4o-transcribe"):
		return 2.5, 2.5, 10
	case strings.HasPrefix(value, "gpt-4o-mini-transcribe"):
		return 1.25, 1.25, 5
	case strings.HasPrefix(value, "gpt-4o-mini"):
		return 0.15, 0.075, 0.6
	case strings.HasPrefix(value, "gpt-4o"):
		return 2.5, 1.25, 10
	default:
		return 0, 0, 0
	}
}

func usesLongContextPremium(model string) bool {
	value := strings.ToLower(strings.TrimSpace(model))
	return (strings.HasPrefix(value, "gpt-5.4") &&
		!strings.HasPrefix(value, "gpt-5.4-mini") &&
		!strings.HasPrefix(value, "gpt-5.4-nano")) ||
		strings.HasPrefix(value, "gpt-5.5") ||
		strings.HasPrefix(value, "gpt-5.6")
}
