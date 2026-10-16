package quota

import (
	"context"
	"sync"
	"time"

	"github.com/47vigen/remora/internal/engine/cliproxyapi"
)

type reply struct {
	status int
	body   string
	err    error
}

type apiCall struct {
	at  time.Time
	req cliproxyapi.APICallRequest
}

// fakeEngine is an in-memory Engine: no network. Replies are scripted per URL and popped in order; the last one
// repeats; an unscripted URL answers 200 {}.
type fakeEngine struct {
	mu         sync.Mutex
	creds      []cliproxyapi.Credential
	credsErr   error
	credCalls  []time.Time
	script     map[string][]reply
	calls      []apiCall
	refreshErr error
	refreshed  []string
}

func newFakeEngine(creds ...cliproxyapi.Credential) *fakeEngine {
	return &fakeEngine{creds: creds, script: map[string][]reply{}}
}

func (e *fakeEngine) on(url string, rs ...reply) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.script[url] = append(e.script[url], rs...)
}

func (e *fakeEngine) Credentials(context.Context) ([]cliproxyapi.Credential, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.credCalls = append(e.credCalls, time.Now())
	if e.credsErr != nil {
		return nil, e.credsErr
	}
	return append([]cliproxyapi.Credential(nil), e.creds...), nil
}

func (e *fakeEngine) APICall(_ context.Context, in cliproxyapi.APICallRequest) (cliproxyapi.APICallResponse, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, apiCall{at: time.Now(), req: in})
	r := reply{status: 200, body: `{}`}
	if rs := e.script[in.URL]; len(rs) > 0 {
		r = rs[0]
		if len(rs) > 1 {
			e.script[in.URL] = rs[1:]
		}
	}
	if r.err != nil {
		return cliproxyapi.APICallResponse{}, r.err
	}
	return cliproxyapi.APICallResponse{StatusCode: r.status, Body: r.body}, nil
}

func (e *fakeEngine) RefreshCredential(_ context.Context, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.refreshed = append(e.refreshed, name)
	return e.refreshErr
}

func (e *fakeEngine) recorded() []apiCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]apiCall(nil), e.calls...)
}

// staticVersion is a versionCache that never reads a manifest.
func staticVersion() *versionCache {
	return &versionCache{now: time.Now, get: func(context.Context) (string, error) { return "", context.Canceled }}
}
