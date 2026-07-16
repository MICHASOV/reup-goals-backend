package strategicmemory

import "testing"

func TestStrategicDocumentDefinitionsAreCompleteAndUnique(t *testing.T) {
	definitions := strategicDocumentDefinitions()
	if len(definitions) != 13 {
		t.Fatalf("expected 13 strategic documents, got %d", len(definitions))
	}

	seen := map[string]bool{}
	lastOrder := 0
	for _, definition := range definitions {
		if definition.DocumentType == "" || definition.Title == "" || definition.Description == "" {
			t.Fatalf("incomplete document definition: %#v", definition)
		}
		if seen[definition.DocumentType] {
			t.Fatalf("duplicate document type %q", definition.DocumentType)
		}
		if definition.SortOrder <= lastOrder {
			t.Fatalf("document catalog is not ordered at %q", definition.DocumentType)
		}
		seen[definition.DocumentType] = true
		lastOrder = definition.SortOrder
	}
}

func TestValidStrategicDocumentTypeRejectsLegacyTypes(t *testing.T) {
	if !validStrategicDocumentType("company_governance") {
		t.Fatal("current document type must be valid")
	}
	if validStrategicDocumentType("company_snapshot") {
		t.Fatal("legacy document type must not be accepted by document chat")
	}
}

func TestClaimsForDocumentContextKeepsOnlySelectedDocument(t *testing.T) {
	claims := []Claim{
		{ID: 1, TopicKey: "finance_economics"},
		{ID: 2, TopicKey: "economic_engine"},
		{ID: 3, TopicKey: "team_organization"},
	}

	result := claimsForDocumentContext(claims, "finance_economics", 10)
	if len(result) != 2 || result[0].ID != 1 || result[1].ID != 2 {
		t.Fatalf("unexpected claims: %#v", result)
	}
}

func TestFactsOnlyMaterializationDropsStrategyContent(t *testing.T) {
	input := materializerOutput{
		ExtractedItems: []materializerItem{
			{Text: "Revenue is 10M", Type: "metric", PrimaryDocument: "finance_economics"},
			{Text: "Enter a new market", Type: "decision", PrimaryDocument: "strategy_development"},
			{Text: "Australia may grow", Type: "hypothesis", PrimaryDocument: "hypotheses_assumptions"},
			{Text: "Sales are founder-led", Type: "process", PrimaryDocument: "marketing_sales_relationships"},
		},
		OpenQuestions: []materializerQuestion{{QuestionGoal: "Which market next?"}},
		DocumentBrief: []materializerDocumentNote{
			{DocumentType: "finance_economics", KeyPoints: []string{"Revenue is 10M"}},
			{DocumentType: "strategy_development", KeyPoints: []string{"Enter a new market"}},
		},
	}

	result := factsOnlyMaterialization(input)
	if len(result.ExtractedItems) != 2 {
		t.Fatalf("expected two factual items, got %#v", result.ExtractedItems)
	}
	if len(result.OpenQuestions) != 0 {
		t.Fatalf("facts-only mode must not create agenda questions: %#v", result.OpenQuestions)
	}
	if len(result.DocumentBrief) != 1 || result.DocumentBrief[0].DocumentType != "finance_economics" {
		t.Fatalf("unexpected document brief: %#v", result.DocumentBrief)
	}
}
