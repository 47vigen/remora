package soniox

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func tok(text string, s, e int64, spk string) Token {
	return Token{Text: text, StartMs: s, EndMs: e, Speaker: spk}
}

var defaultGrouping = Grouping{Pause: time.Second, MaxSegment: 30 * time.Second}

func TestGroupSegmentsBySpeaker(t *testing.T) {
	toks := []Token{tok("Hel", 0, 100, "1"), tok("lo", 100, 200, "1"), tok(" there", 250, 500, "1"), tok(" Hi", 600, 800, "2"), tok("!", 800, 850, "2")}
	got := groupSegments(toks, defaultGrouping)
	want := []Segment{{ID: 0, Speaker: "1", Start: 0, End: 0.5, Text: "Hello there"}, {ID: 1, Speaker: "2", Start: 0.6, End: 0.85, Text: "Hi!"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestGroupSegmentsSplitsOnPause(t *testing.T) {
	got := groupSegments([]Token{tok(" a", 0, 100, "1"), tok(" b", 1200, 1300, "1")}, defaultGrouping)
	if len(got) != 2 || got[0].Text != "a" || got[1].Text != "b" || got[1].Start != 1.2 {
		t.Fatalf("1100ms gap: %+v", got)
	}
	got = groupSegments([]Token{tok(" a", 0, 100, "1"), tok(" b", 1099, 1200, "1")}, defaultGrouping)
	if len(got) != 1 || got[0].Text != "a b" {
		t.Fatalf("999ms gap: %+v", got)
	}
}

func TestGroupSegmentsPauseNeverCutsWord(t *testing.T) {
	got := groupSegments([]Token{tok("wor", 0, 100, ""), tok("ld", 1500, 1600, "")}, defaultGrouping)
	if len(got) != 1 || got[0].Text != "world" || got[0].End != 1.6 {
		t.Fatalf("got %+v", got)
	}
}

func TestGroupSegmentsMaxLength(t *testing.T) {
	var toks []Token
	for i := range int64(40) {
		toks = append(toks, tok(" w", i*900, i*900+100, "1"))
	}
	got := groupSegments(toks, defaultGrouping)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(got), got)
	}
	for _, s := range got {
		if s.End-s.Start > 30 {
			t.Fatalf("segment %d spans %.1fs", s.ID, s.End-s.Start)
		}
	}
}

func TestGroupSegmentsNoSpeaker(t *testing.T) {
	toks := []Token{tok(" a", 0, 100, ""), tok(" b", 200, 300, ""), tok(" c", 2000, 2100, "")}
	got := groupSegments(toks, defaultGrouping)
	if len(got) != 2 || got[0].Text != "a b" || got[1].Text != "c" {
		t.Fatalf("got %+v", got)
	}
	b, err := json.Marshal(got[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "speaker") {
		t.Fatalf("segment JSON has a speaker key: %s", b)
	}
}

func TestGroupSegmentsSkipsTranslation(t *testing.T) {
	toks := []Token{
		tok(" Hallo", 0, 100, "1"),
		{Text: " Hello", Speaker: "1", TranslationStatus: "translation"},
		tok(" Welt", 100, 200, "1"),
		{Text: " world", Speaker: "1", TranslationStatus: "translation"},
	}
	got := groupSegments(toks, defaultGrouping)
	if len(got) != 1 || got[0].Text != "Hallo Welt" || got[0].Start != 0 || got[0].End != 0.2 {
		t.Fatalf("got %+v", got)
	}
	if w := groupWords(toks); len(w) != 2 || w[1].Word != "Welt" {
		t.Fatalf("words %+v", w)
	}
}

func TestGroupSegmentsKeepsZWNJ(t *testing.T) {
	toks := []Token{tok(" می", 0, 100, "1"), tok("\u200cخو", 100, 200, "1"), tok("اهم", 200, 300, "1")}
	want := []byte("می\u200cخواهم")
	if got := groupSegments(toks, defaultGrouping); len(got) != 1 || !bytes.Equal([]byte(got[0].Text), want) {
		t.Fatalf("segments %+v", got)
	}
	if got := groupWords(toks); len(got) != 1 || !bytes.Equal([]byte(got[0].Word), want) {
		t.Fatalf("words %+v", got)
	}
}

func TestGroupSegmentsIDsContiguous(t *testing.T) {
	toks := []Token{tok(" a", 0, 100, "1"), tok(" ", 100, 150, "2"), tok(" b", 200, 300, "3")}
	got := groupSegments(toks, defaultGrouping)
	if len(got) != 2 || got[0].ID != 0 || got[1].ID != 1 || got[1].Speaker != "3" {
		t.Fatalf("got %+v", got)
	}
}

func TestGroupWords(t *testing.T) {
	toks := []Token{tok("Hel", 0, 100, "1"), tok("lo", 100, 200, "1"), tok(" there", 250, 500, "1"), tok(",", 500, 510, "1"), tok(" Hi", 600, 800, "2")}
	want := []Word{
		{Word: "Hello", Start: 0, End: 0.2, Speaker: "1"},
		{Word: "there,", Start: 0.25, End: 0.51, Speaker: "1"},
		{Word: "Hi", Start: 0.6, End: 0.8, Speaker: "2"},
	}
	if got := groupWords(toks); !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	// A speaker change in the middle of a run starts a new word.
	got := groupWords([]Token{tok(" ab", 0, 100, "1"), tok("cd", 100, 200, "2")})
	if len(got) != 2 || got[0].Word != "ab" || got[1].Word != "cd" || got[1].Speaker != "2" {
		t.Fatalf("speaker change: %+v", got)
	}
}

func TestPickLanguage(t *testing.T) {
	lang := func(ls ...string) []Token {
		var out []Token
		for _, l := range ls {
			out = append(out, Token{Text: "x", Language: l})
		}
		return out
	}
	cases := []struct {
		tokens    []Token
		requested string
		hints     []string
		want      string
	}{
		{lang("fa", "fa", "en"), "", nil, "fa"},
		{lang("en", "fa"), "", nil, "en"},
		{lang("", "fa", "en", "en", "fa"), "", nil, "fa"},
		{nil, "de", []string{"fr"}, "de"},
		{lang(""), "", []string{"fr"}, "fr"},
		{nil, "", nil, ""},
	}
	for i, tc := range cases {
		if got := pickLanguage(tc.tokens, tc.requested, tc.hints); got != tc.want {
			t.Fatalf("case %d: got %q, want %q", i, got, tc.want)
		}
	}
}

func TestBuildVerbose(t *testing.T) {
	ts := Transcript{Text: " Hello there ", Tokens: []Token{
		{Text: "Hello", StartMs: 0, EndMs: 300, Speaker: "1", Language: "en"},
		{Text: " there", StartMs: 400, EndMs: 900, Speaker: "1", Language: "en"},
	}}
	v := buildVerbose(Transcription{}, ts, request{}, defaultGrouping)
	if v.Task != "transcribe" || v.Text != " Hello there " || v.Language != "en" || v.Duration != 0.9 {
		t.Fatalf("verbose %+v", v)
	}
	if v.Usage != (Usage{Type: "duration", Seconds: 0.9}) || len(v.Words) != 2 || len(v.Segments) != 1 {
		t.Fatalf("verbose %+v", v)
	}
}

func TestBuildVerboseEmpty(t *testing.T) {
	v := buildVerbose(Transcription{AudioDurationMs: 2500}, Transcript{}, request{Language: "fa"}, defaultGrouping)
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"words":[]`) || !strings.Contains(s, `"segments":[]`) {
		t.Fatalf("json %s", s)
	}
	if v.Duration != 2.5 || v.Language != "fa" || v.Usage.Seconds != 2.5 {
		t.Fatalf("verbose %+v", v)
	}
}
