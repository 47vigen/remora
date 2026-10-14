package api

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/engine/bifrost"
)

// engineWords must never reach a user in any log or error text.
var engineWords = []string{"bifrost", "cliproxy", "cli proxy", "remora-oauth", "sk-bf-"}

func assertNoEngineWords(t *testing.T, what, s string) {
	t.Helper()
	low := strings.ToLower(s)
	for _, w := range engineWords {
		if strings.Contains(low, w) {
			t.Errorf("%s = %q contains %q", what, s, w)
		}
	}
}

func TestScrub(t *testing.T) {
	const persian = "درخواست\u200cها رد شد: سلام\u200cدنیا"
	cases := []struct{ in, want string }{
		{"bifrost: provider cliproxyapi returned 429", "gateway: provider oauth engine returned 429"},
		{"key sk-bf-abc123 invalid", "key [redacted] invalid"},
		{"Bifrost via CLIProxyAPI and CLI Proxy API and cliproxy", "gateway via oauth engine and oauth engine and oauth engine"},
		{"dial tcp 127.0.0.1:18317: connection refused", "dial tcp upstream: connection refused"},
		{"get http://localhost:8080/v1/models", "get http://upstream/v1/models"},
		{"model remora-oauth/codex/gpt-x is cooling", "model codex/gpt-x is cooling"},
		{"provider remora-oauth failed with key remora-oauth-key", "provider oauth engine failed with key [redacted]"},
		{persian, persian},
	}
	for _, c := range cases {
		got := Scrub(c.in)
		if got != c.want {
			t.Errorf("Scrub(%q) = %q, want %q", c.in, got, c.want)
		}
		assertNoEngineWords(t, "Scrub", got)
	}
	if got := Scrub(persian); got != persian || !strings.Contains(got, "\u200c") {
		t.Errorf("Persian text changed: % x", got)
	}
}

func ptr[T any](v T) *T { return &v }

func errDetails(msg string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"status_code": 429, "error": map[string]any{"message": msg}})
	return b
}

func TestToEntry(t *testing.T) {
	names := &fakeNames{
		keys:    map[string]string{"svc": "vk1"},
		rules:   map[string]string{"fast": "r1"},
		targets: map[[2]string]alias.Target{{"oauth-internal", "codex/gpt-x"}: {Provider: "codex", Model: "gpt-x"}},
	}
	s := New(Deps{Names: names})
	ctx := context.Background()
	ts := time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)

	root := bifrost.Log{
		ID: "r", Timestamp: ts, Provider: "oauth-internal", Model: "codex/gpt-x", Status: "error",
		VirtualKeyID: ptr("vk1"), RoutingRuleID: ptr("r1"), Latency: ptr(812.6), Cost: ptr(0.5), Stream: true,
		TokenUsage:   &bifrost.TokenUsage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
		ErrorDetails: errDetails("bifrost: rate limited"),
	}
	e := s.toEntry(ctx, root, "")
	if e.Id != "r" || e.ParentId != nil || e.Attempt != 0 || !e.Time.Equal(ts) || e.Key == nil || *e.Key != "svc" ||
		e.Alias == nil || *e.Alias != "fast" || e.Provider != "codex" || e.Model != "gpt-x" || e.Status != "error" ||
		e.LatencyMs == nil || *e.LatencyMs != 813 || e.CostUsd == nil || *e.CostUsd != 0.5 || !e.Stream ||
		e.Tokens.Prompt != 3 || e.Tokens.Completion != 4 || e.Tokens.Total != 7 ||
		e.Error == nil || *e.Error != "gateway: rate limited" {
		t.Fatalf("root entry = %+v", e)
	}

	child := bifrost.Log{
		ID: "c1", ParentRequestID: ptr("r"), FallbackIndex: 1, Timestamp: ts, Provider: "openrouter", Model: "m",
		Status: "success",
	}
	e = s.toEntry(ctx, child, "fast")
	if e.ParentId == nil || *e.ParentId != "r" || e.Attempt != 1 || e.Alias == nil || *e.Alias != "fast" ||
		e.Key != nil || e.Error != nil || e.Provider != "openrouter" || e.Model != "m" || e.LatencyMs != nil {
		t.Fatalf("child entry = %+v", e)
	}
	// A routing rule that is not an alias (the OAuth engine's own rules) names no alias.
	child.RoutingRuleID = ptr("internal-rule")
	if e := s.toEntry(ctx, child, ""); e.Alias != nil {
		t.Fatalf("alias for an internal rule = %q", *e.Alias)
	}
}

