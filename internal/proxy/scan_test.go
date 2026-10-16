package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// jsonModelRef and jsonWithModelRef are the decoder-based functions scanBody replaced, kept verbatim as the parity
// oracle for the fuzz tests.
func jsonModelRef(raw []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return "", fmt.Errorf("%w: body is not a JSON object", ErrNoModel)
	}
	var (
		model      string
		modelCount int
		modelErr   error
	)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return "", fmt.Errorf("%w: invalid JSON: %w", ErrNoModel, err)
		}
		key, ok := tok.(string)
		if !ok {
			return "", fmt.Errorf("%w: invalid JSON object key", ErrNoModel)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return "", fmt.Errorf("%w: invalid JSON: %w", ErrNoModel, err)
		}
		switch {
		case strings.EqualFold(key, fallbacksField):
			return "", ErrClientFallbacks
		case strings.EqualFold(key, modelField):
			modelCount++
			if key != modelField {
				modelErr = fmt.Errorf("%w: the field must be spelled %q", ErrNoModel, modelField)
				continue
			}
			if err := json.Unmarshal(value, &model); err != nil {
				modelErr = fmt.Errorf("%w: model is not a string", ErrNoModel)
			}
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return "", fmt.Errorf("%w: invalid JSON object", ErrNoModel)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("%w: data after the JSON object", ErrNoModel)
	}
	switch {
	case modelCount > 1:
		return "", fmt.Errorf("%w: model given more than once", ErrNoModel)
	case modelErr != nil:
		return "", modelErr
	case model == "":
		return "", ErrNoModel
	}
	return model, nil
}

func jsonWithModelRef(raw []byte, model string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("%w: body is not a JSON object", ErrNoModel)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: invalid JSON: %w", ErrNoModel, err)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, fmt.Errorf("%w: invalid JSON: %w", ErrNoModel, err)
		}
		if tok != modelField {
			continue
		}
		// The decoder stops right after the value, and value holds exactly its bytes.
		end := int(dec.InputOffset())
		v, err := json.Marshal(model)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 0, len(raw)-len(value)+len(v))
		out = append(out, raw[:end-len(value)]...)
		out = append(out, v...)
		return append(out, raw[end:]...), nil
	}
	return nil, ErrNoModel
}

func errClass(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrClientFallbacks):
		return ErrClientFallbacks
	case errors.Is(err, ErrNoModel):
		return ErrNoModel
	}
	return err
}

func TestScanObjectSpans(t *testing.T) {
	b := []byte(`{"a":1, "b" : "x\"y" ,"c":{"d":[1,{"e":"]"}]},"f":null}`)
	var keys, vals []string
	ok := scanObject(b, func(k []byte, v span) bool {
		keys = append(keys, string(k))
		vals = append(vals, string(b[v.start:v.end]))
		return true
	})
	if !ok {
		t.Fatal("scanObject failed")
	}
	if want := []string{"a", "b", "c", "f"}; fmt.Sprint(keys) != fmt.Sprint(want) {
		t.Errorf("keys = %q, want %q", keys, want)
	}
	if want := []string{`1`, `"x\"y"`, `{"d":[1,{"e":"]"}]}`, `null`}; fmt.Sprint(vals) != fmt.Sprint(want) {
		t.Errorf("values = %q, want %q", vals, want)
	}
}

func TestScanObjectEscapedKey(t *testing.T) {
	var keys []string
	scanObject([]byte(`{"mod\u0065l":"m"}`), func(k []byte, _ span) bool { keys = append(keys, string(k)); return true })
	if len(keys) != 1 || keys[0] != "model" {
		t.Fatalf("keys = %q, want [model]", keys)
	}
}

func TestScanObjectRejects(t *testing.T) {
	for _, in := range []string{``, `[]`, `{`, `{"a"}`, `{"a":}`, `{"a":"x`, `{"a":1,}`, `{"a":1 "b":2}`, `{"a\`, `{"a":[1,2}`} {
		if scanObject([]byte(in), func([]byte, span) bool { return true }) {
			t.Errorf("scanObject(%q) = true, want false", in)
		}
	}
}

func FuzzScanObject(f *testing.F) {
	for _, s := range []string{`{"a":1}`, `{"a":{"b":[1,"]",{"c":"\\"}]}}`, `{`, `{"a":"\`, ` {"x" : [ ] } `} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		ok := scanObject(b, func(_ []byte, v span) bool {
			if v.start < 0 || v.end > len(b) || v.start >= v.end {
				t.Fatalf("span %+v out of %d bytes", v, len(b))
			}
			if json.Valid(b) && !json.Valid(b[v.start:v.end]) {
				t.Fatalf("span %q is not a JSON value", b[v.start:v.end])
			}
			return true
		})
		if json.Valid(b) && bytes.HasPrefix(bytes.TrimLeft(b, " \t\r\n"), []byte("{")) && !ok {
			t.Fatalf("scanObject rejected a valid object %q", b)
		}
	})
}

