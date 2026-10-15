package accounts

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/engine/cliproxyapi/cliproxyapitest"
	"github.com/47vigen/remora/internal/store"
)

const testSecret = "test-management-secret"

// env is a Service wired to a fake engine and a fresh store.
type env struct {
	svc   *Service
	fake  *cliproxyapitest.Fake
	store *store.Store
	clock *clock
}

// clock is a settable time source for the Service.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "remora.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newEnv(t *testing.T) *env {
	t.Helper()
	fake := cliproxyapitest.NewServer(t, testSecret)
	return newEnvAt(t, fake, fake.URL)
}

// newEnvAt builds a Service whose client talks to engineURL (the fake itself, or a proxy in front of it).
func newEnvAt(t *testing.T, fake *cliproxyapitest.Fake, engineURL string) *env {
	t.Helper()
	st := openStore(t)
	c := &clock{t: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}
	svc := New(cliproxyapi.New(engineURL, testSecret, nil), st, slog.New(slog.DiscardHandler))
	svc.now = c.now
	svc.modelWait = time.Millisecond
	return &env{svc: svc, fake: fake, store: st, clock: c}
}

func (e *env) rows(t *testing.T) []store.Account {
	t.Helper()
	rows, err := e.store.Accounts(context.Background())
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	return rows
}

func (e *env) addRow(t *testing.T, provider, label, credential string, enabled bool) store.Account {
	t.Helper()
	a, err := e.store.CreateAccount(context.Background(),
		store.Account{Provider: provider, Label: label, Credential: credential, Enabled: enabled})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return a
}

func (e *env) start(t *testing.T, provider string) Login {
	t.Helper()
	l, err := e.svc.StartLogin(context.Background(), provider)
	if err != nil {
		t.Fatalf("StartLogin(%q): %v", provider, err)
	}
	return l
}