func TestToEntryNeverLeaksEngineProvider(t *testing.T) {
	s := New(Deps{Names: &fakeNames{}})
	row := bifrost.Log{
		ID: "r", ParentRequestID: ptr("p"), Timestamp: time.Now(), Provider: bifrost.OAuthProvider,
		Model: "codex/gpt-x", Status: "error", VirtualKeyID: ptr("sk-bf-internal"), RoutingRuleID: ptr("remora-oauth-codex"),
		ErrorDetails: errDetails("bifrost: provider remora-oauth via cliproxyapi at 127.0.0.1:18317 refused key sk-bf-xyz"),
	}
	e := s.toEntry(context.Background(), row, "")
	if e.Provider != "codex" || e.Model != "gpt-x" {
		t.Errorf("target = %s/%s, want codex/gpt-x", e.Provider, e.Model)
	}
	var walk func(path string, v reflect.Value)
	walk = func(path string, v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
			if !v.IsNil() {
				walk(path, v.Elem())
			}
		case reflect.Struct:
			for i := range v.NumField() {
				walk(path+"."+v.Type().Field(i).Name, v.Field(i))
			}
		case reflect.String:
			assertNoEngineWords(t, path, v.String())
		default:
		}
	}
	walk("LogEntry", reflect.ValueOf(e))
	b, _ := json.Marshal(e)
	assertNoEngineWords(t, "json", string(b))
}

func TestToFilter(t *testing.T) {
	names := &fakeNames{keys: map[string]string{"k1": "vk1"}, rules: map[string]string{"fast": "r1"}}
	s := New(Deps{Names: names})
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	ctx := context.Background()

	from, to := now.Add(-2*time.Hour), now.Add(-time.Hour)
	status := gen.ListLogsParamsStatus("error")
	f, err := s.toFilter(ctx, gen.ListLogsParams{
		Key: ptr("k1"), Alias: ptr("fast"), Provider: ptr("or"), Model: ptr("or/a"), Status: &status,
		From: &from, To: &to, Q: ptr("x"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.VirtualKeyIDs, []string{"vk1"}) || !slices.Equal(f.RoutingRuleIDs, []string{"r1"}) ||
		!slices.Equal(f.Providers, []string{"fd-or"}) || !slices.Equal(f.Models, []string{"fd-or/a"}) ||
		!slices.Equal(f.Status, []string{"error"}) || !f.Start.Equal(from) || !f.End.Equal(to) || f.ContentSearch != "x" {
		t.Fatalf("filter = %+v", f)
	}

	f, err = s.toFilter(ctx, gen.ListLogsParams{})
	if err != nil || !f.End.Equal(now) || !f.Start.Equal(now.Add(-24*time.Hour)) || f.VirtualKeyIDs != nil ||
		f.Providers != nil || f.Status != nil {
		t.Fatalf("default filter = %+v, %v", f, err)
	}

	for _, p := range []gen.ListLogsParams{{Key: ptr("nope")}, {Alias: ptr("nope")}} {
		if _, err := s.toFilter(ctx, p); !errors.Is(err, errNoMatch) {
			t.Errorf("toFilter(%+v) = %v, want errNoMatch", p, err)
		}
	}
	if _, err := s.toFilter(ctx, gen.ListLogsParams{From: &to, To: &from}); err == nil {
		t.Error("from after to accepted")
	}
}
