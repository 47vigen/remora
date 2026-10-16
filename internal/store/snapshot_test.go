package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/47vigen/remora/internal/alias"
)

func putKey(t *testing.T, s *Store, k ClientKey, value string) ClientKey {
	t.Helper()
	var out ClientKey
	if err := s.Write(context.Background(), func(tx *Tx) error {
		var err error
		out, err = tx.PutClientKey(context.Background(), k, value)
		return err
	}); err != nil {
		t.Fatalf("PutClientKey: %v", err)
	}
	return out
}

func putAlias(t *testing.T, s *Store, name string) {
	t.Helper()
	if err := s.Write(context.Background(), func(tx *Tx) error {
		return tx.PutAlias(context.Background(), alias.Alias{Name: name, Kind: alias.KindChat, Members: []alias.Member{{Ref: "p/m"}}})
	}); err != nil {
		t.Fatalf("PutAlias: %v", err)
	}
}

func TestSnapshotSeesWriteAtCommit(t *testing.T) {
	ctx := context.Background()
	s := openSealed(t)
	k := putKey(t, s, ClientKey{Name: "k", Allow: []string{"*"}, Enabled: true}, "rk-snapshot-value-1")
	putAlias(t, s, "a")
	if got, err := s.ClientKeyByValue(ctx, "rk-snapshot-value-1"); err != nil || !got.Enabled {
		t.Fatalf("ClientKeyByValue = %+v, %v; want enabled", got, err)
	}
	k.Enabled = false
	putKey(t, s, k, "rk-snapshot-value-1")
	if got, err := s.ClientKeyByValue(ctx, "rk-snapshot-value-1"); err != nil || got.Enabled {
		t.Fatalf("after disable: %+v, %v; want disabled at once", got, err)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteClientKey(ctx, k.ID) }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClientKeyByValue(ctx, "rk-snapshot-value-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: err = %v, want ErrNotFound", err)
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.DeleteAlias(ctx, "a") }); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Alias(ctx, "a"); ok || err != nil {
		t.Fatalf("Alias after delete: ok %v err %v", ok, err)
	}
}

func TestSnapshotRolledBackWriteChangesNothing(t *testing.T) {
	ctx := context.Background()
	s := openSealed(t)
	boom := errors.New("boom")
	err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.PutAlias(ctx, alias.Alias{Name: "x", Kind: alias.KindChat, Members: []alias.Member{{Ref: "p/m"}}}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Write = %v", err)
	}
	if _, ok, _ := s.Alias(ctx, "x"); ok {
		t.Fatal("a rolled-back alias is visible")
	}
}

func TestSnapshotConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	s := openSealed(t)
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Go(func() {
			for i := range 20 {
				name := fmt.Sprintf("a-%d-%d", g, i)
				if err := s.Write(ctx, func(tx *Tx) error {
					return tx.PutAlias(ctx, alias.Alias{Name: name, Kind: alias.KindChat, Members: []alias.Member{{Ref: "p/m"}}})
				}); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	got, err := s.Aliases(ctx)
	if err != nil || len(got) != 320 {
		t.Fatalf("Aliases: %d, %v; want 320", len(got), err)
	}
	var want map[string]alias.Alias
	if err := s.Write(ctx, func(tx *Tx) error {
		var err error
		want, err = tx.Aliases(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("snapshot differs from the database")
	}
}

func TestSnapshotLookupHitsNoSQL(t *testing.T) {
	ctx := context.Background()
	s := openSealed(t)
	putKey(t, s, ClientKey{Name: "k", Allow: []string{"*"}, Enabled: true}, "rk-snapshot-value-2")
	putAlias(t, s, "a")
	for _, q := range []string{
		"ALTER TABLE alias_members RENAME TO am_gone", "ALTER TABLE aliases RENAME TO a_gone",
		"ALTER TABLE client_keys RENAME TO ck_gone",
	} {
		if _, err := s.DB().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := s.ClientKeyByValue(ctx, "rk-snapshot-value-2"); err != nil {
		t.Errorf("ClientKeyByValue: %v", err)
	}
	if _, ok, err := s.Alias(ctx, "a"); !ok || err != nil {
		t.Errorf("Alias: ok %v err %v", ok, err)
	}
}

func TestRefreshFailureFallsBackToSQL(t *testing.T) {
	ctx := context.Background()
	s := openSealed(t)
	putAlias(t, s, "a")
	if _, err := s.DB().ExecContext(ctx, `UPDATE aliases SET capabilities = 'not a list'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Refresh(ctx); err == nil {
		t.Fatal("Refresh succeeded over a broken row")
	}
	if _, err := s.Aliases(ctx); err == nil {
		t.Fatal("Aliases succeeded; want the SQL path's error (fail closed)")
	}
	if _, _, err := s.Alias(ctx, "a"); err == nil {
		t.Fatal("Alias succeeded; want the SQL path's error (fail closed)")
	}
}

func TestAliasesReturnsACopy(t *testing.T) {
	ctx := context.Background()
	s := openSealed(t)
	putAlias(t, s, "a")
	got, _ := s.Aliases(ctx)
	delete(got, "a")
	if again, _ := s.Aliases(ctx); len(again) != 1 {
		t.Fatal("deleting from the returned map changed the store's")
	}
}
