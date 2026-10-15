package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/store"
)

const (
	// wsCreateEvent is the client event that starts a response; the engine routes on its "model"
	// (handlers/wsresponses.go).
	wsCreateEvent = "response.create"
	// maxWSMessage is the largest WebSocket message remora relays in either direction.
	maxWSMessage = MaxBodyBytes
	// wsDialTimeout bounds the handshake with the front door.
	wsDialTimeout = 30 * time.Second
	// maxCloseReason is the longest close reason a close frame can carry.
	maxCloseReason = 123
)

// wsHandshakeHeaders are request headers that belong to the client's own handshake (or name its origin) and are
// never copied to the front door's handshake.
var wsHandshakeHeaders = []string{
	"Connection", "Upgrade", "Host", "Origin", "Content-Length", "Transfer-Encoding", "Te", "Trailer", "Keep-Alive",
	"Proxy-Connection", "Proxy-Authorization", "Sec-Websocket-Key", "Sec-Websocket-Version",
	"Sec-Websocket-Extensions", "Sec-Websocket-Protocol", "Sec-Websocket-Accept",
}

func isWebSocketUpgrade(r *http.Request) bool {
	return headerHasToken(r.Header, "Connection", "upgrade") && headerHasToken(r.Header, "Upgrade", "websocket")
}

func headerHasToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for t := range strings.SplitSeq(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

// serveResponsesWS relays the Responses WebSocket: it dials the front door's /v1/responses with the key's virtual
// key, accepts the client's socket and relays frames both ways. Each client response.create event is checked
// against the key's allowlist on its own model, because the model arrives per message, not per request.
func (p *Proxy) serveResponsesWS(w http.ResponseWriter, r *http.Request, key store.ClientKey) {
	if key.VKValue == "" {
		errKeyNotReady.write(w, shapeOpenAI)
		return
	}
	ctx := r.Context()

	h := r.Header.Clone()
	for _, name := range wsHandshakeHeaders {
		h.Del(name)
	}
	setUpstreamHeaders(h, key.VKValue)
	u := *p.upstream
	u.Path = strings.TrimSuffix(u.Path, "/") + "/v1/responses"
	u.RawPath = ""
	u.RawQuery = r.URL.RawQuery
	u.Fragment = ""

	dialCtx, cancelDial := context.WithTimeout(ctx, wsDialTimeout)
	up, resp, err := websocket.Dial(dialCtx, u.String(), &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: p.transport},
		HTTPHeader: h,
	})
	cancelDial()
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		p.log.WarnContext(ctx, "proxy: dial upstream websocket", slog.Any("err", err))
		errUpstream.write(w, shapeOpenAI)
		return
	}
	defer func() { _ = up.CloseNow() }()
	up.SetReadLimit(maxWSMessage)
	p.runWSHooks(w, r, resp)

	client, err := websocket.Accept(w, r, nil)
	if err != nil {
		// Accept has answered the client already.
		p.log.DebugContext(ctx, "proxy: accept client websocket", slog.Any("err", err))
		_ = up.Close(websocket.StatusGoingAway, "")
		return
	}
	defer func() { _ = client.CloseNow() }()
	client.SetReadLimit(maxWSMessage)

	relayCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type end struct {
		fromClient bool
		err        error
	}
	ends := make(chan end, 2)
	go func() {
		ends <- end{fromClient: true, err: relayClientFrames(relayCtx, client, up, p.wsModelCheck(relayCtx, presentedKey(r)))}
	}()
	go func() { ends <- end{fromClient: false, err: relayServerFrames(relayCtx, up, client)} }()

	first := <-ends
	code, reason := closeFor(first.err)
	if first.fromClient {
		_ = up.Close(code, reason)
	} else {
		_ = client.Close(code, closeReason(reason))
	}
	cancel()
	<-ends
	if first.err != nil && websocket.CloseStatus(first.err) == -1 && !errors.Is(first.err, context.Canceled) {
		p.log.DebugContext(ctx, "proxy: websocket relay ended", slog.Bool("client", first.fromClient),
			slog.Any("err", first.err))
	}
}

// runWSHooks runs the response hooks on the front door's handshake answer, cleaned of engine and handshake
// headers, and copies the headers they leave into the client's handshake answer.
func (p *Proxy) runWSHooks(w http.ResponseWriter, r *http.Request, upResp *http.Response) {
	p.hooksMu.RLock()
	hooks := p.hooks
	p.hooksMu.RUnlock()
	if len(hooks) == 0 || upResp == nil {
		return
	}
	resp := &http.Response{
		Status: upResp.Status, StatusCode: upResp.StatusCode, Proto: upResp.Proto,
		ProtoMajor: upResp.ProtoMajor, ProtoMinor: upResp.ProtoMinor,
		Header: upResp.Header.Clone(), Body: http.NoBody, Request: r,
	}
	cleanResponseHeaders(resp.Header)
	for _, name := range wsHandshakeHeaders {
		resp.Header.Del(name)
	}
	for _, hook := range hooks {
		hook(r, nil, resp)
	}
	for name, vals := range resp.Header {
		w.Header()[name] = vals
	}
}

