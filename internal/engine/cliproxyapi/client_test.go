package cliproxyapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/engine/cliproxyapi/cliproxyapitest"
)

const secret = "s3cret"

func newClient(t *testing.T) (*cliproxyapi.Client, *cliproxyapitest.Fake) {
	t.Helper()
	f := cliproxyapitest.NewServer(t, secret)
	return cliproxyapi.New(f.URL, secret, nil), f
}

func TestClientSendsBearerSecret(t *testing.T) {
	f := cliproxyapitest.NewServer(t, "s")
	ctx := context.Background()
	if _, err := cliproxyapi.New(f.URL, "s", nil).Credentials(ctx); err != nil {
		t.Fatalf("Credentials with the right secret: %v", err)
	}
	_, err := cliproxyapi.New(f.URL, "wrong", nil).Credentials(ctx)
	var e *cliproxyapi.Error
	if !errors.As(err, &e) || e.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Credentials with a wrong secret: err = %v, want *Error 401", err)
	}
	if e.Message == "" {
		t.Error("401 has no message")
	}
}

func TestStartOAuth(t *testing.T) {
	c, _ := newClient(t)
	tests := []struct {
		engine string
		device bool
	}{
		{"codex", false},
		{"claude", false},
		{"antigravity", false},
		{"devin", false},
		{"xai", true},
		{"kimi", true},
		{"kimi-ai", true},
		{"meta", true},
	}
	seen := map[string]bool{}
	for _, tt := range tests {
		t.Run(tt.engine, func(t *testing.T) {
			got, err := c.StartOAuth(context.Background(), tt.engine)
			if err != nil {
				t.Fatalf("StartOAuth: %v", err)
			}
			if got.URL == "" || got.State == "" {
				t.Errorf("StartOAuth = %+v, want URL and State", got)
			}
			if seen[got.State] {
				t.Errorf("state %q repeated", got.State)
			}
			seen[got.State] = true
			if tt.device {
				if got.Flow != "device" || got.UserCode == "" || got.ExpiresIn <= 0 {
					t.Errorf("device StartOAuth = %+v, want flow device, user code, expiry", got)
				}
			} else if got.Flow != "" || got.UserCode != "" || got.ExpiresIn != 0 {
				t.Errorf("paste StartOAuth = %+v, want URL and State only", got)
			}
		})
	}

	_, err := c.StartOAuth(context.Background(), "nope")
	var e *cliproxyapi.Error
	if !errors.As(err, &e) || e.StatusCode != http.StatusNotFound {
		t.Errorf("StartOAuth(nope) err = %v, want *Error 404", err)
	}
}

