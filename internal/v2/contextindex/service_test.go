package contextindex

import (
	"strings"
	"testing"
)

func TestStandardMetricCatalogIsSearchableWorkspaceContext(t *testing.T) {
	catalog, err := standardMetricCatalogJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`"key": "revenue"`,
		`"name": "Выручка"`,
		`"formula"`,
		`"interpretation"`,
	} {
		if !strings.Contains(catalog, expected) {
			t.Fatalf("metric catalog does not contain %q", expected)
		}
	}
	if !strings.Contains(RetrievalInstructions, "Standard business metric catalog") {
		t.Fatal("retrieval instructions must require metric catalog search")
	}
}
