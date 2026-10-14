package soniox

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Word is one word of the OpenAI verbose_json answer.
type Word struct {
	Word    string  `json:"word"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker,omitempty"`
}

// Segment is one speaker segment of the OpenAI verbose_json answer.
type Segment struct {
	ID      int     `json:"id"`
	Speaker string  `json:"speaker,omitempty"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
}

// Usage is the billed audio duration.
type Usage struct {
	Type    string  `json:"type"` // "duration"
	Seconds float64 `json:"seconds"`
}

// VerboseJSON is the OpenAI verbose_json transcription answer.
type VerboseJSON struct {
	Task     string    `json:"task"` // "transcribe"
	Language string    `json:"language,omitempty"`
	Duration float64   `json:"duration"`
	Text     string    `json:"text"`
	Words    []Word    `json:"words"`
	Segments []Segment `json:"segments"`
	Usage    Usage     `json:"usage"`
}

// Grouping bounds segments: a pause of at least Pause or a segment longer
// than MaxSegment starts a new one, but only at a word boundary.
type Grouping struct {
	Pause      time.Duration // default 1s
	MaxSegment time.Duration // default 30s
}

// translationStatus marks a token that is a translation, without audio timing.
const translationStatus = "translation"

// sourceTokens drops translated tokens; they carry no audio timing.
func sourceTokens(tokens []Token) []Token {
	out := make([]Token, 0, len(tokens))
	for _, t := range tokens {
		if t.TranslationStatus != translationStatus {
			out = append(out, t)
		}
	}
	return out
}

// startsWord reports whether a token opens a new word: Soniox puts the space
// before a word at the start of its first token.
func startsWord(t Token) bool {
	r, _ := utf8.DecodeRuneInString(t.Text)
	return t.Text != "" && unicode.IsSpace(r)
}

func seconds(ms int64) float64 { return float64(ms) / 1000 }

// groupWords joins sub-word tokens into words. A new word starts at the first
// token, at a token with leading whitespace, or when the speaker changes.
// Token text is concatenated byte for byte; only surrounding whitespace is
// trimmed, so a ZWNJ inside a word survives.
func groupWords(tokens []Token) []Word {
	out := []Word{}
	var (
		b       strings.Builder
		cur     Word
		open    bool
		startMs int64
		endMs   int64
	)
	closeWord := func() {
		if !open {
			return
		}
		if text := strings.TrimSpace(b.String()); text != "" {
			cur.Word = text
			cur.Start, cur.End = seconds(startMs), seconds(endMs)
			out = append(out, cur)
		}
		b.Reset()
		open = false
	}
	for _, t := range sourceTokens(tokens) {
		if !open || startsWord(t) || t.Speaker != cur.Speaker {
			closeWord()
			cur = Word{Speaker: t.Speaker}
			startMs = t.StartMs
			open = true
		}
		b.WriteString(t.Text)
		endMs = t.EndMs
	}
	closeWord()
	return out
}

// groupSegments groups tokens into segments. A speaker change always splits;
// a pause of at least g.Pause or a segment that would grow past g.MaxSegment
// splits only at a word boundary, so a word is never cut. Whitespace-only
// segments are dropped and IDs stay contiguous.
func groupSegments(tokens []Token, g Grouping) []Segment {
	out := []Segment{}
	type open struct {
		speaker        string
		startMs, endMs int64
		text           strings.Builder
	}
	var cur *open
	closeSeg := func() {
		if cur == nil {
			return
		}
		if text := strings.TrimSpace(cur.text.String()); text != "" {
			out = append(out, Segment{
				ID: len(out), Speaker: cur.speaker,
				Start: seconds(cur.startMs), End: seconds(cur.endMs), Text: text,
			})
		}
		cur = nil
	}
	var prev Token
	for _, t := range sourceTokens(tokens) {
		split := cur == nil ||
			t.Speaker != cur.speaker ||
			startsWord(t) && t.StartMs-prev.EndMs >= g.Pause.Milliseconds() ||
			startsWord(t) && t.EndMs-cur.startMs > g.MaxSegment.Milliseconds()
		if split {
			closeSeg()
			cur = &open{speaker: t.Speaker, startMs: t.StartMs}
		}
		cur.text.WriteString(t.Text)
		cur.endMs = t.EndMs
		prev = t
	}
	closeSeg()
	return out
}

// pickLanguage returns the most frequent token language (a tie goes to the one
// seen first), else the requested language, else the first hint, else "".
func pickLanguage(tokens []Token, requested string, hints []string) string {
	counts := map[string]int{}
	var order []string
	for _, t := range sourceTokens(tokens) {
		if t.Language == "" {
			continue
		}
		if counts[t.Language] == 0 {
			order = append(order, t.Language)
		}
		counts[t.Language]++
	}
	best := ""
	for _, l := range order {
		if counts[l] > counts[best] {
			best = l
		}
	}
	switch {
	case best != "":
		return best
	case requested != "":
		return requested
	case len(hints) > 0:
		return hints[0]
	}
	return ""
}

// buildVerbose assembles the verbose_json answer. Text is Soniox's text
// verbatim; the duration is the audio duration, or the end of the last token
// when Soniox reports none.
func buildVerbose(tr Transcription, ts Transcript, req request, g Grouping) VerboseJSON {
	duration := seconds(tr.AudioDurationMs)
	if tr.AudioDurationMs == 0 {
		if src := sourceTokens(ts.Tokens); len(src) > 0 {
			duration = seconds(src[len(src)-1].EndMs)
		}
	}
	return VerboseJSON{
		Task:     "transcribe",
		Language: pickLanguage(ts.Tokens, req.Language, req.Create.LanguageHints),
		Duration: duration,
		Text:     ts.Text,
		Words:    groupWords(ts.Tokens),
		Segments: groupSegments(ts.Tokens, g),
		Usage:    Usage{Type: "duration", Seconds: duration},
	}
}
