package quota

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/47vigen/remora/internal/accounts"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/store"
)

type rig struct {
	t   *testing.T
	st  *store.Store
	e   *fakeEngine
	svc *Service
	ids map[string]int64
}

// newRig opens a store and a Service; call it inside the synctest bubble. Accounts are added with add.
func newRig(t *testing.T, interval time.Duration) *rig {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "remora.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e := newFakeEngine()
	svc := New(e, st, Config{Interval: interval}, nil)
	svc.f.ver = staticVersion()
	return &rig{t: t, st: st, e: e, svc: svc, ids: map[string]int64{}}
}

// add stores an enabled account named name of provider and gives the engine its credential.
func (r *rig) add(name, provider string, mod ...func(*cliproxyapi.Credential)) int64 {
	r.t.Helper()
	a, err := r.st.CreateAccount(context.Background(), store.Account{Provider: provider, Label: name, Credential: name + ".json", Enabled: true})
	if err != nil {
		r.t.Fatal(err)
	}
	c := cliproxyapi.Credential{Name: name + ".json", AuthIndex: name, Provider: provider, ProjectID: "p"}
	for _, m := range mod {
		m(&c)
	}
	r.e.mu.Lock()
	r.e.creds = append(r.e.creds, c)
	r.e.mu.Unlock()
	r.ids[name] = a.ID
	return a.ID
}

// run starts the worker and stops it when the bubble's test function returns.
func (r *rig) run(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.svc.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func (r *rig) snap(id int64) (store.AccountQuota, bool) {
	r.t.Helper()
	all, err := r.st.AccountQuotas(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	q, ok := all[id]
	return q, ok
}

func (r *rig) callsTo(authIndex string) []time.Time {
	var out []time.Time
	for _, c := range r.e.recorded() {
		if c.req.AuthIndex == authIndex {
			out = append(out, c.at)
		}
	}
	return out
}

// bubble runs f in a synctest bubble; the worker is stopped by a cleanup before the bubble ends.
func bubble(t *testing.T, f func(t *testing.T)) { synctest.Test(t, f) }

func TestWorkerSpacesCalls(t *testing.T) {
	bubble(t, func(t *testing.T) {
		r := newRig(t, DefaultInterval)
		r.add("a", "codex")
		r.add("b", "claude")
		r.add("c", "codex")
		r.run(t)
		time.Sleep(time.Minute)
		calls := r.e.recorded()
		if len(calls) != 3 {
			t.Fatalf("calls = %d, want 3", len(calls))
		}
		for i := 1; i < len(calls); i++ {
			if gap := calls[i].at.Sub(calls[i-1].at); gap < Spacing {
				t.Errorf("gap %d = %v, want at least %v", i, gap, Spacing)
			}
		}
	})
}

func TestWorkerStartupOnlyStale(t *testing.T) {
	bubble(t, func(t *testing.T) {
		r := newRig(t, DefaultInterval)
		start := time.Now()
		for name, ago := range map[string]time.Duration{"a": time.Minute, "b": 20 * time.Minute} {
			id := r.add(name, "codex")
			if err := r.st.PutAccountQuota(context.Background(), store.AccountQuota{AccountID: id, Source: "poll", CheckedAt: start.Add(-ago)}); err != nil {
				t.Fatal(err)
			}
		}
		r.add("c", "codex")
		r.run(t)
		time.Sleep(time.Minute)
		if len(r.callsTo("a")) != 0 || len(r.callsTo("b")) != 1 || len(r.callsTo("c")) != 1 {
			t.Errorf("start: a %d, b %d, c %d; want 0, 1, 1", len(r.callsTo("a")), len(r.callsTo("b")), len(r.callsTo("c")))
		}
		time.Sleep(14*time.Minute - time.Minute + 30*time.Second)
		if a := r.callsTo("a"); len(a) != 1 || a[0].Sub(start) < 14*time.Minute {
			t.Errorf("a polled at %v, want once at +14m", a)
		}
	})
}

func TestWorkerIntervalZeroIsOff(t *testing.T) {
	bubble(t, func(t *testing.T) {
		r := newRig(t, 0)
		id := r.add("a", "codex")
		r.run(t)
		time.Sleep(2 * time.Hour)
		if n := len(r.e.recorded()); n != 0 {
			t.Fatalf("calls with interval 0 = %d", n)
		}
		if queued, _, err := r.svc.Refresh(context.Background(), id, ""); err != nil || len(queued) != 1 {
			t.Fatalf("Refresh = %v, %v", queued, err)
		}
		time.Sleep(time.Second)
		if n := len(r.e.recorded()); n != 1 {
			t.Errorf("calls after Refresh = %d, want 1", n)
		}
	})
}

func TestWorkerBackoff(t *testing.T) {
	bubble(t, func(t *testing.T) {
		r := newRig(t, DefaultInterval)
		id := r.add("a", "codex")
		r.e.on(urlCodex, reply{status: 429}, reply{status: 429}, reply{status: 429}, reply{status: 429}, reply{status: 429},
			reply{status: 429}, reply{status: 200, body: codexBody})
		r.run(t)
		time.Sleep(16 * time.Hour)
		calls := r.callsTo("a")
		want := []time.Duration{30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 4 * time.Hour, 4 * time.Hour, 15 * time.Minute}
		if len(calls) < len(want)+1 {
			t.Fatalf("calls = %d, want at least %d", len(calls), len(want)+1)
		}
		for i, w := range want {
			if gap := calls[i+1].Sub(calls[i]); gap < w || gap > w+time.Minute {
				t.Errorf("gap %d = %v, want %v", i+1, gap, w)
			}
		}
		if q, _ := r.snap(id); q.Error != "" {
			t.Errorf("error after success = %q", q.Error)
		}
	})
}

func TestWorker401RefreshesOnce(t *testing.T) {
	for _, tt := range []struct {
		name       string
		replies    []reply
		refreshErr error
		calls      int
		wantErr    string
	}{
		{"refresh fixes it", []reply{{status: 401}, {status: 200, body: claudeBody}}, nil, 2, ""},
		{"still refused", []reply{{status: 401}, {status: 403}}, nil, 2, MsgSignIn},
		{"refresh fails", []reply{{status: 401}, {status: 200, body: claudeBody}}, errors.New("no"), 1, MsgSignIn},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bubble(t, func(t *testing.T) {
				r := newRig(t, DefaultInterval)
				id := r.add("a", "claude")
				r.e.on(urlClaude, tt.replies...)
				r.e.refreshErr = tt.refreshErr
				r.run(t)
				time.Sleep(time.Minute)
				q, _ := r.snap(id)
				r.e.mu.Lock()
				refreshed := append([]string(nil), r.e.refreshed...)
				r.e.mu.Unlock()
				if n := len(r.e.recorded()); n != tt.calls || len(refreshed) != 1 || refreshed[0] != "a.json" || q.Error != tt.wantErr {
					t.Errorf("calls %d, refreshed %v, error %q; want %d calls, one refresh of a.json, %q", n, refreshed, q.Error, tt.calls, tt.wantErr)
				}
				if tt.wantErr != "" && (len(q.Windows) != 0 || !q.FetchedAt.IsZero()) {
					t.Errorf("failed snapshot = %+v, want no windows, never fetched", q)
				}
			})
		})
	}
}

