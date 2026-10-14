package store

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/47vigen/remora/internal/alias"
)

func openSealed(t *testing.T) *Store {
	t.Helper()
	s := openTemp(t)
	s.SetSealer(mustSealer(t, bytes.Repeat([]byte{1}, 32)))
	return s
}

// write runs fn in a write transaction and fails the test on error.
func write(t *testing.T, s *Store, fn func(ctx context.Context, tx *Tx) error) {
	t.Helper()
	ctx := context.Background()
	if err := s.Write(ctx, func(tx *Tx) error { return fn(ctx, tx) }); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

// rawContains reports whether any column of any row of table contains needle.
func rawContains(t *testing.T, s *Store, table, needle string) bool {
	t.Helper()
	rows, err := s.DB().QueryContext(context.Background(), "SELECT * FROM "+table) //nolint:gosec // test-only, fixed names
	if err != nil {
		t.Fatalf("select %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for _, v := range vals {
			switch v := v.(type) {
			case []byte:
				found = found || bytes.Contains(v, []byte(needle))
			case string:
				found = found || strings.Contains(v, needle)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return found
}

func TestClientKeys(t *testing.T) {
	ctx := context.Background()
	s := openSealed(t)

	var full ClientKey
	write(t, s, func(ctx context.Context, tx *Tx) error {
		var err error
		full, err = tx.PutClientKey(ctx, ClientKey{Name: "full", Allow: []string{"*"}, Enabled: true}, "sk-e2e-full")
		return err
	})
	if full.ID == "" {
		t.Fatal("PutClientKey returned no ID")
	}
	got, err := s.ClientKeyByValue(ctx, "sk-e2e-full")
	if err != nil {
		t.Fatalf("ClientKeyByValue: %v", err)
	}
	want := ClientKey{ID: full.ID, Name: "full", Allow: []string{"*"}, Enabled: true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ClientKeyByValue = %+v, want %+v", got, want)
	}
	if rawContains(t, s, "client_keys", "sk-e2e-full") {
		t.Error("client_keys row contains the key value")
	}
	if _, err := s.ClientKeyByValue(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ClientKeyByValue(nope) err = %v, want ErrNotFound", err)
	}

	err = s.Write(ctx, func(tx *Tx) error {
		_, err := tx.PutClientKey(ctx, ClientKey{Name: "other", Enabled: true}, "sk-e2e-full")
		return err
	})
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate value err = %v, want ErrDuplicate", err)
	}
	err = s.Write(ctx, func(tx *Tx) error {
		_, err := tx.PutClientKey(ctx, ClientKey{Name: "full", Enabled: true}, "sk-e2e-other")
		return err
	})
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate name err = %v, want ErrDuplicate", err)
	}

	write(t, s, func(ctx context.Context, tx *Tx) error {
		full.Enabled = false
		_, err := tx.PutClientKey(ctx, full, "")
		return err
	})
	got, err = s.ClientKeyByValue(ctx, "sk-e2e-full")
	if err != nil || got.Enabled {
		t.Errorf("after disable: ClientKeyByValue = (%+v, %v), want Enabled false", got, err)
	}

	write(t, s, func(ctx context.Context, tx *Tx) error { return tx.SetClientKeyVK(ctx, full.ID, "vk1", "sk-bf-x") })
	got, err = s.ClientKeyByValue(ctx, "sk-e2e-full")
	if err != nil || got.VKID != "vk1" || got.VKValue != "sk-bf-x" {
		t.Errorf("after SetClientKeyVK: ClientKeyByValue = (%+v, %v), want VKID vk1, VKValue sk-bf-x", got, err)
	}
	if rawContains(t, s, "client_keys", "sk-bf-x") {
		t.Error("client_keys row contains the virtual key value")
	}
	ids, err := s.ClientKeyVKIDs(ctx)
	if err != nil || !reflect.DeepEqual(ids, map[string]string{full.ID: "vk1"}) {
		t.Errorf("ClientKeyVKIDs = (%v, %v), want {%s: vk1}", ids, err, full.ID)
	}

	// Re-saving keeps the virtual key.
	write(t, s, func(ctx context.Context, tx *Tx) error {
		_, err := tx.PutClientKey(ctx, full, "")
		return err
	})
	if got, _ := s.ClientKeyByValue(ctx, "sk-e2e-full"); got.VKID != "vk1" || got.VKValue != "sk-bf-x" {
		t.Errorf("PutClientKey dropped the virtual key: %+v", got)
	}

	var imported ClientKey
	write(t, s, func(ctx context.Context, tx *Tx) error {
		var err error
		imported, err = tx.PutClientKey(ctx, ClientKey{Name: "imported", Enabled: true}, "omni-imported-123")
		return err
	})
	got, err = s.ClientKeyByValue(ctx, "omni-imported-123")
	if err != nil || got.ID != imported.ID {
		t.Errorf("ClientKeyByValue(imported) = (%+v, %v)", got, err)
	}
	if rawContains(t, s, "client_keys", "omni-imported-123") {
		t.Error("client_keys row contains the imported key value")
	}

	write(t, s, func(ctx context.Context, tx *Tx) error {
		keys, err := tx.ClientKeys(ctx)
		if err != nil {
			return err
		}
		if len(keys) != 2 || keys[0].Name != "full" || keys[1].Name != "imported" || keys[0].VKValue != "sk-bf-x" {
			t.Errorf("ClientKeys = %+v", keys)
		}
		if _, err := tx.PutClientKey(ctx, ClientKey{Name: "novalue"}, ""); err == nil {
			t.Error("PutClientKey of a new key without a value succeeded")
		}
		if err := tx.DeleteClientKey(ctx, imported.ID); err != nil {
			return err
		}
		if err := tx.DeleteClientKey(ctx, imported.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("second DeleteClientKey err = %v, want ErrNotFound", err)
		}
		return nil
	})
	if _, err := s.ClientKeyByValue(ctx, "omni-imported-123"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted key still found: %v", err)
	}
}

func TestProvidersAndKeys(t *testing.T) {
	ctx := context.Background()
	s := openSealed(t)

	upA := Provider{Name: "up-a", Kind: "openai-compatible", BaseURL: "http://fake-openai:8080/v1", AllowPrivateNetwork: true}
	write(t, s, func(ctx context.Context, tx *Tx) error {
		if err := tx.PutProvider(ctx, upA); err != nil {
			return err
		}
		return tx.PutProviderKey(ctx, ProviderKey{Provider: "up-a", Name: "main", Secret: "test-upstream-key", Enabled: true})
	})
	write(t, s, func(ctx context.Context, tx *Tx) error {
		ps, err := tx.Providers(ctx)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(ps, []Provider{upA}) {
			t.Errorf("Providers = %+v, want [%+v]", ps, upA)
		}
		keys, err := tx.ProviderKeys(ctx)
		if err != nil {
			return err
		}
		if len(keys) != 1 || keys[0].ID == "" || keys[0].Secret != "test-upstream-key" || !keys[0].Enabled {
			t.Errorf("ProviderKeys = %+v", keys)
		}
		return nil
	})
	if rawContains(t, s, "provider_keys", "test-upstream-key") {
		t.Error("provider_keys row contains the secret")
	}

	err := s.Write(ctx, func(tx *Tx) error {
		return tx.PutProvider(ctx, Provider{Name: "bad", Kind: "weird"})
	})
	if err == nil {
		t.Error("PutProvider with kind weird succeeded")
	}

	router := Provider{Name: "openrouter", Kind: "builtin", Type: "openrouter"}
	write(t, s, func(ctx context.Context, tx *Tx) error {
		if err := tx.PutProvider(ctx, router); err != nil {
			return err
		}
		if err := tx.PutProviderKey(ctx, ProviderKey{Provider: "up-a", Name: "off", Secret: "test-disabled-key"}); err != nil {
			return err
		}
		return tx.PutProviderKey(ctx, ProviderKey{Provider: "openrouter", Name: "main", Secret: "test-router-key", Enabled: true})
	})
	write(t, s, func(ctx context.Context, tx *Tx) error {
		ps, err := tx.Providers(ctx)
		if err != nil {
			return err
		}
		if len(ps) != 2 || ps[0] != router {
			t.Errorf("Providers = %+v, want openrouter first with Type openrouter", ps)
		}
		return nil
	})
	byKind, err := s.ProviderKeysByKind(ctx, "openai-compatible")
	if err != nil || !reflect.DeepEqual(byKind, []string{"test-upstream-key"}) {
		t.Errorf("ProviderKeysByKind(openai-compatible) = (%v, %v), want [test-upstream-key]", byKind, err)
	}
	byKind, err = s.ProviderKeysByKind(ctx, "soniox")
	if err != nil || byKind == nil || len(byKind) != 0 {
		t.Errorf("ProviderKeysByKind(soniox) = (%#v, %v), want empty, not nil", byKind, err)
	}

	err = s.Write(ctx, func(tx *Tx) error {
		return tx.PutProviderKey(ctx, ProviderKey{Provider: "up-a", Name: "main", Secret: "x", Enabled: true})
	})
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate key name err = %v, want ErrDuplicate", err)
	}

	write(t, s, func(ctx context.Context, tx *Tx) error { return tx.DeleteProvider(ctx, "up-a") })
	write(t, s, func(ctx context.Context, tx *Tx) error {
		keys, err := tx.ProviderKeys(ctx)
		if err != nil {
			return err
		}
		if len(keys) != 1 || keys[0].Provider != "openrouter" {
			t.Errorf("after DeleteProvider: ProviderKeys = %+v, want only openrouter's key", keys)
		}
		if err := tx.DeleteProvider(ctx, "up-a"); !errors.Is(err, ErrNotFound) {
			t.Errorf("second DeleteProvider err = %v, want ErrNotFound", err)
		}
		if err := tx.DeleteProviderKey(ctx, keys[0].ID); err != nil {
			return err
		}
		keys, err = tx.ProviderKeys(ctx)
		if err != nil || len(keys) != 0 {
			t.Errorf("after DeleteProviderKey: ProviderKeys = (%+v, %v)", keys, err)
		}
		return nil
	})
}

func TestAliases(t *testing.T) {
	ctx := context.Background()
	s := openSealed(t)

	mf := alias.Alias{
		Name: "memory/fast", Kind: alias.KindChat,
		Members:      []alias.Member{{Ref: "inner"}, {Ref: "up-c/m1"}},
		Capabilities: []string{"structured_outputs"}, ContextWindow: 131072,
	}
	write(t, s, func(ctx context.Context, tx *Tx) error { return tx.PutAlias(ctx, mf) })
	all, err := s.Aliases(ctx)
	if err != nil || !reflect.DeepEqual(all["memory/fast"], mf) || len(all) != 1 {
		t.Errorf("Aliases = (%+v, %v), want {memory/fast: %+v}", all, err, mf)
	}

	mf.Members = []alias.Member{{Ref: "up-c/m1"}, {Ref: "inner"}}
	write(t, s, func(ctx context.Context, tx *Tx) error { return tx.PutAlias(ctx, mf) })
	write(t, s, func(ctx context.Context, tx *Tx) error {
		all, err := tx.Aliases(ctx)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(all["memory/fast"].Members, mf.Members) {
			t.Errorf("members = %+v, want reversed %+v", all["memory/fast"].Members, mf.Members)
		}
		return tx.DeleteAlias(ctx, "memory/fast")
	})
	var n int
	if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM alias_members").Scan(&n); err != nil || n != 0 {
		t.Errorf("alias_members after DeleteAlias = (%d, %v), want 0", n, err)
	}

	fast := alias.Alias{Name: "fast", Kind: alias.KindEmbeddings, Members: []alias.Member{{Ref: "up-a/m1"}}}
	write(t, s, func(ctx context.Context, tx *Tx) error {
		if err := tx.PutAlias(ctx, fast); err != nil {
			return err
		}
		return tx.SetAliasRuleID(ctx, "fast", "r1")
	})
	write(t, s, func(ctx context.Context, tx *Tx) error { return tx.PutAlias(ctx, fast) })
	ids, err := s.AliasRuleIDs(ctx)
	if err != nil || !reflect.DeepEqual(ids, map[string]string{"fast": "r1"}) {
		t.Errorf("AliasRuleIDs = (%v, %v), want {fast: r1}", ids, err)
	}
	all, err = s.Aliases(ctx)
	if err != nil || !reflect.DeepEqual(all, map[string]alias.Alias{"fast": fast}) {
		t.Errorf("Aliases = (%+v, %v), want {fast: %+v}", all, err, fast)
	}

	for _, bad := range []alias.Alias{
		{Name: "x", Kind: "video"},
		{Name: "/x", Kind: alias.KindChat},
	} {
		if err := s.Write(ctx, func(tx *Tx) error { return tx.PutAlias(ctx, bad) }); err == nil {
			t.Errorf("PutAlias(%+v) succeeded", bad)
		}
	}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.SetAliasRuleID(ctx, "nope", "r") }); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetAliasRuleID(nope) err = %v, want ErrNotFound", err)
	}
}

