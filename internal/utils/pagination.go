package utils

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

const (
	// DefaultLimit is the page size used when the client omits limit.
	DefaultLimit = 20
	// MaxLimit is the largest page size a client may request.
	MaxLimit = 100
)

// Page is the envelope returned by every paginated list endpoint.
type Page[T any] struct {
	Items  []T   `json:"items"`
	Total  int64 `json:"total" example:"128"`
	Limit  int   `json:"limit" example:"20"`
	Offset int   `json:"offset" example:"0"`
}

// Pagination holds a normalized page window.
type Pagination struct {
	Limit  int `form:"limit"`
	Offset int `form:"offset"`
}

// ParsePagination reads limit and offset from the query string. Non-numeric,
// non-positive or oversized values fall back to the default/upper bound.
func ParsePagination(c *gin.Context) Pagination {
	limit, err := strconv.Atoi(c.Query("limit"))
	if err != nil || limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}

	offset, err := strconv.Atoi(c.Query("offset"))
	if err != nil || offset < 0 {
		offset = 0
	}

	return Pagination{Limit: limit, Offset: offset}
}

// NewPage wraps a page of items and the unpaginated total in the list envelope.
func NewPage[T any](items []T, total int64, p Pagination) Page[T] {
	if items == nil {
		items = []T{}
	}
	return Page[T]{Items: items, Total: total, Limit: p.Limit, Offset: p.Offset}
}