func TestWorkerSkips(t *testing.T) {
	bubble(t, func(t *testing.T) {
		r := newRig(t, DefaultInterval)
		now := time.Now()
		disabled, err := r.st.CreateAccount(context.Background(), store.Account{Provider: "codex", Credential: "d.json"})
		if err != nil {
			t.Fatal(err)
		}
		r.e.creds = append(r.e.creds, cliproxyapi.Credential{Name: "d.json", AuthIndex: "d", Provider: "codex"})
		observed := now.Add(-2 * time.Minute)
		fresh := r.add("p", "codex", func(c *cliproxyapi.Credential) {
			c.Quota = cliproxyapi.Quota{ObservedAt: &observed, Signals: map[string]string{
				"x-codex-primary-used-percent": "40", "x-codex-primary-window-minutes": "300",
			}}
		})
		reset := now.Add(2 * time.Hour)
		ex := r.add("x", "codex")
		if err := r.st.PutAccountQuota(context.Background(), store.AccountQuota{
			AccountID: ex, Source: "poll", CheckedAt: now.Add(-time.Minute),
			FetchedAt: now.Add(-time.Minute), LimitReached: true, Windows: []store.QuotaWindow{win("", "5h", f(0), &reset)},
		}); err != nil {
			t.Fatal(err)
		}
		r.run(t)
		time.Sleep(10 * time.Minute)
		if n := len(r.e.recorded()); n != 0 {
			t.Errorf("calls in 10m = %d, want none", n)
		}
		snaps, err := r.svc.Snapshots(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if q := snaps[fresh]; q.Source != "passive" || len(q.Windows) != 1 || *q.Windows[0].Remaining != 0.6 {
			t.Errorf("passive snapshot = %+v", q)
		}
		if _, ok := snaps[disabled.ID]; ok {
			t.Error("disabled account has a snapshot")
		}
		time.Sleep(time.Until(reset) - time.Second)
		if n := len(r.callsTo("x")); n != 0 {
			t.Errorf("exhausted polled %d times before its reset", n)
		}
		time.Sleep(2 * time.Second)
		if n := len(r.callsTo("x")); n != 1 {
			t.Errorf("exhausted polled %d times at its reset, want 1", n)
		}
		if len(r.callsTo("d")) != 0 {
			t.Error("disabled account polled")
		}
	})
}

func TestWorkerKeepsLastGoodOnError(t *testing.T) {
	bubble(t, func(t *testing.T) {
		r := newRig(t, DefaultInterval)
		id := r.add("a", "codex")
		good := store.AccountQuota{
			AccountID: id, Plan: "plus", Source: "poll", Windows: []store.QuotaWindow{win("", "5h", f(0.7), nil)},
			FetchedAt: time.Now().Add(-time.Hour).Truncate(time.Millisecond), CheckedAt: time.Now().Add(-time.Hour).Truncate(time.Millisecond),
		}
		if err := r.st.PutAccountQuota(context.Background(), good); err != nil {
			t.Fatal(err)
		}
		r.e.on(urlCodex, reply{status: 502})
		r.run(t)
		time.Sleep(time.Minute)
		q, _ := r.snap(id)
		if q.Plan != "plus" || len(q.Windows) != 1 || !q.FetchedAt.Equal(good.FetchedAt) || q.Error != MsgUnavailable || !q.CheckedAt.After(good.CheckedAt) {
			t.Errorf("after a failure = %+v", q)
		}
	})
}

func TestWorkerDropsDeletedAccount(t *testing.T) {
	bubble(t, func(t *testing.T) {
		r := newRig(t, DefaultInterval)
		id := r.add("a", "codex")
		r.e.onCall = func() { _ = r.st.DeleteAccount(context.Background(), id) }
		r.run(t)
		time.Sleep(time.Hour)
		if n := len(r.e.recorded()); n != 1 {
			t.Errorf("calls = %d, want the one in flight", n)
		}
		if _, ok := r.snap(id); ok {
			t.Error("snapshot written for a deleted account")
		}
	})
}

func TestWorkerEngineDownDoesNotSpin(t *testing.T) {
	bubble(t, func(t *testing.T) {
		r := newRig(t, DefaultInterval)
		r.add("a", "codex")
		r.e.credsErr = errors.New("connection refused")
		r.run(t)
		time.Sleep(20 * time.Second)
		r.e.mu.Lock()
		n := len(r.e.credCalls)
		r.e.mu.Unlock()
		if n > 11 {
			t.Errorf("Credentials calls in 20s = %d, want at most 11", n)
		}
	})
}

func TestRefreshFloor(t *testing.T) {
	bubble(t, func(t *testing.T) {
		r := newRig(t, 0)
		a := r.add("a", "codex")
		ctx := context.Background()
		r.run(t)
		if q, s, err := r.svc.Refresh(ctx, a, ""); err != nil || len(q) != 1 || q[0] != a || len(s) != 0 {
			t.Fatalf("Refresh = %v, %v, %v", q, s, err)
		}
		time.Sleep(30 * time.Second)
		if q, s, _ := r.svc.Refresh(ctx, a, ""); len(q) != 0 || len(s) != 1 || s[0] != a {
			t.Errorf("at +30s: queued %v skipped %v, want skipped [a]", q, s)
		}
		time.Sleep(time.Second)
		if n := len(r.e.recorded()); n != 1 {
			t.Errorf("calls = %d, want 1", n)
		}
		time.Sleep(31 * time.Second)
		if q, _, _ := r.svc.Refresh(ctx, a, ""); len(q) != 1 {
			t.Errorf("at +62s: queued %v, want [a]", q)
		}
		time.Sleep(time.Second)
		b := r.add("b", "codex")
		if q, s, _ := r.svc.Refresh(ctx, 0, "codex"); len(q) != 1 || q[0] != b || len(s) != 1 || s[0] != a {
			t.Errorf("provider refresh: queued %v skipped %v, want [b] [a]", q, s)
		}
		for _, tt := range []struct {
			id       int64
			provider string
			want     error
		}{{0, "", ErrRefreshTarget}, {a, "codex", ErrRefreshTarget}, {0, "kimi", ErrUnsupported}, {0, "nope", accounts.ErrUnknownProvider}, {999, "", store.ErrNotFound}} {
			if _, _, err := r.svc.Refresh(ctx, tt.id, tt.provider); !errors.Is(err, tt.want) {
				t.Errorf("Refresh(%d, %q) err = %v, want %v", tt.id, tt.provider, err, tt.want)
			}
		}
	})
}
