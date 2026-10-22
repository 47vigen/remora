package mcp

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/47vigen/remora/internal/auth"
)

func TestMain(m *testing.M) {
	auth.HashCost = bcrypt.MinCost
	os.Exit(m.Run())
}
