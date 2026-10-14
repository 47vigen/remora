package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
		t.Fatalf("Login = (%q, %v)", tok, err)
	}
	if want := newClock().now().Add(SessionTTL); !exp.Equal(want) {
		t.Errorf("expires = %v, want %v", exp, want)
	}
	p, err := s.Authenticate(cookieReq(tok))
	if err != nil || p != (Principal{Via: "session"}) {
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

func TestLoginLimiter(t *testing.T) {
	ctx := context.Background()
	s, _, c := newService(t)
	for i := range 5 {
		if _, _, err := s.Login(ctx, "192.0.2.1", "wrong-password-1"); !errors.Is(err, ErrBadPassword) {
			t.Fatalf("attempt %d err = %v, want ErrBadPassword", i+1, err)
		}
	}
	if _, _, err := s.Login(ctx, "192.0.2.1", testPassword); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("6th attempt err = %v, want ErrTooManyAttempts", err)
	}
	if _, _, err := s.Login(ctx, "192.0.2.2", testPassword); err != nil {
		t.Errorf("other IP: %v", err)
	}
	c.add(15 * time.Minute)
	if _, _, err := s.Login(ctx, "192.0.2.1", testPassword); err != nil {
		t.Errorf("after the window: %v", err)
	}
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
