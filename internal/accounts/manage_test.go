package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/engine/cliproxyapi/cliproxyapitest"
)

// newFaultyEnv puts a proxy in front of the fake that answers 500 to the given "METHOD /path" routes (path below
// the management prefix) and passes everything else through.
func newFaultyEnv(t *testing.T, faults ...string) *env {
	t.Helper()
	fake := cliproxyapitest.NewServer(t, testSecret)
	target, err := url.Parse(fake.URL)
	if err != nil {
		t.Fatal(err)
	}
	rp := httputil.NewSingleHostReverseProxy(target)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/v8/management")
		if slices.Contains(faults, route) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return newEnvAt(t, fake, srv.URL)
}

func TestListMergesEngineState(t *testing.T) {
	e := newEnv(t)
	retry := time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC)
	e.fake.AddCredential(cliproxyapi.Credential{
		Name: "codex-a.json", Provider: "codex", Email: "a@x",
		Status: "error", StatusMessage: "quota exhausted", Unavailable: true, NextRetryAfter: &retry,
		Cooldowns: []cliproxyapi.Cooldown{{Scope: "model", ModelKey: "gpt-x", Reason: "quota", RetryAt: retry, RemainingSeconds: 60, HTTPStatus: 429}},
		Quota:     cliproxyapi.Quota{Signals: map[string]string{"plan": "plus"}},
		Success:   7, Failed: 2,
	})
	e.fake.SetModels("codex-a.json", "gpt-x")
	row := e.addRow(t, "codex", "a@x", "codex-a.json", true)

	got, err := e.svc.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List len = %d, want 1", len(got))
	}
	a := got[0]
	if a.ID != row.ID || a.Provider != "codex" || a.Label != "a@x" || !a.Enabled {
		t.Errorf("row fields = %+v", a.Account)
	}
	if a.Status != "error" || a.StatusMessage != "quota exhausted" || !a.Unavailable {
		t.Errorf("status = %q %q %v", a.Status, a.StatusMessage, a.Unavailable)
	}
	if a.NextRetryAfter == nil || !a.NextRetryAfter.Equal(retry) {
		t.Errorf("NextRetryAfter = %v, want %v", a.NextRetryAfter, retry)
	}
	if len(a.Cooldowns) != 1 || a.Cooldowns[0].ModelKey != "gpt-x" || a.Cooldowns[0].HTTPStatus != 429 {
		t.Errorf("Cooldowns = %+v", a.Cooldowns)
	}
	if a.Quota["plan"] != "plus" || a.Success != 7 || a.Failed != 2 {
		t.Errorf("Quota = %v, Success = %d, Failed = %d", a.Quota, a.Success, a.Failed)
	}
	if !slices.Equal(a.Models, []string{"codex/gpt-x"}) {
		t.Errorf("Models = %v, want [codex/gpt-x]", a.Models)
	}
}

func TestListKeepsPrefixedModel(t *testing.T) {
	e := newEnv(t)
	e.fake.AddCredential(cliproxyapi.Credential{Name: "xai-a.json", Provider: "xai"})
	e.fake.SetModels("xai-a.json", "grok/grok-5", "grok-5-mini")
	e.addRow(t, "grok", "", "xai-a.json", true)
	got, err := e.svc.List(context.Background(), "grok")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || !slices.Equal(got[0].Models, []string{"grok/grok-5", "grok/grok-5-mini"}) {
		t.Errorf("List = %+v", got)
	}
}

func TestListMissingCredential(t *testing.T) {
	e := newEnv(t)
	e.addRow(t, "codex", "a@x", "codex-gone.json", true)
	got, err := e.svc.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Status != "missing" || len(got[0].Models) != 0 {
		t.Errorf("List = %+v, want one missing account", got)
	}
	if n := e.fake.Calls("/credentials/models"); n != 0 {
		t.Errorf("models calls = %d, want 0 for a missing credential", n)
	}
}

func TestListFiltersProvider(t *testing.T) {
	e := newEnv(t)
	e.addRow(t, "codex", "a", "codex-a.json", true)
	e.addRow(t, "grok", "b", "xai-b.json", true)
	got, err := e.svc.List(context.Background(), "grok")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Provider != "grok" {
		t.Errorf("List(grok) = %+v", got)
	}
	all, err := e.svc.List(context.Background(), "")
	if err != nil || len(all) != 2 {
		t.Errorf("List(\"\") = %d accounts, %v; want 2", len(all), err)
	}
	if _, err := e.svc.List(context.Background(), "nope"); !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("List(nope) err = %v, want ErrUnknownProvider", err)
	}
}

