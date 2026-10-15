//go:build e2e

package e2e

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/47vigen/remora/test/fakes/control"
)

// errorCode reads error.code from an OpenAI error body.
func errorCode(t *testing.T, rep reply) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeJSON(t, rep.Body, &body)
	return body.Error.Code
}

func TestAllowlist(t *testing.T) {
	resetFakes(t)
	limited := func(model string) reply {
		return doV1(t, http.MethodPost, "/v1/chat/completions", keyLimited, chatBody(model, false))
	}

	wantStatus(t, limited("fast"), http.StatusOK)
	wantStatus(t, limited("up-a/m1"), http.StatusOK)
	for _, model := range []string{"memory/fast", "up-b/m1"} {
		rep := limited(model)
		wantStatus(t, rep, http.StatusForbidden)
		if code := errorCode(t, rep); code != "model_not_allowed" {
			t.Errorf("%s: error.code %q, want model_not_allowed; body %s", model, code, rep.Body)
		}
	}
	// Only the two allowed calls reached an upstream.
	var total int
	for _, up := range []string{"a", "b", "c"} {
		total += len(received(t, up, control.RouteOpenAIChat))
	}
	if total != 2 {
		t.Errorf("upstreams got %d chat requests, want 2 (none for the refused models)", total)
	}

	wantStatus(t, doV1(t, http.MethodPost, "/v1/chat/completions", keyOff, chatBody("fast", false)), http.StatusUnauthorized)
	wantStatus(t, doV1(t, http.MethodPost, "/v1/chat/completions", "", chatBody("fast", false)), http.StatusUnauthorized)
}

type modelsReply struct {
	Data []struct {
		ID                  string   `json:"id"`
		ContextLength       int      `json:"context_length"`
		SupportedParameters []string `json:"supported_parameters"`
	} `json:"data"`
}

func listModels(t *testing.T, key string) (modelsReply, []string) {
	t.Helper()
	rep := doV1(t, http.MethodGet, "/v1/models", key, nil)
	wantStatus(t, rep, http.StatusOK)
	var m modelsReply
	decodeJSON(t, rep.Body, &m)
	ids := make([]string, 0, len(m.Data))
	for _, d := range m.Data {
		ids = append(ids, d.ID)
	}
	return m, ids
}

func TestModelsList(t *testing.T) {
	m, ids := listModels(t, keyFull)
	if want := []string{"cool", "emb", "fast", "inner", "jev", "memory/fast", "openai/fast"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("full key ids %q, want %q", ids, want)
	}
	for _, d := range m.Data {
		if strings.Contains(d.ID, "up-") || strings.Contains(d.ID, "ts-") {
			t.Errorf("model list names a provider model: %q", d.ID)
		}
		if d.ID != "fast" {
			continue
		}
		if d.ContextLength != 131072 {
			t.Errorf("fast context_length %d, want 131072", d.ContextLength)
		}
		if want := []string{"structured_outputs", "tools"}; !reflect.DeepEqual(d.SupportedParameters, want) {
			t.Errorf("fast supported_parameters %q, want %q", d.SupportedParameters, want)
		}
	}

	if _, ids := listModels(t, keyLimited); !reflect.DeepEqual(ids, []string{"fast"}) {
		t.Errorf("limited key ids %q, want [fast]", ids)
	}
}
