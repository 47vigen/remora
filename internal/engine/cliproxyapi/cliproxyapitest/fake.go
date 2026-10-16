// Package cliproxyapitest is an in-memory fake of the OAuth engine's v8 management API for tests. It serves the
// routes remora's client uses with the engine's status codes, messages and body shapes (checked against the real
// engine by the contract test in package cliproxyapi), and hooks for driving logins from a test.
package cliproxyapitest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/engine/cliproxyapi"
)

const prefix = "/v8/management"

// deviceEngines are the engine providers that answer with a device flow.
var deviceEngines = map[string]bool{"xai": true, "kimi": true, "kimi-ai": true, "meta": true}

// pasteEngines are the engine providers that answer with an authorization address only.
var pasteEngines = map[string]bool{"claude": true, "codex": true, "antigravity": true, "devin": true}

type session struct {
	provider  string
	completed bool
	err       string
}

type credential struct {
	c      cliproxyapi.Credential
	prefix string
	models []string
}

// Fake is a running fake engine. URL is its base address (without the management prefix).
type Fake struct {
	URL    string
	secret string

	mu           sync.Mutex
	sessions     map[string]*session
	creds        map[string]*credential
	calls        map[string]int
	config       map[string]any
	patches      []json.RawMessage
	cbState      string
	cbRedirect   string
	nextUserCode int
	tokens       map[string]string
}

// NewServer starts a fake that accepts the management secret and stops it when the test ends.
func NewServer(t *testing.T, secret string) *Fake {
	t.Helper()
	f := &Fake{
		secret:   secret,
		sessions: map[string]*session{},
		creds:    map[string]*credential{},
		calls:    map[string]int{},
		config:   map[string]any{"config-version": float64(8)},
		tokens:   map[string]string{},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	f.URL = srv.URL
	return f
}

// FinishLogin completes a login: its status becomes "ok" and c appears among the credentials.
func (f *Fake) FinishLogin(state string, c cliproxyapi.Credential) {
	f.mu.Lock()
	if s, ok := f.sessions[state]; ok {
		s.completed = true
	}
	f.mu.Unlock()
	f.AddCredential(c)
}

// FailLogin fails a login with msg as its status error.
func (f *Fake) FailLogin(state, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.sessions[state]; ok {
		s.err = msg
	}
}

// Forget drops every login session, as an engine restart does.
func (f *Fake) Forget() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = map[string]*session{}
}

// AddCredential adds or replaces a credential by c.Name. An empty ID becomes the name, an empty AuthIndex a fresh
// one, an empty Status "active" (or "disabled" when Disabled), an empty Label the email or else the provider.
func (f *Fake) AddCredential(c cliproxyapi.Credential) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putLocked(c, nil)
}

// SetToken sets what "$TOKEN$" becomes in an api-call as the named credential; the default is "tok-" + its auth
// index.
func (f *Fake) SetToken(name, token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[name] = token
}

// SetModels sets the models a credential serves.
func (f *Fake) SetModels(name string, models ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if cr, ok := f.creds[name]; ok {
		cr.models = slices.Clone(models)
	}
}

// Credential returns a credential and its prefix.
func (f *Fake) Credential(name string) (c cliproxyapi.Credential, prefix string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cr, ok := f.creds[name]
	if !ok {
		return cliproxyapi.Credential{}, "", false
	}
	return cr.c, cr.prefix, true
}

// LastCallback returns the state and redirect address of the last accepted callback.
func (f *Fake) LastCallback() (state, redirectURL string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cbState, f.cbRedirect
}

// Calls counts the requests that reached a route, by path below the management prefix (for example
// "/credentials/refresh"), whatever their outcome.
func (f *Fake) Calls(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[path]
}

// ConfigPatches returns the bodies of the accepted PATCH /config calls, in order.
func (f *Fake) ConfigPatches() []json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]json.RawMessage, len(f.patches))
	copy(out, f.patches)
	return out
}

