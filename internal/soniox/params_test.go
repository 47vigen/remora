package soniox

import (
	"errors"
	"mime/multipart"
	"reflect"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func form(values map[string][]string) *multipart.Form {
	return &multipart.Form{Value: values}
}

func TestParseParams(t *testing.T) {
	cases := []struct {
		name   string
		values map[string][]string
		want   request
	}{
		{
			"diarization and hints",
			map[string][]string{"model": {"stt-async-v5"}, "enable_speaker_diarization": {"True"}, "language": {"fa"}, "language_hints": {"en, fa"}},
			request{Create: CreateRequest{Model: "stt-async-v5", EnableSpeakerDiarization: ptr(true), LanguageHints: []string{"en", "fa"}}, ResponseFormat: "json", Language: "fa"},
		},
		{
			"json array hints",
			map[string][]string{"model": {"m"}, "language_hints": {`["fa","en"]`}},
			request{Create: CreateRequest{Model: "m", LanguageHints: []string{"fa", "en"}}, ResponseFormat: "json"},
		},
		{
			"language prepended",
			map[string][]string{"model": {"m"}, "language": {"de"}, "language_hints": {"fa"}},
			request{Create: CreateRequest{Model: "m", LanguageHints: []string{"de", "fa"}}, ResponseFormat: "json", Language: "de"},
		},
		{
			"language alone becomes a hint",
			map[string][]string{"model": {"m"}, "language": {"fa"}},
			request{Create: CreateRequest{Model: "m", LanguageHints: []string{"fa"}}, ResponseFormat: "json", Language: "fa"},
		},
		{
			"prompt becomes context string",
			map[string][]string{"model": {"m"}, "prompt": {"cardiology"}},
			request{Create: CreateRequest{Model: "m", Context: []byte(`"cardiology"`)}, ResponseFormat: "json"},
		},
		{
			"context object",
			map[string][]string{"model": {"m"}, "context": {`{"terms":["Soniox"]}`}},
			request{Create: CreateRequest{Model: "m", Context: []byte(`{"terms":["Soniox"]}`)}, ResponseFormat: "json"},
		},
		{
			"context text",
			map[string][]string{"model": {"m"}, "context": {"heart surgery"}},
			request{Create: CreateRequest{Model: "m", Context: []byte(`"heart surgery"`)}, ResponseFormat: "json"},
		},
		{
			"verbose with granularities",
			map[string][]string{"model": {"m"}, "response_format": {"verbose_json"}, "timestamp_granularities[]": {"segment", "word"}},
			request{Create: CreateRequest{Model: "m"}, ResponseFormat: "verbose_json"},
		},
		{
			"flags, translation and stream false",
			map[string][]string{
				"model": {"m"}, "language_hints_strict": {"1"}, "enable_language_identification": {"FALSE"},
				"translation": {`{"type":"one_way","target_language":"en"}`}, "stream": {"false"}, "response_format": {"text"},
				"timestamp_granularities": {"word"},
			},
			request{Create: CreateRequest{
				Model: "m", LanguageHintsStrict: ptr(true), EnableLanguageIdentification: ptr(false),
				Translation: []byte(`{"type":"one_way","target_language":"en"}`),
			}, ResponseFormat: "text"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseParams(form(tc.values))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		values map[string][]string
		field  string
	}{
		{map[string][]string{"model": {"m"}, "foo": {"1"}}, "foo"},
		{map[string][]string{"foo": {"1"}}, "foo"},
		{map[string][]string{"model": {"m"}, "temperature": {"0"}}, "temperature"},
		{map[string][]string{"model": {"m"}, "enable_speaker_diarization": {"yes"}}, "enable_speaker_diarization"},
		{map[string][]string{"model": {"m"}, "context": {"{bad"}}, "context"},
		{map[string][]string{"model": {"m"}, "translation": {"x"}}, "translation"},
		{map[string][]string{"model": {"m"}, "translation": {`["x"]`}}, "translation"},
		{map[string][]string{"model": {"m"}, "prompt": {"a"}, "context": {"b"}}, "prompt"},
		{map[string][]string{"model": {"m"}, "webhook_auth_header_value": {"s"}}, "webhook_auth_header_value"},
		{map[string][]string{"model": {"m"}, "client_reference_id": {"x"}}, "client_reference_id"},
		{map[string][]string{"model": {"m"}, "file_id": {"x"}}, "file_id"},
		{map[string][]string{"model": {"m"}, "stream": {"true"}}, "stream"},
		{map[string][]string{"model": {"m"}, "timestamp_granularities[]": {"char"}}, "timestamp_granularities[]"},
		{map[string][]string{"model": {"m"}, "language_hints": {`["fa",1]`}}, "language_hints"},
		{map[string][]string{"model": {"m", "n"}}, "model"},
		{map[string][]string{"language": {"fa"}}, "model"},
		{map[string][]string{"model": {""}}, "model"},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			_, err := parseParams(form(tc.values))
			var pe *ParamError
			if !errors.As(err, &pe) {
				t.Fatalf("err = %v, want *ParamError", err)
			}
			if pe.Field != tc.field {
				t.Fatalf("Field = %q, want %q (err %v)", pe.Field, tc.field, err)
			}
		})
	}
}

func TestParseRejectsResponseFormat(t *testing.T) {
	for _, f := range []string{"srt", "vtt", "diarized_json"} {
		_, err := parseParams(form(map[string][]string{"model": {"m"}, "response_format": {f}}))
		var pe *ParamError
		if !errors.As(err, &pe) || pe.Field != "response_format" {
			t.Fatalf("%s: err = %v", f, err)
		}
		if want := `invalid parameter "response_format": supported: json, verbose_json, text`; pe.Error() != want {
			t.Fatalf("%s: Error() = %q", f, pe.Error())
		}
	}
}

func TestParamErrorMessages(t *testing.T) {
	if got := (&ParamError{Field: "foo", Reason: "unsupported"}).Error(); got != `unsupported parameter "foo"` {
		t.Fatalf("got %q", got)
	}
	if got := (&ParamError{Field: "x", Reason: "bad"}).Error(); got != `invalid parameter "x": bad` {
		t.Fatalf("got %q", got)
	}
}
