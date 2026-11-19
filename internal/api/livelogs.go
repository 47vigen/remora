package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/47vigen/remora/internal/api/gen"
)

const (
	// liveBuffer is the number of rows queued in a live viewer's subscription channel.
	liveBuffer = 64
	// liveBacklog is how many more rows wait in the tail for a viewer whose channel is full, so a burst does not cost
	// the viewer its connection.
	liveBacklog = 1000
	// liveWriteTimeout bounds one frame's write to a viewer.
	liveWriteTimeout = 10 * time.Second
	// liveRootAliases bounds the remembered aliases of requests whose fallback attempts may still arrive.
	liveRootAliases = 4096
)

// liveFilter is the live stream's optional filter, applied to translated entries.
type liveFilter struct{ key, alias, provider, status string }

func (f liveFilter) match(e gen.LogEntry) bool {
	return (f.key == "" || deref(e.Key) == f.key) &&
		(f.alias == "" || deref(e.Alias) == f.alias) &&
		(f.provider == "" || e.Provider == f.provider) &&
		(f.status == "" || string(e.Status) == f.status)
}

// streamLogs serves GET /api/logs/live: a WebSocket of completed log entries, one JSON gen.LogEntry per text frame,
// fed by the server's one front-door log tail. It sits behind the same authentication as every /api route (the
// session cookie, which a same-origin browser WebSocket sends, or an API token) and accepts only the request's own
// origin. Frames from the client (the UI's "ping" keepalive) are read and discarded. The tail never waits for a viewer:
// on queue overflow the handler sends 1013 (try again later) if it can still write to the socket.
func (s *Server) streamLogs(w http.ResponseWriter, r *http.Request) {
	if s.d.Tail == nil || s.d.Names == nil {
		s.writeError(w, r, errNotImplemented)
		return
	}
	q := r.URL.Query()
	f := liveFilter{key: q.Get("key"), alias: q.Get("alias"), provider: q.Get("provider"), status: q.Get("status")}
	if f.status != "" && f.status != "success" && f.status != "error" {
		s.writeError(w, r, badRequest("status must be success or error"))
		return
	}
	// Subscribe before the upgrade, so every row completed once the client is connected reaches it.
	rows, unsubscribe := s.d.Tail.Subscribe(r.Context(), s.d.liveBuffer, s.d.liveBacklog)
	defer unsubscribe()
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{r.Host}})
	if err != nil {
		return // Accept has answered
	}
	defer func() { _ = c.CloseNow() }()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		defer cancel()
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	}()

	roots := map[string]string{} // request id → its alias, for its fallback attempts
	for {
		select {
		case <-ctx.Done():
			return
		case l, ok := <-rows:
			if !ok {
				_ = c.Close(websocket.StatusTryAgainLater, "fell behind; reconnect")
				return
			}
			rootAlias := ""
			if l.ParentRequestID == nil {
				if len(roots) >= liveRootAliases {
					clear(roots)
				}
				roots[l.ID] = s.ruleAlias(ctx, l.RoutingRuleID)
			} else {
				rootAlias = roots[*l.ParentRequestID]
			}
			e := s.toEntry(ctx, l, rootAlias)
			if !f.match(e) {
				continue
			}
			s.fillStatusCode(ctx, l, &e)
			b, err := json.Marshal(e)
			if err != nil {
				s.log().ErrorContext(ctx, "encoding a live log entry failed", "err", err)
				continue
			}
			wctx, wcancel := context.WithTimeout(ctx, liveWriteTimeout)
			err = c.Write(wctx, websocket.MessageText, b)
			wcancel()
			if err != nil {
				return
			}
		}
	}
}
