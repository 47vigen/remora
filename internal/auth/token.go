package auth

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/47vigen/remora/internal/store"
)

// tokenPrefix starts every API token value.
const tokenPrefix = "rmt_"

// tokenBytes of randomness encode to 32 base32 characters.
const tokenBytes = 20

// tokenShownPrefix is how many characters of a token value are kept to recognise it in a list.
const tokenShownPrefix = len(tokenPrefix) + 6

var tokenEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// CreateToken creates an admin API token without expiry.
func (s *Service) CreateToken(ctx context.Context, name string) (store.APIToken, string, error) {
	return s.CreateScopedToken(ctx, name, store.ScopeAdmin, nil)
}

// CreateScopedToken creates a token with the given scope (admin if empty) and optional expiry.
// It returns the token value once; the store keeps only its hash and a short prefix.
func (s *Service) CreateScopedToken(ctx context.Context, name, scope string, expiresAt *time.Time) (store.APIToken, string, error) {
	if scope == "" {
		scope = store.ScopeAdmin
	}
	buf := make([]byte, tokenBytes)
	_, _ = rand.Read(buf) // crypto/rand.Read never fails (it aborts the program instead)
	value := tokenPrefix + strings.ToLower(tokenEncoding.EncodeToString(buf))

	id := make([]byte, 16)
	_, _ = rand.Read(id)
	t := store.APIToken{
		ID:        hex.EncodeToString(id),
		Name:      name,
		Prefix:    value[:tokenShownPrefix],
		Hash:      hashToken(value),
		CreatedAt: s.now().UTC(),
		Scope:     scope,
		ExpiresAt: expiresAt,
	}
	if err := s.st.CreateAPIToken(ctx, t); err != nil {
		return store.APIToken{}, "", fmt.Errorf("auth: create token: %w", err)
	}
	return t, value, nil
}

// authenticateToken returns the principal for an unexpired token and records its use.
func (s *Service) authenticateToken(ctx context.Context, value string) (Principal, error) {
	t, err := s.st.APITokenByHash(ctx, hashToken(value))
	if errors.Is(err, store.ErrNotFound) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, fmt.Errorf("auth: token: %w", err)
	}
	at := s.now()
	if t.ExpiresAt != nil && !at.Before(*t.ExpiresAt) {
		return Principal{}, ErrUnauthenticated
	}
	if v, ok := activeTokenTouches.Load(s); ok {
		uses := v.(*tokenTouches)
		uses.mu.Lock()
		if at.After(uses.pending[t.ID]) {
			uses.pending[t.ID] = at
		}
		uses.mu.Unlock()
	}
	return Principal{Via: "token", TokenID: t.ID, TokenName: t.Name, Scope: t.Scope}, nil
}

// Buffers are scoped to a running auth service and removed when it stops.
var activeTokenTouches sync.Map // *Service -> *tokenTouches

type tokenTouches struct {
	mu      sync.Mutex
	pending map[string]time.Time
}

// StartTokenTouches starts one last-used flusher for the service. Call the returned stop after draining requests
// and before closing the store. ctx bounds database writes, including the final flush.
func (s *Service) StartTokenTouches(ctx context.Context, logger *slog.Logger) func() {
	uses := &tokenTouches{pending: make(map[string]time.Time)}
	activeTokenTouches.Store(s, uses)
	stopping, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				uses.flush(ctx, s.st, logger)
				timer.Reset(time.Minute)
			case <-stopping:
				uses.flush(ctx, s.st, logger)
				return
			}
		}
	}()
	return sync.OnceFunc(func() {
		activeTokenTouches.Delete(s)
		close(stopping)
		<-done
	})
}

func (u *tokenTouches) flush(ctx context.Context, st *store.Store, logger *slog.Logger) {
	u.mu.Lock()
	batch := u.pending
	if len(batch) > 0 {
		u.pending = make(map[string]time.Time)
	}
	u.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	if err := st.TouchAPITokens(ctx, batch); err != nil {
		logger.WarnContext(ctx, "could not record API token use", "err", err)
		u.mu.Lock()
		for id, at := range batch {
			if at.After(u.pending[id]) {
				u.pending[id] = at
			}
		}
		u.mu.Unlock()
	}
}

// ListTokens lists the API tokens, oldest first. Values are never kept, so none is returned.
func (s *Service) ListTokens(ctx context.Context) ([]store.APIToken, error) {
	ts, err := s.st.ListAPITokens(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: list tokens: %w", err)
	}
	// Uses not flushed yet still show, so the list is current between flushes.
	if v, ok := activeTokenTouches.Load(s); ok {
		uses := v.(*tokenTouches)
		uses.mu.Lock()
		for i := range ts {
			if at, ok := uses.pending[ts[i].ID]; ok && (ts[i].LastUsedAt == nil || at.After(*ts[i].LastUsedAt)) {
				ts[i].LastUsedAt = &at
			}
		}
		uses.mu.Unlock()
	}
	return ts, nil
}

// DeleteToken revokes an API token at once. store.ErrNotFound when it does not exist.
func (s *Service) DeleteToken(ctx context.Context, id string) error {
	if err := s.st.DeleteAPIToken(ctx, id); err != nil {
		return fmt.Errorf("auth: delete token: %w", err)
	}
	return nil
}
