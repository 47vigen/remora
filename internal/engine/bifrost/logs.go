package bifrost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// MaxLogLimit is the largest page the front door's log search accepts; a larger limit is answered 400.
const MaxLogLimit = 1000

// LogFilter narrows GET /api/logs. Zero fields are left out of the query, so the engine's defaults apply
// (limit 50, sort_by timestamp, order desc).
type LogFilter struct {
	Status []string   // "processing", "success", "error"; sent comma-joined
	Start  *time.Time // rows whose timestamp is at or after Start
	Limit  int        // 1..MaxLogLimit
	Offset int
	SortBy string // "timestamp", "latency", "tokens" or "cost"
	Order  string // "asc" or "desc"
}

// Log is one row of the front door's request log. Timestamp is when the request started; the row is written when it
// completes.
type Log struct {
	ID              string    `json:"id"`
	ParentRequestID *string   `json:"parent_request_id"`
	Timestamp       time.Time `json:"timestamp"`
	Provider        string    `json:"provider"`
	Model           string    `json:"model"`
	Status          string    `json:"status"` // "processing", "success" or "error"
	// ErrorDetails is filled by GetLog only: the list leaves it out.
	ErrorDetails json.RawMessage `json:"error_details,omitempty"`
}

// LogPage is one page of a log search. Total counts every row the filter matches.
type LogPage struct {
	Logs  []Log
	Total int64
}

// SearchLogs calls GET /api/logs.
func (c *Client) SearchLogs(ctx context.Context, f LogFilter) (LogPage, error) {
	q := url.Values{}
	if len(f.Status) > 0 {
		q.Set("status", strings.Join(f.Status, ","))
	}
	if f.Start != nil {
		q.Set("start_time", f.Start.Format(time.RFC3339Nano))
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	if f.Offset > 0 {
		q.Set("offset", strconv.Itoa(f.Offset))
	}
	if f.SortBy != "" {
		q.Set("sort_by", f.SortBy)
	}
	if f.Order != "" {
		q.Set("order", f.Order)
	}
	path := "/api/logs"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var out struct {
		Logs       []Log `json:"logs"`
		Pagination struct {
			TotalCount int64 `json:"total_count"`
		} `json:"pagination"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return LogPage{}, err
	}
	return LogPage{Logs: out.Logs, Total: out.Pagination.TotalCount}, nil
}

// GetLog calls GET /api/logs/{id}, which carries the row's error details. A missing row matches ErrNotFound.
func (c *Client) GetLog(ctx context.Context, id string) (Log, error) {
	if err := requireID("log id", id); err != nil {
		return Log{}, err
	}
	var out Log
	if err := c.do(ctx, http.MethodGet, "/api/logs/"+url.PathEscape(id), nil, &out); err != nil {
		return Log{}, err
	}
	return out, nil
}

// Failure reads the upstream status code and its retry hint from the row's error details. It returns zeros when the
// row has no details or they carry no status code. The engine clamps the hint to 1s..5m before logging it.
func (l Log) Failure() (statusCode int, retryAfter time.Duration) {
	if len(l.ErrorDetails) == 0 {
		return 0, 0
	}
	var d struct {
		StatusCode  *int `json:"status_code"`
		ExtraFields struct {
			RetryAfterMS int64 `json:"retry_after_ms"`
		} `json:"extra_fields"`
	}
	if json.Unmarshal(l.ErrorDetails, &d) != nil || d.StatusCode == nil {
		return 0, 0
	}
	if d.ExtraFields.RetryAfterMS > 0 {
		retryAfter = time.Duration(d.ExtraFields.RetryAfterMS) * time.Millisecond
	}
	return *d.StatusCode, retryAfter
}