// putLocked stores c; a nil pfx keeps the prefix a credential of that name already has.
func (f *Fake) putLocked(c cliproxyapi.Credential, pfx *string) {
	if c.ID == "" {
		c.ID = c.Name
	}
	old, exists := f.creds[c.Name]
	if c.AuthIndex == "" {
		if exists {
			c.AuthIndex = old.c.AuthIndex
		} else {
			c.AuthIndex = randomHex(8)
		}
	}
	if c.Status == "" {
		c.Status = "active"
		if c.Disabled {
			c.Status = "disabled"
		}
	}
	if c.Label == "" {
		c.Label = c.Email
		if c.Label == "" {
			c.Label = c.Provider
		}
	}
	if c.Cooldowns == nil {
		c.Cooldowns = []cliproxyapi.Cooldown{}
	}
	if c.Quota.Signals == nil {
		c.Quota.Signals = map[string]string{}
	}
	cr := &credential{c: c}
	if exists {
		cr.models, cr.prefix = old.models, old.prefix
	}
	if pfx != nil {
		cr.prefix = *pfx
	}
	f.creds[c.Name] = cr
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	path, ok := strings.CutPrefix(r.URL.Path, prefix)
	if !ok {
		if r.URL.Path == "/" {
			writeJSON(w, http.StatusOK, map[string]any{"message": "fake oauth engine"})
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	// The engine takes OAuth callbacks without the management secret: they are validated by their pending state.
	if path != "/oauth/callback" && !f.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid management key"})
		return
	}
	if r.Method == http.MethodPost && path == "/requests/api-call" {
		f.apiCall(w, r) // sends upstream, so it takes the lock only around its lookups
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[path]++

	switch r.Method + " " + path {
	case "GET /oauth/auth-url":
		f.startOAuth(w, r)
	case "GET /oauth/status":
		f.oauthStatus(w, r)
	case "POST /oauth/callback":
		f.oauthCallback(w, r)
	case "DELETE /oauth/session":
		f.cancelOAuth(w, r)
	case "GET /credentials":
		f.listCredentials(w)
	case "POST /credentials":
		f.uploadCredential(w, r)
	case "DELETE /credentials":
		f.deleteCredential(w, r)
	case "GET /credentials/models":
		f.credentialModels(w, r)
	case "PATCH /credentials/status":
		f.patchStatus(w, r)
	case "PATCH /credentials/fields":
		f.patchFields(w, r)
	case "POST /credentials/refresh":
		f.refresh(w, r)
	case "POST /routing/cooldown/reset":
		f.resetCooldown(w, r)
	case "GET /config":
		writeJSON(w, http.StatusOK, f.config)
	case "PATCH /config":
		f.patchConfig(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
	}
}

func (f *Fake) authorized(r *http.Request) bool {
	provided := r.Header.Get("Authorization")
	if scheme, rest, ok := strings.Cut(provided, " "); ok && strings.EqualFold(scheme, "bearer") {
		provided = rest
	}
	if provided == "" {
		provided = r.Header.Get("X-Management-Key")
	}
	return provided != "" && provided == f.secret
}

// validState mirrors the engine's state check: 1-128 characters of [A-Za-z0-9._-] without "..".
func validState(state string) bool {
	if state == "" || len(state) > 128 || strings.Contains(state, "..") {
		return false
	}
	for _, r := range state {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

func (f *Fake) startOAuth(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("provider")))
	switch {
	case provider == "":
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "provider is required"})
		return
	case !pasteEngines[provider] && !deviceEngines[provider]:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "provider_not_found"})
		return
	}
	state := randomHex(16)
	f.sessions[state] = &session{provider: provider}
	resp := map[string]any{
		"status": "ok",
		"url":    "https://login.example/" + provider + "/authorize?" + url.Values{"state": {state}}.Encode(),
		"state":  state,
	}
	if deviceEngines[provider] {
		f.nextUserCode++
		resp["url"] = "https://login.example/" + provider + "/device"
		resp["flow"] = "device"
		resp["user_code"] = fmt.Sprintf("FAKE-%04d", f.nextUserCode)
		resp["expires_in"] = 900
	}
	writeJSON(w, http.StatusOK, resp)
}

func (f *Fake) oauthStatus(w http.ResponseWriter, r *http.Request) {
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state == "" {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		return
	}
	if !validState(state) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "invalid state"})
		return
	}
	s, ok := f.sessions[state]
	switch {
	case !ok:
		writeJSON(w, http.StatusOK, map[string]any{"status": "error", "error": "unknown or expired state"})
	case s.completed:
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	case s.err != "":
		writeJSON(w, http.StatusOK, map[string]any{"status": "error", "error": s.err})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"status": "wait"})
	}
}

