package auth

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// Password limits. bcrypt reads at most 72 bytes, so a longer password would be silently truncated.
const (
	MinPasswordLen = 12
	MaxPasswordLen = 72
)

// Authentication errors.
var (
	ErrBadPassword     = errors.New("auth: wrong password")
	ErrWeakPassword    = fmt.Errorf("auth: the password must be %d to %d bytes long", MinPasswordLen, MaxPasswordLen)
	ErrUnauthenticated = errors.New("auth: not authenticated")
	ErrTooManyAttempts = errors.New("auth: too many failed sign-in attempts; try again later")
)

// HashPassword returns the bcrypt hash of pw. ErrWeakPassword when pw is shorter than 12 or longer than 72 bytes.
func HashPassword(pw string) (string, error) {
	if len(pw) < MinPasswordLen || len(pw) > MaxPasswordLen {
		return "", ErrWeakPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("auth: hash password: %w", err)
	}
	return string(hash), nil
}

// CheckPassword reports whether pw matches the bcrypt hash.
func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}
