// Package bifrost is remora's client for the front door's management API. It speaks to the engine over loopback
// HTTP with the admin credentials remora generated; nothing it returns is meant for a remora user.
package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// ErrNotFound is matched by errors.Is when the engine answers 404.
var ErrNotFound = errors.New("bifrost: not found")

// maxErrorBody caps how much of an error answer is read; maxBody caps a successful answer.
const (
	maxErrorBody = 64 << 10
	maxBody      = 64 << 20
)

// APIError is a non-2xx answer from the engine. Its text never reaches a client: the proxy maps errors itself.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("bifrost: status %d: %s", e.Status, e.Message)
}

// Is makes a 404 match ErrNotFound.
func (e *APIError) Is(target error) bool {
	return target == ErrNotFound && e.Status == http.StatusNotFound
}

// Client calls the front door's management API. It logs in once and sends the session token as a Bearer: the engine
// checks a Basic credential with bcrypt on every call, which costs tens of milliseconds of CPU each time.
type Client struct {
	base     string
	user     string
	password string
	hc       *http.Client

	mu    sync.Mutex
	token string
}

// New returns a client for the engine at baseURL (for example "http://127.0.0.1:18080"). A nil hc uses a fresh
// http.Client with no timeout; callers bound each call with its context.
func New(baseURL, user, password string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{}
	}
	return &Client{base: strings.TrimRight(baseURL, "/"), user: user, password: password, hc: hc}
}

// Health reports whether the engine answers GET /health with status "ok".
func (c *Client) Health(ctx context.Context) error {
	var out struct {
		Status string `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, "/health", nil, &out); err != nil {
		return err
	}
	if out.Status != "ok" {
		return fmt.Errorf("bifrost: health status %q", out.Status)
	}
	return nil
}

// requireID refuses an empty path segment: the engine answers 404 for the shortened route, which would read as
// ErrNotFound and let a caller believe an object is gone.
func requireID(what, id string) error {
	if id == "" {
		return fmt.Errorf("bifrost: empty %s", what)
	}
	return nil
}

// do sends one management call: in (when not nil) as the JSON body, out (when not nil) decoded from a 2xx answer.
// path must already be escaped.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var data []byte
	if in != nil {
		var err error
		if data, err = json.Marshal(in); err != nil {
			return fmt.Errorf("bifrost: encode %s %s: %w", method, path, err)
		}
	}
	token, err := c.session(ctx, "")
	if err != nil {
		return err
	}
	resp, err := c.send(ctx, method, path, data, token)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		// The session expired or the engine lost it: log in again once.
		_ = resp.Body.Close()
		if token, err = c.session(ctx, token); err != nil {
			return err
		}
		resp, err = c.send(ctx, method, path, data, token)
	}
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return &APIError{Status: resp.StatusCode, Message: errorMessage(resp.StatusCode, raw)}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out); err != nil {
		return fmt.Errorf("bifrost: decode %s %s: %w", method, path, err)
	}
	return nil
}

func (c *Client) send(ctx context.Context, method, path string, data []byte, token string) (*http.Response, error) {
	var body io.Reader
	if data != nil {
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, fmt.Errorf("bifrost: %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bifrost: %s %s: %w", method, path, err)
	}
	return resp, nil
}

// session returns the current session token, logging in when there is none or when the current one is stale (the
// token a call was just refused with). Concurrent callers share one login.
func (c *Client) session(ctx context.Context, stale string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.token != stale {
		return c.token, nil
	}
	data, err := json.Marshal(map[string]string{"username": c.user, "password": c.password})
	if err != nil {
		return "", fmt.Errorf("bifrost: encode login: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/session/login", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("bifrost: login: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("bifrost: login: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Not an APIError: a 404 here must not read as ErrNotFound for the object the caller asked about.
		return "", fmt.Errorf("bifrost: login: status %d: %s", resp.StatusCode, errorMessage(resp.StatusCode, raw))
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == "token" && ck.Value != "" {
			c.token = ck.Value
			return c.token, nil
		}
	}
	return "", errors.New("bifrost: login answered without a session token")
}

// errorMessage picks the message out of an engine error answer. The engine's management handlers answer
// {"error":{"message":..}}; older handlers answer {"error":"..","message":".."}.
func errorMessage(status int, raw []byte) string {
	var shaped struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(raw, &shaped) == nil {
		var nested struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(shaped.Error, &nested) == nil && nested.Message != "" {
			return nested.Message
		}
		if shaped.Message != "" {
			return shaped.Message
		}
		var flat string
		if json.Unmarshal(shaped.Error, &flat) == nil && flat != "" {
			return flat
		}
	}
	if text := strings.TrimSpace(string(raw)); text != "" {
		const maxText = 512
		if len(text) > maxText {
			text = text[:maxText]
		}
		return text
	}
	return http.StatusText(status)
}
