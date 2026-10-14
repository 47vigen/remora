package proxy

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestModels(t *testing.T) {
	f := newFixture(t)

	type entry map[string]json.RawMessage
	list := func(t *testing.T, key string) (int, []entry, []byte) {
		t.Helper()
		resp := f.do(t, http.MethodGet, "/v1/models", key, "", nil, nil)
		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode, nil, resp.Body
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("Content-Type = %q", ct)
		}
		var body struct {
			Object string          `json:"object"`
			Data   json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(resp.Body, &body); err != nil {
			t.Fatalf("body %s: %v", resp.Body, err)
		}
		if body.Object != "list" || string(body.Data) == "null" {
			t.Fatalf("body = %s", resp.Body)
		}
		var data []entry
		if err := json.Unmarshal(body.Data, &data); err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, data, resp.Body
	}
	ids := func(data []entry) []string {
		out := []string{}
		for _, e := range data {
			var id string
			_ = json.Unmarshal(e["id"], &id)
			out = append(out, id)
		}
		return out
	}

	t.Run("all aliases", func(t *testing.T) {
		_, data, raw := list(t, keyFull)
		if got, want := ids(data), []string{"emb", "fast", "memory/fast"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("ids = %q, want %q", got, want)
		}
		fast, mem := data[1], data[2]
		for k, want := range map[string]string{
			"object": `"model"`, "created": `0`, "owned_by": `"remora"`, "context_length": `131072`,
			"supported_parameters": `["structured_outputs","tools"]`,
		} {
			if got := string(fast[k]); got != want {
				t.Errorf("fast.%s = %s, want %s (%s)", k, got, want, raw)
			}
		}
		if _, ok := mem["context_length"]; ok {
			t.Errorf("memory/fast has context_length: %s", raw)
		}
		if got := string(mem["supported_parameters"]); got != `[]` {
			t.Errorf("memory/fast supported_parameters = %s, want []", got)
		}
	})
	t.Run("only allowed aliases, never provider models", func(t *testing.T) {
		_, data, _ := list(t, keyLimited) // allow ["fast","up-a/*"]
		if got := ids(data); !reflect.DeepEqual(got, []string{"fast"}) {
			t.Fatalf("ids = %q, want [fast]", got)
		}
	})
	t.Run("no alias allowed", func(t *testing.T) {
		_, data, raw := list(t, keyNarrow) // allow ["up-a/*"]
		if len(data) != 0 {
			t.Fatalf("data = %s, want []", raw)
		}
	})
	t.Run("no key", func(t *testing.T) {
		status, _, raw := list(t, "")
		if status != http.StatusUnauthorized || errCode(t, raw) != "invalid_api_key" {
			t.Fatalf("got %d %s", status, raw)
		}
	})
	t.Run("wrong method", func(t *testing.T) {
		resp := f.do(t, http.MethodPost, "/v1/models", keyFull, "application/json", chatBody("fast"), nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})
	if n := f.hits.Load(); n != 0 {
		t.Fatalf("front door was called %d times", n)
	}
}
