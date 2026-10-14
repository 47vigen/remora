package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/47vigen/remora/internal/store"
)

func TestAPIToken(t *testing.T) {
	ctx := context.Background()
	s, st, _ := newService(t)
	tok, value, err := s.CreateToken(ctx, "ci")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if !strings.HasPrefix(value, "rmt_") || len(value) != 36 {
		t.Fatalf("token value %q: want rmt_ + 32 chars", value)
	}
	if tok.ID == "" || tok.Name != "ci" || !strings.HasPrefix(value, tok.Prefix) || len(tok.Prefix) <= len("rmt_") {
		t.Errorf("token = %+v", tok)
	}

	var raw []byte
	rows, err := st.DB().QueryContext(ctx, "SELECT id, name, prefix, hash FROM api_tokens")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, name, prefix string
		var hash []byte
		if err := rows.Scan(&id, &name, &prefix, &hash); err != nil {
			t.Fatal(err)
		}
		raw = append(raw, []byte(id+name+prefix)...)
		raw = append(raw, hash...)
	}
	_ = rows.Close()
	if bytes.Contains(raw, []byte(value)) {
		t.Error("the stored row holds the token in plaintext")
	}

	p, err := s.Authenticate(bearerReq(value))
	if err != nil || p != (Principal{Via: "token"}) {
		t.Fatalf("Authenticate(bearer) = (%+v, %v)", p, err)
	}
	got, err := st.APITokenByHash(ctx, hashToken(value))
	if err != nil || got.LastUsedAt == nil {
		t.Errorf("LastUsedAt after use = %+v, %v", got.LastUsedAt, err)
	}

	list, err := s.ListTokens(ctx)
	if err != nil || len(list) != 1 || list[0].ID != tok.ID || list[0].Prefix != tok.Prefix {
		t.Errorf("ListTokens = %+v, %v", list, err)
	}
	if err := s.DeleteToken(ctx, tok.ID); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}
	if err := s.DeleteToken(ctx, tok.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("DeleteToken again err = %v, want ErrNotFound", err)
	}
	if _, err := s.Authenticate(bearerReq(value)); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("deleted token err = %v, want ErrUnauthenticated", err)
	}
}

func TestClientKeyRejectedOnAPI(t *testing.T) {
	s, _, _ := newService(t)
	if _, err := s.Authenticate(bearerReq("sk-anything")); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("client key err = %v, want ErrUnauthenticated", err)
	}
	if _, err := s.Authenticate(bearerReq("rmt_unknown")); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("unknown token err = %v, want ErrUnauthenticated", err)
	}
}
