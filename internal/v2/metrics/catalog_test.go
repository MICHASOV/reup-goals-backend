package metrics

import "testing"

func TestCatalogHasUsefulBreadth(t *testing.T) {
	items := Catalog("", "")
	if len(items) < 50 {
		t.Fatalf("expected at least 50 templates, got %d", len(items))
	}
	keys := map[string]bool{}
	for _, item := range items {
		if item.Key == "" || item.Name == "" || item.Description == "" || item.Formula == "" {
			t.Fatalf("incomplete catalog item: %#v", item)
		}
		if keys[item.Key] {
			t.Fatalf("duplicate catalog key %q", item.Key)
		}
		keys[item.Key] = true
	}
}

func TestCatalogSearchesAliasesAndFiltersCategory(t *testing.T) {
	items := Catalog("customer acquisition cost", "")
	if len(items) != 1 || items[0].Key != "cac" {
		t.Fatalf("expected CAC alias match, got %#v", items)
	}
	items = Catalog("", "Операции")
	if len(items) == 0 {
		t.Fatal("expected operations templates")
	}
	for _, item := range items {
		if item.Category != "Операции" {
			t.Fatalf("unexpected category %q", item.Category)
		}
	}
}

func TestCatalogRanksCanonicalExpenseMetricsForNaturalRussianQuery(t *testing.T) {
	items := Catalog("метрики снижения расходов", "")
	if len(items) < 4 {
		t.Fatalf("expected canonical expense metrics, got %#v", items)
	}
	topKeys := map[string]bool{}
	for _, item := range items[:4] {
		topKeys[item.Key] = true
	}
	for _, key := range []string{"burn_rate", "ebitda", "operating_cash_flow", "operating_profit"} {
		if !topKeys[key] {
			t.Fatalf("expected %q among top results, got %#v", key, items[:4])
		}
	}
}

func TestValidTargetInput(t *testing.T) {
	input := TargetInput{
		TemplateKey: "revenue",
		ScopeType:   ScopeProject,
		ScopeID:     42,
		Role:        RolePrimary,
		Cadence:     "monthly",
	}
	if !validTargetInput(input) {
		t.Fatal("expected valid standard metric target")
	}
	input.ScopeID = 0
	if validTargetInput(input) {
		t.Fatal("expected scoped target without id to be invalid")
	}
}
