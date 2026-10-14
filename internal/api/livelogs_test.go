package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/engine/bifrost"
)

// tailSource is the front-door log a test Tail polls: rows in the order added, newest last.
type tailSource struct {
	mu   sync.Mutex
	rows []bifrost.Log
	n    int
}

// add appends rows stamped now, each with a fresh id.
func (s *tailSource) add(rows ...bifrost.Log) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for _, l := range rows {
		s.n++
		if l.ID == "" {
			l.ID = fmt.Sprintf("row-%d", s.n)
		}
		l.Timestamp = time.Now()
		if l.Status == "" {
			l.Status = "success"
		}
		s.rows = append(s.rows, l)
		ids = append(ids, l.ID)
	}
	return ids
}

func (s *tailSource) SearchLogs(_ context.Context, f bifrost.LogFilter) (bifrost.LogPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []bifrost.Log
	for _, l := range s.rows {
		if f.Start == nil || !l.Timestamp.Before(*f.Start) {
			out = append(out, l)
		}
	}
	total := int64(len(out))
	out = out[min(f.Offset, len(out)):]
	out = out[:min(f.Limit, len(out))]
	return bifrost.LogPage{Logs: slices.Clone(out), Total: total}, nil
}

// dropSignal is a log handler that closes dropped when the Tail drops a subscriber.
type dropSignal struct {
	once    sync.Once
	dropped chan struct{}
}

func (d *dropSignal) Enabled(context.Context, slog.Level) bool { return true }
func (d *dropSignal) WithAttrs([]slog.Attr) slog.Handler       { return d }
func (d *dropSignal) WithGroup(string) slog.Handler            { return d }
func (d *dropSignal) Handle(_ context.Context, r slog.Record) error {
	if strings.Contains(r.Message, "dropped") {
		d.once.Do(func() { close(d.dropped) })
	}
	return nil
}

type liveEnv struct {
	*testEnv
	src  *tailSource
	drop *dropSignal
}

func newLiveEnv(t *testing.T) *liveEnv {
	t.Helper()
	src := &tailSource{}
	drop := &dropSignal{dropped: make(chan struct{})}
	tail := bifrost.NewTail(src, 5*time.Millisecond, slog.New(drop))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); tail.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	env := newTestEnvTail(t, tail)
	env.names.keys = map[string]string{"k1": "vk1"}
	env.names.rules = map[string]string{"fast": "r1", "smart": "r2"}
	return &liveEnv{testEnv: env, src: src, drop: drop}
}

func (e *liveEnv) wsURL(query string) string {
	return "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/api/logs/live" + query
}

// dialWS dials the live stream and answers the handshake's status code, closing its body.
func dialWS(url string, opts *websocket.DialOptions) (*websocket.Conn, int, error) {
	c, resp, err := websocket.Dial(context.Background(), url, opts)
	code := 0
	if resp != nil {
		code = resp.StatusCode
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}
	return c, code, err
}

// dial opens the live stream with the logged-in session.
func (e *liveEnv) dial(t *testing.T, query string) *websocket.Conn {
	t.Helper()
	c, code, err := dialWS(e.wsURL(query), &websocket.DialOptions{HTTPClient: e.client})
	if err != nil {
		t.Fatalf("dial: %v (%d)", err, code)
	}
	c.SetReadLimit(1 << 20)
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func readEntry(t *testing.T, c *websocket.Conn) gen.LogEntry {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typ, b, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("frame type = %v", typ)
	}
	var e gen.LogEntry
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return e
}

func TestStreamLogsSessionAuth(t *testing.T) {
	env := newLiveEnv(t)
	if _, code, err := dialWS(env.wsURL(""), nil); err == nil || code != http.StatusUnauthorized {
		t.Fatalf("anonymous dial = %d, %v", code, err)
	}

	byCookie := env.dial(t, "")

	code, body := env.do(t, env.client, http.MethodPost, "/api/settings/tokens", `{"name":"live"}`)
	if code != http.StatusCreated {
		t.Fatalf("token = %d %s", code, body)
	}
	var tok struct{ Value string }
	decode(t, body, &tok)
	byToken, code, err := dialWS(env.wsURL(""), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + tok.Value}},
	})
	if err != nil {
		t.Fatalf("token dial: %v (%d)", err, code)
	}
	t.Cleanup(func() { _ = byToken.CloseNow() })

	env.src.add(bifrost.Log{
		ID: "r", Provider: bifrost.OAuthProvider, Model: "codex/gpt-x", Status: "error", VirtualKeyID: ptr("vk1"),
		RoutingRuleID: ptr("r1"), ErrorDetails: errDetails("bifrost: slow down"),
	})
	for _, c := range []*websocket.Conn{byCookie, byToken} {
		e := readEntry(t, c)
		if e.Id != "r" || deref(e.Key) != "k1" || deref(e.Alias) != "fast" || e.Provider != "codex" || e.Model != "gpt-x" ||
			e.Status != "error" {
			t.Errorf("entry = %+v", e)
		}
		b, _ := json.Marshal(e)
		assertNoEngineWords(t, "live entry", string(b))
	}
}