func (f *Fake) oauthCallback(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider    string `json:"provider"`
		RedirectURL string `json:"redirect_url"`
		Code        string `json:"code"`
		State       string `json:"state"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "invalid body"})
		return
	}
	state, code, errMsg := strings.TrimSpace(req.State), strings.TrimSpace(req.Code), strings.TrimSpace(req.Error)
	if raw := strings.TrimSpace(req.RedirectURL); raw != "" {
		u, err := url.Parse(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "invalid redirect_url"})
			return
		}
		q := u.Query()
		if state == "" {
			state = strings.TrimSpace(q.Get("state"))
		}
		if code == "" {
			code = strings.TrimSpace(q.Get("code"))
		}
		if errMsg == "" {
			errMsg = strings.TrimSpace(q.Get("error"))
			if errMsg == "" {
				errMsg = strings.TrimSpace(q.Get("error_description"))
			}
		}
	}
	if state == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "state is required"})
		return
	}
	if !validState(state) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "invalid state"})
		return
	}
	if code == "" && errMsg == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "code or error is required"})
		return
	}
	s, ok := f.sessions[state]
	switch {
	case !ok:
		writeJSON(w, http.StatusNotFound, map[string]any{"status": "error", "error": "unknown or expired state"})
		return
	case s.completed:
		writeJSON(w, http.StatusConflict, map[string]any{"status": "error", "error": "oauth flow is already completed"})
		return
	case s.err != "":
		writeJSON(w, http.StatusConflict, map[string]any{"status": "error", "error": s.err})
		return
	case req.Provider != "" && !strings.EqualFold(req.Provider, s.provider):
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "provider does not match state"})
		return
	}
	f.cbState, f.cbRedirect = req.State, req.RedirectURL
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (f *Fake) cancelOAuth(w http.ResponseWriter, r *http.Request) {
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "missing state"})
		return
	}
	if !validState(state) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "invalid state"})
		return
	}
	s, ok := f.sessions[state]
	cancelled := ok && !s.completed && s.err == ""
	if cancelled {
		delete(f.sessions, state)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "cancelled": cancelled})
}

func (f *Fake) listCredentials(w http.ResponseWriter) {
	names := make([]string, 0, len(f.creds))
	for name := range f.creds {
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	files := make([]cliproxyapi.Credential, 0, len(names))
	for _, name := range names {
		files = append(files, f.creds[name].c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"observed_at": time.Now().UTC(), "files": files})
}

func unsafeName(name string) bool {
	return name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..")
}

func (f *Fake) uploadCredential(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if unsafeName(name) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid name"})
		return
	}
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name must end with .json"})
		return
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "failed to read body"})
		return
	}
	var meta map[string]any
	if err := json.Unmarshal(data, &meta); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "invalid auth file: " + err.Error()})
		return
	}
	str := func(k string) string { s, _ := meta[k].(string); return s }
	provider := str("type")
	if provider == "" {
		provider = "unknown"
	}
	disabled, _ := meta["disabled"].(bool)
	pfx := strings.TrimSpace(str("prefix"))
	f.putLocked(cliproxyapi.Credential{
		Name: name, Provider: provider, Email: str("email"), Disabled: disabled, UpdatedAt: time.Now().UTC(),
	}, &pfx)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (f *Fake) deleteCredential(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if unsafeName(name) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid name"})
		return
	}
	if _, ok := f.creds[name]; !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "auth file not found"})
		return
	}
	delete(f.creds, name)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (f *Fake) credentialModels(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
		return
	}
	models := []map[string]string{}
	if cr, ok := f.creds[name]; ok {
		for _, m := range cr.models {
			models = append(models, map[string]string{"id": m})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// decodeBody decodes a JSON request body or answers 400 "invalid request body".
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return false
	}
	return true
}

func (f *Fake) lookup(w http.ResponseWriter, name string) (*credential, bool) {
	cr, ok := f.creds[strings.TrimSpace(name)]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "auth file not found"})
	}
	return cr, ok
}

func (f *Fake) patchStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Disabled *bool  `json:"disabled"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	switch {
	case strings.TrimSpace(req.Name) == "":
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
		return
	case req.Disabled == nil:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "disabled is required"})
		return
	}
	cr, ok := f.lookup(w, req.Name)
	if !ok {
		return
	}
	cr.c.Disabled = *req.Disabled
	cr.c.Status = "active"
	if cr.c.Disabled {
		cr.c.Status = "disabled"
	}
	cr.c.UpdatedAt = time.Now().UTC()
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "disabled": cr.c.Disabled})
}

