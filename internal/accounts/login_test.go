package accounts

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/engine/cliproxyapi"
)

func TestStartLoginPaste(t *testing.T) {
	e := newEnv(t)
	l := e.start(t, "codex")
	if l.Mode != "paste" || l.URL == "" || l.State == "" || l.Provider != "codex" {
		t.Errorf("login = %+v, want paste with URL and state", l)
	}
	if want := e.clock.now().Add(sessionTTL); !l.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v (engine session TTL)", l.ExpiresAt, want)
	}
	if l.UserCode != "" {
		t.Errorf("UserCode = %q, want empty for a paste flow", l.UserCode)
	}
}

func TestStartLoginDevice(t *testing.T) {
	e := newEnv(t)
	l := e.start(t, "grok")
	if l.Mode != "device" || l.UserCode == "" || l.URL == "" || l.Provider != "grok" {
		t.Errorf("login = %+v, want device flow with a user code", l)
	}
	if !l.ExpiresAt.After(e.clock.now()) {
		t.Errorf("ExpiresAt = %v, want in the future", l.ExpiresAt)
	}
}

func TestStartLoginUnknownProvider(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"cline", "xai", ""} {
		if _, err := e.svc.StartLogin(context.Background(), p); !errors.Is(err, ErrUnknownProvider) {
			t.Errorf("StartLogin(%q) err = %v, want ErrUnknownProvider", p, err)
		}
	}
	if n := e.fake.Calls("/oauth/auth-url"); n != 0 {
		t.Errorf("auth-url calls = %d, want 0", n)
	}
}

func TestCompleteLoginRejectsBadRedirect(t *testing.T) {
	e := newEnv(t)
	l := e.start(t, "codex")
	for _, raw := range []string{
		"",
		"   ",
		"not a url",
		"http://localhost:1455/auth/callback",
		"http://localhost:1455/auth/callback?state=" + l.State,
		"http://localhost:1455/auth/callback?code=x&state=OTHER",
		"http://%zz/?code=x",
		"FAKE-0001",
	} {
		err := e.svc.CompleteLogin(context.Background(), l.State, raw)
		if !errors.Is(err, ErrBadRedirect) {
			t.Errorf("CompleteLogin(%q) err = %v, want ErrBadRedirect", raw, err)
		}
	}
	if state, _ := e.fake.LastCallback(); state != "" {
		t.Errorf("engine received a callback for %q, want none", state)
	}
	if n := e.fake.Calls("/oauth/callback"); n != 0 {
		t.Errorf("callback calls = %d, want 0", n)
	}
}

func TestCompleteLoginDeviceFlow(t *testing.T) {
	e := newEnv(t)
	l := e.start(t, "grok")
	err := e.svc.CompleteLogin(context.Background(), l.State, "http://localhost/cb?code=c&state="+l.State)
	if !errors.Is(err, ErrNotPasteFlow) {
		t.Errorf("err = %v, want ErrNotPasteFlow", err)
	}
	if n := e.fake.Calls("/oauth/callback"); n != 0 {
		t.Errorf("callback calls = %d, want 0", n)
	}
}

func TestCompleteLoginForwardsURL(t *testing.T) {
	e := newEnv(t)
	l := e.start(t, "codex")
	u := "http://localhost:1455/auth/callback?code=c&state=" + l.State
	if err := e.svc.CompleteLogin(context.Background(), l.State, "  "+u+"\n"); err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	state, redirect := e.fake.LastCallback()
	if state != l.State || redirect != u {
		t.Errorf("callback = (%q, %q), want (%q, %q)", state, redirect, l.State, u)
	}
}

func TestCompleteLoginAcceptsProviderError(t *testing.T) {
	e := newEnv(t)
	l := e.start(t, "codex")
	// The provider's own refusal is a valid sign-in address: the engine records it as the login's outcome.
	if err := e.svc.CompleteLogin(context.Background(), l.State, "http://localhost:1455/auth/callback?error=access_denied"); err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
}

func TestCompleteLoginEngineForgot(t *testing.T) {
	e := newEnv(t)
	l := e.start(t, "codex")
	e.fake.Forget()
	err := e.svc.CompleteLogin(context.Background(), l.State, "http://localhost:1455/auth/callback?code=c")
	if !errors.Is(err, ErrLoginExpired) {
		t.Errorf("err = %v, want ErrLoginExpired", err)
	}
}

func TestCompleteLoginUnknownState(t *testing.T) {
	e := newEnv(t)
	err := e.svc.CompleteLogin(context.Background(), "never", "http://localhost/cb?code=c")
	if !errors.Is(err, ErrLoginNotFound) {
		t.Errorf("err = %v, want ErrLoginNotFound", err)
	}
}

func TestLoginStatusWait(t *testing.T) {
	e := newEnv(t)
	l := e.start(t, "codex")
	st, err := e.svc.LoginStatus(context.Background(), l.State)
	if err != nil || st.Status != "wait" || st.Account != nil {
		t.Errorf("LoginStatus = %+v, %v; want wait", st, err)
	}
}

