package api

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/alias"
)

type aliasJSON struct {
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	Members       []string `json:"members"`
	Capabilities  []string `json:"capabilities"`
	ContextWindow *int     `json:"context_window"`
}

type chainJSON struct {
	Targets []struct {
		Provider     string     `json:"provider"`
		Model        string     `json:"model"`
		CoolingUntil *time.Time `json:"cooling_until"`
	} `json:"targets"`
}

func refs(a alias.Alias) []string {
	out := make([]string, 0, len(a.Members))
	for _, m := range a.Members {
		out = append(out, m.Ref)
	}
	return out
}

func TestAliasesCRUD(t *testing.T) {
	env := newTestEnv(t)
	env.aliases.m = map[string]alias.Alias{"cheap": {Name: "cheap", Kind: alias.KindChat, Members: []alias.Member{{Ref: "ag/b"}}}}

	code, body := env.do(t, env.client, http.MethodPost, "/api/aliases",
		`{"name":"fast","kind":"chat","members":["or/a","cheap"],"capabilities":["tools"],"context_window":128000}`)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, body)
	}
	var a aliasJSON
	decode(t, body, &a)
	if a.Name != "fast" || a.Kind != "chat" || !slices.Equal(a.Members, []string{"or/a", "cheap"}) ||
		!slices.Equal(a.Capabilities, []string{"tools"}) || a.ContextWindow == nil || *a.ContextWindow != 128000 {
		t.Errorf("create = %s", body)
	}

	code, body = env.do(t, env.client, http.MethodGet, "/api/aliases/fast", "")
	if code != http.StatusOK {
		t.Fatalf("get = %d %s", code, body)
	}
	decode(t, body, &a)
	if !slices.Equal(a.Members, []string{"or/a", "cheap"}) {
		t.Errorf("get = %s", body)
	}

	code, body = env.do(t, env.client, http.MethodPut, "/api/aliases/fast", `{"name":"fast","kind":"chat","members":["cheap","or/a"]}`)
	if code != http.StatusOK {
		t.Fatalf("put = %d %s", code, body)
	}
	if !strings.Contains(string(body), `"capabilities":[]`) {
		t.Errorf("put answer has no empty capabilities list: %s", body)
	}
	stored, _ := env.aliases.Get(t.Context(), "fast")
	if got := refs(stored); !slices.Equal(got, []string{"cheap", "or/a"}) {
		t.Errorf("stored members after PUT = %v", got)
	}
	if code, body := env.do(t, env.client, http.MethodPut, "/api/aliases/fast", `{"name":"slow","kind":"chat","members":["or/a"]}`); code != http.StatusBadRequest {
		t.Errorf("put with a different name = %d %s", code, body)
	}
	if code, body := env.do(t, env.client, http.MethodPost, "/api/aliases", `{"name":"fast","kind":"chat","members":["or/a"]}`); code != http.StatusConflict {
		t.Errorf("create duplicate = %d %s", code, body)
	}

	if code, body := env.do(t, env.client, http.MethodDelete, "/api/aliases/fast", ""); code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", code, body)
	}
	if code, body := env.do(t, env.client, http.MethodGet, "/api/aliases/fast", ""); code != http.StatusNotFound {
		t.Errorf("get after delete = %d %s", code, body)
	}
	if code, body := env.do(t, env.client, http.MethodDelete, "/api/aliases/fast", ""); code != http.StatusNotFound {
		t.Errorf("delete again = %d %s", code, body)
	}
}

func TestAliasErrors(t *testing.T) {
	env := newTestEnv(t)
	for _, tc := range []struct {
		name, body string
		putErr     error
		contains   string
	}{
		{"cycle", `{"name":"a","kind":"chat","members":["b"]}`, fmt.Errorf("reconcile: change: %w", fmt.Errorf("%w: a -> b -> a", alias.ErrCycle)), "cycle"},
		{"unknown member", `{"name":"a","kind":"chat","members":["nope"]}`, fmt.Errorf("%w: %q in a", alias.ErrUnknownMember, "nope"), "unknown member"},
		{"unknown kind", `{"name":"a","kind":"images","members":["or/a"]}`, nil, "kind"},
		{"no members", `{"name":"a","kind":"chat","members":[]}`, nil, "member"},
		{"empty member", `{"name":"a","kind":"chat","members":[""]}`, nil, "member"},
		{"bad name", `{"name":" a","kind":"chat","members":["or/a"]}`, nil, "name"},
		{"negative context window", `{"name":"a","kind":"chat","members":["or/a"],"context_window":-1}`, nil, "context_window"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env.aliases.mu.Lock()
			env.aliases.putErr = tc.putErr
			env.aliases.mu.Unlock()
			code, body := env.do(t, env.client, http.MethodPost, "/api/aliases", tc.body)
			if code != http.StatusBadRequest || errCode(t, body) != "invalid_request" {
				t.Errorf("status = %d %s, want 400 invalid_request", code, body)
			}
			if !strings.Contains(string(body), tc.contains) {
				t.Errorf("body %s does not mention %q", body, tc.contains)
			}
		})
	}
}

