package accounts

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/engine/cliproxyapi"
)

func TestPoolCountsOnlyDroppedAccounts(t *testing.T) {
	e := newEnv(t)
	retry := time.Now().Add(30 * time.Second)
	e.fake.AddCredential(cliproxyapi.Credential{Name: "ag-ok.json", Provider: "antigravity", Status: "active"})
	e.fake.AddCredential(cliproxyapi.Credential{Name: "ag-off.json", Provider: "antigravity", Disabled: true, Unavailable: true})
	e.fake.AddCredential(cliproxyapi.Credential{
		Name: "ag-cool.json", Provider: "antigravity", Status: "error", Unavailable: true,
		Cooldowns: []cliproxyapi.Cooldown{{Scope: "model", Reason: "quota", RetryAt: retry}},
	})
	e.fake.AddCredential(cliproxyapi.Credential{
		Name: "ag-dead.json", Provider: "antigravity", Status: "error", Unavailable: true,
		StatusMessage: "unauthorized (refresh token invalid)",
	})
	e.fake.AddCredential(cliproxyapi.Credential{
		Name: "ag-grant.json", Provider: "antigravity", Status: "error", Unavailable: true,
		Cooldowns: []cliproxyapi.Cooldown{{Scope: "auth", Reason: "invalid_grant", RetryAt: retry}},
	})
	e.addRow(t, "antigravity", "ok", "ag-ok.json", true)
	e.addRow(t, "antigravity", "off", "ag-off.json", false)
	e.addRow(t, "antigravity", "cool", "ag-cool.json", true)
	dead := e.addRow(t, "antigravity", "dead", "ag-dead.json", true)
	grant := e.addRow(t, "antigravity", "grant", "ag-grant.json", true)
	missing := e.addRow(t, "antigravity", "gone", "ag-gone.json", true)

	got, err := e.svc.Pool(context.Background())
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}
	want := []PoolStatus{{Provider: "antigravity", Enabled: 5, Out: []OutAccount{
		{ID: dead.ID, Label: "dead", Reason: "unauthorized (refresh token invalid)"},
		{ID: grant.ID, Label: "grant", Reason: "signed out"},
		{ID: missing.ID, Label: "gone", Reason: "missing"},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Pool = %+v\nwant   %+v", got, want)
	}
}

func TestPoolEmpty(t *testing.T) {
	e := newEnv(t)
	got, err := e.svc.Pool(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("Pool = %+v, %v; want none", got, err)
	}
}
