package control

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
)

// DefaultChatContent is the assistant text of the default chat answer, streamed
// or not. It holds two zero-width non-joiners (U+200C) so byte-for-byte
// fidelity is tested even without a script.
const DefaultChatContent = "می\u200cشود و نمی\u200cخواهم"

// DefaultChatJSONContent is the default chat answer when the request asks for
// a JSON response_format.
const DefaultChatJSONContent = `{"answer":"` + DefaultChatContent + `"}`

// DefaultEmbeddingDimensions is the vector length when a request sets no
// dimensions.
const DefaultEmbeddingDimensions = 8

// Fixed values of the default answers, so tests can assert on them.
const (
	DefaultChatID      = "chatcmpl-fake"
	DefaultResponseID  = "resp_fake"
	DefaultCreated     = 1700000000
	DefaultNoul        = 0.75
	defaultPromptToks  = 3
	defaultOutputToks  = 5
	defaultInputTokens = 7
)

// defaultModels are the ids the default GET /v1/models lists.
var defaultModels = []string{"m1", "emb-1", "jev"}

// Defaults returns the default answer of every route this plan serves: OpenAI
// chat (plain and streamed), Responses, embeddings and decisions, TypeSafe System One and
// the model listing. Each echoes the requested model.
func Defaults() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		RouteOpenAIChat:        defaultChat,
		RouteOpenAIEmbeddings:  defaultEmbeddings,
		RouteOpenAIDecisions:   defaultDecisions,
		RouteOpenAIResponses:   defaultResponses,
		RouteTypeSafeSystemOne: defaultSystemOne,
		RouteModels:            defaultModelList,
		RouteOpenRouterAuthKey: defaultAuthKey,
	}
}

