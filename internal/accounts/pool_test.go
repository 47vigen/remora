package accounts

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
)

func TestPoolCountsOnlyDroppedAccounts(t *testing.T) {
	e := newEnv(t)
	retry := time.Now().Add(30 * time.Second)
	e.fake.AddCredential(cliproxyapi.Credential{Name: "ag-ok.json", Provider: "antigravity", ProjectID: "p", Status: "active"})
	e.fake.AddCredential(cliproxyapi.Credential{Name: "ag-off.json", Provider: "antigravity", Disabled: true, Unavailable: true})
	e.fake.AddCredential(cliproxyapi.Credential{
		Name: "ag-cool.json", Provider: "antigravity", ProjectID: "p", Status: "error", Unavailable: true,
		Cooldowns: []cliproxyapi.Cooldown{{Scope: "model", Reason: "quota", RetryAt: retry}},
	})
	e.fake.AddCredential(cliproxyapi.Credential{
		Name: "ag-dead.json", Provider: "antigravity", ProjectID: "p", Status: "error", Unavailable: true,
		StatusMessage: "unauthorized (refresh token invalid)",
	})
	e.fake.AddCredential(cliproxyapi.Credential{
		Name: "ag-grant.json", Provider: "antigravity", ProjectID: "p", Status: "error", Unavailable: true,
		Cooldowns: []cliproxyapi.Cooldown{{Scope: "auth", Reason: "invalid_grant", RetryAt: retry}},
	})
	e.fake.AddCredential(cliproxyapi.Credential{Name: "ag-noproj.json", Provider: "antigravity", Status: "active"})
	e.addRow(t, "antigravity", "ok", "ag-ok.json", true)
	e.addRow(t, "antigravity", "off", "ag-off.json", false)
	e.addRow(t, "antigravity", "cool", "ag-cool.json", true)
	dead := e.addRow(t, "antigravity", "dead", "ag-dead.json", true)
	grant := e.addRow(t, "antigravity", "grant", "ag-grant.json", true)
	missing := e.addRow(t, "antigravity", "gone", "ag-gone.json", true)
	noProject := e.addRow(t, "antigravity", "noproj", "ag-noproj.json", true)

	got, err := e.svc.Pool(context.Background())
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}
	want := []PoolStatus{{Provider: "antigravity", Enabled: 6, Out: []OutAccount{
		{ID: dead.ID, Label: "dead", Reason: "unauthorized (refresh token invalid)"},
		{ID: grant.ID, Label: "grant", Reason: "signed out"},
		{ID: missing.ID, Label: "gone", Reason: "missing"},
		{ID: noProject.ID, Label: "noproj", Reason: MsgNoProject},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Pool = %+v\nwant   %+v", got, want)
	}
}

func TestFilterDropsProviderWithoutAccount(t *testing.T) {
	const provider = "antigravity"
	e := newEnv(t)
	ctx := t.Context()
	upstream := alias.Target{Provider: "up-a", Model: "m"}
	member := alias.Target{Provider: provider, Model: "m"}
	chain := []alias.Target{member, upstream}
	cred := cliproxyapi.Credential{Name: "sub.json", Provider: provider, ProjectID: "project"}
	row := e.addRow(t, provider, "sub", cred.Name, false)
	e.fake.AddCredential(cred)

	check := func(want []alias.Target, skipped ...string) {
		t.Helper()
		if err := e.svc.LoadAvailability(ctx); err != nil {
			t.Fatal(err)
		}
		if kept, dropped := e.svc.Available(slices.Clone(chain)); !slices.Equal(kept, want) || !slices.Equal(dropped, skipped) {
			t.Fatalf("Available = %v, skipped %v; want %v, skipped %v", kept, dropped, want, skipped)
		}
	}
	check([]alias.Target{upstream}, provider) // engine credential alone is not enough
	if err := e.svc.SetEnabled(ctx, row.ID, true); err != nil {
		t.Fatal(err)
	}
	check(chain)
	cred.Disabled = true
	e.fake.AddCredential(cred)
	check([]alias.Target{upstream}, provider) // enabled store row alone is not enough
	cred.Disabled, cred.Unavailable = false, true
	cred.Cooldowns = []cliproxyapi.Cooldown{{Scope: "auth", Reason: "invalid_grant"}}
	e.fake.AddCredential(cred)
	check([]alias.Target{upstream}, provider)
	cred.Cooldowns[0].Reason = "quota"
	e.fake.AddCredential(cred)
	check(chain) // a temporary cooldown does not sign the account out
}

func TestPoolEmpty(t *testing.T) {
	e := newEnv(t)
	got, err := e.svc.Pool(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("Pool = %+v, %v; want none", got, err)
	}
}
