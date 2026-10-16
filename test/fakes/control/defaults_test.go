package control_test

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/47vigen/remora/test/fakes/control"
)

// persian holds two zero-width non-joiners (U+200C).
const persian = "می\u200cشود و نمی\u200cخواهم"

func newDefaultsServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(control.New(control.Defaults()).Handler())
	t.Cleanup(srv.Close)
	return srv
}

// response is a fully read reply.
type response struct {
	StatusCode int
	Header     http.Header
	Body       io.Reader
}

func post(t *testing.T, url, body string) response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return response{StatusCode: resp.StatusCode, Header: resp.Header, Body: bytes.NewReader(b)}
}

func decode(t *testing.T, r io.Reader, v any) {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("decode %q: %v", b, err)
	}
}

// readEvents returns the data payload of every SSE event in order.
func readEvents(t *testing.T, r io.Reader) []string {
	t.Helper()
	var out []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			t.Fatalf("unexpected SSE line %q", line)
		}
		out = append(out, data)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return out
}

type chatChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func TestDefaultsAndSSE(t *testing.T) {
	t.Run("chat", func(t *testing.T) {
		srv := newDefaultsServer(t)
		resp := post(t, srv.URL+"/u/a/v1/chat/completions",
			`{"model":"m1","messages":[{"role":"user","content":"hi"}]}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("content-type = %q", ct)
		}
		var body struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Model   string `json:"model"`
			Choices []struct {
				Message struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage struct {
				TotalTokens int `json:"total_tokens"`
			} `json:"usage"`
		}
		decode(t, resp.Body, &body)
		if body.Object != "chat.completion" || body.Model != "m1" || body.ID == "" {
			t.Fatalf("body = %+v", body)
		}
		if len(body.Choices) != 1 || body.Choices[0].Message.Role != "assistant" ||
			body.Choices[0].FinishReason != "stop" {
			t.Fatalf("choices = %+v", body.Choices)
		}
		if got := body.Choices[0].Message.Content; !bytes.Equal([]byte(got), []byte(control.DefaultChatContent)) {
			t.Fatalf("content = %q", got)
		}
		if control.DefaultChatContent != persian {
			t.Fatalf("default content %q is not the Persian fixture", control.DefaultChatContent)
		}
		if body.Usage.TotalTokens == 0 {
			t.Fatal("usage missing")
		}
	})

	t.Run("chat json_schema", func(t *testing.T) {
		srv := newDefaultsServer(t)
		resp := post(t, srv.URL+"/u/a/v1/chat/completions",
			`{"model":"m1","messages":[],"response_format":{"type":"json_schema","json_schema":{"name":"r"}}}`)
		var body struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		decode(t, resp.Body, &body)
		if !json.Valid([]byte(body.Choices[0].Message.Content)) {
			t.Fatalf("content %q is not JSON", body.Choices[0].Message.Content)
		}
	})

	t.Run("chat stream", func(t *testing.T) {
		srv := newDefaultsServer(t)
		resp := post(t, srv.URL+"/u/a/v1/chat/completions",
			`{"model":"m1","stream":true,"stream_options":{"include_usage":true},"messages":[]}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
			t.Fatalf("content-type = %q", ct)
		}
		events := readEvents(t, resp.Body)
		if len(events) < 3 || events[len(events)-1] != "[DONE]" {
			t.Fatalf("events = %q", events)
		}
		var content strings.Builder
		var id string
		var finished, usage bool
		for _, e := range events[:len(events)-1] {
			var c chatChunk
			if err := json.Unmarshal([]byte(e), &c); err != nil {
				t.Fatalf("chunk %q: %v", e, err)
			}
			if id == "" {
				id = c.ID
			}
			if c.ID != id || c.Object != "chat.completion.chunk" || c.Model != "m1" {
				t.Fatalf("chunk %q: id/object/model mismatch", e)
			}
			for _, ch := range c.Choices {
				content.WriteString(ch.Delta.Content)
				if ch.FinishReason != nil && *ch.FinishReason == "stop" {
					finished = true
				}
			}
			if c.Usage != nil && c.Usage.TotalTokens > 0 {
				usage = true
			}
		}
		if content.String() != control.DefaultChatContent || !finished || !usage {
			t.Fatalf("content %q finished %v usage %v", content.String(), finished, usage)
		}
	})

	t.Run("embeddings", func(t *testing.T) {
		srv := newDefaultsServer(t)
		resp := post(t, srv.URL+"/u/a/v1/embeddings", `{"model":"emb-1","input":["a","b"],"dimensions":4}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var body struct {
			Object string `json:"object"`
			Model  string `json:"model"`
			Data   []struct {
				Object    string    `json:"object"`
				Index     int       `json:"index"`
				Embedding []float64 `json:"embedding"`
			} `json:"data"`
		}
		decode(t, resp.Body, &body)
		if body.Object != "list" || body.Model != "emb-1" || len(body.Data) != 2 {
			t.Fatalf("body = %+v", body)
		}
		for i, d := range body.Data {
			if d.Object != "embedding" || d.Index != i || len(d.Embedding) != 4 {
				t.Fatalf("data[%d] = %+v", i, d)
			}
		}
	})

	t.Run("embeddings base64", func(t *testing.T) {
		srv := newDefaultsServer(t)
		resp := post(t, srv.URL+"/u/a/v1/embeddings",
			`{"model":"emb-1","input":"a","dimensions":3,"encoding_format":"base64"}`)
		var body struct {
			Data []struct {
				Embedding string `json:"embedding"`
			} `json:"data"`
		}
		decode(t, resp.Body, &body)
		if len(body.Data) != 1 {
			t.Fatalf("data = %+v", body.Data)
		}
		raw, err := base64.StdEncoding.DecodeString(body.Data[0].Embedding)
		if err != nil || len(raw) != 3*4 {
			t.Fatalf("embedding %q: %v (len %d)", body.Data[0].Embedding, err, len(raw))
		}
		if v := math.Float32frombits(binary.LittleEndian.Uint32(raw)); v == 0 || math.IsNaN(float64(v)) {
			t.Fatalf("first float = %v", v)
		}
	})

	t.Run("decisions", func(t *testing.T) {
		srv := newDefaultsServer(t)
		resp := post(t, srv.URL+"/u/a/v1/decisions", `{"model":"jev","input":"x","questions":[
			{"type":"predicate","name":"p","instructions":"is it?"},
			{"type":"choice","instructions":"which?","choices":[{"value":"red"},{"value":"blue"}]},
			{"type":"score","name":"s","instructions":"how much?","levels":[{"label":"low"},{"label":"high"}]}]}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var body struct {
			Model   string `json:"model"`
			Answers []struct {
				Type        string   `json:"type"`
				Name        *string  `json:"name"`
				Probability *float64 `json:"probability"`
				Choice      any      `json:"choice"`
				Score       *float64 `json:"score"`
			} `json:"answers"`
			Usage struct {
				InputTokens int `json:"input_tokens"`
			} `json:"usage"`
		}
		decode(t, resp.Body, &body)
		if body.Model != "jev" || len(body.Answers) != 3 {
			t.Fatalf("body = %+v", body)
		}
		a := body.Answers
		if a[0].Type != "predicate" || a[0].Name == nil || *a[0].Name != "p" || a[0].Probability == nil {
			t.Fatalf("answer 0 = %+v", a[0])
		}
		if a[1].Type != "choice" || a[1].Name != nil || a[1].Choice != "red" {
			t.Fatalf("answer 1 = %+v", a[1])
		}
		if a[2].Type != "score" || a[2].Score == nil {
			t.Fatalf("answer 2 = %+v", a[2])
		}
	})

	t.Run("systemone", func(t *testing.T) {
		srv := newDefaultsServer(t)
		resp := post(t, srv.URL+"/u/ts-a/v1/systemone", `{"model":"jev","state":"x","questions":{
			"p":{"type":"noul","instructions":"is it?"},
			"c":{"kind":"choice","criteria":{"red":"r","blue":null}},
			"s":{"type":"score","criteria":["low","mid","high"]}}}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var body struct {
			Model   string `json:"model"`
			Answers map[string]struct {
				Type   string   `json:"type"`
				Noul   *float64 `json:"noul"`
				Choice *string  `json:"choice"`
				Score  *float64 `json:"score"`
			} `json:"answers"`
			Usage *struct {
				InputTokens int `json:"input_tokens"`
			} `json:"usage"`
		}
		decode(t, resp.Body, &body)
		if body.Model != "jev" || len(body.Answers) != 3 || body.Usage == nil {
			t.Fatalf("body = %+v", body)
		}
		if p := body.Answers["p"]; p.Type != "noul" || p.Noul == nil || *p.Noul < 0 || *p.Noul > 1 {
			t.Fatalf("p = %+v", p)
		}
		if c := body.Answers["c"]; c.Type != "choice" || c.Choice == nil || (*c.Choice != "red" && *c.Choice != "blue") {
			t.Fatalf("c = %+v", c)
		}
		if s := body.Answers["s"]; s.Type != "score" || s.Score == nil {
			t.Fatalf("s = %+v", s)
		}
	})

	t.Run("models", func(t *testing.T) {
		srv := newDefaultsServer(t)
		code, b := do(t, http.MethodGet, srv.URL+"/u/a/v1/models", "")
		if code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		var body struct {
			Object string `json:"object"`
			Data   []struct {
				ID string `json:"id"`
			} `json:"data"`
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.Unmarshal([]byte(b), &body); err != nil {
			t.Fatal(err)
		}
		if body.Object != "list" || len(body.Data) == 0 || len(body.Models) == 0 {
			t.Fatalf("body = %s", b)
		}
	})

	t.Run("bad json", func(t *testing.T) {
		srv := newDefaultsServer(t)
		for _, p := range []string{"/v1/chat/completions", "/v1/embeddings", "/v1/decisions", "/v1/systemone"} {
			if resp := post(t, srv.URL+"/u/a"+p, `{nope`); resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s: status = %d, want 400", p, resp.StatusCode)
			}
		}
	})

	t.Run("scripted once then default", func(t *testing.T) {
		srv := newDefaultsServer(t)
		errBody := `{"error":{"code":"insufficient_quota"}}`
		putScript(t, srv.URL, "a", control.RouteOpenAIChat, control.Script{Steps: []control.Step{
			{Status: http.StatusTooManyRequests, Body: errBody},
		}})
		url := srv.URL + "/u/a/v1/chat/completions"
		code, body := do(t, http.MethodPost, url, `{"model":"m1"}`)
		if code != http.StatusTooManyRequests || body != errBody {
			t.Fatalf("call 1: %d %q", code, body)
		}
		if code, body := do(t, http.MethodPost, url, `{"model":"m1"}`); code != http.StatusOK {
			t.Fatalf("call 2: %d %q", code, body)
		}
	})

	t.Run("scripted SSE", func(t *testing.T) {
		srv := newDefaultsServer(t)
		entries := []string{
			"{\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"می\u200cشود\"}}]}",
			`{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":" و "}}]}`,
			"{\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"نمی\u200cخواهم\"}}]}",
		}
		putScript(t, srv.URL, "a", control.RouteOpenAIChat, control.Script{Steps: []control.Step{
			{SSE: entries, Headers: map[string]string{"X-Fake": "sse"}},
		}})
		reqBody := "{\"model\":\"m1\",\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":\"" + persian + "\"}]}"
		resp := post(t, srv.URL+"/u/a/v1/chat/completions", reqBody)
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" ||
			resp.Header.Get("X-Fake") != "sse" {
			t.Fatalf("status %d headers %v", resp.StatusCode, resp.Header)
		}

		// Exact framing; TestSSEFlushesEachEvent checks one flush per event.
		br := bufio.NewReader(resp.Body)
		for i, want := range append(entries, "[DONE]") {
			line, err := br.ReadString('\n')
			if err != nil {
				t.Fatalf("event %d: %v", i, err)
			}
			if line != "data: "+want+"\n" {
				t.Fatalf("event %d = %q, want %q", i, line, want)
			}
			if blank, err := br.ReadString('\n'); err != nil || blank != "\n" {
				t.Fatalf("event %d separator = %q, %v", i, blank, err)
			}
		}
		if rest, _ := io.ReadAll(br); len(rest) != 0 {
			t.Fatalf("trailing bytes %q", rest)
		}

		recs := requests(t, srv.URL, "a")
		if len(recs) != 1 || !bytes.Equal(recs[0].Body, []byte(reqBody)) {
			t.Fatalf("recordings = %+v", recs)
		}
	})
}

// flushRecorder counts flushes so the test can see one per SSE event.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (f *flushRecorder) Flush() { f.flushes++; f.ResponseRecorder.Flush() }

func TestSSEFlushesEachEvent(t *testing.T) {
	h := control.New(nil).Handler()
	put := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/_control/upstreams/a/routes/openai.chat",
		strings.NewReader(`{"steps":[{"sse":["1","2","3"]}]}`))
	h.ServeHTTP(httptest.NewRecorder(), put)

	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/u/a/v1/chat/completions", nil))
	if w.flushes < 4 {
		t.Fatalf("flushes = %d, want one per event (4)", w.flushes)
	}
	if got, want := w.Body.String(), "data: 1\n\ndata: 2\n\ndata: 3\n\ndata: [DONE]\n\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestDefaultResponses(t *testing.T) {
	srv := newDefaultsServer(t)
	resp := post(t, srv.URL+"/u/a/v1/responses", `{"model":"m1","input":"hi"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	var got struct {
		ID     string `json:"id"`
		Object string `json:"object"`
		Model  string `json:"model"`
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	decode(t, resp.Body, &got)
	if got.ID != control.DefaultResponseID || got.Object != "response" || got.Model != "m1" || got.Status != "completed" {
		t.Errorf("response %+v: want id %s, object response, model m1, status completed", got, control.DefaultResponseID)
	}
	if len(got.Output) != 1 || got.Output[0].Type != "message" || got.Output[0].Role != "assistant" ||
		len(got.Output[0].Content) != 1 || got.Output[0].Content[0].Type != "output_text" ||
		!bytes.Equal([]byte(got.Output[0].Content[0].Text), []byte(persian)) {
		t.Errorf("output %+v, want one assistant message with the default text", got.Output)
	}
	if got.Usage.TotalTokens != got.Usage.InputTokens+got.Usage.OutputTokens || got.Usage.TotalTokens == 0 {
		t.Errorf("usage %+v", got.Usage)
	}
}

func TestDefaultAuthKey(t *testing.T) {
	srv := newDefaultsServer(t)
	resp, err := http.Get(srv.URL + "/u/a/v1/auth/key") //nolint:noctx // a test against a local server
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	var got struct {
		Data map[string]any `json:"data"`
	}
	decode(t, resp.Body, &got)
	if got.Data == nil {
		t.Error("no data object")
	}
}

func TestQuotaDefaults(t *testing.T) {
	srv := newDefaultsServer(t)
	for _, tt := range []struct{ method, path, want string }{
		{http.MethodPost, "/v1internal:retrieveUserQuotaSummary", control.DefaultQuotaAntigravitySummary},
		{http.MethodPost, "/v1internal:loadCodeAssist", control.DefaultQuotaAntigravityLoad},
		{http.MethodGet, "/manifest/latest-arm64-mac.yml", control.DefaultQuotaAntigravityManifest},
		{http.MethodGet, "/backend-api/wham/usage", control.DefaultQuotaCodexUsage},
		{http.MethodGet, "/api/oauth/usage", control.DefaultQuotaClaudeUsage},
	} {
		req, err := http.NewRequestWithContext(t.Context(), tt.method, srv.URL+"/u/quota"+tt.path, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || string(b) != tt.want {
			t.Errorf("%s %s = %d %s", tt.method, tt.path, resp.StatusCode, b)
		}
	}
}
