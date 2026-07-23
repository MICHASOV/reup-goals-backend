package strategicmemory

import (
	"encoding/json"
	"testing"
)

func TestDocumentConversationPinsScopeOnceAndKeepsTurnsCompact(t *testing.T) {
	document := StrategicDocument{DocumentType: "finance_economics", Title: "Финансы", Version: 3}
	var initial map[string]any
	if err := json.Unmarshal([]byte(documentChatInitialInput(document, "Исправь выручку")), &initial); err != nil {
		t.Fatal(err)
	}
	if initial["selected_document"] == nil || initial["related_business_context"] != nil {
		t.Fatalf("unexpected document initial context: %#v", initial)
	}
	var turn map[string]any
	if err := json.Unmarshal([]byte(documentChatTurnInput("Теперь маржу")), &turn); err != nil {
		t.Fatal(err)
	}
	if len(turn) != 1 || turn["latest_user_message"] != "Теперь маржу" {
		t.Fatalf("document turn repeated context: %#v", turn)
	}
}

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
		Snapshot: map[string]any{"future_market": "Australia"},
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
	if result.Snapshot != nil {
		t.Fatalf("facts-only sources must not rewrite an untyped snapshot: %#v", result.Snapshot)
	}
}

func TestClaimStatusForMaterializedClaim(t *testing.T) {
	tests := []struct {
		name  string
		claim aiMemoryResponseClaim
		want  string
	}{
		{name: "normal user fact is confirmed", claim: aiMemoryResponseClaim{Confidence: "high", Relation: "new"}, want: ClaimStatusConfirmed},
		{name: "low confidence item is suggested", claim: aiMemoryResponseClaim{Confidence: "low", Relation: "new"}, want: ClaimStatusSuggested},
		{name: "explicit confirmation stays confirmed", claim: aiMemoryResponseClaim{Confidence: "low", Relation: "confirms"}, want: ClaimStatusConfirmed},
		{name: "contradiction is conflicted", claim: aiMemoryResponseClaim{Confidence: "high", Relation: "contradicts"}, want: ClaimStatusConflicted},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := claimStatusForMaterializedClaim(test.claim); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestMaterializerClaimPreservesImportance(t *testing.T) {
	claims := materializerItemsToClaims([]materializerItem{{
		Text: "Cash runway is six weeks", Type: "constraint",
		PrimaryDocument: "finance_economics", Importance: "critical",
	}})
	if len(claims) != 1 || claims[0].Importance != "critical" {
		t.Fatalf("importance was lost during materialization: %#v", claims)
	}
	if normalizeImportance("unexpected") != "medium" {
		t.Fatal("unknown importance must use the safe medium default")
	}
}

func TestValidClaimStatus(t *testing.T) {
	for _, status := range []string{
		ClaimStatusSuggested,
		ClaimStatusConfirmed,
		ClaimStatusRejected,
		ClaimStatusConflicted,
		ClaimStatusOutdated,
	} {
		if !validClaimStatus(status) {
			t.Fatalf("expected %q to be valid", status)
		}
	}
	if validClaimStatus("active") {
		t.Fatal("legacy active status must not remain valid")
	}
}
