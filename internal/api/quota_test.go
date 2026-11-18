package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/accounts"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/quota"
	"github.com/47vigen/remora/internal/store"
)

func TestGetQuota(t *testing.T) {
	env := newTestEnv(t)
	reset := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	frac := 0.8
	env.quota.overview = []quota.ProviderView{
		{
			Provider: "antigravity", Supported: true, Available: 1, Exhausted: 2, Cooling: 3, SignIn: 4, NextReset: &reset,
			Accounts: []quota.AccountView{{
				Account: accounts.Account{
					Account:   store.Account{ID: 1, Provider: "antigravity", Label: "a@example.test", Enabled: true},
					Status:    "active",
					Cooldowns: []cliproxyapi.Cooldown{{Scope: "model", ModelKey: "m", Reason: "quota", RetryAt: reset}},
				},
				Quota: &store.AccountQuota{AccountID: 1, Plan: "Pro", Source: "poll", CheckedAt: reset, Windows: []store.QuotaWindow{
					{Scope: "gemini", Window: "5h", Remaining: &frac, ResetsAt: &reset}, {Scope: "gemini", Window: "weekly"},
				}},
			}},
		},
		{Provider: "kimi", Accounts: []quota.AccountView{{Account: accounts.Account{Account: store.Account{ID: 2, Provider: "kimi"}}}}},
	}
	code, body := env.do(t, env.client, http.MethodGet, "/api/quota", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/quota = %d %s", code, body)
	}
	var page struct{ Items []map[string]any }
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	got := page.Items
	ag := got[0]
	if ag["available"] != 1.0 || ag["exhausted"] != 2.0 || ag["cooling"] != 3.0 || ag["sign_in"] != 4.0 || ag["next_reset"] != "2099-01-01T00:00:00Z" {
		t.Errorf("antigravity = %v", ag)
	}
	acc := ag["accounts"].([]any)[0].(map[string]any)
	if _, isArray := acc["account"].(map[string]any)["quota"].([]any); !isArray {
		t.Errorf("account.quota is not an array: %v", acc["account"])
	}
	q := acc["quota"].(map[string]any)
	if _, has := q["fetched_at"]; has || q["plan"] != "Pro" || q["source"] != "poll" {
		t.Errorf("quota = %v", q)
	}
	if !strings.Contains(string(body), `"remaining":null`) {
		t.Errorf("unknown window does not read null: %s", body)
	}
	if _, has := got[1]["accounts"].([]any)[0].(map[string]any)["quota"]; has {
		t.Errorf("unsupported account has a quota: %v", got[1])
	}
}

func TestGetQuotaPages(t *testing.T) {
	env := newTestEnv(t)
	account := func(id int64, label, plan string) quota.AccountView {
		return quota.AccountView{
			Account: accounts.Account{Account: store.Account{ID: id, Label: label}},
			Quota:   &store.AccountQuota{Plan: plan},
		}
	}
	env.quota.overview = []quota.ProviderView{
		{Provider: "one", Available: 3, Accounts: []quota.AccountView{account(1, "a", "Team"), account(2, "Teammate", "Free"), account(3, "c", "Free")}},
		{Provider: "two", Available: 1, Accounts: []quota.AccountView{account(4, "d", "Team")}},
	}
	for _, tc := range []struct {
		query string
		total int
		ids   []string
	}{
		{"?limit=2&offset=2", 4, []string{"3", "4"}},
		{"?limit=1&offset=1&q=TEAM", 3, []string{"2"}},
		{"?q=team&provider=two", 1, []string{"4"}},
		{"?offset=10", 4, nil},
		{"?q=missing", 0, nil},
	} {
		t.Run(tc.query, func(t *testing.T) {
			code, body := env.do(t, env.client, http.MethodGet, "/api/quota"+tc.query, "")
			if code != http.StatusOK {
				t.Fatalf("GET quota = %d %s", code, body)
			}
			var got struct {
				Total int
				Items []struct {
					Provider  string
					Available int
					Accounts  []struct{ Account struct{ ID string } }
				}
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, p := range got.Items {
				if p.Provider == "one" && p.Available != 3 {
					t.Errorf("provider summary changed: %+v", p)
				}
				for _, a := range p.Accounts {
					ids = append(ids, a.Account.ID)
				}
			}
			if got.Total != tc.total || !slices.Equal(ids, tc.ids) {
				t.Errorf("total %d, ids %v; want %d, %v", got.Total, ids, tc.total, tc.ids)
			}
		})
	}
}

func TestRefreshQuota(t *testing.T) {
	env := newTestEnv(t)
	env.quota.queued, env.quota.skipped = []string{"2"}, []string{"1"}
	code, body := env.do(t, env.client, http.MethodPost, "/api/quota/refresh", `{"provider":"codex"}`)
	if code != http.StatusAccepted || strings.TrimSpace(string(body)) != `{"queued":["2"],"skipped":["1"]}` {
		t.Errorf("refresh = %d %s", code, body)
	}
	env.do(t, env.client, http.MethodPost, "/api/quota/refresh", `{"account_id":"7"}`)
	if r := env.quota.refreshed; len(r) != 2 || r[0] != [2]string{"", "codex"} || r[1] != [2]string{"7", ""} {
		t.Errorf("refreshed = %v", r)
	}
	env.quota.queued, env.quota.skipped = nil, nil
	if _, body := env.do(t, env.client, http.MethodPost, "/api/quota/refresh", `{"provider":"codex"}`); strings.TrimSpace(string(body)) != `{"queued":[],"skipped":[]}` {
		t.Errorf("empty refresh = %s", body)
	}
	for err, want := range map[error]int{
		quota.ErrRefreshTarget: http.StatusBadRequest, quota.ErrUnsupported: http.StatusBadRequest, store.ErrNotFound: http.StatusNotFound,
	} {
		env.quota.err = err
		if code, body := env.do(t, env.client, http.MethodPost, "/api/quota/refresh", `{}`); code != want {
			t.Errorf("%v: %d %s, want %d", err, code, body, want)
		}
	}
}

func TestQuotaNotImplemented(t *testing.T) {
	h := New(Deps{}).Handler()
	for _, r := range []*http.Request{
		httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/quota", http.NoBody),
		httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/quota/refresh", strings.NewReader(`{"provider":"codex"}`)),
	} {
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s = %d, want 501", r.Method, r.URL.Path, rec.Code)
		}
	}
}
