package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestAccountQuotaRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	a, err := s.CreateAccount(ctx, account("antigravity", "a@example.test", "antigravity-a.json"))
	if err != nil {
		t.Fatal(err)
	}
	reset := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	frac := 0.8
	want := AccountQuota{
		AccountID: a.ID, Plan: "Pro", LimitReached: true, Source: "poll",
		Windows: []QuotaWindow{
			{Scope: "gemini", Window: "5h", Remaining: &frac, ResetsAt: &reset},
			{Scope: "", Window: "weekly"},
		},
		FetchedAt: reset.Add(-time.Hour), CheckedAt: reset.Add(-time.Minute), Error: "",
	}
	if err := s.PutAccountQuota(ctx, want); err != nil {
		t.Fatalf("PutAccountQuota: %v", err)
	}
	got, err := s.AccountQuotas(ctx)
	if err != nil {
		t.Fatalf("AccountQuotas: %v", err)
	}
	if !reflect.DeepEqual(got[a.ID], want) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got[a.ID], want)
	}

	want.Plan, want.Windows, want.Error, want.LimitReached = "", []QuotaWindow{}, "sign in again", false
	if err := s.PutAccountQuota(ctx, want); err != nil {
		t.Fatalf("second PutAccountQuota: %v", err)
	}
	got, _ = s.AccountQuotas(ctx)
	if len(got) != 1 || !reflect.DeepEqual(got[a.ID], want) {
		t.Errorf("overwrite: got %+v, want %+v", got, want)
	}
}

func TestAccountQuotaZeroFetchedAt(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	a, _ := s.CreateAccount(ctx, account("codex", "a@example.test", "codex-a.json"))
	checked := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	if err := s.PutAccountQuota(ctx, AccountQuota{AccountID: a.ID, Source: "poll", CheckedAt: checked, Error: "x"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.AccountQuotas(ctx)
	if q := got[a.ID]; !q.FetchedAt.IsZero() || !q.CheckedAt.Equal(checked) || q.Windows == nil {
		t.Errorf("got %+v, want zero FetchedAt, empty windows", q)
	}
}

func TestAccountQuotaGoesWithAccount(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	a, _ := s.CreateAccount(ctx, account("codex", "a@example.test", "codex-a.json"))
	if err := s.PutAccountQuota(ctx, AccountQuota{AccountID: a.ID, Source: "poll", CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.AccountQuotas(ctx); len(got) != 0 {
		t.Errorf("snapshot outlived its account: %+v", got)
	}
}

func TestAccountQuotaUnknownAccount(t *testing.T) {
	s := openTemp(t)
	err := s.PutAccountQuota(context.Background(), AccountQuota{AccountID: 999, Source: "poll", CheckedAt: time.Now()})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
