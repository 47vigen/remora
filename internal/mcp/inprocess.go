package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"

	"github.com/47vigen/remora/internal/api/gen"
)

type authKey struct{}

// WithAuthorization returns ctx carrying the caller's Authorization header value for the in-process client.
func WithAuthorization(ctx context.Context, v string) context.Context {
	return context.WithValue(ctx, authKey{}, v)
}

// InProcess returns a management client whose every call is served by h in process. Each request carries the
// Authorization in its context (WithAuthorization) and no credential otherwise, so h authenticates the caller
// itself.
func InProcess(h http.Handler) (*gen.ClientWithResponses, error) {
	return gen.NewClientWithResponses("http://remora.internal",
		gen.WithHTTPClient(&http.Client{Transport: handlerTransport{h}}),
		gen.WithRequestEditorFn(func(ctx context.Context, r *http.Request) error {
			if v, ok := ctx.Value(authKey{}).(string); ok {
				r.Header.Set("Authorization", v)
			}
			return nil
		}))
}

type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, r)
	return rec.Result(), nil
}
