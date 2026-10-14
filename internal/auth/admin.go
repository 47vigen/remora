// Package auth holds remora's admin password, sessions and keys.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"

	"github.com/47vigen/remora/internal/store"
)

// SettingAdminPasswordHash is the settings key that holds the admin password's bcrypt hash.
const SettingAdminPasswordHash = "admin_password_hash" //nolint:gosec // G101: a settings key, not a credential.

// generatedPasswordBytes is the entropy of a generated admin password (32 characters in base64url).
const generatedPasswordBytes = 24

// EnsureAdmin makes sure an admin exists. The env value applies only while no admin exists in the store.
//   - a hash stored: returns "" and ignores envPassword (the stored password wins, even after a change in Settings).
//   - no hash, envPassword != "": stores its hash; returns "".
//   - no hash, envPassword == "": generates a 24-byte random password (base64url), stores its hash, returns it.
func EnsureAdmin(ctx context.Context, s *store.Store, envPassword string) (generated string, err error) {
	_, ok, err := s.Setting(ctx, SettingAdminPasswordHash)
	if err != nil {
		return "", fmt.Errorf("auth: read admin password: %w", err)
	}
	if ok {
		return "", nil
	}

	password := envPassword
	if password == "" {
		buf := make([]byte, generatedPasswordBytes)
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("auth: generate admin password: %w", err)
		}
		password = base64.RawURLEncoding.EncodeToString(buf)
		generated = password
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("auth: hash admin password: %w", err)
	}
	if err := s.SetSetting(ctx, SettingAdminPasswordHash, string(hash)); err != nil {
		return "", fmt.Errorf("auth: store admin password: %w", err)
	}
	return generated, nil
}

// CheckAdminPassword reports whether password is the admin password. With no admin stored it returns false.
func CheckAdminPassword(ctx context.Context, s *store.Store, password string) (bool, error) {
	hash, ok, err := s.Setting(ctx, SettingAdminPasswordHash)
	if err != nil {
		return false, fmt.Errorf("auth: read admin password: %w", err)
	}
	if !ok {
		return false, nil
	}
	err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
		return false, nil
	default:
		return false, fmt.Errorf("auth: check admin password: %w", err)
	}
}
