package soniox

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// OpenAI error types the adapter answers with.
const (
	typeInvalidRequest = "invalid_request_error"
	typeAuthentication = "authentication_error"
	typeQuota          = "insufficient_quota"
	typeRateLimit      = "rate_limit_exceeded"
	typeUpstream       = "upstream_error"
	typeTimeout        = "timeout"
	typeNotFound       = "not_found"
)

// writeError writes the OpenAI error shape {"error":{"message","type","code":null}}.
func writeError(w http.ResponseWriter, status int, typ, msg string) {
	b, err := json.Marshal(map[string]any{"error": map[string]any{"message": msg, "type": typ, "code": nil}})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// mapError turns a Soniox client error into the status, type and message the
// client sees. Soniox's 401, 402, 403 and 429 keep their status so the front
// door's fallback can act on 402 and 429; another 4xx is a 400; a 5xx or a
// transport error is a 502. The message names Soniox, never an engine, and
// never contains key.
func mapError(err error, key string) (status int, typ, msg string) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return http.StatusBadGateway, typeUpstream, "Soniox request failed: the Soniox API could not be reached"
	}
	msg = redact(fmt.Sprintf("Soniox %s failed: %d %s: %s", apiErr.Op, apiErr.Status, apiErr.Type, apiErr.Message), key)
	switch apiErr.Status {
	case http.StatusUnauthorized:
		return apiErr.Status, typeAuthentication, msg
	case http.StatusPaymentRequired:
		return apiErr.Status, typeQuota, msg
	case http.StatusTooManyRequests:
		return apiErr.Status, typeRateLimit, msg
	case http.StatusForbidden:
		return apiErr.Status, typeUpstream, msg
	}
	if apiErr.Status >= 400 && apiErr.Status < 500 {
		return http.StatusBadRequest, typeUpstream, msg
	}
	return http.StatusBadGateway, typeUpstream, msg
}
