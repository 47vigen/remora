// Package cliproxyapi is remora's client for the OAuth engine's v8 management API. It speaks to the engine over
// loopback HTTP with the management secret remora generated. Nothing it returns is meant for a remora user, and its
// errors never carry the engine's name.
package cliproxyapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// basePath is the v8 management prefix.
const basePath = "/v8/management"

// maxErrorBody caps how much of an error answer is read; maxBody caps a successful answer.
const (
	maxErrorBody = 64 << 10
	maxBody      = 64 << 20
)

// Client calls the OAuth engine's management API.
type Client struct {
	base   string
	secret string
	hc     *http.Client
}

// New returns a client for the engine at baseURL (for example "http://127.0.0.1:18317"). A nil hc uses a fresh
// http.Client with no timeout; callers bound each call with its context.
func New(baseURL, managementSecret string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{}
	}
	return &Client{base: strings.TrimRight(baseURL, "/") + basePath, secret: managementSecret, hc: hc}
}

// StartOAuth starts a login for the engine provider id (Provider.Engine).
func (c *Client) StartOAuth(ctx context.Context, engineProvider string) (OAuthStart, error) {
	var out OAuthStart
	err := c.do(ctx, http.MethodGet, "/oauth/auth-url?"+query("provider", engineProvider), nil, &out)
	return out, err
}

// OAuthStatus reports a login's status. An unknown or expired state is Status "error", not an error.
func (c *Client) OAuthStatus(ctx context.Context, state string) (OAuthStatus, error) {
	var out OAuthStatus
	err := c.do(ctx, http.MethodGet, "/oauth/status?"+query("state", state), nil, &out)
	return out, err
}

// SubmitOAuthCallback hands the engine the address the browser ended on after a paste-flow login. Acceptance does
// not mean the login finished; poll OAuthStatus.
func (c *Client) SubmitOAuthCallback(ctx context.Context, state, redirectURL string) error {
	in := struct {
		State       string `json:"state"`
		RedirectURL string `json:"redirect_url"`
	}{state, redirectURL}
	return c.do(ctx, http.MethodPost, "/oauth/callback", in, nil)
}

// CancelOAuth cancels a pending login. Cancelling a finished, failed or unknown login is not an error.
func (c *Client) CancelOAuth(ctx context.Context, state string) error {
	return c.do(ctx, http.MethodDelete, "/oauth/session?"+query("state", state), nil, nil)
}

// Credentials lists the engine's credentials, ordered by name.
func (c *Client) Credentials(ctx context.Context) ([]Credential, error) {
	var out struct {
		Files []Credential `json:"files"`
	}
	if err := c.do(ctx, http.MethodGet, "/credentials", nil, &out); err != nil {
		return nil, err
	}
	return out.Files, nil
}

