package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/47vigen/remora/internal/alias"

	"github.com/coder/websocket"

	"github.com/47vigen/remora/internal/store"
)

// wsEcho is a fake front-door WebSocket that records each frame and echoes it back; reply, when set, answers
// instead of the echo.
type wsEcho struct {
	mu     sync.Mutex
	frames []string
	reply  func(frame string) string
}

func (e *wsEcho) recorded() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.frames...)
}

func (e *wsEcho) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("upstream accept: %v", err)
			return
		}
		defer func() { _ = c.CloseNow() }()
		for {
			typ, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			e.mu.Lock()
			e.frames = append(e.frames, string(data))
			e.mu.Unlock()
			out := string(data)
			if e.reply != nil {
				out = e.reply(out)
			}
			if err := c.Write(r.Context(), typ, []byte(out)); err != nil {
				return
			}
		}
	}
}

// dialWS opens a client socket to the proxy's /v1/responses; status is the handshake answer's status code.
func (f *fixture) dialWS(t *testing.T, key string) (c *websocket.Conn, status int, err error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	h := http.Header{}
	if key != "" {
		h.Set("Authorization", "Bearer "+key)
	}
	h.Set("X-Bf-Vk", "sk-bf-evil")
	h.Set("Cookie", "a=b")
	c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.srv.URL, "http")+"/v1/responses",
		&websocket.DialOptions{HTTPHeader: h})
	if c != nil {
		t.Cleanup(func() { _ = c.CloseNow() })
	}
	if resp != nil {
		status = resp.StatusCode
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}
	return c, status, err
}

func wsRoundTrip(t *testing.T, c *websocket.Conn, frame string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(data)
}

func createFrame(model string) string {
	return `{"type":"response.create","model":"` + model + "\",\"input\":\"سلام\u200cدنیا\"}"
}

func TestWS_RelaysAndSwapsKey(t *testing.T) {
	f := newFixture(t)
	echo := &wsEcho{}
	f.upstream(echo.handler(t))
	c, _, err := f.dialWS(t, keyLimited)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	frame := createFrame("fast")
	if got := wsRoundTrip(t, c, frame); got != upstreamFrame("fast") {
		t.Fatalf("echo = %q, want %q", got, upstreamFrame("fast"))
	}
	s := f.seen()
	if s.path != "/v1/responses" || s.method != http.MethodGet {
		t.Fatalf("upstream saw %s %s", s.method, s.path)
	}
	if got := s.header.Get("X-Bf-Vk"); got != vkLimited {
		t.Fatalf("x-bf-vk = %q, want %q", got, vkLimited)
	}
	if got := s.header.Get("X-Bf-Passthrough-Extra-Params"); got != "true" {
		t.Fatalf("passthrough = %q", got)
	}
	for _, h := range []string{"Authorization", "Cookie", "Origin"} {
		if v := s.header.Get(h); v != "" {
			t.Fatalf("upstream saw %s: %q", h, v)
		}
	}
	if got := echo.recorded(); len(got) != 1 || got[0] != upstreamFrame("fast") {
		t.Fatalf("upstream frames = %q", got)
	}
}

