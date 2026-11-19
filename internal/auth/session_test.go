package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/47vigen/remora/internal/store"
)

func TestChangedPasswordSurvivesEnv(t *testing.T) {
	ctx := context.Background()
	s, st, _ := newService(t)
	tok := login(t, s, testPassword)
	if err := s.ChangePassword(ctx, tok, testPassword, "second-password"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	// A restart with the same env password.
	if _, err := EnsureAdmin(ctx, st, testPassword); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	if _, _, err := s.Login(ctx, "192.0.2.1", "second-password"); err != nil {
		t.Errorf("login with the changed password: %v", err)
	}
	if _, _, err := s.Login(ctx, "192.0.2.1", testPassword); !errors.Is(err, ErrBadPassword) {
		t.Errorf("login with the env password err = %v, want ErrBadPassword", err)
	}
}

func TestChangePasswordChecks(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newService(t)
	tok := login(t, s, testPassword)
	if err := s.ChangePassword(ctx, tok, "not-the-password", "second-password"); !errors.Is(err, ErrBadPassword) {
		t.Errorf("wrong current err = %v, want ErrBadPassword", err)
	}
	if err := s.ChangePassword(ctx, tok, testPassword, "short"); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("short next err = %v, want ErrWeakPassword", err)
	}
}

func TestLoginLogout(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newService(t)
	if _, _, err := s.Login(ctx, "192.0.2.1", "wrong-password-1"); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("Login(wrong) err = %v, want ErrBadPassword", err)
	}
	tok, exp, err := s.Login(ctx, "192.0.2.1", testPassword)
	if err != nil || tok == "" {
		t.Fatalf("Login failed or returned an empty session: %v", err)
	}
	if want := newClock().now().Add(SessionTTL); !exp.Equal(want) {
		t.Errorf("expires = %v, want %v", exp, want)
	}
	p, err := s.Authenticate(cookieReq(tok))
	if err != nil || p != (Principal{Via: "session", Scope: "admin"}) {
		t.Fatalf("Authenticate = (%+v, %v)", p, err)
	}
	if err := s.Logout(ctx, tok); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := s.Authenticate(cookieReq(tok)); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("after Logout err = %v, want ErrUnauthenticated", err)
	}
	if _, err := s.Authenticate(cookieReq("")); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("empty cookie err = %v, want ErrUnauthenticated", err)
	}
}

func TestSessionExpires(t *testing.T) {
	s, _, c := newService(t)
	tok := login(t, s, testPassword)
	c.add(SessionTTL + time.Second)
	if _, err := s.Authenticate(cookieReq(tok)); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("expired session err = %v, want ErrUnauthenticated", err)
	}
}

func TestSessionPurgeOnSuccessfulLogin(t *testing.T) {
	ctx := context.Background()
	s, st, c := newService(t)
	expired := login(t, s, testPassword)
	c.add(SessionTTL)
	activeHash := []byte("active-session-hash")
	if err := st.CreateSession(ctx, activeHash, c.now().Add(time.Nanosecond)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, _, err := s.Login(ctx, "192.0.2.1", "wrong-password-1"); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("failed login: %v", err)
	}
	if _, err := st.SessionExpiry(ctx, hashToken(expired)); err != nil {
		t.Fatalf("failed login removed expired session: %v", err)
	}
	fresh := login(t, s, testPassword)
	if _, err := st.SessionExpiry(ctx, hashToken(expired)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expired session after login: %v, want ErrNotFound", err)
	}
	for _, hash := range [][]byte{activeHash, hashToken(fresh)} {
		if _, err := st.SessionExpiry(ctx, hash); err != nil {
			t.Errorf("active session after login: %v", err)
		}
	}
}

func TestLoginLimiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		s, _, _ := newService(t)
		const ip = "192.0.2.1"
		for range 5 {
			if _, _, err := s.Login(ctx, ip, "wrong-password-1"); !errors.Is(err, ErrBadPassword) {
				t.Fatalf("failed login = %v, want ErrBadPassword", err)
			}
		}

		start := time.Now()
		if _, _, err := s.Login(ctx, ip, "wrong-password-1"); !errors.Is(err, ErrBadPassword) {
			t.Fatalf("delayed failure = %v, want ErrBadPassword", err)
		}
		if got := time.Since(start); got != time.Second {
			t.Errorf("failed login delay = %v, want 1s", got)
		}

		canceled, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if _, _, err := s.Login(canceled, ip, testPassword); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("canceled login = %v, want context deadline exceeded", err)
		}

		start = time.Now()
		if _, _, err := s.Login(ctx, ip, testPassword); err != nil {
			t.Fatalf("delayed correct login: %v", err)
		}
		if got := time.Since(start); got != 2*time.Second {
			t.Errorf("correct login delay = %v, want 2s", got)
		}

		start = time.Now()
		if _, _, err := s.Login(ctx, ip, testPassword); err != nil {
			t.Fatalf("next login: %v", err)
		}
		if got := time.Since(start); got != 0 {
			t.Errorf("delay after success = %v, want none", got)
		}
	})
}

func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newService(t)
	s1 := login(t, s, testPassword)
	s2 := login(t, s, testPassword)
	if err := s.ChangePassword(ctx, s1, testPassword, "second-password"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, err := s.Authenticate(cookieReq(s1)); err != nil {
		t.Errorf("s1 after change: %v", err)
	}
	if _, err := s.Authenticate(cookieReq(s2)); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("s2 after change err = %v, want ErrUnauthenticated", err)
	}
}

func TestHTTPS(t *testing.T) {
	for _, tc := range []struct {
		name, scheme, peer, forwarded string
		wantSecure                    bool
	}{
		{"direct TLS", "https", "192.0.2.1:4000", "http", true},
		{"IPv4 loopback", "http", "127.0.0.1:4000", "https", true},
		{"IPv6 loopback", "http", "[::1]:4000", "HTTPS", true},
		{"untrusted peer", "http", "192.0.2.1:4000", "https", false},
		{"malformed peer", "http", "127.0.0.1", "https", false},
		{"plain HTTP", "http", "127.0.0.1:4000", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, tc.scheme+"://remora.test/api/auth/login", http.NoBody)
			r.RemoteAddr = tc.peer
			r.Header.Set("X-Forwarded-Proto", tc.forwarded)
			if got := isHTTPS(r); got != tc.wantSecure {
				t.Errorf("isHTTPS = %t, want %t", got, tc.wantSecure)
			}
		})
	}
}

func TestSessionCookieFlags(t *testing.T) {
	exp := newClock().now().Add(SessionTTL)
	for _, tc := range []struct {
		name       string
		req        func() *http.Request
		wantSecure bool
	}{
		{"https", func() *http.Request {
			return httptest.NewRequestWithContext(context.Background(), http.MethodPost, "https://remora.test/api/auth/login", http.NoBody)
		}, true},
		{"forwarded https", func() *http.Request {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://remora.test/api/auth/login", http.NoBody)
			r.Header.Set("X-Forwarded-Proto", "https")
			r.RemoteAddr = "127.0.0.1:4000"
			return r
		}, true},
		{"plain http", func() *http.Request {
			return httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://remora.test/api/auth/login", http.NoBody)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			SetSessionCookie(rec, tc.req(), "tok", exp)
			cs := rec.Result().Cookies()
			if len(cs) != 1 {
				t.Fatalf("cookies = %v", cs)
			}
			c := cs[0]
			if c.Name != CookieName || c.Value != "tok" || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode ||
				c.Path != "/" || c.Secure != tc.wantSecure {
				t.Errorf("cookie = %+v", c)
			}

			rec = httptest.NewRecorder()
			ClearSessionCookie(rec, tc.req())
			cs = rec.Result().Cookies()
			if len(cs) != 1 || cs[0].MaxAge >= 0 || cs[0].Value != "" {
				t.Errorf("cleared cookie = %+v", cs)
			}
		})
	}
}
