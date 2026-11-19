package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
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
}

func (s *tailSource) add(rows ...bifrost.Log) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range rows {
		l.Timestamp = time.Now()
		if l.Status == "" {
			l.Status = "success"
		}
		s.rows = append(s.rows, l)
	}
}

func (s *tailSource) GetLog(_ context.Context, id string) (bifrost.Log, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.rows {
		if l.ID == id {
			return l, nil
		}
	}
	return bifrost.Log{}, bifrost.ErrNotFound
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

func newLiveEnv(t *testing.T, buffer, backlog int) *liveEnv {
	t.Helper()
	src := &tailSource{}
	drop := &dropSignal{dropped: make(chan struct{})}
	tail := bifrost.NewTail(src, 5*time.Millisecond, nil, slog.New(drop))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); tail.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	env := newTestEnvTail(t, tail, buffer, backlog)
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
	env := newLiveEnv(t, 0, 0)
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
	env := newLiveEnv(t, 0, 0)
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
	env := newLiveEnv(t, 0, 0)
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

// pausedLiveWriter blocks the first frame after the upgrade, independent of TCP buffering.
type pausedLiveWriter struct {
	io.Writer
	ctx     context.Context
	writing chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (w *pausedLiveWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.writing) })
	select {
	case <-w.resume:
		return w.Writer.Write(p)
	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	}
}

type pausedLiveUpgrade struct {
	http.ResponseWriter
	writer *pausedLiveWriter
}

func (w pausedLiveUpgrade) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.writer.Writer = conn
		rw.Writer.Reset(w.writer)
	}
	return conn, rw, err
}

func TestStreamLogsSlowClientClosed(t *testing.T) {
	env := newLiveEnv(t, 1, 2)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	writer := &pausedLiveWriter{ctx: ctx, writing: make(chan struct{}), resume: make(chan struct{})}
	h := env.server.Handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(pausedLiveUpgrade{w, writer}, r)
	}))
	t.Cleanup(srv.Close)
	slow, code, err := dialWS("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/logs/live", &websocket.DialOptions{HTTPClient: env.client})
	if err != nil {
		t.Fatalf("dial slow viewer: %v (%d)", err, code)
	}
	t.Cleanup(func() { _ = slow.CloseNow() })
	fast := env.dial(t, "")

	publish := func(id string) {
		t.Helper()
		env.src.add(bifrost.Log{ID: id, Provider: "or", Model: "a"})
		if got := readEntry(t, fast).Id; got != id {
			t.Fatalf("fast viewer got %q, want %q", got, id)
		}
	}
	publish("in-flight")
	select {
	case <-writer.writing:
	case <-ctx.Done():
		t.Fatal("slow viewer did not start writing its first frame")
	}
	for _, id := range []string{"buffered", "backlog-1", "backlog-2", "overflow"} {
		publish(id)
	}
	select {
	case <-env.drop.dropped:
	case <-ctx.Done():
		t.Fatal("slow viewer was not dropped at one in-flight, one buffered, and three backlogged rows")
	}

	close(writer.resume)
	for _, want := range []string{"in-flight", "buffered"} {
		if got := readEntry(t, slow).Id; got != want {
			t.Fatalf("slow viewer got %q, want %q", got, want)
		}
	}
	if _, _, err := slow.Read(ctx); websocket.CloseStatus(err) != websocket.StatusTryAgainLater {
		t.Fatalf("slow viewer closed with %v, want 1013", err)
	}
	publish("after")
}

func TestStreamLogsRejectsForeignOrigin(t *testing.T) {
	env := newLiveEnv(t, 0, 0)
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
