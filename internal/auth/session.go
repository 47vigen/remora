package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/47vigen/remora/internal/store"
)

// CookieName is the admin session cookie.
const CookieName = "remora_session"

// SessionTTL is how long a session lasts after login.
const SessionTTL = 7 * 24 * time.Hour

// sessionBytes is the entropy of a session token.
const sessionBytes = 32

// Service authenticates the admin on /api/*: session cookies for the UI, API tokens for scripts.
type Service struct {
	st      *store.Store
	now     func() time.Time
	limiter *limiter
}

// NewService returns a Service over st. now is the clock; nil means time.Now.
func NewService(st *store.Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{st: st, now: now, limiter: newLimiter()}
}

// hashToken is how sessions and API tokens are stored: sha256 of the value.
func hashToken(v string) []byte {
	h := sha256.Sum256([]byte(v))
	return h[:]
}

// Login checks the admin password and opens a session. Five failures from one IP within 15 minutes lock that IP out
// with ErrTooManyAttempts until the window ends.
func (s *Service) Login(ctx context.Context, remoteIP, password string) (token string, expires time.Time, err error) {
	now := s.now()
	if !s.limiter.allow(remoteIP, now) {
		return "", time.Time{}, ErrTooManyAttempts
	}
	ok, err := CheckAdminPassword(ctx, s.st, password)
	if err != nil {
		return "", time.Time{}, err
	}
	if !ok {
		s.limiter.fail(remoteIP, now)
		return "", time.Time{}, ErrBadPassword
	}
	s.limiter.succeed(remoteIP)

	buf := make([]byte, sessionBytes)
	_, _ = rand.Read(buf) // crypto/rand.Read never fails (it aborts the program instead)
	token = base64.RawURLEncoding.EncodeToString(buf)
	expires = now.Add(SessionTTL)
	if err := s.st.CreateSession(ctx, hashToken(token), expires); err != nil {
		return "", time.Time{}, fmt.Errorf("auth: login: %w", err)
	}
	return token, expires, nil
}

// Logout ends the session; an unknown token is not an error.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if err := s.st.DeleteSession(ctx, hashToken(token)); err != nil {
		return fmt.Errorf("auth: logout: %w", err)
	}
	return nil
}

// ChangePassword replaces the admin password after checking current, and revokes every session but currentSession
// ("" when the caller used an API token: every session is revoked). ErrBadPassword when current is wrong,
// ErrWeakPassword when next is out of bounds.
func (s *Service) ChangePassword(ctx context.Context, currentSession, current, next string) error {
	ok, err := CheckAdminPassword(ctx, s.st, current)
	if err != nil {
		return err
	}
	if !ok {
		return ErrBadPassword
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	if err := s.st.SetSetting(ctx, SettingAdminPasswordHash, hash); err != nil {
		return fmt.Errorf("auth: change password: %w", err)
	}
	var keep []byte
	if currentSession != "" {
		keep = hashToken(currentSession)
	}
	if err := s.st.DeleteSessionsExcept(ctx, keep); err != nil {
		return fmt.Errorf("auth: change password: %w", err)
	}
	return nil
}

// Principal is how a request is authenticated.
type Principal struct {
	Via string // "session" | "token"
}

// Authenticate accepts a valid session cookie or "Authorization: Bearer rmt_…". Anything else, including a /v1
// client key, is ErrUnauthenticated.
func (s *Service) Authenticate(r *http.Request) (Principal, error) {
	ctx := r.Context()
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		ok, err := s.validSession(ctx, c.Value)
		if err != nil {
			return Principal{}, err
		}
		if ok {
			return Principal{Via: "session"}, nil
		}
	}
	if v, ok := bearer(r); ok && strings.HasPrefix(v, tokenPrefix) {
		ok, err := s.validToken(ctx, v)
		if err != nil {
			return Principal{}, err
		}
		if ok {
			return Principal{Via: "token"}, nil
		}
	}
	return Principal{}, ErrUnauthenticated
}

// SessionToken returns the session cookie's value, or "".
func SessionToken(r *http.Request) string {
	if c, err := r.Cookie(CookieName); err == nil {
		return c.Value
	}
	return ""
}

func (s *Service) validSession(ctx context.Context, token string) (bool, error) {
	h := hashToken(token)
	exp, err := s.st.SessionExpiry(ctx, h)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("auth: session: %w", err)
	}
	if !s.now().Before(exp) {
		_ = s.st.DeleteSession(ctx, h)
		return false, nil
	}
	return true, nil
}

func bearer(r *http.Request) (string, bool) {
	scheme, v, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	v = strings.TrimSpace(v)
	return v, v != ""
}

// isHTTPS reports whether the client reached remora over TLS, directly or through a proxy that says so.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// SessionCookie returns the session cookie for token. It is Secure unless the request is plain HTTP, so an admin
// on a LAN address without TLS is not locked out.
func SessionCookie(r *http.Request, token string, expires time.Time) *http.Cookie {
	//nolint:gosec // G124: Secure is deliberately off on plain HTTP so a LAN admin without TLS can sign in.
	return &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	}
}

// ClearedSessionCookie returns a cookie that deletes the session cookie.
func ClearedSessionCookie(r *http.Request) *http.Cookie {
	//nolint:gosec // G124: Secure is deliberately off on plain HTTP so a LAN admin without TLS can sign in.
	return &http.Cookie{
		Name:     CookieName,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	}
}

// SetSessionCookie sets the session cookie on w.
func SetSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, SessionCookie(r, token, expires))
}

// ClearSessionCookie deletes the session cookie.
func ClearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, ClearedSessionCookie(r))
}