type chatRequest struct {
	Model          string `json:"model"`
	Stream         bool   `json:"stream"`
	ResponseFormat *struct {
		Type string `json:"type"`
	} `json:"response_format"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

var defaultUsage = usage{
	PromptTokens:     defaultPromptToks,
	CompletionTokens: defaultOutputToks,
	TotalTokens:      defaultPromptToks + defaultOutputToks,
}

func defaultChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if !decodeBody(w, r, &req, writeError) {
		return
	}
	content := DefaultChatContent
	if f := req.ResponseFormat; f != nil && (f.Type == "json_schema" || f.Type == "json_object") {
		content = DefaultChatJSONContent
	}
	if req.Stream {
		streamChat(w, r, req.Model, content)
		return
	}
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type choice struct {
		Index        int     `json:"index"`
		Message      message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	}
	writeJSON(w, http.StatusOK, struct {
		ID      string   `json:"id"`
		Object  string   `json:"object"`
		Created int64    `json:"created"`
		Model   string   `json:"model"`
		Choices []choice `json:"choices"`
		Usage   usage    `json:"usage"`
	}{
		ID:      DefaultChatID,
		Object:  "chat.completion",
		Created: DefaultCreated,
		Model:   req.Model,
		Choices: []choice{{Message: message{Role: "assistant", Content: content}, FinishReason: "stop"}},
		Usage:   defaultUsage,
	})
}

// defaultResponses answers a non-streamed OpenAI Responses request with one
// assistant message holding DefaultChatContent.
func defaultResponses(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model string `json:"model"`
	}
	if !decodeBody(w, r, &req, writeError) {
		return
	}
	type part struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Annotations []any  `json:"annotations"`
	}
	type item struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Status  string `json:"status"`
		Role    string `json:"role"`
		Content []part `json:"content"`
	}
	type respUsage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	}
	writeJSON(w, http.StatusOK, struct {
		ID        string    `json:"id"`
		Object    string    `json:"object"`
		CreatedAt int64     `json:"created_at"`
		Status    string    `json:"status"`
		Model     string    `json:"model"`
		Output    []item    `json:"output"`
		Usage     respUsage `json:"usage"`
	}{
		ID: DefaultResponseID, Object: "response", CreatedAt: DefaultCreated, Status: "completed", Model: req.Model,
		Output: []item{{
			ID: "msg_fake", Type: "message", Status: "completed", Role: "assistant",
			Content: []part{{Type: "output_text", Text: DefaultChatContent, Annotations: []any{}}},
		}},
		Usage: respUsage{InputTokens: defaultPromptToks, OutputTokens: defaultOutputToks, TotalTokens: defaultPromptToks + defaultOutputToks},
	})
}

// defaultAuthKey answers OpenRouter's key check, which the routing engine calls before it lists an OpenRouter
// provider's models: any key is valid.
func defaultAuthKey(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"label": "fake", "usage": 0, "limit": nil}})
}

// streamChat answers as OpenAI streams: a role chunk, the content in three
// pieces, a finish chunk, then a usage chunk with no choices, all with one id.
func streamChat(w http.ResponseWriter, r *http.Request, model, content string) {
	type delta struct {
		Role    string `json:"role,omitempty"`
		Content string `json:"content,omitempty"`
	}
	type choice struct {
		Index        int     `json:"index"`
		Delta        delta   `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	}
	type chunk struct {
		ID      string   `json:"id"`
		Object  string   `json:"object"`
		Created int64    `json:"created"`
		Model   string   `json:"model"`
		Choices []choice `json:"choices"`
		Usage   *usage   `json:"usage,omitempty"`
	}
	mk := func(choices []choice, u *usage) string {
		b, err := json.Marshal(chunk{
			ID: DefaultChatID, Object: "chat.completion.chunk", Created: DefaultCreated,
			Model: model, Choices: choices, Usage: u,
		})
		if err != nil {
			panic(err) // only plain types: cannot fail
		}
		return string(b)
	}
	stop := "stop"
	events := []string{mk([]choice{{Delta: delta{Role: "assistant"}}}, nil)}
	for _, part := range splitRunes(content, 3) {
		events = append(events, mk([]choice{{Delta: delta{Content: part}}}, nil))
	}
	u := defaultUsage
	events = append(events,
		mk([]choice{{FinishReason: &stop}}, nil),
		mk([]choice{}, &u),
	)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	writeSSE(w, r, events, 0)
}

// splitRunes cuts s into at most n non-empty pieces on rune boundaries.
func splitRunes(s string, n int) []string {
	var starts []int
	for i := range s {
		starts = append(starts, i)
	}
	total := len(starts)
	if total == 0 {
		return nil
	}
	n = min(n, total)
	out := make([]string, 0, n)
	for k := range n {
		from, to := starts[k*total/n], len(s)
		if k < n-1 {
			to = starts[(k+1)*total/n]
		}
		out = append(out, s[from:to])
	}
	return out
}

type embeddingsRequest struct {
	Model          string          `json:"model"`
	Input          json.RawMessage `json:"input"`
	Dimensions     int             `json:"dimensions"`
	EncodingFormat string          `json:"encoding_format"`
}

// inputCount is the number of embeddings an OpenAI input asks for: one for a
// string or a token array, one per element for an array of strings or of
// token arrays.
func inputCount(raw json.RawMessage) (int, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return 1, nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return 0, fmt.Errorf("input must be a string or an array")
	}
	if len(arr) == 0 {
		return 0, fmt.Errorf("input must not be empty")
	}
	var n float64
	if json.Unmarshal(arr[0], &n) == nil {
		return 1, nil // one token array
	}
	return len(arr), nil
}