// TestTxAliasRuleIDs checks that the transaction reads its own uncommitted rule ids, not the committed ones.
func TestTxAliasRuleIDs(t *testing.T) {
	ctx := context.Background()
	s := openSealed(t)
	fast := alias.Alias{Name: "fast", Kind: alias.KindChat, Members: []alias.Member{{Ref: "up-a/m1"}}}
	slow := alias.Alias{Name: "slow", Kind: alias.KindChat, Members: []alias.Member{{Ref: "up-a/m1"}}}
	write(t, s, func(ctx context.Context, tx *Tx) error {
		if err := tx.PutAlias(ctx, fast); err != nil {
			return err
		}
		return tx.SetAliasRuleID(ctx, "fast", "r1")
	})
	write(t, s, func(ctx context.Context, tx *Tx) error {
		if err := tx.DeleteAlias(ctx, "fast"); err != nil {
			return err
		}
		if err := tx.PutAlias(ctx, fast); err != nil {
			return err
		}
		if err := tx.PutAlias(ctx, slow); err != nil {
			return err
		}
		if err := tx.SetAliasRuleID(ctx, "slow", "r2"); err != nil {
			return err
		}
		ids, err := tx.AliasRuleIDs(ctx)
		if err != nil || !reflect.DeepEqual(ids, map[string]string{"slow": "r2"}) {
			t.Errorf("tx.AliasRuleIDs = (%v, %v), want {slow: r2}", ids, err)
		}
		return nil
	})
	ids, err := s.AliasRuleIDs(ctx)
	if err != nil || !reflect.DeepEqual(ids, map[string]string{"slow": "r2"}) {
		t.Errorf("AliasRuleIDs after commit = (%v, %v), want {slow: r2}", ids, err)
	}
}