func (f *Fake) patchFields(w http.ResponseWriter, r *http.Request) {
	var req map[string]json.RawMessage
	if !decodeBody(w, r, &req) {
		return
	}
	var name string
	if raw, ok := req["name"]; !ok || json.Unmarshal(raw, &name) != nil || strings.TrimSpace(name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
		return
	}
	cr, ok := f.lookup(w, name)
	if !ok {
		return
	}
	if raw, ok := req["prefix"]; ok {
		var p string
		if err := json.Unmarshal(raw, &p); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "prefix must be a string"})
			return
		}
		cr.prefix = strings.TrimSpace(p)
	}
	cr.c.UpdatedAt = time.Now().UTC()
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (f *Fake) refresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name or all=true is required"})
		return
	}
	if _, ok := f.lookup(w, req.Name); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (f *Fake) resetCooldown(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AuthIndex string `json:"auth_index"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	index := strings.TrimSpace(req.AuthIndex)
	if index == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "auth_index is required"})
		return
	}
	for _, cr := range f.creds {
		if cr.c.AuthIndex != index {
			continue
		}
		cr.c.Cooldowns = []cliproxyapi.Cooldown{}
		cr.c.Unavailable = false
		cr.c.NextRetryAfter = nil
		if !cr.c.Disabled {
			cr.c.Status, cr.c.StatusMessage = "active", ""
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "auth_index": index, "models": []string{}})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "auth not found"})
}

func (f *Fake) apiCall(w http.ResponseWriter, r *http.Request) {
	var req cliproxyapi.APICallRequest
	f.mu.Lock()
	f.calls["/requests/api-call"]++
	f.mu.Unlock()
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	raw := strings.TrimSpace(req.URL)
	switch u, err := url.Parse(raw); {
	case method == "":
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing method"})
		return
	case raw == "":
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing url"})
		return
	case err != nil || u.Scheme == "" || u.Host == "":
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid url"})
		return
	}
	token := func() (string, bool) {
		f.mu.Lock()
		defer f.mu.Unlock()
		for name, cr := range f.creds {
			if cr.c.AuthIndex == req.AuthIndex {
				if t, ok := f.tokens[name]; ok {
					return t, true
				}
				return "tok-" + cr.c.AuthIndex, true
			}
		}
		return "", false
	}
	headers := map[string]string{}
	for k, v := range req.Header {
		if strings.Contains(v, "$TOKEN$") {
			t, ok := token()
			if !ok {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "auth credential not found for auth_index"})
				return
			}
			v = strings.ReplaceAll(v, "$TOKEN$", t)
		}
		headers[k] = v
	}
	var body io.Reader
	if req.Data != "" {
		body = strings.NewReader(req.Data)
	}
	up, err := http.NewRequestWithContext(r.Context(), method, raw, body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "failed to build request"})
		return
	}
	for k, v := range headers {
		up.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(up)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "request failed"})
		return
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "failed to read response"})
		return
	}
	writeJSON(w, http.StatusOK, cliproxyapi.APICallResponse{StatusCode: resp.StatusCode, Header: resp.Header, Body: string(data)})
}

func (f *Fake) patchConfig(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_body"})
		return
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
		return
	}
	patch, ok := v.(map[string]any)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "config_must_be_object"})
		return
	}
	merge(f.config, patch)
	f.patches = append(f.patches, json.RawMessage(raw))
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "config-version": 8})
}

// merge merges src into dst: objects merge key by key, everything else (lists included) is replaced.
func merge(dst, src map[string]any) {
	for k, v := range src {
		sub, isObj := v.(map[string]any)
		cur, curObj := dst[k].(map[string]any)
		if isObj && curObj {
			merge(cur, sub)
			continue
		}
		if isObj {
			fresh := map[string]any{}
			merge(fresh, sub)
			dst[k] = fresh
			continue
		}
		dst[k] = v
	}
}