func defaultEmbeddings(w http.ResponseWriter, r *http.Request) {
	var req embeddingsRequest
	if !decodeBody(w, r, &req, writeError) {
		return
	}
	n, err := inputCount(req.Input)
	if err != nil {
		writeError(w, http.StatusBadRequest, "fake: "+err.Error())
		return
	}
	dims := req.Dimensions
	if dims <= 0 {
		dims = DefaultEmbeddingDimensions
	}
	type item struct {
		Object    string `json:"object"`
		Index     int    `json:"index"`
		Embedding any    `json:"embedding"`
	}
	data := make([]item, n)
	for i := range data {
		vec := Embedding(i, dims)
		var emb any = vec
		if req.EncodingFormat == "base64" {
			buf := make([]byte, 4*len(vec))
			for j, v := range vec {
				binary.LittleEndian.PutUint32(buf[4*j:], math.Float32bits(v))
			}
			emb = base64.StdEncoding.EncodeToString(buf)
		}
		data[i] = item{Object: "embedding", Index: i, Embedding: emb}
	}
	writeJSON(w, http.StatusOK, struct {
		Object string `json:"object"`
		Data   []item `json:"data"`
		Model  string `json:"model"`
		Usage  struct {
			PromptTokens int `json:"prompt_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}{
		Object: "list", Data: data, Model: req.Model,
		Usage: struct {
			PromptTokens int `json:"prompt_tokens"`
			TotalTokens  int `json:"total_tokens"`
		}{PromptTokens: n, TotalTokens: n},
	})
}

// Embedding is the default vector for the input at index: deterministic,
// non-zero, and exact in float32.
func Embedding(index, dims int) []float32 {
	vec := make([]float32, dims)
	for j := range vec {
		vec[j] = float32(index+1) + float32(j+1)/8
	}
	return vec
}

type decisionsRequest struct {
	Model     string `json:"model"`
	Questions []struct {
		Type    string  `json:"type"`
		Name    *string `json:"name"`
		Choices []struct {
			Value json.RawMessage `json:"value"`
		} `json:"choices"`
	} `json:"questions"`
}

// defaultDecisions answers OpenAI's POST /v1/decisions in question order: a
// predicate with DefaultNoul, a choice with its first value, a score of 0.
func defaultDecisions(w http.ResponseWriter, r *http.Request) {
	var req decisionsRequest
	if !decodeBody(w, r, &req, writeError) {
		return
	}
	type answer struct {
		Type        string          `json:"type"`
		Name        *string         `json:"name"`
		Probability *float64        `json:"probability,omitempty"`
		Choice      json.RawMessage `json:"choice,omitempty"`
		Score       *float64        `json:"score,omitempty"`
		Confidence  float64         `json:"confidence"`
	}
	answers := make([]answer, 0, len(req.Questions))
	for i, q := range req.Questions {
		a := answer{Type: q.Type, Name: q.Name, Confidence: DefaultNoul}
		switch q.Type {
		case "predicate":
			p := DefaultNoul
			a.Probability = &p
		case "choice":
			if len(q.Choices) == 0 {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("fake: choice question %d has no choices", i))
				return
			}
			a.Choice = q.Choices[0].Value
		case "score":
			s := 0.0
			a.Score = &s
		default:
			writeError(w, http.StatusBadRequest, fmt.Sprintf("fake: question %d has unsupported type %q", i, q.Type))
			return
		}
		answers = append(answers, a)
	}
	type usageT struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	}
	writeJSON(w, http.StatusOK, struct {
		Model   string   `json:"model"`
		Answers []answer `json:"answers"`
		Usage   usageT   `json:"usage"`
	}{
		Model: req.Model, Answers: answers,
		Usage: usageT{InputTokens: defaultInputTokens, TotalTokens: defaultInputTokens},
	})
}

type systemOneQuestion struct {
	Type     string          `json:"type"`
	Kind     string          `json:"kind"`
	Criteria json.RawMessage `json:"criteria"`
}

type systemOneAnswer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        *string            `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    float64            `json:"confidence"`
}

// defaultSystemOne answers TypeSafe's POST /v1/systemone: noul with
// DefaultNoul, choice with the alphabetically first option, score 0.
func defaultSystemOne(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model     string                       `json:"model"`
		Questions map[string]systemOneQuestion `json:"questions"`
	}
	if !decodeBody(w, r, &req, writeTypeSafeError) {
		return
	}
	if len(req.Questions) == 0 {
		writeTypeSafeError(w, http.StatusUnprocessableEntity, "fake: questions must not be empty")
		return
	}
	answers := make(map[string]systemOneAnswer, len(req.Questions))
	for name, q := range req.Questions {
		a, err := systemOneAnswerFor(q)
		if err != nil {
			writeTypeSafeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("fake: question %q: %v", name, err))
			return
		}
		answers[name] = a
	}
	type usageT struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	}
	writeJSON(w, http.StatusOK, struct {
		Model   string                     `json:"model"`
		Answers map[string]systemOneAnswer `json:"answers"`
		Usage   usageT                     `json:"usage"`
	}{Model: req.Model, Answers: answers, Usage: usageT{InputTokens: defaultInputTokens}})
}

