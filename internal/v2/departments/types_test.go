package departments

import "testing"

func TestNormalizeKPIsDropsEmptyAndTrims(t *testing.T) {
	items := normalizeKPIs([]KPI{{Name: " Revenue ", Current: " 10 ", Target: " 20 "}, {Name: " "}})
	if len(items) != 1 || items[0].Name != "Revenue" || items[0].Current != "10" || items[0].Target != "20" {
		t.Fatalf("unexpected KPIs: %#v", items)
	}
}

func TestDedupePositive(t *testing.T) {
	items := dedupePositive([]int{3, 0, 3, -1, 5})
	if len(items) != 2 || items[0] != 3 || items[1] != 5 {
		t.Fatalf("unexpected ids: %#v", items)
	}
}
