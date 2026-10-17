package proxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/store"
)

// Request headers remora sets for the front door.
const (
	headerVirtualKey  = "X-Bf-Vk"                       // the front door's virtual key (core/utils.go)
	headerExtraParams = "X-Bf-Passthrough-Extra-Params" // forward unknown JSON params instead of dropping them
)

// upstreamIdleConns is how many loopback connections to the front door stay open, so a burst of streams reuses them
// instead of dialing.
const upstreamIdleConns = 1024

// ResponseHook sees every proxied response in ModifyResponse, after remora's own x-remora-* headers are set and
// before the first byte is written, so it may rewrite those headers. r is the client's request; reqBody is the
// exact body ExtractModel held (nil on the WebSocket route). Plan 03 uses it for served-by names of internal
// providers; x-remora-dropped-params is set after every hook has run, from the served-by they leave.
type ResponseHook func(r *http.Request, reqBody []byte, resp *http.Response)

// Proxy serves remora's /v1 surface: it authenticates the remora key, checks the model against the key's
// allowlist, swaps in the key's front-door virtual key and streams the front door's answer back unbuffered,
// rewriting only headers and error envelopes so no engine detail reaches a client.
type Proxy struct {
	upstream  *url.URL
	store     *store.Store
	log       *slog.Logger
	transport *http.Transport
	rp        *httputil.ReverseProxy

	hooksMu sync.RWMutex
	hooks   []ResponseHook

	// Ready, when set, reports whether the front door holds the store's state; until it does, /v1 answers 503
	// instead of meeting missing or stale providers, rules or keys. serve sets it to the reconciler's Synced.
	Ready func() bool
	// Picker, when set, picks the first member of a spreading alias's request (X-Remora-First).
	Picker *Picker
}

// New returns a Proxy in front of the front door at upstream (its inference routes), reading keys and aliases from
// st. Mount it at /v1/. The /v1 server must not set a WriteTimeout: streams and transcriptions run long.
func New(upstream *url.URL, st *store.Store, log *slog.Logger) *Proxy {
	p := &Proxy{
		upstream: upstream,
		store:    st,
		log:      log,
		transport: &http.Transport{
			Proxy:                 nil, // loopback only; never an environment proxy
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns:          upstreamIdleConns,
			MaxIdleConnsPerHost:   upstreamIdleConns,
			IdleConnTimeout:       90 * time.Second,
			ExpectContinueTimeout: time.Second,
			DisableCompression:    true, // bodies pass byte for byte; the client's Accept-Encoding is not forwarded
			ResponseHeaderTimeout: 0,    // no limit: the provider's own deadline applies
		},
	}
	p.rp = &httputil.ReverseProxy{
		Rewrite:        p.rewrite,
		Transport:      p.transport,
		FlushInterval:  -1,
		ModifyResponse: p.modifyResponse,
		ErrorHandler:   p.errorHandler,
		ErrorLog:       slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	return p
}

// OnResponse adds a hook that runs on every proxied response, in the order added.
func (p *Proxy) OnResponse(h ResponseHook) {
	p.hooksMu.Lock()
	defer p.hooksMu.Unlock()
	p.hooks = append(p.hooks, h)
}

type routeKind int

const (
	routeForward routeKind = iota // proxied to the front door
	routeModels                   // served by remora from its aliases
)

// route is one /v1 endpoint.
type route struct {
	kind      routeKind
	upstream  string     // front-door path, for routeForward
	aliasKind alias.Kind // the only alias kind this endpoint serves, for routeForward
}

// routes maps "METHOD /path" to its route. GET /v1/responses with a WebSocket upgrade is served by
// serveResponsesWS.
var routes = map[string]route{
	"POST /v1/chat/completions":     {kind: routeForward, upstream: "/v1/chat/completions", aliasKind: alias.KindChat},
	"POST /v1/responses":            {kind: routeForward, upstream: "/v1/responses", aliasKind: alias.KindChat},
	"POST /v1/messages":             {kind: routeForward, upstream: "/anthropic/v1/messages", aliasKind: alias.KindChat},
	"POST /v1/embeddings":           {kind: routeForward, upstream: "/v1/embeddings", aliasKind: alias.KindEmbeddings},
	"POST /v1/audio/transcriptions": {kind: routeForward, upstream: "/v1/audio/transcriptions", aliasKind: alias.KindTranscription},
	"POST /v1/decisions":            {kind: routeForward, upstream: "/v1/decisions", aliasKind: alias.KindDecisions},
	"POST /v1/systemone":            {kind: routeForward, upstream: "/typesafe/v1/systemone", aliasKind: alias.KindDecisions},
	"GET /v1/models":                {kind: routeModels},
}

// shapeFor is the error shape of a remora path, whatever the method: the SDK that calls it reads that shape.
func shapeFor(path string) shape {
	switch path {
	case "/v1/messages":
		return shapeAnthropic
	case "/v1/systemone":
		return shapeTypeSafe
	default:
		return shapeOpenAI
	}
}

// reqInfo travels with a forwarded request from ServeHTTP to the ReverseProxy callbacks.
type reqInfo struct {
	in       *http.Request // the client's request
	upstream string
	shape    shape
	model    string
	isAlias  bool
	first    string // X-Remora-First to send, "" for none
	vk       string
	body     []byte
	spans    bodySpans // where the parts of body the proxy reads sit; zero for multipart
}

type reqInfoKey struct{}

func infoFrom(ctx context.Context) *reqInfo {
	info, _ := ctx.Value(reqInfoKey{}).(*reqInfo)
	return info
}

// ServeHTTP serves one /v1 request.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sh := shapeFor(r.URL.Path)
	ws := r.Method == http.MethodGet && r.URL.Path == "/v1/responses" && isWebSocketUpgrade(r)
	rt, ok := routes[r.Method+" "+r.URL.Path]
	if !ok && !ws {
		errNotFound.write(w, sh)
		return
	}
	if p.Ready != nil && !p.Ready() {
		errNotReady.write(w, sh)
		return
	}
	if ws {
		if key, ok := p.authenticate(w, r, sh); ok {
			p.serveResponsesWS(w, r, key)
		}
		return
	}
	key, ok := p.authenticate(w, r, sh)
	if !ok {
		return
	}
	switch rt.kind {
	case routeForward:
		p.forward(w, r, rt, sh, key)
	case routeModels:
		p.serveModels(w, r, key)
	}
}

