package control

import (
	"io"
	"net/http"
)

// Default answers of the providers' private quota endpoints. Resets are in 2099 so they never pass during a run.
const (
	DefaultQuotaAntigravitySummary = `{"groups":[` +
		`{"groupId":"gemini","displayName":"Gemini","buckets":[` +
		`{"window":"5h","remaining":{"remainingFraction":0.8,"resetTime":"2099-01-01T05:00:00Z"}},` +
		`{"window":"weekly","remaining":{"remainingFraction":0.5,"resetTime":"2099-01-07T00:00:00Z"}}]},` +
		`{"groupId":"claude-gpt","displayName":"Claude & GPT","buckets":[` +
		`{"window":"5h","remaining":{"remainingFraction":0,"resetTime":"2099-01-01T05:00:00Z"}},` +
		`{"window":"weekly","remaining":{"remainingFraction":0.25,"resetTime":"2099-01-07T00:00:00Z"}}]}]}`
	DefaultQuotaAntigravityLoad     = `{"paidTier":{"id":"g1-pro-tier","name":"Pro"}}`
	DefaultQuotaAntigravityManifest = "version: 9.9.9\n"
	DefaultQuotaCodexUsage          = `{"plan_type":"plus","rate_limit":{"limit_reached":false,` +
		`"primary_window":{"used_percent":30,"limit_window_seconds":18000,"reset_at":4070912400},` +
		`"secondary_window":{"used_percent":60,"limit_window_seconds":604800,"reset_at":4071430800}}}`
	DefaultQuotaClaudeUsage = `{"five_hour":{"utilization":10,"resets_at":"2099-01-01T05:00:00Z"},` +
		`"seven_day":{"utilization":40,"resets_at":"2099-01-07T00:00:00Z"},"seven_day_opus":null}`
)

// fixed answers 200 with body.
func fixed(contentType, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, body)
	}
}
