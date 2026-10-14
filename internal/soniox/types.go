// Package soniox serves OpenAI-style speech-to-text over Soniox's async API:
// one multipart request becomes upload, create, poll, fetch and delete, and
// Soniox tokens are grouped into words and speaker segments.
package soniox

import (
	"encoding/json"
	"time"
)

// RefPrefix starts the client_reference_id of every file and transcription
// remora creates; the sweeper only touches objects that carry it.
const RefPrefix = "remora:"

// Transcription statuses.
const (
	StatusQueued     = "queued"
	StatusProcessing = "processing"
	StatusCompleted  = "completed"
	StatusError      = "error"
)

// File is an uploaded Soniox file.
type File struct {
	ID                string    `json:"id"`
	Filename          string    `json:"filename"`
	ClientReferenceID string    `json:"client_reference_id"`
	CreatedAt         time.Time `json:"created_at"`
}

// Transcription is a Soniox async transcription job.
type Transcription struct {
	ID                string    `json:"id"`
	Status            string    `json:"status"` // queued|processing|completed|error
	FileID            string    `json:"file_id"`
	ClientReferenceID string    `json:"client_reference_id"`
	ErrorType         string    `json:"error_type"`
	ErrorMessage      string    `json:"error_message"`
	AudioDurationMs   int64     `json:"audio_duration_ms"`
	CreatedAt         time.Time `json:"created_at"`
}

// Token is one (often sub-word) transcript token.
type Token struct {
	Text              string  `json:"text"`
	StartMs           int64   `json:"start_ms"`
	EndMs             int64   `json:"end_ms"`
	Confidence        float64 `json:"confidence"`
	Speaker           string  `json:"speaker"`
	Language          string  `json:"language"`
	TranslationStatus string  `json:"translation_status"`
}

// Transcript is the result of a completed transcription.
type Transcript struct {
	ID     string  `json:"id"`
	Text   string  `json:"text"`
	Tokens []Token `json:"tokens"`
}

// CreateRequest is the body of POST /v1/transcriptions.
type CreateRequest struct {
	Model                        string          `json:"model"`
	FileID                       string          `json:"file_id"`
	ClientReferenceID            string          `json:"client_reference_id"`
	LanguageHints                []string        `json:"language_hints,omitempty"`
	LanguageHintsStrict          *bool           `json:"language_hints_strict,omitempty"`
	EnableSpeakerDiarization     *bool           `json:"enable_speaker_diarization,omitempty"`
	EnableLanguageIdentification *bool           `json:"enable_language_identification,omitempty"`
	Context                      json.RawMessage `json:"context,omitempty"`     // a JSON string or object
	Translation                  json.RawMessage `json:"translation,omitempty"` // a JSON object
}
