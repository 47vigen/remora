package api

import "strings"

// Paging bounds, matching the contract's Limit parameter.
const (
	defaultLimit = 50
	maxLimit     = 200
)

// page returns one page of items and the total before paging. limit defaults to 50 and is capped at 200; offset
// defaults to 0. The page is never nil, so it serialises as [].
func page[T any](items []T, limit, offset *int) ([]T, int) {
	total := len(items)
	l, o := defaultLimit, 0
	if limit != nil && *limit > 0 {
		l = min(*limit, maxLimit)
	}
	if offset != nil && *offset > 0 {
		o = *offset
	}
	if o >= total {
		return []T{}, total
	}
	end := min(o+l, total)
	out := make([]T, end-o)
	copy(out, items[o:end])
	return out, total
}

// matches reports whether q is a case-insensitive substring of any field; a nil or empty q matches everything.
func matches(q *string, fields ...string) bool {
	if q == nil || *q == "" {
		return true
	}
	needle := strings.ToLower(*q)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), needle) {
			return true
		}
	}
	return false
}
