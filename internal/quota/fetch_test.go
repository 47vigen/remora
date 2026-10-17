package quota

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/store"
)

const (
	urlAGDaily   = "https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary"
	urlAGProd    = "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary"
	urlAGLoad    = "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
	urlCodex     = "https://chatgpt.com/backend-api/wham/usage"
	urlClaude    = "https://api.anthropic.com/api/oauth/usage"
	codexBody    = `{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":30,"limit_window_seconds":18000}}}`
	claudeBody   = `{"five_hour":{"utilization":10}}`
	planBody     = `{"paidTier":{"id":"g1-pro-tier","name":"Pro"}}`
	agFloorAgent = "antigravity/hub/2.23.0 darwin/arm64"
)

func newFetcher(e *fakeEngine) *fetcher {
	return &fetcher{eng: e, ver: staticVersion(), plans: map[int64]time.Time{}}
}

func checkCall(t *testing.T, got apiCall, method, url, data string, header map[string]string) {
	t.Helper()
	want := cliproxyapi.APICallRequest{AuthIndex: "ai", Method: method, URL: url, Header: header, Data: data}
	if !reflect.DeepEqual(got.req, want) {
		t.Errorf("call:\n got %+v\nwant %+v", got.req, want)
	}
}

func TestFetchAntigravity(t *testing.T) {
	e := newFakeEngine()
	e.on(urlAGDaily, reply{status: 200, body: agSummaryBody})
	e.on(urlAGLoad, reply{status: 200, body: planBody})
	f := newFetcher(e)
	a := store.Account{ID: 1, Provider: "antigravity"}
	c := cliproxyapi.Credential{AuthIndex: "ai", ProjectID: "p1"}

	q, err := f.fetch(context.Background(), a, c, store.AccountQuota{})
	if err != nil {
		t.Fatal(err)
	}
	calls := e.recorded()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	h := map[string]string{"Authorization": "Bearer $TOKEN$", "Content-Type": "application/json", "User-Agent": agFloorAgent}
	checkCall(t, calls[0], http.MethodPost, urlAGDaily, `{"project":"p1"}`, h)
	checkCall(t, calls[1], http.MethodPost, urlAGLoad, `{"metadata":{"ideType":"ANTIGRAVITY"}}`, h)
	if q.Plan != "Pro" || len(q.Windows) != 5 || !q.LimitReached {
		t.Errorf("q = %+v", q)
	}

	q, err = f.fetch(context.Background(), a, c, store.AccountQuota{Plan: "Kept"})
	if err != nil || len(e.recorded()) != 3 || q.Plan != "Kept" {
		t.Errorf("second fetch: plan %q, calls %d, err %v; want no loadCodeAssist and the previous plan", q.Plan, len(e.recorded()), err)
	}
}

func TestFetchAntigravityFallsBackToSecondBase(t *testing.T) {
	e := newFakeEngine()
	e.on(urlAGDaily, reply{status: 503})
	e.on(urlAGProd, reply{status: 200, body: agSummaryBody})
	f := newFetcher(e)
	c := cliproxyapi.Credential{AuthIndex: "ai", ProjectID: "p1"}
	if _, err := f.fetch(context.Background(), store.Account{ID: 1, Provider: "antigravity"}, c, store.AccountQuota{}); err != nil {
		t.Fatal(err)
	}
	if calls := e.recorded(); len(calls) < 2 || calls[1].req.URL != urlAGProd {
		t.Errorf("calls = %+v, want the second base after 503", calls)
	}

	e = newFakeEngine()
	e.on(urlAGDaily, reply{status: 429})
	_, err := newFetcher(e).fetch(context.Background(), store.Account{ID: 1, Provider: "antigravity"}, c, store.AccountQuota{})
	var ue *upstreamError
	if !errors.As(err, &ue) || ue.Status != 429 || len(e.recorded()) != 1 {
		t.Errorf("429: err %v, calls %d; want upstreamError 429 and no second call", err, len(e.recorded()))
	}
}

func TestFetchAntigravityWithoutProject(t *testing.T) {
	e := newFakeEngine()
	e.on(urlAGLoad, reply{status: 200, body: planBody})
	_, err := newFetcher(e).fetch(context.Background(), store.Account{ID: 1, Provider: "antigravity"}, cliproxyapi.Credential{AuthIndex: "ai"}, store.AccountQuota{})
	if !errors.Is(err, errSignIn) || len(e.recorded()) != 1 {
		t.Errorf("err %v, calls %d; want errSignIn after one loadCodeAssist", err, len(e.recorded()))
	}
}

func TestFetchAntigravitySuspended(t *testing.T) {
	const banned = `{"error":{"code":403,"message":"This service has been disabled in this account.","status":"PERMISSION_DENIED",` +
		`"details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"TOS_VIOLATION"}]}}`
	e := newFakeEngine()
	e.on(urlAGLoad, reply{status: 403, body: banned})
	_, err := newFetcher(e).fetch(context.Background(), store.Account{ID: 1, Provider: "antigravity"}, cliproxyapi.Credential{AuthIndex: "ai"}, store.AccountQuota{})
	if !errors.Is(err, errSuspended) {
		t.Fatalf("err %v, want errSuspended", err)
	}
	if msg, _ := outcome(err); msg != MsgSuspended+": This service has been disabled in this account." || !Suspended(msg) {
		t.Errorf("outcome %q", msg)
	}

	e = newFakeEngine()
	e.on(urlAGLoad, reply{status: 403, body: `{"error":{"message":"denied"}}`})
	_, err = newFetcher(e).fetch(context.Background(), store.Account{ID: 1, Provider: "antigravity"}, cliproxyapi.Credential{AuthIndex: "ai"}, store.AccountQuota{})
	if !errors.Is(err, errSignIn) {
		t.Errorf("a 403 without TOS_VIOLATION: err %v, want errSignIn", err)
	}
}