func TestLoginStatusAdoptsCredential(t *testing.T) {
	e := newEnv(t)
	l := e.start(t, "codex")
	e.fake.FinishLogin(l.State, cliproxyapi.Credential{Name: "codex-a@x.json", Provider: "codex", Email: "a@x"})

	st, err := e.svc.LoginStatus(context.Background(), l.State)
	if err != nil {
		t.Fatalf("LoginStatus: %v", err)
	}
	if st.Status != "ok" || st.Account == nil {
		t.Fatalf("LoginStatus = %+v, want ok with an account", st)
	}
	if st.Account.Provider != "codex" || st.Account.Label != "a@x" || st.Account.Credential != "codex-a@x.json" || !st.Account.Enabled {
		t.Errorf("Account = %+v", st.Account.Account)
	}
	if _, prefix, _ := e.fake.Credential("codex-a@x.json"); prefix != "codex" {
		t.Errorf("prefix = %q, want codex", prefix)
	}
	if rows := e.rows(t); len(rows) != 1 || rows[0].ID != st.Account.ID {
		t.Errorf("store = %+v, want the one adopted row", rows)
	}
}

func TestLoginStatusAdoptsDeviceProviderByUserName(t *testing.T) {
	e := newEnv(t)
	l := e.start(t, "grok")
	// A credential of another provider that is not in the store is not this login's to adopt.
	e.fake.AddCredential(cliproxyapi.Credential{Name: "codex-other.json", Provider: "codex"})
	e.fake.FinishLogin(l.State, cliproxyapi.Credential{Name: "xai-b.json", Provider: "xai", Label: "b"})

	st, err := e.svc.LoginStatus(context.Background(), l.State)
	if err != nil || st.Status != "ok" {
		t.Fatalf("LoginStatus = %+v, %v", st, err)
	}
	if st.Account.Provider != "grok" || st.Account.Label != "b" {
		t.Errorf("Account = %+v, want provider grok label b", st.Account.Account)
	}
	if _, prefix, _ := e.fake.Credential("xai-b.json"); prefix != "grok" {
		t.Errorf("prefix = %q, want grok", prefix)
	}
	if rows := e.rows(t); len(rows) != 1 {
		t.Errorf("store = %+v, want 1 row", rows)
	}
}

func TestLoginStatusRelogin(t *testing.T) {
	e := newEnv(t)
	old := time.Now().Add(-time.Hour).UTC()
	e.fake.AddCredential(cliproxyapi.Credential{Name: "codex-a@x.json", Provider: "codex", Email: "a@x", UpdatedAt: old})
	e.fake.AddCredential(cliproxyapi.Credential{Name: "codex-b@x.json", Provider: "codex", Email: "b@x", UpdatedAt: old})
	existing := e.addRow(t, "codex", "a@x", "codex-a@x.json", true)
	e.addRow(t, "codex", "b@x", "codex-b@x.json", true)

	l := e.start(t, "codex")
	e.fake.FinishLogin(l.State, cliproxyapi.Credential{Name: "codex-a@x.json", Provider: "codex", Email: "a@x", UpdatedAt: time.Now().UTC()})

	st, err := e.svc.LoginStatus(context.Background(), l.State)
	if err != nil || st.Status != "ok" {
		t.Fatalf("LoginStatus = %+v, %v", st, err)
	}
	if st.Account.ID != existing.ID {
		t.Errorf("Account.ID = %d, want the existing %d", st.Account.ID, existing.ID)
	}
	if rows := e.rows(t); len(rows) != 2 {
		t.Errorf("store has %d rows, want 2", len(rows))
	}
	if _, prefix, _ := e.fake.Credential("codex-a@x.json"); prefix != "codex" {
		t.Errorf("prefix = %q, want codex", prefix)
	}
}

func TestLoginStatusIdempotent(t *testing.T) {
	e := newEnv(t)
	l := e.start(t, "codex")
	e.fake.FinishLogin(l.State, cliproxyapi.Credential{Name: "codex-a@x.json", Provider: "codex", Email: "a@x"})
	first, err := e.svc.LoginStatus(context.Background(), l.State)
	if err != nil {
		t.Fatalf("first LoginStatus: %v", err)
	}
	e.clock.advance(10 * time.Minute)
	second, err := e.svc.LoginStatus(context.Background(), l.State)
	if err != nil {
		t.Fatalf("second LoginStatus: %v", err)
	}
	if first.Status != "ok" || second.Status != "ok" || second.Account == nil || first.Account.ID != second.Account.ID {
		t.Errorf("statuses = %+v / %+v, want the same ok account", first, second)
	}
	if rows := e.rows(t); len(rows) != 1 {
		t.Errorf("store has %d rows, want 1", len(rows))
	}
}

