// Package control implements the scripted fake upstreams used by the e2e tests
// and the control API that scripts them and reads back what they received.
package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// maxBodyBytes caps a recorded or control request body.
const maxBodyBytes = 64 << 20

// Step is one scripted reply.
type Step struct {
	Status  int               `json:"status"`             // default 200
	Headers map[string]string `json:"headers,omitempty"`  // Content-Type defaults to application/json
	Body    string            `json:"body,omitempty"`     // written verbatim
	DelayMS int               `json:"delay_ms,omitempty"` // before the first byte; honours request cancellation
	SSE     []string          `json:"sse,omitempty"`      // when set: text/event-stream, one flushed `data:` event each, then [DONE]
}

// Script is the ordered replies for one (upstream, route).
type Script struct {
	Steps  []Step `json:"steps"`
	Repeat bool   `json:"repeat"` // after the last step: true = repeat it, false = fall back to the route default
}

// RecordedRequest is one request a fake upstream received.
type RecordedRequest struct {
	Route  string      `json:"route"`
	Method string      `json:"method"`
	Path   string      `json:"path"`
	Header http.Header `json:"header"`
	Body   []byte      `json:"body"` // base64 in JSON
	At     time.Time   `json:"at"`
}

type scriptKey struct {
	upstream string
	route    string
}

type scriptState struct {
	script Script
	next   int
}

// Server holds the scripts and recordings of every fake upstream.
type Server struct {
	defaults map[string]http.HandlerFunc
	routes   map[string]bool

	mu       sync.Mutex
	scripts  map[scriptKey]*scriptState
	requests map[string][]RecordedRequest
}

// New returns a Server. defaults maps a route name to the handler used when
// the route has no script step left; a missing entry answers 501.
func New(defaults map[string]http.HandlerFunc) *Server {
	s := &Server{
		defaults: make(map[string]http.HandlerFunc, len(defaults)),
		routes:   make(map[string]bool),
		scripts:  make(map[scriptKey]*scriptState),
		requests: make(map[string][]RecordedRequest),
	}
	for name, h := range defaults {
		s.defaults[name] = h
	}
	for _, r := range routeTable() {
		s.routes[r.name] = true
	}
	return s
}

// Handler returns the HTTP handler serving the fakes and the control API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, r := range routeTable() {
		mux.HandleFunc(r.method+" /u/{upstream}"+r.path, s.serveUpstream(r.name))
	}
	mux.HandleFunc("PUT /_control/upstreams/{upstream}/routes/{route}", s.putScript)
	mux.HandleFunc("GET /_control/upstreams/{upstream}/requests", s.getRequests)
	mux.HandleFunc("POST /_control/reset", s.reset)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	return mux
}

func (s *Server) serveUpstream(route string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		upstream := r.PathValue("upstream")
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("fake: read body: %v", err))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		step, ok := s.record(upstream, route, RecordedRequest{
			Route:  route,
			Method: r.Method,
			Path:   r.URL.Path,
			Header: r.Header.Clone(),
			Body:   body,
			At:     time.Now().UTC(),
		})
		if ok {
			writeStep(w, r, step)
			return
		}
		if h := s.defaults[route]; h != nil {
			h(w, r)
			return
		}
		writeError(w, http.StatusNotImplemented, "fake: no default for "+route)
	}
}

// record stores the request and returns the scripted step to answer with,
// if any.
func (s *Server) record(upstream, route string, rec RecordedRequest) (Step, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests[upstream] = append(s.requests[upstream], rec)

	st := s.scripts[scriptKey{upstream: upstream, route: route}]
	if st == nil || len(st.script.Steps) == 0 {
		return Step{}, false
	}
	if st.next < len(st.script.Steps) {
		step := st.script.Steps[st.next]
		st.next++
		return step, true
	}
	if st.script.Repeat {
		return st.script.Steps[len(st.script.Steps)-1], true
	}
	return Step{}, false
}

func writeStep(w http.ResponseWriter, r *http.Request, step Step) {
	if step.DelayMS > 0 {
		t := time.NewTimer(time.Duration(step.DelayMS) * time.Millisecond)
		defer t.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
		}
	}
	h := w.Header()
	if step.SSE != nil {
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
	} else {
		h.Set("Content-Type", "application/json")
	}
	for k, v := range step.Headers {
		h.Set(k, v)
	}
	status := step.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if step.SSE != nil {
		writeSSE(w, r, step.SSE)
		return
	}
	if step.Body != "" {
		_, _ = io.WriteString(w, step.Body)
	}
}

// writeSSE sends each entry verbatim as one `data:` event, flushing after
// each, then the terminating `data: [DONE]` event. It stops when the client
// goes away.
func writeSSE(w http.ResponseWriter, r *http.Request, entries []string) {
	rc := http.NewResponseController(w)
	for _, e := range append(entries[:len(entries):len(entries)], "[DONE]") {
		if r.Context().Err() != nil {
			return
		}
		if _, err := io.WriteString(w, "data: "+e+"\n\n"); err != nil {
			return
		}
		_ = rc.Flush()
	}
}

func (s *Server) putScript(w http.ResponseWriter, r *http.Request) {
	route := r.PathValue("route")
	if !s.routes[route] {
		writeError(w, http.StatusNotFound, "fake: unknown route "+route)
		return
	}
	var script Script
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&script); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("fake: decode script: %v", err))
		return
	}
	key := scriptKey{upstream: r.PathValue("upstream"), route: route}
	s.mu.Lock()
	s.scripts[key] = &scriptState{script: script}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getRequests(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	recs := append([]RecordedRequest{}, s.requests[r.PathValue("upstream")]...)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, recs)
}

func (s *Server) reset(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.scripts = make(map[scriptKey]*scriptState)
	s.requests = make(map[string][]RecordedRequest)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	type errBody struct {
		Message string `json:"message"`
	}
	writeJSON(w, status, struct {
		Error errBody `json:"error"`
	}{Error: errBody{Message: msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