func TestFetchCodex(t *testing.T) {
	e := newFakeEngine()
	e.on(urlCodex, reply{status: 200, body: `{"rate_limit":{}}`}, reply{status: 200, body: codexBody})
	f := newFetcher(e)
	a := store.Account{ID: 2, Provider: "codex"}
	c := cliproxyapi.Credential{AuthIndex: "ai", IDToken: &cliproxyapi.IDTokenClaims{ChatGPTAccountID: "acct", PlanType: "pro"}}
	q, err := f.fetch(context.Background(), a, c, store.AccountQuota{})
	if err != nil {
		t.Fatal(err)
	}
	checkCall(t, e.recorded()[0], http.MethodGet, urlCodex, "", map[string]string{
		"Authorization": "Bearer $TOKEN$", "chatgpt-account-id": "acct", "User-Agent": codexUserAgent,
	})
	if q.Plan != "pro" {
		t.Errorf("plan = %q, want the id_token's", q.Plan)
	}

	c.IDToken = nil
	q, _ = f.fetch(context.Background(), a, c, store.AccountQuota{})
	checkCall(t, e.recorded()[1], http.MethodGet, urlCodex, "", map[string]string{
		"Authorization": "Bearer $TOKEN$", "User-Agent": codexUserAgent,
	})
	if q.Plan != "plus" || len(q.Windows) != 1 {
		t.Errorf("q = %+v", q)
	}
}

func TestFetchClaude(t *testing.T) {
	e := newFakeEngine()
	e.on(urlClaude, reply{status: 200, body: claudeBody})
	q, err := newFetcher(e).fetch(context.Background(), store.Account{ID: 3, Provider: "claude"}, cliproxyapi.Credential{AuthIndex: "ai"}, store.AccountQuota{})
	if err != nil {
		t.Fatal(err)
	}
	checkCall(t, e.recorded()[0], http.MethodGet, urlClaude, "", map[string]string{
		"Authorization": "Bearer $TOKEN$", "anthropic-beta": "oauth-2025-04-20", "User-Agent": claudeUserAgent,
	})
	if len(q.Windows) != 1 || *q.Windows[0].Remaining != 0.9 {
		t.Errorf("q = %+v", q)
	}
}

func TestFetchUpstreamStatus(t *testing.T) {
	e := newFakeEngine()
	e.on(urlClaude, reply{status: 401}, reply{err: &cliproxyapi.Error{StatusCode: 400, Message: "x"}}, reply{status: 200, body: "x"})
	f := newFetcher(e)
	a, c := store.Account{ID: 3, Provider: "claude"}, cliproxyapi.Credential{AuthIndex: "ai"}
	_, err := f.fetch(context.Background(), a, c, store.AccountQuota{})
	var ue *upstreamError
	if !errors.As(err, &ue) || ue.Status != 401 {
		t.Errorf("err = %v, want upstreamError 401", err)
	}
	_, err = f.fetch(context.Background(), a, c, store.AccountQuota{})
	var ee *cliproxyapi.Error
	if !errors.As(err, &ee) || ee.StatusCode != http.StatusBadRequest {
		t.Errorf("err = %v, want the engine's *Error", err)
	}
	if _, err = f.fetch(context.Background(), a, c, store.AccountQuota{}); !errors.Is(err, errUnreadable) {
		t.Errorf("err = %v, want errUnreadable", err)
	}
	if _, err := f.fetch(context.Background(), store.Account{Provider: "kimi"}, c, store.AccountQuota{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("kimi: err = %v, want ErrUnsupported", err)
	}
}

func TestUpstreamRewrite(t *testing.T) {
	for _, tt := range []struct{ base, raw, want string }{
		{"", urlCodex, urlCodex},
		{"http://fakes:9090/u/quota", urlCodex, "http://fakes:9090/u/quota/backend-api/wham/usage"},
		{"http://fakes:9090/u/quota/", urlAGDaily, "http://fakes:9090/u/quota/v1internal:retrieveUserQuotaSummary"},
	} {
		if got := upstream(tt.base, tt.raw); got != tt.want {
			t.Errorf("upstream(%q, %q) = %q, want %q", tt.base, tt.raw, got, tt.want)
		}
	}
}

func TestFetchUsesBase(t *testing.T) {
	e := newFakeEngine()
	f := newFetcher(e)
	f.base = "http://fakes.example.test/u/quota"
	_, _ = f.fetch(context.Background(), store.Account{Provider: "claude"}, cliproxyapi.Credential{AuthIndex: "ai"}, store.AccountQuota{})
	if got := e.recorded()[0].req.URL; got != "http://fakes.example.test/u/quota/api/oauth/usage" {
		t.Errorf("URL = %q", got)
	}
}