func TestLoginStatusExpired(t *testing.T) {
	e := newEnv(t)
	l := e.start(t, "codex")
	e.fake.Forget()
	if _, err := e.svc.LoginStatus(context.Background(), l.State); !errors.Is(err, ErrLoginExpired) {
		t.Errorf("err = %v, want ErrLoginExpired", err)
	}
	if len(e.svc.PendingProviders()) != 0 {
		t.Errorf("PendingProviders = %v, want none after expiry", e.svc.PendingProviders())
	}
}

func TestLoginStatusExpiredByTTL(t *testing.T) {
	e := newEnv(t)
	l := e.start(t, "codex")
	e.clock.advance(sessionTTL + time.Second)
	if _, err := e.svc.LoginStatus(context.Background(), l.State); !errors.Is(err, ErrLoginExpired) {
		t.Errorf("err = %v, want ErrLoginExpired", err)
	}
}

func TestLoginStatusUnknownState(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.LoginStatus(context.Background(), "never-started"); !errors.Is(err, ErrLoginNotFound) {
		t.Errorf("err = %v, want ErrLoginNotFound", err)
	}
	if n := e.fake.Calls("/oauth/status"); n != 0 {
		t.Errorf("status calls = %d, want 0", n)
	}
}

func TestLoginStatusWithoutProject(t *testing.T) {
	e := newEnv(t)
	l := e.start(t, "antigravity")
	e.fake.FinishLogin(l.State, cliproxyapi.Credential{Name: "antigravity-a@x.json", Provider: "antigravity", Email: "a@x", Status: "active"})

	st, err := e.svc.LoginStatus(context.Background(), l.State)
	if err != nil || st.Status != "error" || !strings.Contains(st.Message, MsgNoProject) {
		t.Fatalf("LoginStatus = %+v, %v; want an error naming the missing project", st, err)
	}
	if len(e.svc.PendingProviders()) != 0 {
		t.Errorf("PendingProviders = %v, want none", e.svc.PendingProviders())
	}
	accts, err := e.svc.List(context.Background(), "antigravity")
	if err != nil || len(accts) != 1 {
		t.Fatalf("List = %+v, %v; want the one kept account", accts, err)
	}
	if a := accts[0]; a.Status != statusRefused || a.StatusMessage != MsgNoProject {
		t.Errorf("account status %q %q, want refused", a.Status, a.StatusMessage)
	}
}

func TestLoginStatusFailed(t *testing.T) {
	e := newEnv(t)
	l := e.start(t, "codex")
	e.fake.FailLogin(l.State, "state code error")
	st, err := e.svc.LoginStatus(context.Background(), l.State)
	if err != nil || st.Status != "error" || st.Message == "" {
		t.Errorf("LoginStatus = %+v, %v; want error with a message", st, err)
	}
	if len(e.svc.PendingProviders()) != 0 {
		t.Errorf("PendingProviders = %v, want none after a failed login", e.svc.PendingProviders())
	}
}

func TestCancelLogin(t *testing.T) {
	e := newEnv(t)
	l := e.start(t, "codex")
	if err := e.svc.CancelLogin(context.Background(), l.State); err != nil {
		t.Fatalf("CancelLogin: %v", err)
	}
	if n := e.fake.Calls("/oauth/session"); n != 1 {
		t.Errorf("session calls = %d, want 1", n)
	}
	if len(e.svc.PendingProviders()) != 0 {
		t.Errorf("PendingProviders = %v, want none", e.svc.PendingProviders())
	}
	if err := e.svc.CancelLogin(context.Background(), l.State); !errors.Is(err, ErrLoginNotFound) {
		t.Errorf("second CancelLogin err = %v, want ErrLoginNotFound", err)
	}
}

func TestPendingProviders(t *testing.T) {
	e := newEnv(t)
	c := e.start(t, "codex")
	e.start(t, "grok")
	got := e.svc.PendingProviders()
	if len(got) != 2 || !got["codex"] || !got["xai"] {
		t.Fatalf("PendingProviders = %v, want codex and xai (engine ids)", got)
	}
	e.fake.FinishLogin(c.State, cliproxyapi.Credential{Name: "codex-a.json", Provider: "codex"})
	if _, err := e.svc.LoginStatus(context.Background(), c.State); err != nil {
		t.Fatalf("LoginStatus: %v", err)
	}
	if got := e.svc.PendingProviders(); len(got) != 1 || !got["xai"] {
		t.Errorf("PendingProviders = %v, want only xai once codex is adopted", got)
	}
	e.clock.advance(sessionTTL + time.Second)
	if got := e.svc.PendingProviders(); len(got) != 0 {
		t.Errorf("PendingProviders = %v, want none after the TTL", got)
	}
}

func TestErrorsNeverNameEngine(t *testing.T) {
	for _, err := range []error{ErrUnknownProvider, ErrLoginNotFound, ErrLoginExpired, ErrBadRedirect, ErrNotPasteFlow} {
		text := strings.ToLower(err.Error())
		if strings.Contains(text, "cliproxy") || strings.Contains(text, "bifrost") {
			t.Errorf("%q names an engine", err)
		}
	}
}
