package store

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestAuthSessions(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	exp := time.Date(2026, 10, 21, 12, 0, 0, 123456789, time.UTC)

	if err := s.CreateSession(ctx, []byte("h1"), exp); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.CreateSession(ctx, []byte("h2"), exp); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, err := s.SessionExpiry(ctx, []byte("h1"))
	if err != nil || !got.Equal(exp) {
		t.Fatalf("SessionExpiry = (%v, %v), want %v", got, err, exp)
	}
	if _, err := s.SessionExpiry(ctx, []byte("nope")); !errors.Is(err, ErrNotFound) {
		t.Errorf("SessionExpiry(nope) err = %v, want ErrNotFound", err)
	}

	if err := s.DeleteSessionsExcept(ctx, []byte("h1")); err != nil {
		t.Fatalf("DeleteSessionsExcept: %v", err)
	}
	if _, err := s.SessionExpiry(ctx, []byte("h2")); !errors.Is(err, ErrNotFound) {
		t.Errorf("h2 survived DeleteSessionsExcept: %v", err)
	}
	if err := s.DeleteSession(ctx, []byte("h1")); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := s.SessionExpiry(ctx, []byte("h1")); !errors.Is(err, ErrNotFound) {
		t.Errorf("h1 survived DeleteSession: %v", err)
	}
}

func TestAuthAPITokens(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	created := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

	tok := APIToken{ID: "t1", Name: "ci", Prefix: "rmt_abcd", Hash: []byte("hash-1"), CreatedAt: created}
	if err := s.CreateAPIToken(ctx, tok); err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	if err := s.CreateAPIToken(ctx, APIToken{ID: "t2", Name: "x", Prefix: "p", Hash: []byte("hash-1"), CreatedAt: created}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate hash err = %v, want ErrDuplicate", err)
	}

	got, err := s.APITokenByHash(ctx, []byte("hash-1"))
	if err != nil {
		t.Fatalf("APITokenByHash: %v", err)
	}
	if got.ID != "t1" || got.Name != "ci" || got.Prefix != "rmt_abcd" || !bytes.Equal(got.Hash, []byte("hash-1")) ||
		!got.CreatedAt.Equal(created) || got.LastUsedAt != nil {
		t.Errorf("APITokenByHash = %+v", got)
	}
	if _, err := s.APITokenByHash(ctx, []byte("nope")); !errors.Is(err, ErrNotFound) {
		t.Errorf("APITokenByHash(nope) err = %v, want ErrNotFound", err)
	}

	used := created.Add(time.Hour)
	if err := s.TouchAPIToken(ctx, "t1", used); err != nil {
		t.Fatalf("TouchAPIToken: %v", err)
	}
	list, err := s.ListAPITokens(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListAPITokens = (%+v, %v)", list, err)
	}
	if list[0].LastUsedAt == nil || !list[0].LastUsedAt.Equal(used) {
		t.Errorf("LastUsedAt = %v, want %v", list[0].LastUsedAt, used)
	}

	if err := s.DeleteAPIToken(ctx, "t1"); err != nil {
		t.Fatalf("DeleteAPIToken: %v", err)
	}
	if err := s.DeleteAPIToken(ctx, "t1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second DeleteAPIToken err = %v, want ErrNotFound", err)
	}
	if list, _ := s.ListAPITokens(ctx); len(list) != 0 {
		t.Errorf("ListAPITokens after delete = %+v", list)
	}
}
