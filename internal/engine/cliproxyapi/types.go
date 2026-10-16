package cliproxyapi

import (
	"fmt"
	"time"
)

// OAuthStart answers GET /oauth/auth-url. A paste flow has URL and State; a device flow also has Flow "device",
// UserCode and ExpiresIn (seconds).
type OAuthStart struct {
	URL       string `json:"url"`
	State     string `json:"state"`
	Flow      string `json:"flow,omitempty"`
	UserCode  string `json:"user_code,omitempty"`
	ExpiresIn int    `json:"expires_in,omitempty"`
}

// OAuthStatus answers GET /oauth/status. Status is "wait", "ok" or "error"; Error explains "error". The engine
// answers 200 for every status, including an unknown or expired state.
type OAuthStatus struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Quota is the engine's passive quota observation for a credential.
type Quota struct {
	ObservedAt *time.Time        `json:"observed_at,omitempty"`
	Signals    map[string]string `json:"signals"`
}

// Cooldown is one unexpired retry restriction on a credential (scope "credential") or one of its models (scope
// "model", ModelKey set).
type Cooldown struct {
	Scope            string    `json:"scope"`
	ModelKey         string    `json:"model_key,omitempty"`
	Reason           string    `json:"reason"`
	RetryAt          time.Time `json:"retry_at"`
	RemainingSeconds int64     `json:"remaining_seconds"`
	HTTPStatus       int       `json:"http_status,omitempty"`
}

// Credential is one entry of GET /credentials. Name is the credential file name; Provider is the engine's provider
// id (for example "xai", which remora shows as "grok"). Fields the engine sends but remora does not use are dropped.
type Credential struct {
	ID             string         `json:"id"`
	AuthIndex      string         `json:"auth_index"`
	Name           string         `json:"name"`
	Provider       string         `json:"provider"`
	Label          string         `json:"label"`
	Email          string         `json:"email,omitempty"`
	ProjectID      string         `json:"project_id,omitempty"`
	IDToken        *IDTokenClaims `json:"id_token,omitempty"`
	Status         string         `json:"status"`
	StatusMessage  string         `json:"status_message"`
	Disabled       bool           `json:"disabled"`
	Unavailable    bool           `json:"unavailable"`
	NextRetryAfter *time.Time     `json:"next_retry_after,omitempty"`
	Quota          Quota          `json:"quota"`
	Cooldowns      []Cooldown     `json:"cooldowns"`
	Success        int64          `json:"success"`
	Failed         int64          `json:"failed"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// IDTokenClaims are the Codex id_token claims the engine lists with a credential.
type IDTokenClaims struct {
	ChatGPTAccountID string `json:"chatgpt_account_id"`
	PlanType         string `json:"plan_type"`
}

// APICallRequest asks the engine to send one HTTP request upstream as a credential. "$TOKEN$" in Header or Data
// becomes the credential's token.
type APICallRequest struct {
	AuthIndex string            `json:"auth_index"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Header    map[string]string `json:"header,omitempty"`
	Data      string            `json:"data,omitempty"`
	ProxyURL  string            `json:"proxy_url,omitempty"`
}

// APICallResponse is the upstream's answer; a non-2xx StatusCode is an answer, not an error.
type APICallResponse struct {
	StatusCode int                 `json:"status_code"`
	Header     map[string][]string `json:"header"`
	Body       string              `json:"body"`
}

// Error is a non-2xx answer from the engine. Message is the body's "error" field, followed by its "message" field
// when the engine sends one. Neither its text nor the engine's name ever reaches a remora user.
type Error struct {
	StatusCode int
	Message    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("oauth engine: %d: %s", e.StatusCode, e.Message)
}
