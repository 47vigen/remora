//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/47vigen/remora/test/fakes/control"
)

// The quota fakes answer under this upstream; compose points REMORA_QUOTA_BASE_URL at it.
const quotaUpstream = "quota"

type quotaWindow struct {
	Scope     string   `json:"scope"`
	Window    string   `json:"window"`
	Remaining *float64 `json:"remaining"`
}

type quotaSnapshot struct {
	Plan         string        `json:"plan"`
	Windows      []quotaWindow `json:"windows"`
	LimitReached bool          `json:"limit_reached"`
	Source       string        `json:"source"`
	FetchedAt    *time.Time    `json:"fetched_at"`
	Error        string        `json:"error"`
}

type quotaProvider struct {
	Provider string `json:"provider"`
	Accounts []struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
		Quota *quotaSnapshot `json:"quota"`
	} `json:"accounts"`
}

// importQuotaAccount imports testdata/quota/<fixture>.json as a provider account and deletes it when the test ends.
func importQuotaAccount(t *testing.T, a *admin, provider, fixture string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/quota/" + fixture + ".json") //nolint:gosec // G304: a fixture name from this file
	if err != nil {
		t.Fatal(err)
	}
	var cred map[string]any
	decodeJSON(t, raw, &cred)
	var acc struct {
		ID string `json:"id"`
	}
	decodeJSON(t, a.must(http.MethodPost, "/api/accounts/import", mustJSON(t, map[string]any{"provider": provider, "credential": cred}),
		http.StatusCreated), &acc)
	t.Cleanup(func() { a.deleteIfAny("/api/accounts/" + acc.ID) })
	return acc.ID
}

// snapshotOf returns the quota of account id from GET /api/quota, nil when it has none.
func snapshotOf(t *testing.T, a *admin, id string) *quotaSnapshot {
	t.Helper()
	var providers []quotaProvider
	decodeJSON(t, a.must(http.MethodGet, "/api/quota", "", http.StatusOK), &providers)
	for _, p := range providers {
		for _, acc := range p.Accounts {
			if acc.Account.ID == id {
				return acc.Quota
			}
		}
	}
	return nil
}

func refreshQuota(t *testing.T, a *admin, provider string) (queued, skipped []string) {
	t.Helper()
	var out struct {
		Queued  []string `json:"queued"`
		Skipped []string `json:"skipped"`
	}
	decodeJSON(t, a.must(http.MethodPost, "/api/quota/refresh", mustJSON(t, map[string]string{"provider": provider}), http.StatusAccepted), &out)
	return out.Queued, out.Skipped
}

func windowsOf(q *quotaSnapshot) map[string]*float64 {
	out := map[string]*float64{}
	for _, w := range q.Windows {
		out[w.Scope+"/"+w.Window] = w.Remaining
	}
	return out
}

func is(p *float64, want float64) bool { return p != nil && *p == want }

func TestQuotaEndToEnd(t *testing.T) {
	resetFakes(t)
	a := loginClient(t)
	ids := map[string]string{}
	for _, p := range []string{"antigravity", "codex", "claude"} {
		ids[p] = importQuotaAccount(t, a, p, p)
	}
	for p, id := range ids {
		if queued, _ := refreshQuota(t, a, p); !slices.Equal(queued, []string{id}) {
			t.Fatalf("refresh %s: queued %v, want [%s]", p, queued, id)
		}
	}
	eventually(t, 30*time.Second, "every account polled", func() bool {
		for _, id := range ids {
			if q := snapshotOf(t, a, id); q == nil || q.FetchedAt == nil {
				return false
			}
		}
		return true
	})

	ag := snapshotOf(t, a, ids["antigravity"])
	w := windowsOf(ag)
	if ag.Plan != "Pro" || !ag.LimitReached || ag.Source != "poll" || len(ag.Windows) != 4 || !is(w["gemini/5h"], 0.8) ||
		!is(w["gemini/weekly"], 0.5) || !is(w["claude-gpt/5h"], 0) || !is(w["claude-gpt/weekly"], 0.25) {
		t.Errorf("antigravity = %+v", ag)
	}
	cx := snapshotOf(t, a, ids["codex"])
	if w := windowsOf(cx); cx.Plan != "plus" || !is(w["/5h"], 0.7) || !is(w["/weekly"], 0.4) {
		t.Errorf("codex = %+v", cx)
	}
	cl := snapshotOf(t, a, ids["claude"])
	if w := windowsOf(cl); !is(w["/5h"], 0.9) || !is(w["/weekly"], 0.6) || w["opus/weekly"] != nil || len(cl.Windows) != 2 {
		t.Errorf("claude = %+v", cl)
	}

	summary := received(t, quotaUpstream, control.RouteQuotaAntigravitySummary)
	if len(summary) != 1 {
		t.Fatalf("summary requests = %d", len(summary))
	}
	h := summary[0].Header
	if h.Get("Authorization") != "Bearer fake-antigravity-token" || h.Get("User-Agent") != "antigravity/hub/9.9.9 darwin/arm64" ||
		string(summary[0].Body) != `{"project":"e2e-project"}` {
		t.Errorf("summary request: %v %s", h, summary[0].Body)
	}
	if r := received(t, quotaUpstream, control.RouteQuotaCodexUsage); len(r) != 1 || r[0].Header.Get("chatgpt-account-id") != "acct-e2e" ||
		r[0].Header.Get("Authorization") != "Bearer fake-codex-token" {
		t.Errorf("codex requests = %+v", r)
	}
	if r := received(t, quotaUpstream, control.RouteQuotaClaudeUsage); len(r) != 1 || r[0].Header.Get("anthropic-beta") != "oauth-2025-04-20" {
		t.Errorf("claude requests = %+v", r)
	}

	if _, skipped := refreshQuota(t, a, "codex"); !slices.Equal(skipped, []string{ids["codex"]}) {
		t.Errorf("second codex refresh skipped %v, want [%s]", skipped, ids["codex"])
	}
	time.Sleep(3 * time.Second)
	if n := len(received(t, quotaUpstream, control.RouteQuotaCodexUsage)); n != 1 {
		t.Errorf("codex requests after a skipped refresh = %d, want 1", n)
	}

	var list struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	decodeJSON(t, a.must(http.MethodGet, "/api/accounts?limit=200", "", http.StatusOK), &list)
	for _, item := range list.Items {
		if raw := item["quota"]; len(raw) == 0 || raw[0] != '[' {
			t.Errorf("GET /api/accounts quota is not an array: %s", raw)
		}
	}
}

func TestQuotaSignInAgain(t *testing.T) {
	resetFakes(t)
	a := loginClient(t)
	id := importQuotaAccount(t, a, "codex", "codex")
	once(t, quotaUpstream, control.RouteQuotaCodexUsage, control.Step{Status: http.StatusUnauthorized}, control.Step{Status: http.StatusUnauthorized})
	var out struct {
		Queued []string `json:"queued"`
	}
	decodeJSON(t, a.must(http.MethodPost, "/api/quota/refresh", mustJSON(t, map[string]string{"account_id": id}), http.StatusAccepted), &out)
	if !slices.Equal(out.Queued, []string{id}) {
		t.Fatalf("queued = %v", out.Queued)
	}
	eventually(t, 30*time.Second, "sign in again", func() bool {
		q := snapshotOf(t, a, id)
		return q != nil && q.Error == "sign in again"
	})
	if q := snapshotOf(t, a, id); len(q.Windows) != 0 || q.FetchedAt != nil {
		t.Errorf("snapshot = %+v, want no windows and never fetched", q)
	}
}