// authenticate finds the enabled client key the request presents (Authorization: Bearer, else x-api-key). On
// failure it writes the error and returns false.
func (p *Proxy) authenticate(w http.ResponseWriter, r *http.Request, sh shape) (store.ClientKey, bool) {
	value := presentedKey(r)
	if value == "" {
		errInvalidKey.write(w, sh)
		return store.ClientKey{}, false
	}
	key, err := p.store.ClientKeyByValue(r.Context(), value)
	switch {
	case errors.Is(err, store.ErrNotFound):
		errInvalidKey.write(w, sh)
		return store.ClientKey{}, false
	case err != nil:
		p.log.ErrorContext(r.Context(), "proxy: look up client key", slog.Any("err", err))
		errInternal.write(w, sh)
		return store.ClientKey{}, false
	case !key.Enabled:
		errInvalidKey.write(w, sh)
		return store.ClientKey{}, false
	}
	return key, true
}

// presentedKey is the client key value the request presents: Authorization: Bearer, else x-api-key.
func presentedKey(r *http.Request) string {
	if v := bearer(r.Header.Get("Authorization")); v != "" {
		return v
	}
	return strings.TrimSpace(r.Header.Get("X-Api-Key"))
}

func bearer(h string) string {
	scheme, token, ok := strings.Cut(strings.TrimSpace(h), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// forward checks the request's model and hands it to the reverse proxy.
func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, rt route, sh shape, key store.ClientKey) {
	ctx := r.Context()
	model, raw, sp, err := extractModel(r)
	switch {
	case errors.Is(err, ErrBodyTooLarge):
		errTooLarge.write(w, sh)
		return
	case errors.Is(err, ErrClientFallbacks):
		errClientFallbacks.write(w, sh)
		return
	case errors.Is(err, ErrNoModel):
		errModelRequired.write(w, sh)
		return
	case err != nil:
		// The client went away or sent a broken body.
		p.log.DebugContext(ctx, "proxy: read request body", slog.Any("err", err))
		errModelRequired.write(w, sh)
		return
	}
	if !Allowed(key.Allow, model) {
		errModelNotAllowed(model).write(w, sh)
		return
	}
	if key.VKValue == "" {
		errKeyNotReady.write(w, sh)
		return
	}

	info := &reqInfo{in: r, upstream: rt.upstream, shape: sh, model: model, vk: key.VKValue, body: raw, spans: sp}
	isAlias, e := p.checkAliasKind(ctx, model, rt.aliasKind, r.URL.Path)
	if e != nil {
		e.write(w, sh)
		return
	}
	info.isAlias = isAlias
	body := raw
	if isAlias && p.Picker != nil {
		info.first = p.Picker.Pick(model)
	}
	if isAlias {
		if body, err = withModel(raw, sp, r.Header.Get("Content-Type"), alias.EngineModel(model)); err != nil {
			errModelRequired.write(w, sh)
			return
		}
	}

	out := r.WithContext(context.WithValue(ctx, reqInfoKey{}, info))
	out.Body = &dropReader{b: body}
	out.ContentLength = int64(len(body))
	out.TransferEncoding = nil
	p.rp.ServeHTTP(w, out)
}

// dropReader serves b once and lets go of it at EOF or Close, so a long stream does not keep its request body alive.
// The transport may Close it from another goroutine while a Read runs, hence the lock.
type dropReader struct {
	mu sync.Mutex
	b  []byte
}

func (d *dropReader) Read(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.b) == 0 {
		d.b = nil
		return 0, io.EOF
	}
	n := copy(p, d.b)
	if d.b = d.b[n:]; len(d.b) == 0 {
		d.b = nil
	}
	return n, nil
}

