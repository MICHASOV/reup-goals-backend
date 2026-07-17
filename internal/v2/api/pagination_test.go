package api

import (
	"net/http/httptest"
	"testing"
)

func TestParsePaginationClampsValues(t *testing.T) {
	request := httptest.NewRequest("GET", "/items?limit=999&offset=-3", nil)
	page := ParsePagination(request, 50, 200)
	if page.Limit != 200 || page.Offset != 0 {
		t.Fatalf("unexpected page: %+v", page)
	}
}

func TestPaginationMeta(t *testing.T) {
	meta := PaginationMeta(Pagination{Limit: 20, Offset: 20}, 41)
	if meta["has_more"] != true {
		t.Fatalf("expected more pages: %+v", meta)
	}
}
