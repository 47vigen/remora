package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/store"
)

// clock is a settable time source.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

const testPassword = "first-password-12"

// newService opens a store with an admin whose password is testPassword.
func newService(t *testing.T) (*Service, *store.Store, *clock) {
	t.Helper()
	st := openStore(t)
	if _, err := EnsureAdmin(context.Background(), st, testPassword); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	c := newClock()
	return NewService(st, c.now), st, c
}

func login(t *testing.T, s *Service, password string) string {
	t.Helper()
	tok, _, err := s.Login(context.Background(), "192.0.2.1", password)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	return tok
}

func cookieReq(token string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/x", http.NoBody)
	addSession(r, token)
	return r
}

// addSession adds the session cookie to a request.
func addSession(r *http.Request, token string) {
	r.AddCookie(&http.Cookie{Name: CookieName, Value: token}) //nolint:gosec // G124: a request cookie carries no attributes.
}

func bearerReq(token string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/x", http.NoBody)
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}
