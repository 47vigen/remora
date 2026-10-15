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

// LogFilter narrows GET /api/logs and the stats and histogram built on the same filters. Zero fields are left out
// of the query, so the engine's defaults apply (limit 50, sort_by timestamp, order desc). List fields are sent
// comma-joined.
type LogFilter struct {
	Status          []string   // "processing", "success", "error"
	Providers       []string   // front-door provider names
	Models          []string   // front-door model names
	VirtualKeyIDs   []string   // the virtual keys the requests came in on
	RoutingRuleIDs  []string   // the routing rules that routed the requests
	Start           *time.Time // rows whose timestamp is at or after Start
	End             *time.Time // rows whose timestamp is at or before End
	ContentSearch   string     // free text over the request and response content
	ParentRequestID string     // the fallback attempts of one request
	Limit           int        // 1..MaxLogLimit
	Offset          int
	SortBy          string // "timestamp", "latency", "tokens" or "cost"
	Order           string // "asc" or "desc"
}

// query encodes f; page adds limit, offset, sort_by and order, which only the list reads.
func (f LogFilter) query(page bool) url.Values {
	q := url.Values{}
	for name, v := range map[string][]string{
		"status": f.Status, "providers": f.Providers, "models": f.Models, "virtual_key_ids": f.VirtualKeyIDs,
		"routing_rule_ids": f.RoutingRuleIDs,
	} {
		if len(v) > 0 {
			q.Set(name, strings.Join(v, ","))
		}
	}
	if f.Start != nil {
		q.Set("start_time", f.Start.Format(time.RFC3339Nano))
	}
	if f.End != nil {
		q.Set("end_time", f.End.Format(time.RFC3339Nano))
	}
	if f.ContentSearch != "" {
		q.Set("content_search", f.ContentSearch)
	}
	if f.ParentRequestID != "" {
		q.Set("parent_request_id", f.ParentRequestID)
	}
	if !page {
		return q
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
	return q
}

// withQuery appends q to path when it is not empty.
func withQuery(path string, q url.Values) string {
	if enc := q.Encode(); enc != "" {
		return path + "?" + enc
	}
	return path
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
	// FallbackIndex is the attempt: 0 for the request itself, n for its nth fallback, whose ParentRequestID is the
	// request's id.
	FallbackIndex int         `json:"fallback_index"`
	VirtualKeyID  *string     `json:"virtual_key_id"`
	RoutingRuleID *string     `json:"routing_rule_id"`
	Latency       *float64    `json:"latency"` // milliseconds
	Cost          *float64    `json:"cost"`    // dollars
	Stream        bool        `json:"stream"`
	TokenUsage    *TokenUsage `json:"token_usage"`
	// ErrorDetails is filled by GetLog only: the list leaves it out.
	ErrorDetails json.RawMessage `json:"error_details,omitempty"`
	// The bodies, filled by GetLog only.
	InputHistory  json.RawMessage `json:"input_history,omitempty"`
	OutputMessage json.RawMessage `json:"output_message,omitempty"`
	Params        json.RawMessage `json:"params,omitempty"`
}

// TokenUsage is a row's token counts.
type TokenUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

// LogStats is GET /api/logs/stats over a filter. The user-facing figures count a fallback chain once, as its root
// request; UserFacingSuccessRate is a percentage.
type LogStats struct {
	UserFacingTotalRequests int64   `json:"user_facing_total_requests"`
	UserFacingSuccessRate   float64 `json:"user_facing_success_rate"`
	PromptTokens            int64   `json:"prompt_tokens"`
	CompletionTokens        int64   `json:"completion_tokens"`
	TotalTokens             int64   `json:"total_tokens"`
	TotalCost               float64 `json:"total_cost"`
}

// HistogramBucket is one time bucket of GET /api/logs/histogram: the rows that started in it, by outcome.
type HistogramBucket struct {
	Timestamp time.Time `json:"timestamp"`
	Count     int64     `json:"count"`
	Success   int64     `json:"success"`
	Error     int64     `json:"error"`
}

// LogPage is one page of a log search. Total counts every row the filter matches.
type LogPage struct {
	Logs  []Log
	Total int64
}

// SearchLogs calls GET /api/logs.
func (c *Client) SearchLogs(ctx context.Context, f LogFilter) (LogPage, error) {
	path := withQuery("/api/logs", f.query(true))
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

// LogStats calls GET /api/logs/stats; paging and sorting fields of f are ignored.
func (c *Client) LogStats(ctx context.Context, f LogFilter) (LogStats, error) {
	var out LogStats
	if err := c.do(ctx, http.MethodGet, withQuery("/api/logs/stats", f.query(false)), nil, &out); err != nil {
		return LogStats{}, err
	}
	return out, nil
}

// Histogram calls GET /api/logs/histogram; the engine chooses the bucket size from the time range. Paging and
// sorting fields of f are ignored.
func (c *Client) Histogram(ctx context.Context, f LogFilter) ([]HistogramBucket, error) {
	var out struct {
		Buckets []HistogramBucket `json:"buckets"`
	}
	if err := c.do(ctx, http.MethodGet, withQuery("/api/logs/histogram", f.query(false)), nil, &out); err != nil {
		return nil, err
	}
	return out.Buckets, nil
}

// KeyStatus is what listing models with one provider key found: Status "success", "list_models_failed", or
// "unknown" when the provider lists no models; Description carries the failure.
type KeyStatus struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Description string `json:"description"`
}

// RefreshProviderModels calls POST /api/providers/{name}/refresh-models, which lists models with every enabled key
// of the provider and answers each key's status.
func (c *Client) RefreshProviderModels(ctx context.Context, name string) ([]KeyStatus, error) {
	if err := requireID("provider name", name); err != nil {
		return nil, err
	}
	var out struct {
		Keys []KeyStatus `json:"keys"`
	}
	if err := c.do(ctx, http.MethodPost, providerPath(name)+"/refresh-models", nil, &out); err != nil {
		return nil, err
	}
	return out.Keys, nil
}

// LogModels calls GET /api/logs/filterdata for the distinct models in the log whose name contains query.
func (c *Client) LogModels(ctx context.Context, query string) ([]string, error) {
	q := url.Values{"dimensions": {"models"}, "q": {query}}
	var out struct {
		Models []string `json:"models"`
	}
	if err := c.do(ctx, http.MethodGet, withQuery("/api/logs/filterdata", q), nil, &out); err != nil {
		return nil, err
	}
	return out.Models, nil
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
