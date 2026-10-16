package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/alias"
)

// account builds an enabled account; credential is the engine's credential file name, not a secret.
func account(provider, label, credential string) Account {
	return Account{Provider: provider, Label: label, Credential: credential, Enabled: true}
}

func TestAccountsCRUD(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	before := time.Now().Add(-time.Second)

	b, err := s.CreateAccount(ctx, account("grok", "b@x", "xai-b.json"))
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	a, err := s.CreateAccount(ctx, account("codex", "a@x", "codex-a.json"))
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if a.ID == 0 || b.ID == 0 || a.ID == b.ID {
		t.Errorf("ids = %d, %d", a.ID, b.ID)
	}
	if a.CreatedAt.Before(before) || a.CreatedAt.After(time.Now().Add(time.Second)) {
		t.Errorf("CreatedAt = %v", a.CreatedAt)
	}

	all, err := s.Accounts(ctx)
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if len(all) != 2 || all[0].ID != a.ID || all[1].ID != b.ID {
		t.Fatalf("Accounts = %+v, want codex then grok", all)
	}
	if all[0] != a {
		t.Errorf("listed %+v, created %+v", all[0], a)
	}

	if err := s.SetAccountEnabled(ctx, a.ID, false); err != nil {
		t.Fatalf("SetAccountEnabled: %v", err)
	}
	got, err := s.Account(ctx, a.ID)
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	if got.Enabled || got.Provider != "codex" || got.Label != "a@x" || got.Credential != "codex-a.json" {
		t.Errorf("Account after disable = %+v", got)
	}

	if err := s.DeleteAccount(ctx, a.ID); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	if _, err := s.Account(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Account after delete err = %v, want ErrNotFound", err)
	}
	if all, _ := s.Accounts(ctx); len(all) != 1 || all[0].ID != b.ID {
		t.Errorf("Accounts after delete = %+v", all)
	}
}

func TestAccountsOrderedByProviderThenID(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	for _, a := range []Account{
		{Provider: "kimi", Credential: "k1.json"},
		{Provider: "codex", Credential: "c1.json"},
		{Provider: "kimi", Credential: "k2.json"},
		{Provider: "codex", Credential: "c2.json"},
	} {
		if _, err := s.CreateAccount(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.Accounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range all {
		got = append(got, a.Credential)
	}
	want := []string{"c1.json", "c2.json", "k1.json", "k2.json"}
	if len(got) != len(want) {
		t.Fatalf("Accounts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Accounts = %v, want %v", got, want)
		}
	}
}

func TestAccountCredentialUnique(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	if _, err := s.CreateAccount(ctx, account("codex", "", "codex-a.json")); err != nil {
		t.Fatal(err)
	}
	_, err := s.CreateAccount(ctx, account("claude", "", "codex-a.json"))
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate credential err = %v, want ErrDuplicate", err)
	}
}

func TestAccountMissingIsNotFound(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	if err := s.SetAccountEnabled(ctx, 999, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetAccountEnabled(999) err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteAccount(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteAccount(999) err = %v, want ErrNotFound", err)
	}
	if _, err := s.Account(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("Account(999) err = %v, want ErrNotFound", err)
	}
}

func TestAccountPriorityAndStrategies(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	a, err := s.CreateAccount(ctx, account("codex", "a@x", "codex-a.json"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Priority != 0 {
		t.Errorf("new account priority = %d", a.Priority)
	}
	if err := s.SetAccountPriority(ctx, a.ID, 7); err != nil {
		t.Fatalf("SetAccountPriority: %v", err)
	}
	if got, err := s.Account(ctx, a.ID); err != nil || got.Priority != 7 {
		t.Errorf("Account = %+v, %v; want priority 7", got, err)
	}
	if rows, _ := s.Accounts(ctx); len(rows) != 1 || rows[0].Priority != 7 {
		t.Errorf("Accounts = %+v", rows)
	}
	if err := s.SetAccountPriority(ctx, a.ID+100, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: err = %v, want ErrNotFound", err)
	}

	if got, err := s.AccountStrategies(ctx); err != nil || len(got) != 0 {
		t.Errorf("AccountStrategies = %v, %v; want empty", got, err)
	}
	for _, st := range []alias.Strategy{alias.StrategyP2C, alias.StrategyFillFirst} {
		if err := s.SetAccountStrategy(ctx, "codex", st); err != nil {
			t.Fatalf("SetAccountStrategy: %v", err)
		}
	}
	if err := s.SetAccountStrategy(ctx, "grok", alias.StrategyRandom); err != nil {
		t.Fatal(err)
	}
	got, err := s.AccountStrategies(ctx)
	if want := map[string]alias.Strategy{"codex": alias.StrategyFillFirst, "grok": alias.StrategyRandom}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("AccountStrategies = %v, %v; want %v", got, err, want)
	}
}
