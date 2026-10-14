package soniox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// Defaults and bounds of Config.
const (
	DefaultBaseURL      = "https://api.soniox.com"
	DefaultPollInterval = time.Second
	DefaultDeadline     = 30 * time.Minute
	DefaultPause        = time.Second
	DefaultMaxSegment   = 30 * time.Second
	minPollInterval     = 500 * time.Millisecond
	maxPollInterval     = 60 * time.Second
	cleanupTimeout      = 30 * time.Second
	maxMemory           = 32 << 20
	transcriptionsPath  = "/v1/audio/transcriptions"
)

// Config configures the Adapter. Zero values take the defaults.
type Config struct {
	BaseURL      string        // default "https://api.soniox.com"
	PollInterval time.Duration // default 1s; clamped to [500ms, 60s]
	Deadline     time.Duration // default 30m; whole job from upload to transcript
	Grouping     Grouping      // defaults 1s / 30s
	HTTPClient   *http.Client  // default &http.Client{} (no overall timeout; contexts bound every call)
	Logger       *slog.Logger
}

// Adapter serves POST /v1/audio/transcriptions (OpenAI multipart) over the
// Soniox async API. The Soniox key is the request's bearer token, so the
// adapter holds no key of its own.
type Adapter struct {
	client *Client
	cfg    Config
	log    *slog.Logger
	after  func(time.Duration) <-chan time.Time
}

