package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestHashPassword(t *testing.T) {
	for _, pw := range []string{"short", strings.Repeat("a", 73)} {
		if _, err := HashPassword(pw); !errors.Is(err, ErrWeakPassword) {
			t.Errorf("HashPassword(len %d) err = %v, want ErrWeakPassword", len(pw), err)
		}
	}
	hash, err := HashPassword("long-enough-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !CheckPassword(hash, "long-enough-password") {
		t.Error("CheckPassword(right) = false")
	}
	if CheckPassword(hash, "wrong-password-123") {
		t.Error("CheckPassword(wrong) = true")
	}
}