func TestWS_DeniesModel(t *testing.T) {
	f := newFixture(t)
	echo := &wsEcho{}
	f.upstream(echo.handler(t))
	c, _, err := f.dialWS(t, keyLimited)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	cases := []struct{ name, frame, code string }{
		{"denied model", createFrame("up-b/m1"), "model_not_allowed"},
		{"case variant of model", `{"type":"response.create","model":"fast","MODEL":"up-b/m1","input":"x"}`, "model_required"},
		{"case variant of type", `{"Type":"response.create","model":"up-b/m1","input":"x"}`, "model_not_allowed"},
		{"not json", `not json at all`, "model_required"},
		{"type given twice", `{"type":"response.cancel","type":"response.create","model":"up-b/m1"}`, "model_not_allowed"},
		{"missing model", `{"type":"response.create","input":"x"}`, "model_required"},
		{"client fallbacks", `{"type":"response.create","model":"fast","fallbacks":["up-b/m1"],"input":"x"}`, "fallbacks_not_supported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wsRoundTrip(t, c, tc.frame)
			var ev struct {
				Type  string `json:"type"`
				Error struct {
					Type, Code, Message string
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(got), &ev); err != nil {
				t.Fatalf("event %q: %v", got, err)
			}
			if ev.Type != "error" || ev.Error.Code != tc.code || ev.Error.Message == "" {
				t.Fatalf("event = %s, want code %s", got, tc.code)
			}
		})
	}
	if n := len(echo.recorded()); n != 0 {
		t.Fatalf("upstream recorded %d frames, want 0", n)
	}
	frame := createFrame("fast")
	if got := wsRoundTrip(t, c, frame); got != upstreamFrame("fast") {
		t.Fatalf("after a denial, echo = %q, want %q", got, upstreamFrame("fast"))
	}
	if got := echo.recorded(); len(got) != 1 {
		t.Fatalf("upstream frames = %q", got)
	}
}

func TestWS_RejectsBadKey(t *testing.T) {
	f := newFixture(t)
	echo := &wsEcho{}
	f.upstream(echo.handler(t))
	cases := []struct {
		name, key string
		status    int
	}{
		{"no key", "", http.StatusUnauthorized},
		{"unknown key", "sk-wrong", http.StatusUnauthorized},
		{"disabled key", keyDisabled, http.StatusUnauthorized},
		{"no virtual key yet", keyNoVK, http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, status, err := f.dialWS(t, c.key)
			if err == nil {
				t.Fatal("dial succeeded")
			}
			if status != c.status {
				t.Fatalf("status = %d, want %d", status, c.status)
			}
		})
	}
	if n := f.hits.Load(); n != 0 {
		t.Fatalf("upstream hit %d times", n)
	}
}

func TestWS_NonCreateFramesPass(t *testing.T) {
	f := newFixture(t)
	echo := &wsEcho{}
	f.upstream(echo.handler(t))
	c, _, err := f.dialWS(t, keyLimited)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	for _, frame := range []string{
		`{"type":"response.cancel","model":"up-b/m1"}`,
		`{"type":"session.update","fallbacks":["up-b/m1"]}`,
	} {
		if got := wsRoundTrip(t, c, frame); got != frame {
			t.Fatalf("echo = %q, want %q", got, frame)
		}
	}
}

