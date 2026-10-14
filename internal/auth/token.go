package auth

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/47vigen/remora/internal/store"
)

// tokenPrefix starts every API token value.
const tokenPrefix = "rmt_"

// tokenBytes of randomness encode to 32 base32 characters.
const tokenBytes = 20

// tokenShownPrefix is how many characters of a token value are kept to recognise it in a list.
const tokenShownPrefix = len(tokenPrefix) + 6

var tokenEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// CreateToken creates an API token named name. The value ("rmt_" + 32 base32 characters) is returned only here;
// the store keeps its sha256 and a short prefix.
func (s *Service) CreateToken(ctx context.Context, name string) (store.APIToken, string, error) {
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
	}
	if err := s.st.CreateAPIToken(ctx, t); err != nil {
		return store.APIToken{}, "", fmt.Errorf("auth: create token: %w", err)
	}
	return t, value, nil
}

// validToken reports whether value is a stored API token, and records its use.
func (s *Service) validToken(ctx context.Context, value string) (bool, error) {
	t, err := s.st.APITokenByHash(ctx, hashToken(value))
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("auth: token: %w", err)
	}
	if err := s.st.TouchAPIToken(ctx, t.ID, s.now().UTC()); err != nil && !errors.Is(err, store.ErrNotFound) {
		return false, fmt.Errorf("auth: token: %w", err)
	}
	return true, nil
}