func systemOneAnswerFor(q systemOneQuestion) (systemOneAnswer, error) {
	typ := q.Type
	if typ == "" {
		typ = q.Kind
	}
	a := systemOneAnswer{Type: typ, Confidence: DefaultNoul}
	switch typ {
	case "noul":
		p := DefaultNoul
		a.Noul = &p
		a.Probabilities = map[string]float64{"true": DefaultNoul, "false": 1 - DefaultNoul}
	case "choice":
		var options map[string]json.RawMessage
		if err := json.Unmarshal(q.Criteria, &options); err != nil || len(options) == 0 {
			return a, fmt.Errorf("choice needs a criteria map of options")
		}
		keys := make([]string, 0, len(options))
		for k := range options {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		a.Choice = &keys[0]
		a.Probabilities = map[string]float64{keys[0]: 1}
	case "score":
		var levels []json.RawMessage
		if err := json.Unmarshal(q.Criteria, &levels); err != nil || len(levels) == 0 {
			return a, fmt.Errorf("score needs a criteria array of levels")
		}
		s := 0.0
		a.Score = &s
		a.Probabilities = map[string]float64{"0": 1}
	default:
		return a, fmt.Errorf("unsupported type %q", typ)
	}
	return a, nil
}

// defaultModelList answers GET /v1/models in both OpenAI's shape (data) and
// TypeSafe's native shape (models), so either provider kind can list it.
func defaultModelList(w http.ResponseWriter, _ *http.Request) {
	type openAIModel struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}
	type nativeModel struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	data := make([]openAIModel, len(defaultModels))
	models := make([]nativeModel, len(defaultModels))
	for i, id := range defaultModels {
		data[i] = openAIModel{ID: id, Object: "model", Created: DefaultCreated, OwnedBy: "fake"}
		models[i] = nativeModel{Name: id, Description: "fake model"}
	}
	writeJSON(w, http.StatusOK, struct {
		Object string        `json:"object"`
		Data   []openAIModel `json:"data"`
		Models []nativeModel `json:"models"`
	}{Object: "list", Data: data, Models: models})
}

// decodeBody decodes the JSON request body into v, answering 400 through
// fail when it is not valid JSON.
func decodeBody(w http.ResponseWriter, r *http.Request, v any, fail func(http.ResponseWriter, int, string)) bool {
	b, err := io.ReadAll(r.Body)
	if err == nil {
		err = json.Unmarshal(b, v)
	}
	if err != nil {
		fail(w, http.StatusBadRequest, fmt.Sprintf("fake: decode body: %v", err))
		return false
	}
	return true
}

// writeTypeSafeError answers in TypeSafe's native error shape.
func writeTypeSafeError(w http.ResponseWriter, status int, msg string) {
	type detail struct {
		ErrorType string `json:"error_type"`
		Message   string `json:"message"`
	}
	writeJSON(w, status, struct {
		Detail detail `json:"detail"`
	}{Detail: detail{ErrorType: "invalid_request", Message: msg}})
}