func TestOAuthStatusLifecycle(t *testing.T) {
	c, f := newClient(t)
	ctx := context.Background()
	status := func(state string) cliproxyapi.OAuthStatus {
		t.Helper()
		st, err := c.OAuthStatus(ctx, state)
		if err != nil {
			t.Fatalf("OAuthStatus(%s): %v", state, err)
		}
		return st
	}

	a, err := c.StartOAuth(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if got := status(a.State); got != (cliproxyapi.OAuthStatus{Status: "wait"}) {
		t.Errorf("fresh status = %+v, want wait", got)
	}
	f.FinishLogin(a.State, cliproxyapi.Credential{Name: "codex-a@x.json", Provider: "codex", Email: "a@x"})
	if got := status(a.State); got != (cliproxyapi.OAuthStatus{Status: "ok"}) {
		t.Errorf("finished status = %+v, want ok", got)
	}
	if _, _, ok := f.Credential("codex-a@x.json"); !ok {
		t.Error("FinishLogin did not add the credential")
	}

	b, err := c.StartOAuth(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	f.FailLogin(b.State, "msg")
	if got := status(b.State); got != (cliproxyapi.OAuthStatus{Status: "error", Error: "msg"}) {
		t.Errorf("failed status = %+v, want error msg", got)
	}

	d, err := c.StartOAuth(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	f.Forget()
	if got := status(d.State); got != (cliproxyapi.OAuthStatus{Status: "error", Error: "unknown or expired state"}) {
		t.Errorf("forgotten status = %+v, want unknown or expired state", got)
	}

	// A state the engine refuses to look up is an *Error, not a status.
	_, err = c.OAuthStatus(ctx, "a/b")
	var e *cliproxyapi.Error
	if !errors.As(err, &e) || e.StatusCode != http.StatusBadRequest {
		t.Errorf("OAuthStatus(a/b) err = %v, want *Error 400", err)
	}
}

func TestCancelOAuth(t *testing.T) {
	c, _ := newClient(t)
	ctx := context.Background()
	a, err := c.StartOAuth(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CancelOAuth(ctx, a.State); err != nil {
		t.Fatalf("CancelOAuth: %v", err)
	}
	st, err := c.OAuthStatus(ctx, a.State)
	if err != nil || st.Status != "error" {
		t.Errorf("status after cancel = %+v, %v, want error", st, err)
	}
	// Cancelling again is not an error: the engine answers ok with cancelled false.
	if err := c.CancelOAuth(ctx, a.State); err != nil {
		t.Errorf("second CancelOAuth: %v", err)
	}
}

func TestSubmitOAuthCallbackSendsRedirectURL(t *testing.T) {
	c, f := newClient(t)
	ctx := context.Background()
	a, err := c.StartOAuth(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	redirect := "http://localhost:1455/auth/callback?code=abc&state=" + a.State
	if err := c.SubmitOAuthCallback(ctx, a.State, redirect); err != nil {
		t.Fatalf("SubmitOAuthCallback: %v", err)
	}
	if gotState, gotURL := f.LastCallback(); gotState != a.State || gotURL != redirect {
		t.Errorf("LastCallback = (%q, %q), want (%q, %q)", gotState, gotURL, a.State, redirect)
	}

	err = c.SubmitOAuthCallback(ctx, "unknown", "http://localhost:1455/auth/callback?code=abc&state=unknown")
	var e *cliproxyapi.Error
	if !errors.As(err, &e) || e.StatusCode != http.StatusNotFound {
		t.Errorf("unknown state err = %v, want *Error 404", err)
	}
	err = c.SubmitOAuthCallback(ctx, a.State, "http://localhost:1455/auth/callback?state="+a.State)
	if !errors.As(err, &e) || e.StatusCode != http.StatusBadRequest {
		t.Errorf("no code err = %v, want *Error 400", err)
	}
}

func TestCredentialsDecode(t *testing.T) {
	c, f := newClient(t)
	retry := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	observed := time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC)
	want := cliproxyapi.Credential{
		ID:             "codex-a@x.json",
		AuthIndex:      "idx-1",
		Name:           "codex-a@x.json",
		Provider:       "codex",
		Label:          "a@x",
		Email:          "a@x",
		Status:         "error",
		StatusMessage:  "quota",
		Unavailable:    true,
		NextRetryAfter: &retry,
		Quota:          cliproxyapi.Quota{ObservedAt: &observed, Signals: map[string]string{"x": "y"}},
		Cooldowns: []cliproxyapi.Cooldown{{
			Scope: "model", ModelKey: "gpt-x", Reason: "quota", RetryAt: retry, RemainingSeconds: 60, HTTPStatus: 429,
		}},
		Success:   3,
		Failed:    1,
		UpdatedAt: observed,
	}
	f.AddCredential(want)
	f.AddCredential(cliproxyapi.Credential{Name: "claude-b.json", Provider: "claude"})

	got, err := c.Credentials(context.Background())
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Credentials len = %d, want 2", len(got))
	}
	// The engine lists by name.
	if got[0].Name != "claude-b.json" || got[1].Name != "codex-a@x.json" {
		t.Errorf("order = %q, %q", got[0].Name, got[1].Name)
	}
	if !reflect.DeepEqual(got[1], want) {
		t.Errorf("credential round trip:\n got %+v\nwant %+v", got[1], want)
	}
	if got[0].AuthIndex == "" || got[0].ID != "claude-b.json" {
		t.Errorf("AddCredential defaults: %+v", got[0])
	}
}

func TestCredentialModels(t *testing.T) {
	c, f := newClient(t)
	f.AddCredential(cliproxyapi.Credential{Name: "codex-a.json", Provider: "codex"})
	f.SetModels("codex-a.json", "gpt-x", "gpt-y")
	got, err := c.CredentialModels(context.Background(), "codex-a.json")
	if err != nil {
		t.Fatalf("CredentialModels: %v", err)
	}
	if !slices.Equal(got, []string{"gpt-x", "gpt-y"}) {
		t.Errorf("models = %v", got)
	}
	// The engine answers an empty list for an unknown name.
	got, err = c.CredentialModels(context.Background(), "nope.json")
	if err != nil || len(got) != 0 {
		t.Errorf("unknown credential models = %v, %v", got, err)
	}
}

func TestCredentialMutations(t *testing.T) {
	c, f := newClient(t)
	ctx := context.Background()
	f.AddCredential(cliproxyapi.Credential{Name: "codex-a.json", Provider: "codex", AuthIndex: "i1"})

	if err := c.SetCredentialDisabled(ctx, "codex-a.json", true); err != nil {
		t.Fatalf("SetCredentialDisabled: %v", err)
	}
	if cr, _, _ := f.Credential("codex-a.json"); !cr.Disabled || cr.Status != "disabled" {
		t.Errorf("after disable: %+v", cr)
	}
	if err := c.SetCredentialDisabled(ctx, "codex-a.json", false); err != nil {
		t.Fatalf("SetCredentialDisabled(false): %v", err)
	}
	if cr, _, _ := f.Credential("codex-a.json"); cr.Disabled || cr.Status != "active" {
		t.Errorf("after enable: %+v", cr)
	}
	if err := c.SetCredentialPrefix(ctx, "codex-a.json", "codex"); err != nil {
		t.Fatalf("SetCredentialPrefix: %v", err)
	}
	if _, prefix, _ := f.Credential("codex-a.json"); prefix != "codex" {
		t.Errorf("prefix = %q, want codex", prefix)
	}
	if err := c.RefreshCredential(ctx, "codex-a.json"); err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	if n := f.Calls("/credentials/refresh"); n != 1 {
		t.Errorf("refresh calls = %d, want 1", n)
	}
	if err := c.ResetCooldown(ctx, "i1"); err != nil {
		t.Fatalf("ResetCooldown: %v", err)
	}
	if n := f.Calls("/routing/cooldown/reset"); n != 1 {
		t.Errorf("reset calls = %d, want 1", n)
	}
	if err := c.DeleteCredential(ctx, "codex-a.json"); err != nil {
		t.Fatalf("DeleteCredential: %v", err)
	}
	if _, _, ok := f.Credential("codex-a.json"); ok {
		t.Error("credential still present after delete")
	}

	// Every mutation on a missing credential is a 404.
	var e *cliproxyapi.Error
	for name, err := range map[string]error{
		"status":  c.SetCredentialDisabled(ctx, "codex-a.json", true),
		"fields":  c.SetCredentialPrefix(ctx, "codex-a.json", "codex"),
		"refresh": c.RefreshCredential(ctx, "codex-a.json"),
		"reset":   c.ResetCooldown(ctx, "i1"),
		"delete":  c.DeleteCredential(ctx, "codex-a.json"),
	} {
		if !errors.As(err, &e) || e.StatusCode != http.StatusNotFound {
			t.Errorf("%s on a missing credential: err = %v, want *Error 404", name, err)
		}
	}
}

func TestUploadCredential(t *testing.T) {
	c, f := newClient(t)
	ctx := context.Background()
	data := []byte(`{"type":"codex","email":"c@x","access_token":"t","prefix":"codex"}`)
	if err := c.UploadCredential(ctx, "codex-c@x.json", data); err != nil {
		t.Fatalf("UploadCredential: %v", err)
	}
	cr, prefix, ok := f.Credential("codex-c@x.json")
	if !ok || cr.Provider != "codex" || cr.Email != "c@x" || cr.Label != "c@x" || cr.AuthIndex == "" || prefix != "codex" {
		t.Errorf("uploaded = %+v prefix %q ok %v", cr, prefix, ok)
	}
	var e *cliproxyapi.Error
	if err := c.UploadCredential(ctx, "codex.txt", data); !errors.As(err, &e) || e.StatusCode != http.StatusBadRequest {
		t.Errorf("non-.json name: err = %v, want *Error 400", err)
	}
	if err := c.UploadCredential(ctx, "x.json", []byte("not json")); !errors.As(err, &e) || e.StatusCode != http.StatusInternalServerError {
		t.Errorf("bad body: err = %v, want *Error 500", err)
	}
}

func TestConfigPatch(t *testing.T) {
	c, f := newClient(t)
	ctx := context.Background()
	patch := map[string]any{"routing": map[string]any{"force-model-prefix": true, "strategy": "round-robin"}}
	if err := c.PatchConfig(ctx, patch); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}
	if err := c.PatchConfig(ctx, map[string]any{"access": map[string]any{"api-keys": []string{"k"}}}); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}
	patches := f.ConfigPatches()
	if len(patches) != 2 {
		t.Fatalf("ConfigPatches len = %d, want 2", len(patches))
	}
	raw, err := c.Config(ctx)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	var cfg struct {
		Access struct {
			APIKeys []string `json:"api-keys"`
		} `json:"access"`
		Routing struct {
			ForceModelPrefix bool   `json:"force-model-prefix"`
			Strategy         string `json:"strategy"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !cfg.Routing.ForceModelPrefix || cfg.Routing.Strategy != "round-robin" || !slices.Equal(cfg.Access.APIKeys, []string{"k"}) {
		t.Errorf("merged config = %s", raw)
	}
	// PATCH replaces lists.
	if err := c.PatchConfig(ctx, map[string]any{"access": map[string]any{"api-keys": []string{"k2"}}}); err != nil {
		t.Fatal(err)
	}
	raw, _ = c.Config(ctx)
	if err := json.Unmarshal(raw, &cfg); err != nil || !slices.Equal(cfg.Access.APIKeys, []string{"k2"}) || !cfg.Routing.ForceModelPrefix {
		t.Errorf("config after list patch = %s", raw)
	}

	var e *cliproxyapi.Error
	if err := c.PatchConfig(ctx, []string{"x"}); !errors.As(err, &e) || e.StatusCode != http.StatusBadRequest {
		t.Errorf("non-object patch: err = %v, want *Error 400", err)
	}
}

func TestErrorNeverNamesEngine(t *testing.T) {
	// A raw server that answers like the engine does when a path under its data directory fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"write_failed","message":"open /data/cliproxyapi/auths/x.json: CLIProxyAPI denied"}`))
	}))
	t.Cleanup(srv.Close)
	c := cliproxyapi.New(srv.URL, secret, nil)
	_, err := c.Credentials(context.Background())
	var e *cliproxyapi.Error
	if !errors.As(err, &e) || e.StatusCode != http.StatusInternalServerError {
		t.Fatalf("err = %v, want *Error 500", err)
	}
	if !strings.Contains(e.Message, "write_failed") || !strings.Contains(e.Message, "denied") {
		t.Errorf("message lost detail: %q", e.Message)
	}

	fc, f := newClient(t)
	errs := []error{err}
	_, e1 := cliproxyapi.New(f.URL, "wrong", nil).Credentials(context.Background())
	_, e2 := fc.StartOAuth(context.Background(), "nope")
	e3 := fc.DeleteCredential(context.Background(), "nope.json")
	errs = append(errs, e1, e2, e3)
	for _, err := range errs {
		if err == nil {
			t.Fatal("expected an error")
		}
		if s := strings.ToLower(err.Error()); strings.Contains(s, "cliproxy") {
			t.Errorf("error names the engine: %q", err.Error())
		}
		if !strings.HasPrefix(err.Error(), "oauth engine: ") {
			t.Errorf("error %q lacks the neutral prefix", err.Error())
		}
	}
}