// relayClientFrames copies the client's messages to the front door until either side fails, answering a denied
// message itself instead of forwarding it.
func relayClientFrames(ctx context.Context, client, up *websocket.Conn, check func(model string) (bool, *apiError)) error {
	for {
		typ, data, err := client.Read(ctx)
		if err != nil {
			return err
		}
		data, e := checkClientFrame(data, check)
		if e != nil {
			if err := client.Write(ctx, websocket.MessageText, e.wsEvent()); err != nil {
				return err
			}
			continue
		}
		if err := up.Write(ctx, typ, data); err != nil {
			return err
		}
	}
}

// relayServerFrames copies the front door's messages to the client, mapping and scrubbing error events only.
func relayServerFrames(ctx context.Context, up, client *websocket.Conn) error {
	for {
		typ, data, err := up.Read(ctx)
		if err != nil {
			return err
		}
		if typ == websocket.MessageText {
			data = rewriteServerFrame(data)
		}
		if err := client.Write(ctx, typ, data); err != nil {
			return err
		}
	}
}

// checkClientFrame returns nil when data may reach the front door, or the error to answer instead. A message
// passes unchecked only when it is a JSON object with exactly one "type" key, spelled exactly, naming another
// event; anything that could be read as a response.create (the engine's decoder matches keys case-insensitively)
// is checked like an HTTP request body: one "model", no "fallbacks", and the model allowed for the key. A frame for
// an alias goes on with alias.EngineModel in its "model".
func checkClientFrame(data []byte, check func(model string) (bool, *apiError)) ([]byte, *apiError) {
	if !mayBeCreate(data) {
		return data, nil
	}
	model, err := jsonModel(data)
	switch {
	case errors.Is(err, ErrClientFallbacks):
		return nil, &errClientFallbacks
	case err != nil:
		return nil, &errModelRequired
	}
	isAlias, e := check(model)
	if e != nil || !isAlias {
		return data, e
	}
	out, err := jsonWithModel(data, alias.EngineModel(model))
	if err != nil {
		return nil, &errModelRequired
	}
	return out, nil
}

// wsModelCheck checks the model of one response.create frame against the client key as it is now, not as it was
// at the handshake: a key disabled, deleted or narrowed while the socket is open applies to its next frame.
func (p *Proxy) wsModelCheck(ctx context.Context, value string) func(model string) (bool, *apiError) {
	return func(model string) (bool, *apiError) {
		key, err := p.store.ClientKeyByValue(ctx, value)
		switch {
		case errors.Is(err, store.ErrNotFound):
			return false, &errInvalidKey
		case err != nil:
			p.log.ErrorContext(ctx, "proxy: look up client key", slog.Any("err", err))
			return false, &errInternal
		case !key.Enabled:
			return false, &errInvalidKey
		case !Allowed(key.Allow, model):
			e := errModelNotAllowed(model)
			return false, &e
		}
		return p.checkAliasKind(ctx, model, alias.KindChat, "/v1/responses")
	}
}

// mayBeCreate reports whether the engine could read data as a response.create event.
func mayBeCreate(data []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return true
	}
	var (
		typ   string
		count int
	)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return true
		}
		k, ok := tok.(string)
		if !ok {
			return true
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return true
		}
		if strings.EqualFold(k, "type") {
			count++
			if k != "type" || json.Unmarshal(v, &typ) != nil {
				return true
			}
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return true
	}
	return count != 1 || strings.EqualFold(strings.TrimSpace(typ), wsCreateEvent)
}

// rewriteServerFrame maps the front door's native error envelope to an OpenAI error event and scrubs any other
// error event. Other messages are returned unchanged.
func rewriteServerFrame(data []byte) []byte {
	if n, ok := findNativeError(data); ok {
		out, err := encodeJSON(wsErrorEvent{Type: "error", Error: n.openAI()})
		if err == nil {
			return scrub(out)
		}
	}
	if isErrorEvent(data) {
		return scrub(data)
	}
	return data
}

// wsErrorEvent is the Responses WebSocket error event.
type wsErrorEvent struct {
	Type   string          `json:"type"`
	Status int             `json:"status,omitempty"`
	Error  openAIErrorBody `json:"error"`
}

// wsEvent renders e as a Responses WebSocket error event.
func (e apiError) wsEvent() []byte {
	return marshal(wsErrorEvent{Type: "error", Status: e.status, Error: openAIErrorBody{
		Message: e.message, Type: jsonString(e.typ), Code: jsonString(e.code), Param: json.RawMessage("null"),
	}})
}

// closeFor is the close code and reason to pass on to the other side when one side's relay ends with err.
func closeFor(err error) (websocket.StatusCode, string) {
	var ce websocket.CloseError
	if !errors.As(err, &ce) {
		return websocket.StatusGoingAway, ""
	}
	switch ce.Code {
	case websocket.StatusNoStatusRcvd:
		return websocket.StatusNormalClosure, ce.Reason
	case websocket.StatusAbnormalClosure, websocket.StatusTLSHandshake:
		return websocket.StatusGoingAway, ""
	}
	return ce.Code, ce.Reason
}

// closeReason scrubs a close reason from the front door and fits it in a close frame.
func closeReason(reason string) string {
	reason = scrubString(reason)
	if len(reason) > maxCloseReason {
		reason = strings.ToValidUTF8(reason[:maxCloseReason], "")
	}
	return reason
}