// New returns an Adapter with cfg's zero values replaced by the defaults.
func New(cfg Config) *Adapter {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.Deadline <= 0 {
		cfg.Deadline = DefaultDeadline
	}
	if cfg.Grouping.Pause <= 0 {
		cfg.Grouping.Pause = DefaultPause
	}
	if cfg.Grouping.MaxSegment <= 0 {
		cfg.Grouping.MaxSegment = DefaultMaxSegment
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Adapter{
		client: NewClient(cfg.BaseURL, cfg.HTTPClient),
		cfg:    cfg,
		log:    log,
		after:  time.After,
	}
}

// clampPoll bounds the poll interval to [500ms, 60s].
func clampPoll(d time.Duration) time.Duration {
	return min(max(d, minPollInterval), maxPollInterval)
}

// ServeHTTP runs one transcription: upload, create, poll, fetch, and always
// delete what it created.
func (a *Adapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != transcriptionsPath {
		writeError(w, http.StatusNotFound, typeNotFound, "not found: "+r.Method+" "+r.URL.Path)
		return
	}
	key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if key = strings.TrimSpace(key); !ok || key == "" {
		writeError(w, http.StatusUnauthorized, typeAuthentication, "missing Soniox API key (Authorization: Bearer)")
		return
	}
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		writeError(w, http.StatusBadRequest, typeInvalidRequest, "expected a multipart/form-data body: "+err.Error())
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	req, err := parseParams(r.MultipartForm)
	if err != nil {
		writeError(w, http.StatusBadRequest, typeInvalidRequest, err.Error())
		return
	}
	files := r.MultipartForm.File["file"]
	switch {
	case len(files) == 0:
		writeError(w, http.StatusBadRequest, typeInvalidRequest, `missing required parameter "file"`)
		return
	case len(files) > 1:
		writeError(w, http.StatusBadRequest, typeInvalidRequest, `invalid parameter "file": must be given once`)
		return
	}
	audio, err := files[0].Open()
	if err != nil {
		writeError(w, http.StatusBadRequest, typeInvalidRequest, `invalid parameter "file": unreadable`)
		return
	}
	defer func() { _ = audio.Close() }()

	j := &job{
		a:     a,
		w:     w,
		r:     r,
		key:   key,
		ref:   RefPrefix + randomHex(16),
		req:   req,
		start: time.Now(),
	}
	j.log = a.log.With("ref", j.ref)
	j.run(r.Context(), audio, filename(files[0]))
}

// job is the state of one transcription request.
type job struct {
	a      *Adapter
	w      http.ResponseWriter
	r      *http.Request
	key    string
	ref    string
	req    request
	start  time.Time
	log    *slog.Logger
	fileID string
	txID   string
}

// run executes the job; reqCtx is the request's context.
func (j *job) run(reqCtx context.Context, audio multipart.File, name string) {
	ctx, cancel := context.WithTimeout(reqCtx, j.a.cfg.Deadline)
	defer cancel()
	c := j.a.client

	file, err := c.UploadFile(ctx, j.key, j.ref, name, audio)
	if err != nil {
		j.fail(ctx, err)
		return
	}
	j.fileID = file.ID
	// One cleanup closure, so the file is deleted before the transcription.
	defer j.cleanup(reqCtx)

	cr := j.req.Create
	cr.FileID = file.ID
	cr.ClientReferenceID = j.ref
	tr, err := c.CreateTranscription(ctx, j.key, cr)
	if err != nil {
		j.fail(ctx, err)
		return
	}
	j.txID = tr.ID

	deadline, _ := ctx.Deadline()
	for {
		tr, err = c.GetTranscription(ctx, j.key, j.txID)
		if err != nil {
			j.fail(ctx, err)
			return
		}
		if tr.Status == StatusCompleted {
			break
		}
		if tr.Status == StatusError {
			msg := redact(fmt.Sprintf("transcription failed: %s: %s", tr.ErrorType, tr.ErrorMessage), j.key)
			j.writeError(http.StatusBadGateway, typeUpstream, msg)
			return
		}
		wait := min(clampPoll(j.a.cfg.PollInterval), time.Until(deadline))
		if wait <= 0 {
			j.timeout()
			return
		}
		select {
		case <-ctx.Done():
			j.fail(ctx, ctx.Err())
			return
		case <-j.a.after(wait):
		}
	}

	ts, err := c.GetTranscript(ctx, j.key, j.txID)
	if err != nil {
		j.fail(ctx, err)
		return
	}
	j.respond(tr, ts)
}

func (j *job) respond(tr Transcription, ts Transcript) {
	var body any
	switch j.req.ResponseFormat {
	case formatText:
		j.w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		j.w.WriteHeader(http.StatusOK)
		_, _ = j.w.Write([]byte(ts.Text))
		j.done(http.StatusOK)
		return
	case formatVerbose:
		body = buildVerbose(tr, ts, j.req, j.a.cfg.Grouping)
	default:
		body = struct {
			Text string `json:"text"`
		}{ts.Text}
	}
	b, err := json.Marshal(body)
	if err != nil {
		j.writeError(http.StatusInternalServerError, typeUpstream, "encode response: "+err.Error())
		return
	}
	j.w.Header().Set("Content-Type", "application/json")
	j.w.WriteHeader(http.StatusOK)
	_, _ = j.w.Write(b)
	j.done(http.StatusOK)
}

// fail answers a failed step. A client that went away gets nothing; a job
// past its deadline gets 504; anything else is mapped by mapError.
func (j *job) fail(ctx context.Context, err error) {
	switch {
	case j.r.Context().Err() != nil:
		j.log.Info("transcription abandoned by client", j.attrs()...)
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		j.timeout()
	default:
		status, typ, msg := mapError(err, j.key)
		j.log.Warn("transcription failed", append(j.attrs(), "err", redact(err.Error(), j.key))...)
		j.writeError(status, typ, msg)
	}
}

func (j *job) timeout() {
	j.writeError(http.StatusGatewayTimeout, typeTimeout,
		"transcription did not complete within "+j.a.cfg.Deadline.String())
}

func (j *job) writeError(status int, typ, msg string) {
	writeError(j.w, status, typ, msg)
	j.done(status)
}

func (j *job) done(status int) {
	j.log.Info("transcription answered", append(j.attrs(), "status", status)...)
}

func (j *job) attrs() []any {
	return []any{
		"transcription_id", j.txID,
		"file_id", j.fileID,
		"duration", time.Since(j.start).Round(time.Millisecond),
	}
}

// cleanup deletes the file first, so an abandoned job fails fast, then the
// transcription. It runs even when the client went away; failures are only
// logged, the sweeper is the backstop.
func (j *job) cleanup(reqCtx context.Context) {
	del := func(kind, id string, fn func(context.Context, string, string) error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(reqCtx), cleanupTimeout)
		defer cancel()
		if err := fn(ctx, j.key, id); err != nil {
			j.log.Warn("soniox cleanup failed", append(j.attrs(), "kind", kind, "err", redact(err.Error(), j.key))...)
		}
	}
	del("file", j.fileID, j.a.client.DeleteFile)
	if j.txID != "" {
		del("transcription", j.txID, j.a.client.DeleteTranscription)
	}
}

func filename(fh *multipart.FileHeader) string {
	if fh.Filename != "" {
		return fh.Filename
	}
	return "audio"
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