// registrationEngine serves a credential whose models appear only after patchesNeeded fields patches, like the
// engine after it dropped the registration of a fresh upload. patches reports how many it received.
func registrationEngine(t *testing.T, patchesNeeded int) (c *cliproxyapi.Client, patches func() int) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "PATCH /v8/management/credentials/fields":
			n++
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "GET /v8/management/credentials/models":
			if n >= patchesNeeded {
				_, _ = w.Write([]byte(`{"models":[{"id":"gpt-x"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"models":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return cliproxyapi.New(srv.URL, secret, nil), func() int { mu.Lock(); defer mu.Unlock(); return n }
}

func TestAwaitCredentialModels(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name          string
		patchesNeeded int
		want          []string
		wantPatches   int
	}{
		{"registered at once", 0, []string{"gpt-x"}, 0},
		{"registration dropped", 1, []string{"gpt-x"}, 1},
		{"never registered", 100, []string{}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, patches := registrationEngine(t, tt.patchesNeeded)
			got, err := c.AwaitCredentialModels(ctx, "codex-a.json", "codex", 20*time.Millisecond)
			if err != nil || !slices.Equal(got, tt.want) || patches() != tt.wantPatches {
				t.Errorf("models %v, err %v, patches %d; want %v, %d patches", got, err, patches(), tt.want, tt.wantPatches)
			}
		})
	}
}

func TestAwaitCredentialModelsBoundsSlowReads(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	start := time.Now()
	got, err := cliproxyapi.New(srv.URL, "s", nil).AwaitCredentialModels(context.Background(), "codex-a.json", "codex", 20*time.Millisecond)
	if err != nil || len(got) != 0 || time.Since(start) > time.Second {
		t.Fatalf("models %v, err %v after %v; want none within the rounds", got, err, time.Since(start))
	}
}

func TestSetCredentialPriority(t *testing.T) {
	c, f := newClient(t)
	ctx := context.Background()
	f.AddCredential(cliproxyapi.Credential{Name: "x.json", Provider: "codex"})
	if err := c.SetCredentialPriority(ctx, "x.json", 3); err != nil {
		t.Fatalf("SetCredentialPriority: %v", err)
	}
	if cr, _, _ := f.Credential("x.json"); cr.Priority != 3 {
		t.Errorf("fake priority = %d, want 3", cr.Priority)
	}
	creds, err := c.Credentials(ctx)
	if err != nil || len(creds) != 1 || creds[0].Priority != 3 {
		t.Errorf("Credentials = %+v, %v; want priority 3", creds, err)
	}
	var e *cliproxyapi.Error
	if err := c.SetCredentialPriority(ctx, "missing.json", 1); !errors.As(err, &e) || e.StatusCode != http.StatusNotFound {
		t.Errorf("missing credential: err = %v, want 404", err)
	}
}
