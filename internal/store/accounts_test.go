package store

import (
	"context"
	"errors"
	"testing"
	"time"
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
