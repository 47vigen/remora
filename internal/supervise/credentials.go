package supervise

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/47vigen/remora/internal/store"
)

// BifrostAdminUser is the fixed name of the front door's admin account.
const BifrostAdminUser = "remora"

// secretBytes is the entropy of each generated engine secret (43 characters in base64url).
const secretBytes = 32

// Credentials are the secrets remora shares with its engines. They live in the store's engine_state table and are
// generated once, at first boot.
type Credentials struct {
	BifrostAdminUser         string // engine_state bifrost/admin_username, fixed "remora"
	BifrostAdminPassword     string // engine_state bifrost/admin_password
	BifrostEncryptionKey     string // engine_state bifrost/encryption_key; never rotated (Bifrost cannot change it)
	CLIProxyManagementSecret string // engine_state cliproxyapi/management_secret
	CLIProxyAPIKey           string // engine_state cliproxyapi/api_key: the client key the front door uses (plan 03)
}

// EnsureCredentials returns the engine credentials, generating and storing only the values that are missing. A
// value already in the store is never replaced.
func EnsureCredentials(ctx context.Context, s *store.Store) (Credentials, error) {
	var c Credentials
	fields := []struct {
		engine, key string
		dst         *string
		fixed       string // "" = generate a random secret
	}{
		{"bifrost", "admin_username", &c.BifrostAdminUser, BifrostAdminUser},
		{"bifrost", "admin_password", &c.BifrostAdminPassword, ""},
		{"bifrost", "encryption_key", &c.BifrostEncryptionKey, ""},
		{"cliproxyapi", "management_secret", &c.CLIProxyManagementSecret, ""},
		{"cliproxyapi", "api_key", &c.CLIProxyAPIKey, ""},
	}
	for _, f := range fields {
		v, ok, err := s.EngineValue(ctx, f.engine, f.key)
		if err != nil {
			return Credentials{}, fmt.Errorf("supervise: read credential: %w", err)
		}
		if !ok || v == "" {
			v = f.fixed
			if v == "" {
				if v, err = randomSecret(); err != nil {
					return Credentials{}, fmt.Errorf("supervise: generate %s/%s: %w", f.engine, f.key, err)
				}
			}
			if err := s.SetEngineValue(ctx, f.engine, f.key, v); err != nil {
				return Credentials{}, fmt.Errorf("supervise: store credential: %w", err)
			}
		}
		*f.dst = v
	}
	return c, nil
}

func randomSecret() (string, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