func scanSeeds(f *testing.F) {
	for _, s := range []string{
		`{"model":"fast","messages":[]}`, `{"model":"memory/fast"}`, `{"mod\u0065l":"fast"}`, `{"messages":[]}`,
		`{"model":7}`, `{"model":null}`, `{"model":""}`, `{"Model":"fast"}`, `{"model":"fast","model":"other"}`,
		`{"model":"fast","MODEL":"other"}`, `{"model":"fast","fallbacks":["x"]}`, `{"model":"fast","Fallbacks":[]}`,
		`{"fallbacks":null,"model":"fast"}`, `{"model":"fast","metadata":{"fallbacks":1}}`, `model=fast`,
		`[{"model":"fast"}]`, `{"model":"fast"}{"model":"other"}`, `{"model":"fast",`, ``, `{"model":"m"}`,
		`{"fallbacks":1,"model":"m"}`, `{"model":1}`, "{\"model\":\"m\",\"s\":\"\u0645\u06cc\u200c\"}",
	} {
		f.Add([]byte(s))
	}
}

func FuzzScanBodyParity(f *testing.F) {
	scanSeeds(f)
	f.Fuzz(func(t *testing.T, raw []byte) {
		if !json.Valid(raw) {
			return // the one accepted divergence: both refuse, maybe with another error class
		}
		gotModel, sp, gotErr := scanBody(raw)
		wantModel, wantErr := jsonModelRef(raw)
		if gotModel != wantModel || !errors.Is(errClass(gotErr), errClass(wantErr)) {
			t.Fatalf("scanBody(%q) = %q, %v; want %q, %v", raw, gotModel, gotErr, wantModel, wantErr)
		}
		if gotErr != nil {
			return
		}
		got, err := withModel(raw, sp, "application/json", "remora-alias/x\u200cy")
		want, werr := jsonWithModelRef(raw, "remora-alias/x\u200cy")
		if !bytes.Equal(got, want) || (err == nil) != (werr == nil) {
			t.Fatalf("withModel(%q) = %q, %v; want %q, %v", raw, got, err, want, werr)
		}
	})
}

func TestBodySpansFirstUser(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"messages", `{"model":"m","messages":[{"role":"system","content":"s"},{"role":"developer","content":"d"},{"role":"user","content":"u1"},{"role":"user","content":"u2"}]}`, `{"role":"user","content":"u1"}`},
		{"input array", `{"model":"m","input":[{"role":"system","content":"s"},{"content":"u1","role":"user"}]}`, `{"content":"u1","role":"user"}`},
		{"no user", `{"model":"m","messages":[{"role":"system","content":"s"}]}`, ``},
		{"role case", `{"model":"m","messages":[{"role":"USER","content":"u"}]}`, ``},
		{"input string", `{"model":"m","input":"hi"}`, ``},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw := []byte(c.body)
			_, sp, err := scanBody(raw)
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if sp.firstUser.end > 0 {
				got = string(raw[sp.firstUser.start:sp.firstUser.end])
			}
			if got != c.want {
				t.Fatalf("firstUser = %q, want %q", got, c.want)
			}
		})
	}
}

func TestWithModelSplice(t *testing.T) {
	in := []byte("{\"a\":[1,{\"model\":\"inner\"}],\n  \"model\"\t:\"openai/fast\" ,\"s\":\"\u0645\u06cc\u200c\u0634\u0648\u062f\"}")
	_, sp, err := scanBody(in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := withModel(in, sp, "application/json", "remora-alias/openai/fast")
	want, _ := jsonWithModelRef(in, "remora-alias/openai/fast")
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("withModel = %q, %v; want %q", got, err, want)
	}
}

func TestExtractModelPresized(t *testing.T) {
	body := benchChatBody(100)
	allocs := testing.AllocsPerRun(20, func() {
		r, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		if _, _, _, err := extractModel(r); err != nil {
			t.Fatal(err)
		}
	})
	// The request itself allocates a handful; the body read must be one buffer, not a doubling series.
	if allocs > 20 {
		t.Errorf("allocs = %v, want a presized read", allocs)
	}
	r, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/chat/completions", io.NopCloser(bytes.NewReader(body)))
	r.ContentLength = -1
	if m, _, _, err := extractModel(r); err != nil || m != "alias-3" {
		t.Fatalf("unknown length: %q, %v", m, err)
	}
	r, _ = http.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	r.ContentLength = MaxBodyBytes + 1
	if _, _, _, err := extractModel(r); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("declared too large: %v", err)
	}
}