func TestEngineMACs(t *testing.T) {
	s := openSealed(t)
	write(t, s, func(ctx context.Context, tx *Tx) error {
		for _, kv := range [][2]string{{"provider:up-a", "m1"}, {"rule:fast", "m2"}, {"provider:up-a", "m3"}} {
			if err := tx.SetEngineMAC(ctx, kv[0], kv[1]); err != nil {
				return err
			}
		}
		if err := tx.DeleteEngineMAC(ctx, "rule:fast"); err != nil {
			return err
		}
		if err := tx.DeleteEngineMAC(ctx, "rule:fast"); err != nil {
			return err
		}
		got, err := tx.EngineMACs(ctx)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, map[string]string{"provider:up-a": "m3"}) {
			t.Errorf("EngineMACs = %v", got)
		}
		return nil
	})
}

func TestWrite_RollsBack(t *testing.T) {
	ctx := context.Background()
	s := openSealed(t)
	boom := errors.New("boom")
	err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.PutProvider(ctx, Provider{Name: "up-a", Kind: "openai-compatible"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("Write err = %v, want it to wrap boom", err)
	}
	write(t, s, func(ctx context.Context, tx *Tx) error {
		ps, err := tx.Providers(ctx)
		if err != nil {
			return err
		}
		if len(ps) != 0 {
			t.Errorf("Providers after rollback = %+v, want none", ps)
		}
		return nil
	})
}

func TestWrite_NoSealer(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.PutProvider(ctx, Provider{Name: "up-a", Kind: "openai-compatible"}); err != nil {
			return err
		}
		return tx.PutProviderKey(ctx, ProviderKey{Provider: "up-a", Name: "main", Secret: "k", Enabled: true})
	})
	if !errors.Is(err, ErrNoSealer) {
		t.Errorf("PutProviderKey without sealer err = %v, want ErrNoSealer", err)
	}
	if _, err := s.ClientKeyByValue(ctx, "x"); !errors.Is(err, ErrNoSealer) {
		t.Errorf("ClientKeyByValue without sealer err = %v, want ErrNoSealer", err)
	}
}