func (d *dropReader) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.b = nil
	return nil
}

// checkAliasKind reports whether model names an alias, and refuses an alias of another kind than the endpoint
// serves. It fails closed: when the aliases cannot be read, the request is refused rather than sent unchecked.
func (p *Proxy) checkAliasKind(ctx context.Context, model string, kind alias.Kind, path string) (bool, *apiError) {
	a, ok, err := p.store.Alias(ctx, model)
	if err != nil {
		p.log.ErrorContext(ctx, "proxy: load aliases", slog.Any("err", err))
		return false, &errInternal
	}
	if !ok {
		return false, nil
	}
	if a.Kind != kind {
		e := errAliasKind(model, a.Kind, path)
		return true, &e
	}
	return true, nil
}

// rewrite points the outbound request at the front door and replaces every client credential and engine control
// header with remora's own.
func (p *Proxy) rewrite(pr *httputil.ProxyRequest) {
	info := infoFrom(pr.In.Context())
	u := *p.upstream
	u.Path = strings.TrimSuffix(u.Path, "/") + info.upstream
	u.RawPath = ""
	u.RawQuery = pr.In.URL.RawQuery
	u.Fragment = ""
	pr.Out.URL = &u
	pr.Out.Host = u.Host

	setUpstreamHeaders(pr.Out.Header, info.vk)
	if info.first != "" {
		pr.Out.Header.Set(alias.FirstHeader, info.first)
	}
	pr.Out.Header.Set(headerForwardSession, opencodeSession(pr.In.Header, info.body, info.spans))
	if k := affinityKey(pr.In.Header, info.vk, info.spans.promptCacheKeyIn(info.body), affinitySource(info.body, info.spans)); k != "" {
		pr.Out.Header.Set(headerForwardAffinity, k)
	}
}

// setUpstreamHeaders replaces every client credential and engine control header in h with remora's own. No client
// may set X-Remora-First: it selects a front-door rule.
func setUpstreamHeaders(h http.Header, vk string) {
	for name := range h {
		switch l := strings.ToLower(name); {
		// Expect: the body is already read, so the front door must not be asked to confirm it.
		case l == "authorization", l == "x-api-key", l == "cookie", l == "accept-encoding", l == "expect", l == "x-remora-first",
			strings.HasPrefix(l, "x-bf-"), strings.Contains(l, "bifrost"):
			delete(h, name)
		}
	}
	h.Set(headerVirtualKey, vk)
	h.Set(headerExtraParams, "true")
}

// modifyResponse rewrites the front door's answer: engine headers out, remora headers in, error bodies mapped and
// scrubbed, SSE error events rewritten as they pass. The body itself is never buffered on success.
func (p *Proxy) modifyResponse(resp *http.Response) error {
	info := infoFrom(resp.Request.Context())
	setRemoraHeaders(resp.Header, info)
	switch {
	case isEventStream(resp.Header):
		resp.Body = newSSEScrubber(resp.Body, info.shape)
		resp.Header.Del("Content-Length")
		resp.ContentLength = -1
	case resp.StatusCode >= http.StatusBadRequest:
		if err := rewriteErrorBody(resp, info.shape); err != nil {
			return err
		}
	case isJSON(resp.Header):
		if err := stripJSONBody(resp); err != nil {
			return err
		}
	}
	p.hooksMu.RLock()
	hooks := p.hooks
	p.hooksMu.RUnlock()
	for _, h := range hooks {
		h(info.in, info.body, resp)
	}
	setDroppedParams(resp.Header, info)
	if p.Picker != nil {
		p.Picker.Started(resp.Header.Get(headerServedBy))
	}
	// Nothing reads the request body past this point; a stream may run for minutes.
	info.body, info.spans = nil, bodySpans{}
	return nil
}

// errorHandler answers when the front door cannot be reached or its answer cannot be read. The cause, which names
// the engine's address, is logged and never sent.
func (p *Proxy) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	sh := shapeOpenAI
	if info := infoFrom(r.Context()); info != nil {
		sh = info.shape
		info.body, info.spans = nil, bodySpans{}
	}
	if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
		p.log.DebugContext(r.Context(), "proxy: client went away", slog.Any("err", err))
	} else {
		p.log.WarnContext(r.Context(), "proxy: upstream request failed", slog.Any("err", err))
	}
	errUpstream.write(w, sh)
}