func TestWS_ServerErrorsScrubbed(t *testing.T) {
	f := newFixture(t)
	content := "{\"type\":\"response.output_text.delta\",\"delta\":\"Bifrost \u200c\"}"
	echo := &wsEcho{reply: func(frame string) string {
		switch {
		case strings.Contains(frame, "native"):
			return `{"is_bifrost_error":true,"status_code":429,"error":{"type":"rate_limit","code":"insufficient_quota","message":"bifrost: key sk-bf-abc"}}`
		case strings.Contains(frame, "event"):
			return `{"type":"error","status":502,"error":{"code":"upstream_connection_error","message":"bifrost upstream gone"}}`
		default:
			return content
		}
	}}
	f.upstream(echo.handler(t))
	c, _, err := f.dialWS(t, keyFull)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	cases := []struct{ name, frame, want string }{
		{"content untouched", `{"type":"response.cancel"}`, content},
		{
			"native envelope", `{"type":"response.cancel","x":"native"}`,
			`{"type":"error","error":{"message":"remora: key [redacted]","type":"rate_limit","code":"insufficient_quota","param":null}}`,
		},
		{
			"error event scrubbed", `{"type":"response.cancel","x":"event"}`,
			`{"type":"error","status":502,"error":{"code":"upstream_connection_error","message":"remora upstream gone"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wsRoundTrip(t, c, tc.frame); got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestWS_ClosePropagates(t *testing.T) {
	f := newFixture(t)
	f.upstream(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = c.Close(websocket.StatusPolicyViolation, "bifrost says no")
	})
	c, _, err := f.dialWS(t, keyFull)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, _, err = c.Read(ctx)
	var ce websocket.CloseError
	if !errors.As(err, &ce) {
		t.Fatalf("read err = %v, want a close", err)
	}
	if ce.Code != websocket.StatusPolicyViolation || strings.Contains(strings.ToLower(ce.Reason), "bifrost") {
		t.Fatalf("close = %d %q", ce.Code, ce.Reason)
	}
}

func TestWS_ClientClosePropagates(t *testing.T) {
	f := newFixture(t)
	got := make(chan websocket.StatusCode, 1)
	f.upstream(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		_, _, err = c.Read(r.Context())
		got <- websocket.CloseStatus(err)
	})
	c, _, err := f.dialWS(t, keyFull)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := c.Close(4001, "client done"); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case code := <-got:
		if code != 4001 {
			t.Fatalf("upstream saw close %d, want 4001", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upstream never saw the close")
	}
}

func TestWS_NotReadyUntilSynced(t *testing.T) {
	f := newFixture(t)
	echo := &wsEcho{}
	f.upstream(echo.handler(t))
	var ready atomic.Bool
	f.px.Ready = ready.Load
	if _, status, err := f.dialWS(t, keyFull); err == nil || status != http.StatusServiceUnavailable {
		t.Fatalf("before sync: status %d err %v, want a refused 503 handshake", status, err)
	}
	ready.Store(true)
	if _, _, err := f.dialWS(t, keyFull); err != nil {
		t.Fatalf("after sync: %v", err)
	}
}

func TestWS_AliasKindMustBeChat(t *testing.T) {
	f := newFixture(t)
	echo := &wsEcho{}
	f.upstream(echo.handler(t))
	c, _, err := f.dialWS(t, keyFull)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if got := wsRoundTrip(t, c, createFrame("emb")); !strings.Contains(got, `"model_kind_mismatch"`) {
		t.Fatalf("embeddings alias over the socket: %s, want model_kind_mismatch", got)
	}
	if n := len(echo.recorded()); n != 0 {
		t.Fatalf("upstream recorded %d frames, want 0", n)
	}
	frame := createFrame("fast")
	if got := wsRoundTrip(t, c, frame); got != upstreamFrame("fast") {
		t.Fatalf("chat alias echo = %q, want %q", got, upstreamFrame("fast"))
	}
}

func TestWS_KeyChangeAppliesToOpenSocket(t *testing.T) {
	f := newFixture(t)
	echo := &wsEcho{}
	f.upstream(echo.handler(t))
	c, _, err := f.dialWS(t, keyLimited)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	frame := createFrame("fast")
	if got := wsRoundTrip(t, c, frame); got != upstreamFrame("fast") {
		t.Fatalf("before the change: echo = %q", got)
	}
	// Narrow the key while the socket stays open.
	err = f.st.Write(t.Context(), func(tx *store.Tx) error {
		keys, err := tx.ClientKeys(t.Context())
		if err != nil {
			return err
		}
		for _, k := range keys {
			if k.Name == "limited" {
				k.Allow = []string{"up-a/*"}
				_, err = tx.PutClientKey(t.Context(), k, "")
				return err
			}
		}
		return errors.New("key limited not found")
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := wsRoundTrip(t, c, frame); !strings.Contains(got, `"model_not_allowed"`) {
		t.Fatalf("after narrowing the key: %s, want model_not_allowed", got)
	}
	if n := len(echo.recorded()); n != 1 {
		t.Fatalf("upstream recorded %d frames, want 1", n)
	}
}

// upstreamFrame is createFrame(name) as the front door receives it: an alias travels as alias.EngineModel.
func upstreamFrame(name string) string { return createFrame(alias.EngineModel(name)) }