func TestStreamLogsFilter(t *testing.T) {
	env := newLiveEnv(t)
	c := env.dial(t, "?alias=fast")
	env.src.add(
		bifrost.Log{ID: "f1", Provider: "or", Model: "a", RoutingRuleID: ptr("r1")},
		bifrost.Log{ID: "s1", Provider: "or", Model: "a", RoutingRuleID: ptr("r2")},
		bifrost.Log{ID: "f2", Provider: "or", Model: "a", RoutingRuleID: ptr("r1")},
	)
	for _, want := range []string{"f1", "f2"} {
		if e := readEntry(t, c); e.Id != want || deref(e.Alias) != "fast" {
			t.Fatalf("entry = %+v, want %s", e, want)
		}
	}

	byStatus := env.dial(t, "?status=error&provider=or&key=k1")
	env.src.add(
		bifrost.Log{ID: "ok", Provider: "or", Model: "a", VirtualKeyID: ptr("vk1")},
		bifrost.Log{ID: "other", Provider: "xx", Model: "a", Status: "error", VirtualKeyID: ptr("vk1")},
		bifrost.Log{ID: "bad", Provider: "or", Model: "a", Status: "error", VirtualKeyID: ptr("vk1")},
	)
	if e := readEntry(t, byStatus); e.Id != "bad" {
		t.Fatalf("entry = %+v, want bad", e)
	}

	if _, code, err := dialWS(env.wsURL("?status=nope"), &websocket.DialOptions{HTTPClient: env.client}); err == nil ||
		code != http.StatusBadRequest {
		t.Fatalf("bad status = %d, %v", code, err)
	}
}

func TestStreamLogsIgnoresPing(t *testing.T) {
	env := newLiveEnv(t)
	c := env.dial(t, "")
	ctx := context.Background()
	for range 10 {
		if err := c.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
			t.Fatalf("ping: %v", err)
		}
	}
	env.src.add(bifrost.Log{ID: "after-ping", Provider: "or", Model: "a"})
	// A reply to any ping would arrive before the entry.
	if e := readEntry(t, c); e.Id != "after-ping" {
		t.Fatalf("first frame = %+v", e)
	}
	if err := c.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("socket closed after pings: %v", err)
	}
	env.src.add(bifrost.Log{ID: "again", Provider: "or", Model: "a"})
	if e := readEntry(t, c); e.Id != "again" {
		t.Fatalf("second frame = %+v", e)
	}
}

func TestStreamLogsSlowClientClosed(t *testing.T) {
	env := newLiveEnv(t)
	slow := env.dial(t, "")
	fast := env.dial(t, "")

	got := make(chan string, 4096)
	go func() {
		for {
			_, b, err := fast.Read(context.Background())
			if err != nil {
				close(got)
				return
			}
			var e gen.LogEntry
			_ = json.Unmarshal(b, &e)
			got <- e.Id
		}
	}()
	var fed, received []string
	waitFast := func() {
		t.Helper()
		deadline := time.After(10 * time.Second)
		for len(received) < len(fed) {
			select {
			case id, ok := <-got:
				if !ok {
					t.Fatalf("fast client closed after %d of %d entries", len(received), len(fed))
				}
				received = append(received, id)
			case <-deadline:
				t.Fatalf("fast client got %d of %d entries", len(received), len(fed))
			}
		}
	}

	// Rows with a large model fill the slow socket's buffers, then its 64-row subscription.
	big := strings.Repeat("m", 64<<10)
	for dropped := false; !dropped; {
		if len(fed) > 1500 {
			t.Fatal("the slow client was never dropped")
		}
		for range 8 {
			fed = append(fed, env.src.add(bifrost.Log{Provider: "or", Model: big})...)
		}
		waitFast()
		select {
		case <-env.drop.dropped:
			dropped = true
		default:
		}
	}

	// The slow client now reads what was buffered, then the close.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var err error
	for err == nil {
		_, _, err = slow.Read(ctx)
	}
	if st := websocket.CloseStatus(err); st != websocket.StatusTryAgainLater {
		t.Fatalf("slow client closed with %v (%v), want 1013", st, err)
	}

	fed = append(fed, env.src.add(bifrost.Log{ID: "after", Provider: "or", Model: "a"})...)
	waitFast()
	if !slices.Equal(received, fed) {
		t.Fatalf("fast client got %d entries out of order or missing, want %d", len(received), len(fed))
	}
}

func TestStreamLogsRejectsForeignOrigin(t *testing.T) {
	env := newLiveEnv(t)
	_, code, err := dialWS(env.wsURL(""), &websocket.DialOptions{
		HTTPClient: env.client, HTTPHeader: http.Header{"Origin": {"https://evil.example"}},
	})
	if err == nil || code != http.StatusForbidden {
		t.Fatalf("foreign origin = %d, %v", code, err)
	}
	// The page's own origin is accepted.
	c, code, err := dialWS(env.wsURL(""), &websocket.DialOptions{
		HTTPClient: env.client, HTTPHeader: http.Header{"Origin": {env.srv.URL}},
	})
	if err != nil {
		t.Fatalf("same origin = %d, %v", code, err)
	}
	_ = c.CloseNow()
}
