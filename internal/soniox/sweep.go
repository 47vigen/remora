package soniox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// DefaultMaxAge is how old a remora-tagged leftover must be before the
// sweeper deletes it.
const DefaultMaxAge = time.Hour

// KeySource returns the Soniox keys to sweep, such as the enabled Soniox
// provider keys in the store.
type KeySource func(ctx context.Context) ([]string, error)

// Sweeper deletes Soniox files and transcriptions that remora created (their
// client_reference_id starts with RefPrefix) and that outlived MaxAge, such as
// leftovers of a crash. Objects without the prefix are never touched.
type Sweeper struct {
	Client *Client
	Keys   KeySource
	MaxAge time.Duration    // 1h, from the spec
	Now    func() time.Time // default time.Now
	Logger *slog.Logger
}

// SweepResult counts what one sweep deleted.
type SweepResult struct{ Transcriptions, Files int }

// SweepOnce sweeps every key. A key that cannot be listed is logged and its
// error joined into the result; the other keys still run. A single object
// that fails to delete is logged and skipped.
func (s *Sweeper) SweepOnce(ctx context.Context) (SweepResult, error) {
	var res SweepResult
	keys, err := s.Keys(ctx)
	if err != nil {
		err = fmt.Errorf("soniox sweep: keys: %w", err)
		s.logger().Warn("soniox sweep could not read keys", "err", err)
		return res, err
	}
	var errs []error
	for i, key := range keys {
		if err := s.sweepKey(ctx, key, &res); err != nil {
			err = fmt.Errorf("soniox sweep: key #%d: %w", i+1, redactErr(err, key))
			s.logger().Warn("soniox sweep failed for a key", "key_index", i+1, "err", err)
			errs = append(errs, err)
		}
	}
	return res, errors.Join(errs...)
}

func (s *Sweeper) sweepKey(ctx context.Context, key string, res *SweepResult) error {
	maxAge := s.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}
	cutoff := s.now().Add(-maxAge)
	log := s.logger()

	txs, err := s.Client.ListTranscriptions(ctx, key)
	if err != nil {
		return err
	}
	active := map[string]bool{}
	for _, tr := range txs {
		if tr.Status == StatusQueued || tr.Status == StatusProcessing {
			active[tr.FileID] = true
		}
	}
	for _, tr := range txs {
		if !strings.HasPrefix(tr.ClientReferenceID, RefPrefix) || !tr.CreatedAt.Before(cutoff) ||
			(tr.Status != StatusCompleted && tr.Status != StatusError) {
			continue
		}
		if err := s.Client.DeleteTranscription(ctx, key, tr.ID); err != nil {
			log.Warn("soniox sweep: delete transcription", "transcription_id", tr.ID, "ref", tr.ClientReferenceID,
				"err", redact(err.Error(), key))
			continue
		}
		res.Transcriptions++
	}

	files, err := s.Client.ListFiles(ctx, key)
	if err != nil {
		return err
	}
	for _, f := range files {
		if !strings.HasPrefix(f.ClientReferenceID, RefPrefix) || !f.CreatedAt.Before(cutoff) || active[f.ID] {
			continue
		}
		if err := s.Client.DeleteFile(ctx, key, f.ID); err != nil {
			log.Warn("soniox sweep: delete file", "file_id", f.ID, "ref", f.ClientReferenceID,
				"err", redact(err.Error(), key))
			continue
		}
		res.Files++
	}
	return nil
}

// Run sweeps at once, then every `every`, until ctx is done.
func (s *Sweeper) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		res, _ := s.SweepOnce(ctx) // SweepOnce logs every failure
		if ctx.Err() != nil {
			return
		}
		if res.Transcriptions > 0 || res.Files > 0 {
			s.logger().Info("soniox sweep deleted leftovers", "transcriptions", res.Transcriptions, "files", res.Files)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Sweeper) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Sweeper) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}
