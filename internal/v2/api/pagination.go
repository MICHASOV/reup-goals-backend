package api

import (
	"net/http"
	"strconv"
)

type Pagination struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func ParsePagination(r *http.Request, defaultLimit int, maxLimit int) Pagination {
	if defaultLimit <= 0 {
		defaultLimit = 50
	}
	if maxLimit < defaultLimit {
		maxLimit = defaultLimit
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil || offset < 0 {
		offset = 0
	}
	return Pagination{Limit: limit, Offset: offset}
}

func PaginationMeta(page Pagination, total int) map[string]any {
	return map[string]any{
		"limit": page.Limit, "offset": page.Offset, "total": total,
		"has_more": page.Offset+page.Limit < total,
	}
}