func TestGenerateKeyValue(t *testing.T) {
	a, b := GenerateKeyValue(), GenerateKeyValue()
	if a == b || !strings.HasPrefix(a, "sk-") || len(a) != 3+43 || strings.ContainsAny(a, "+/=") {
		t.Errorf("GenerateKeyValue = %q, %q", a, b)
	}
}

func TestNewID(t *testing.T) {
	id := newID()
	if len(id) != 36 || id[14] != '4' || !strings.ContainsRune("89ab", rune(id[19])) || id == newID() {
		t.Errorf("newID = %q, want a random uuid v4", id)
	}
}

func TestWrite_SerialisesAcrossProcesses(t *testing.T) {
	ctx := context.Background()
	s1 := openSealed(t)
	var file string
	if err := s1.DB().QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&file); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })

	errs := make(chan error, 20)
	for i := range 20 {
		s := s1
		if i%2 == 1 {
			s = s2
		}
		go func() {
			errs <- s.Write(ctx, func(tx *Tx) error {
				macs, err := tx.EngineMACs(ctx)
				if err != nil {
					return err
				}
				return tx.SetEngineMAC(ctx, "n", strconv.Itoa(len(macs)+i))
			})
		}()
	}
	for range 20 {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Write: %v", err)
		}
	}
}