func TestAliasChain(t *testing.T) {
	env := newTestEnv(t)
	env.aliases.m = map[string]alias.Alias{
		"fast": {Name: "fast", Kind: alias.KindChat, Members: []alias.Member{{Ref: "or/a"}, {Ref: "ag/b"}}},
	}
	until := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	env.aliases.setCooling(Cooling{Target: alias.Target{Provider: "ag", Model: "b"}, Until: until, Reason: "rate limited"})

	code, body := env.do(t, env.client, http.MethodGet, "/api/aliases/fast/chain", "")
	if code != http.StatusOK {
		t.Fatalf("chain = %d %s", code, body)
	}
	var c chainJSON
	decode(t, body, &c)
	if len(c.Targets) != 2 || c.Targets[0].Provider != "or" || c.Targets[0].Model != "a" || c.Targets[1].Provider != "ag" || c.Targets[1].Model != "b" {
		t.Fatalf("chain = %s", body)
	}
	if c.Targets[0].CoolingUntil != nil {
		t.Errorf("targets[0] cooling = %v, want none", c.Targets[0].CoolingUntil)
	}
	if c.Targets[1].CoolingUntil == nil || !c.Targets[1].CoolingUntil.Equal(until) {
		t.Errorf("targets[1] cooling = %v, want %v", c.Targets[1].CoolingUntil, until)
	}
	if code, body := env.do(t, env.client, http.MethodGet, "/api/aliases/none/chain", ""); code != http.StatusNotFound {
		t.Errorf("chain of unknown alias = %d %s", code, body)
	}
}

func TestCompileAlias(t *testing.T) {
	env := newTestEnv(t)
	env.aliases.m = map[string]alias.Alias{
		"fast": {Name: "fast", Kind: alias.KindChat, Members: []alias.Member{{Ref: "or/a"}, {Ref: "ag/b"}}},
	}
	code, body := env.do(t, env.client, http.MethodPost, "/api/aliases/compile", `{"name":"draft","kind":"chat","members":["fast","or/c"]}`)
	if code != http.StatusOK {
		t.Fatalf("compile = %d %s", code, body)
	}
	var c chainJSON
	decode(t, body, &c)
	var got []string
	for _, tg := range c.Targets {
		got = append(got, tg.Provider+"/"+tg.Model)
	}
	if !slices.Equal(got, []string{"or/a", "ag/b", "or/c"}) {
		t.Errorf("compiled = %v", got)
	}
	if _, err := env.aliases.Get(t.Context(), "draft"); err == nil {
		t.Error("compile stored the alias")
	}

	// fast -> draft -> fast: a cycle through the stored alias.
	env.aliases.m["fast"] = alias.Alias{Name: "fast", Kind: alias.KindChat, Members: []alias.Member{{Ref: "draft"}}}
	code, body = env.do(t, env.client, http.MethodPost, "/api/aliases/compile", `{"name":"draft","kind":"chat","members":["fast"]}`)
	if code != http.StatusBadRequest || errCode(t, body) != "invalid_request" || !strings.Contains(string(body), "cycle") {
		t.Errorf("compile cycle = %d %s", code, body)
	}
}

func TestListCooling(t *testing.T) {
	env := newTestEnv(t)
	env.aliases.m = map[string]alias.Alias{
		"fast":  {Name: "fast", Kind: alias.KindChat, Members: []alias.Member{{Ref: "or/a"}, {Ref: "ag/b"}}},
		"smart": {Name: "smart", Kind: alias.KindChat, Members: []alias.Member{{Ref: "ag/b"}}},
		"other": {Name: "other", Kind: alias.KindChat, Members: []alias.Member{{Ref: "or/a"}}},
	}
	until := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	env.aliases.setCooling(Cooling{Target: alias.Target{Provider: "ag", Model: "b"}, Until: until, Reason: "rate limited"})

	code, body := env.do(t, env.client, http.MethodGet, "/api/cooling", "")
	if code != http.StatusOK {
		t.Fatalf("cooling = %d %s", code, body)
	}
	var list struct {
		Items []struct {
			Provider, Model, Reason string
			Until                   time.Time
			Aliases                 []string
		}
		Total int
	}
	decode(t, body, &list)
	if list.Total != 1 || len(list.Items) != 1 {
		t.Fatalf("cooling = %s", body)
	}
	it := list.Items[0]
	if it.Provider != "ag" || it.Model != "b" || it.Reason != "rate limited" || !it.Until.Equal(until) ||
		!slices.Equal(it.Aliases, []string{"fast", "smart"}) {
		t.Errorf("cooling item = %+v", it)
	}

	code, body = env.do(t, env.client, http.MethodGet, "/api/cooling?q=nothing", "")
	if code != http.StatusOK || strings.TrimSpace(string(body)) != `{"items":[],"total":0}` {
		t.Errorf("cooling ?q=nothing = %d %s", code, body)
	}
}

func TestListAliasesFilter(t *testing.T) {
	env := newTestEnv(t)
	env.aliases.m = map[string]alias.Alias{
		"fast":  {Name: "fast", Kind: alias.KindChat, Members: []alias.Member{{Ref: "or/a"}}},
		"smart": {Name: "smart", Kind: alias.KindChat, Members: []alias.Member{{Ref: "or/b"}}},
		"embed": {Name: "embed", Kind: alias.KindEmbeddings, Members: []alias.Member{{Ref: "or/e"}}},
	}
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"", []string{"embed", "fast", "smart"}},
		{"?kind=embeddings", []string{"embed"}},
		{"?q=SMA", []string{"smart"}},
		{"?kind=chat&q=fa", []string{"fast"}},
		{"?limit=1&offset=2", []string{"smart"}},
	} {
		code, body := env.do(t, env.client, http.MethodGet, "/api/aliases"+tc.query, "")
		if code != http.StatusOK {
			t.Fatalf("list %s = %d %s", tc.query, code, body)
		}
		var list struct {
			Items []aliasJSON
			Total int
		}
		decode(t, body, &list)
		var names []string
		for _, a := range list.Items {
			names = append(names, a.Name)
		}
		if !slices.Equal(names, tc.want) {
			t.Errorf("list %s = %v, want %v", tc.query, names, tc.want)
		}
	}
	if code, body := env.do(t, env.client, http.MethodGet, "/api/aliases?kind=images", ""); code != http.StatusBadRequest {
		t.Errorf("list with an unknown kind = %d %s", code, body)
	}
}
