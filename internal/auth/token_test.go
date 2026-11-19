package auth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

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
		t.Fatal("token value should have the prefix and 32 more characters")
	}
	if tok.ID == "" || tok.Name != "ci" || !strings.HasPrefix(value, tok.Prefix) || len(tok.Prefix) <= len("rmt_") {
		t.Error("unexpected token metadata")
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
	if err != nil || got.LastUsedAt != nil {
		t.Errorf("LastUsedAt before flush = %v, %v", got.LastUsedAt, err)
	}

	list, err := s.ListTokens(ctx)
	if err != nil || len(list) != 1 || list[0].ID != tok.ID || list[0].Prefix != tok.Prefix {
		t.Errorf("unexpected token list: %v", err)
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

func TestTokenAuthenticatesWithWriteLock(t *testing.T) {
	ctx := context.Background()
	s, st, _ := newService(t)
	defer s.StartTokenTouches(ctx, slog.Default())()
	_, value, err := s.CreateToken(ctx, "ci")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "UPDATE api_tokens SET name = name"); err != nil {
		t.Fatal(err)
	}

	h := s.Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := PrincipalFrom(r.Context()); !ok || p.Via != "token" {
			t.Error("missing token principal")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, bearerReq(value))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestTokenLastUsedBatchesAndFlushesOnShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, st, _ := newService(t)
		s.now = time.Now
		ctx := context.Background()
		stop := s.StartTokenTouches(ctx, slog.Default())
		defer stop()
		_, first, err := s.CreateToken(ctx, "first")
		if err != nil {
			t.Fatal(err)
		}
		_, second, err := s.CreateToken(ctx, "second")
		if err != nil {
			t.Fatal(err)
		}
		use := func(value string) time.Time {
			t.Helper()
			if _, err := s.Authenticate(bearerReq(value)); err != nil {
				t.Fatal(err)
			}
			return time.Now()
		}
		lastUsed := func(value string) *time.Time {
			t.Helper()
			got, err := st.APITokenByHash(ctx, hashToken(value))
			if err != nil {
				t.Fatal(err)
			}
			return got.LastUsedAt
		}
		assertLastUsed := func(value string, want time.Time) {
			t.Helper()
			if got := lastUsed(value); got == nil || !got.Equal(want) {
				t.Fatalf("LastUsedAt = %v, want %v", got, want)
			}
		}

		use(first)
		wantSecond := use(second)
		time.Sleep(59 * time.Second)
		wantFirst := use(first)
		if lastUsed(first) != nil || lastUsed(second) != nil {
			t.Fatal("last-used timestamp persisted before the first minute")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		assertLastUsed(first, wantFirst)
		assertLastUsed(second, wantSecond)

		finalFirst := use(first)
		time.Sleep(59 * time.Second)
		synctest.Wait()
		assertLastUsed(first, wantFirst)
		stop()
		assertLastUsed(first, finalFirst)
		assertLastUsed(second, wantSecond)
	})
}

func TestTokenLastUsedFlushFailureIsLogged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		s, st, _ := newService(t)
		_, value, err := s.CreateToken(ctx, "ci")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB().ExecContext(ctx, `CREATE TRIGGER fail_touch BEFORE UPDATE OF last_used_at ON api_tokens
			BEGIN SELECT RAISE(ABORT, 'test write failure'); END`); err != nil {
			t.Fatal(err)
		}
		var logs bytes.Buffer
		stop := s.StartTokenTouches(ctx, slog.New(slog.NewTextHandler(&logs, nil)))
		defer stop()
		if _, err := s.Authenticate(bearerReq(value)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if !strings.Contains(logs.String(), "test write failure") || strings.Contains(logs.String(), value) {
			t.Fatal("flush failure was not logged safely")
		}
		if _, err := s.Authenticate(bearerReq(value)); err != nil {
			t.Fatal(err)
		}
	})
}
