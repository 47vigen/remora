package api

import (
	"context"
	"errors"
	"time"

	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"

	"github.com/47vigen/remora/internal/engine/bifrost"
)

const engineReadTimeout = 10 * time.Second

type engineLogReads struct {
	source LogSource
	slots  *semaphore.Weighted
	flight singleflight.Group
}

// engineSearchKey overrides the exact filter key for a list request with a moving default range.
type engineSearchKey struct{}

func engineRead[A, T any](ctx context.Context, logs *engineLogReads, op, key string, arg A, read func(context.Context, A) (T, error)) (T, error) {
	var zero T
	deadline := time.Now().Add(engineReadTimeout)
	wait, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err := wait.Err(); err != nil {
		return zero, engineReadError(err)
	}

	result := logs.flight.DoChan(op+"\x00"+key, func() (any, error) {
		// The initiating caller can leave without canceling work another caller is still waiting on.
		ctx, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
		defer cancel()
		if err := logs.slots.Acquire(ctx, 1); err != nil {
			return nil, err
		}
		defer logs.slots.Release(1)
		value, err := read(ctx, arg)
		if deadlineErr := ctx.Err(); deadlineErr != nil {
			return nil, deadlineErr
		}
		return value, err
	})
	select {
	case <-wait.Done():
		return zero, engineReadError(wait.Err())
	case r := <-result:
		if err := wait.Err(); err != nil {
			return zero, engineReadError(err)
		}
		if r.Err != nil {
			return zero, engineReadError(r.Err)
		}
		return r.Val.(T), nil
	}
}

func engineReadError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errEngineTimeout
	}
	return err
}

func (l *engineLogReads) SearchLogs(ctx context.Context, f bifrost.LogFilter) (bifrost.LogPage, error) {
	key, _ := ctx.Value(engineSearchKey{}).(string)
	if key == "" {
		key = f.Query(true).Encode()
	}
	return engineRead(ctx, l, "search", key, f, l.source.SearchLogs)
}

func (l *engineLogReads) GetLog(ctx context.Context, id string) (bifrost.Log, error) {
	return engineRead(ctx, l, "detail", id, id, l.source.GetLog)
}

func (l *engineLogReads) LogStats(ctx context.Context, f bifrost.LogFilter) (bifrost.LogStats, error) {
	return engineRead(ctx, l, "stats", f.Query(false).Encode(), f, l.source.LogStats)
}

func (l *engineLogReads) Histogram(ctx context.Context, f bifrost.LogFilter) ([]bifrost.HistogramBucket, error) {
	return engineRead(ctx, l, "histogram", f.Query(false).Encode(), f, l.source.Histogram)
}

func (l *engineLogReads) TokenHistogram(ctx context.Context, f bifrost.LogFilter) ([]bifrost.TokenBucket, error) {
	return engineRead(ctx, l, "tokens", f.Query(false).Encode(), f, l.source.TokenHistogram)
}

func (l *engineLogReads) CostHistogram(ctx context.Context, f bifrost.LogFilter) ([]bifrost.CostBucket, error) {
	return engineRead(ctx, l, "cost", f.Query(false).Encode(), f, l.source.CostHistogram)
}

func (l *engineLogReads) ModelUsage(ctx context.Context, f bifrost.LogFilter) ([]bifrost.ModelUsage, error) {
	return engineRead(ctx, l, "usage", f.Query(false).Encode(), f, l.source.ModelUsage)
}

func (l *engineLogReads) LogModels(ctx context.Context, query string) ([]string, error) {
	return engineRead(ctx, l, "models", query, query, l.source.LogModels)
}
