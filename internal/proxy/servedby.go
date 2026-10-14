package proxy

import (
	"net/http"
	"strings"
)

// ServedByHook returns a response hook that rewrites x-remora-served-by, which the proxy sets to
// "<front-door provider>/<model>", to rename(provider, model). serve passes the OAuth engine's mapping, which turns
// "remora-oauth/codex/gpt-x" into "codex/gpt-x" so the internal provider never reaches a client. x-remora-dropped-params
// runs after every hook, so it sees the renamed value.
func ServedByHook(rename func(provider, model string) string) ResponseHook {
	return func(_ *http.Request, _ []byte, resp *http.Response) {
		sb := resp.Header.Get(headerServedBy)
		provider, model, ok := strings.Cut(sb, "/")
		if !ok || provider == "" || model == "" {
			return
		}
		if renamed := rename(provider, model); renamed != "" && renamed != sb {
			resp.Header.Set(headerServedBy, renamed)
		}
	}
}