func TestSetEnabledEngineFirst(t *testing.T) {
	e := newFaultyEnv(t, "PATCH /credentials/status")
	e.fake.AddCredential(cliproxyapi.Credential{Name: "codex-a.json", Provider: "codex"})
	row := e.addRow(t, "codex", "a", "codex-a.json", true)
	if err := e.svc.SetEnabled(context.Background(), row.ID, false); err == nil {
		t.Fatal("SetEnabled succeeded, want the engine's error")
	}
	if got, _ := e.store.Account(context.Background(), row.ID); !got.Enabled {
		t.Error("store row disabled although the engine call failed")
	}
}

func TestSetEnabled(t *testing.T) {
	e := newEnv(t)
	e.fake.AddCredential(cliproxyapi.Credential{Name: "codex-a.json", Provider: "codex"})
	row := e.addRow(t, "codex", "a", "codex-a.json", true)
	ctx := context.Background()
	if err := e.svc.SetEnabled(ctx, row.ID, false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	if c, _, _ := e.fake.Credential("codex-a.json"); !c.Disabled {
		t.Error("engine credential not disabled")
	}
	if got, _ := e.store.Account(ctx, row.ID); got.Enabled {
		t.Error("store row still enabled")
	}
	if err := e.svc.SetEnabled(ctx, row.ID, true); err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}
	if c, _, _ := e.fake.Credential("codex-a.json"); c.Disabled {
		t.Error("engine credential still disabled")
	}
	if got, _ := e.store.Account(ctx, row.ID); !got.Enabled {
		t.Error("store row still disabled")
	}
}

func TestSetEnabledMissingCredential(t *testing.T) {
	e := newEnv(t)
	row := e.addRow(t, "codex", "a", "codex-gone.json", true)
	if err := e.svc.SetEnabled(context.Background(), row.ID, false); !errors.Is(err, ErrCredentialMissing) {
		t.Errorf("err = %v, want ErrCredentialMissing", err)
	}
}

func TestDelete(t *testing.T) {
	e := newEnv(t)
	e.fake.AddCredential(cliproxyapi.Credential{Name: "codex-a.json", Provider: "codex"})
	row := e.addRow(t, "codex", "a", "codex-a.json", true)
	if err := e.svc.Delete(context.Background(), row.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, ok := e.fake.Credential("codex-a.json"); ok {
		t.Error("engine credential still there")
	}
	if rows := e.rows(t); len(rows) != 0 {
		t.Errorf("store = %+v, want empty", rows)
	}
}

func TestDeleteToleratesEngine404(t *testing.T) {
	e := newEnv(t)
	row := e.addRow(t, "codex", "a", "codex-gone.json", true)
	if err := e.svc.Delete(context.Background(), row.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if rows := e.rows(t); len(rows) != 0 {
		t.Errorf("store = %+v, want empty", rows)
	}
}

func TestDeleteEngineFailureKeepsRow(t *testing.T) {
	e := newFaultyEnv(t, "DELETE /credentials")
	e.fake.AddCredential(cliproxyapi.Credential{Name: "codex-a.json", Provider: "codex"})
	row := e.addRow(t, "codex", "a", "codex-a.json", true)
	if err := e.svc.Delete(context.Background(), row.ID); err == nil {
		t.Fatal("Delete succeeded, want the engine's error")
	}
	if rows := e.rows(t); len(rows) != 1 {
		t.Errorf("store has %d rows, want the row kept", len(rows))
	}
}

func TestResetCooldownUsesAuthIndex(t *testing.T) {
	e := newEnv(t)
	retry := time.Now().Add(time.Hour).UTC()
	e.fake.AddCredential(cliproxyapi.Credential{
		Name: "codex-a.json", Provider: "codex", AuthIndex: "ab12", Unavailable: true,
		Cooldowns: []cliproxyapi.Cooldown{{Scope: "credential", Reason: "quota", RetryAt: retry}},
	})
	row := e.addRow(t, "codex", "a", "codex-a.json", true)
	if err := e.svc.ResetCooldown(context.Background(), row.ID); err != nil {
		t.Fatalf("ResetCooldown: %v", err)
	}
	c, _, _ := e.fake.Credential("codex-a.json")
	if len(c.Cooldowns) != 0 || c.Unavailable {
		t.Errorf("credential = %+v, want cooldowns cleared through auth index ab12", c)
	}
	if n := e.fake.Calls("/routing/cooldown/reset"); n != 1 {
		t.Errorf("reset calls = %d, want 1", n)
	}
}

func TestResetCooldownMissingCredential(t *testing.T) {
	e := newEnv(t)
	row := e.addRow(t, "codex", "a", "codex-gone.json", true)
	if err := e.svc.ResetCooldown(context.Background(), row.ID); !errors.Is(err, ErrCredentialMissing) {
		t.Errorf("err = %v, want ErrCredentialMissing", err)
	}
	if n := e.fake.Calls("/routing/cooldown/reset"); n != 0 {
		t.Errorf("reset calls = %d, want 0", n)
	}
}

func TestNotFound(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"SetEnabled":    func() error { return e.svc.SetEnabled(ctx, 999, false) },
		"Delete":        func() error { return e.svc.Delete(ctx, 999) },
		"ResetCooldown": func() error { return e.svc.ResetCooldown(ctx, 999) },
	} {
		if err := call(); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s(999) err = %v, want ErrNotFound", name, err)
		}
	}
	for _, path := range []string{"/credentials", "/credentials/status", "/routing/cooldown/reset"} {
		if n := e.fake.Calls(path); n != 0 {
			t.Errorf("%s calls = %d, want 0", path, n)
		}
	}
}