// CredentialModels lists the model ids a credential serves (without its prefix). An unknown name has none.
func (c *Client) CredentialModels(ctx context.Context, name string) ([]string, error) {
	var out struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := c.do(ctx, http.MethodGet, "/credentials/models?"+query("name", name), nil, &out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// modelRounds is how many times AwaitCredentialModels waits for a credential's models, nudging between rounds.
const modelRounds = 3

// AwaitCredentialModels waits up to wait for a just-written credential to list models and returns them. The engine
// can drop the model registration of a credential it is still busy with (its upload handler races its file
// watcher), leaving it with none until it next changes. Setting the prefix again, to the same value, makes the
// engine register the credential anew, so an empty round is followed by that nudge. After modelRounds rounds the
// list is returned empty.
func (c *Client) AwaitCredentialModels(ctx context.Context, name, prefix string, wait time.Duration) ([]string, error) {
	for round := 1; ; round++ {
		models, err := c.pollCredentialModels(ctx, name, wait)
		if err != nil || len(models) > 0 || round == modelRounds {
			return models, err
		}
		if err := c.SetCredentialPrefix(ctx, name, prefix); err != nil {
			return nil, err
		}
	}
}

// pollCredentialModels reads a credential's models until it has some or wait has passed. Each read is bounded by the
// round, so a slow engine answer ends the round empty instead of holding it up.
func (c *Client) pollCredentialModels(ctx context.Context, name string, wait time.Duration) ([]string, error) {
	interval := min(100*time.Millisecond, wait)
	round, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		models, err := c.CredentialModels(round, name)
		switch {
		case err == nil && len(models) > 0:
			return models, nil
		case round.Err() != nil:
			return nil, ctx.Err()
		case err != nil:
			return nil, err
		}
		select {
		case <-round.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// SetCredentialDisabled disables or enables a credential.
func (c *Client) SetCredentialDisabled(ctx context.Context, name string, disabled bool) error {
	in := struct {
		Name     string `json:"name"`
		Disabled bool   `json:"disabled"`
	}{name, disabled}
	return c.do(ctx, http.MethodPatch, "/credentials/status", in, nil)
}

// SetCredentialPrefix sets the model prefix a credential answers to ("<prefix>/<model>").
func (c *Client) SetCredentialPrefix(ctx context.Context, name, prefix string) error {
	in := struct {
		Name   string `json:"name"`
		Prefix string `json:"prefix"`
	}{name, prefix}
	return c.do(ctx, http.MethodPatch, "/credentials/fields", in, nil)
}

// SetCredentialPriority sets a credential's priority tier.
func (c *Client) SetCredentialPriority(ctx context.Context, name string, priority int) error {
	in := struct {
		Name     string `json:"name"`
		Priority int    `json:"priority"`
	}{name, priority}
	return c.do(ctx, http.MethodPatch, "/credentials/fields", in, nil)
}

// UploadCredential writes a credential file. name must end in ".json"; data is the file's JSON.
func (c *Client) UploadCredential(ctx context.Context, name string, data []byte) error {
	return c.send(ctx, http.MethodPost, "/credentials?"+query("name", name), data, nil)
}

// DeleteCredential deletes a credential file.
func (c *Client) DeleteCredential(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/credentials?"+query("name", name), nil, nil)
}

// RefreshCredential asks the engine to refresh a credential's tokens now.
func (c *Client) RefreshCredential(ctx context.Context, name string) error {
	in := struct {
		Name string `json:"name"`
	}{name}
	return c.do(ctx, http.MethodPost, "/credentials/refresh", in, nil)
}

// APICall sends one request upstream as the credential in.AuthIndex and returns the upstream's answer.
func (c *Client) APICall(ctx context.Context, in APICallRequest) (APICallResponse, error) {
	var out APICallResponse
	err := c.do(ctx, http.MethodPost, "/requests/api-call", in, &out)
	return out, err
}

// ResetCooldown clears the engine's cooldown state for the credential with this auth index.
func (c *Client) ResetCooldown(ctx context.Context, authIndex string) error {
	in := struct {
		AuthIndex string `json:"auth_index"`
	}{authIndex}
	return c.do(ctx, http.MethodPost, "/routing/cooldown/reset", in, nil)
}

// Config returns the engine's persisted configuration as JSON (v8 layout).
func (c *Client) Config(ctx context.Context) (json.RawMessage, error) {
	var out json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/config", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// PatchConfig merges patch (a JSON object) into the engine's configuration. Objects merge; lists and scalars are
// replaced.
func (c *Client) PatchConfig(ctx context.Context, patch any) error {
	return c.do(ctx, http.MethodPatch, "/config", patch, nil)
}

func query(key, value string) string {
	return url.Values{key: {value}}.Encode()
}

// do sends one management call with in (when not nil) as its JSON body and decodes a 2xx answer into out (when not
// nil). path is relative to the v8 prefix and already escaped.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return fmt.Errorf("oauth engine: encode %s %s: %w", method, path, err)
		}
	}
	return c.send(ctx, method, path, body, out)
}

// send is do with a raw JSON body (nil for none).
func (c *Client) send(ctx context.Context, method, path string, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return fmt.Errorf("oauth engine: %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("oauth engine: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return &Error{StatusCode: resp.StatusCode, Message: errorMessage(resp.StatusCode, raw)}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out); err != nil {
		return fmt.Errorf("oauth engine: decode %s %s: %w", method, path, err)
	}
	return nil
}

// engineName matches the engine's product name in any spelling, so it can be cut out of messages (for example a
// file path under the engine's data directory).
var engineName = regexp.MustCompile(`(?i)cli[-_ ]?proxy(?:[-_ ]?api)?`)

// errorMessage picks the message out of an engine error answer: {"error": ".."} or {"error": "..", "message": ".."}.
func errorMessage(status int, raw []byte) string {
	var shaped struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	msg := ""
	if json.Unmarshal(raw, &shaped) == nil && (shaped.Error != "" || shaped.Message != "") {
		msg = shaped.Error
		if shaped.Message != "" {
			if msg != "" {
				msg += ": "
			}
			msg += shaped.Message
		}
	} else if text := strings.TrimSpace(string(raw)); text != "" {
		const maxText = 512
		if len(text) > maxText {
			text = text[:maxText]
		}
		msg = text
	} else {
		msg = http.StatusText(status)
	}
	return engineName.ReplaceAllString(msg, "engine")
}
