//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/47vigen/remora/test/fakes/control"
)

func TestEmbeddings(t *testing.T) {
	resetFakes(t)
	want := []string{"AAAAAA==", "AAAAAB=="}
	once(t, "a", control.RouteOpenAIEmbeddings, control.Step{Body: `{"object":"list","model":"emb-1","data":[` +
		`{"object":"embedding","index":0,"embedding":"` + want[0] + `"},` +
		`{"object":"embedding","index":1,"embedding":"` + want[1] + `"}],` +
		`"usage":{"prompt_tokens":2,"total_tokens":2}}`})

	rep := doV1(t, http.MethodPost, "/v1/embeddings", keyFull, map[string]any{
		"model": "emb", "input": []string{"a", "b"}, "dimensions": 1536, "encoding_format": "base64",
	})
	wantStatus(t, rep, http.StatusOK)
	wantHeader(t, rep, "X-Remora-Alias", "emb")

	reqs := received(t, "a", control.RouteOpenAIEmbeddings)
	if len(reqs) != 1 {
		t.Fatalf("up-a got %d embeddings requests, want 1", len(reqs))
	}
	var sent struct {
		Dimensions     int             `json:"dimensions"`
		EncodingFormat string          `json:"encoding_format"`
		Input          json.RawMessage `json:"input"`
	}
	decodeJSON(t, reqs[0].Body, &sent)
	if sent.Dimensions != 1536 || sent.EncodingFormat != "base64" || !jsonEqual(t, sent.Input, []byte(`["a","b"]`)) {
		t.Errorf("upstream body %s, want dimensions 1536, encoding_format base64, input [a b]", reqs[0].Body)
	}

	var got struct {
		Data []struct {
			Embedding json.RawMessage `json:"embedding"`
		} `json:"data"`
	}
	decodeJSON(t, rep.Body, &got)
	embs := make([]string, 0, len(got.Data))
	for _, d := range got.Data {
		var s string
		if err := json.Unmarshal(d.Embedding, &s); err != nil {
			t.Fatalf("embedding %s is not a base64 string: %v", d.Embedding, err)
		}
		embs = append(embs, s)
	}
	if !reflect.DeepEqual(embs, want) {
		t.Errorf("embeddings %q, want %q", embs, want)
	}
}