func TestImport(t *testing.T) {
	e := newEnv(t)
	a, err := e.svc.Import(context.Background(), "codex", []byte(`{"type":"codex","email":"a@x"}`))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if a.Provider != "codex" || a.Label != "a@x" || !a.Enabled || a.ID == 0 {
		t.Errorf("Account = %+v", a.Account)
	}
	if !strings.HasPrefix(a.Credential, "codex-") || !strings.HasSuffix(a.Credential, ".json") || len(a.Credential) != len("codex-12345678.json") {
		t.Errorf("Credential = %q, want codex-<8 hex>.json", a.Credential)
	}
	c, prefix, ok := e.fake.Credential(a.Credential)
	if !ok || prefix != "codex" || c.Provider != "codex" {
		t.Errorf("engine credential = %+v prefix %q ok %v", c, prefix, ok)
	}
	if rows := e.rows(t); len(rows) != 1 || rows[0].Provider != "codex" || rows[0].Credential != a.Credential {
		t.Errorf("store = %+v", rows)
	}
}

func TestImportFillsEngineType(t *testing.T) {
	e := newEnv(t)
	a, err := e.svc.Import(context.Background(), "grok", []byte(`{"email":"g@x","refresh_token":"r"}`))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	c, prefix, ok := e.fake.Credential(a.Credential)
	if !ok || c.Provider != "xai" || prefix != "grok" {
		t.Errorf("engine credential = %+v prefix %q, want provider xai prefix grok", c, prefix)
	}
	if _, err := e.svc.Import(context.Background(), "grok", []byte(`{"type":"codex"}`)); err == nil {
		t.Error("Import of another provider's credential succeeded")
	}
}

func TestImportRollsBack(t *testing.T) {
	for _, fault := range []string{"POST /credentials", "PATCH /credentials/fields"} {
		t.Run(fault, func(t *testing.T) {
			e := newFaultyEnv(t, fault)
			if _, err := e.svc.Import(context.Background(), "codex", []byte(`{"type":"codex"}`)); err == nil {
				t.Fatal("Import succeeded, want the engine's error")
			}
			if rows := e.rows(t); len(rows) != 0 {
				t.Errorf("store = %+v, want empty", rows)
			}
		})
	}
}

func TestImportRejectsNonObject(t *testing.T) {
	e := newEnv(t)
	for _, raw := range []string{"[]", "x", "null", `"s"`, ""} {
		if _, err := e.svc.Import(context.Background(), "codex", []byte(raw)); err == nil {
			t.Errorf("Import(%q) succeeded", raw)
		}
	}
	if _, err := e.svc.Import(context.Background(), "cline", []byte(`{}`)); !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("Import(cline) err = %v, want ErrUnknownProvider", err)
	}
	if n := e.fake.Calls("/credentials"); n != 0 {
		t.Errorf("credentials calls = %d, want 0", n)
	}
	if rows := e.rows(t); len(rows) != 0 {
		t.Errorf("store = %+v, want empty", rows)
	}
}

func TestManageErrorsNeverNameEngine(t *testing.T) {
	for _, err := range []error{ErrNotFound, ErrCredentialMissing} {
		text := strings.ToLower(err.Error())
		if strings.Contains(text, "cliproxy") || strings.Contains(text, "bifrost") {
			t.Errorf("%q names an engine", err)
		}
	}
}
