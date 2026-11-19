package cooling

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/engine/bifrost"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
)

// DefaultTick is how often Run puts back members whose cooldown ended, when Run is given no tick.
const DefaultTick = 5 * time.Second

// ErrFeedClosed means the log feed Run reads was closed, for example because the feed dropped a subscriber that
// fell behind. The caller subscribes again and calls Run again.
var ErrFeedClosed = errors.New("cooling: log feed closed")

// EventFrom turns a front-door log row, with its error details, into an Event. It reports false unless the row is an
// upstream error: 402, 429, or 5xx (including request_timed_out/504 and provider_connection_failed/502).
// The front door records Retry-After separately from the error body, including for timed 503s and OAuth pool 429s.
func EventFrom(l bifrost.Log) (Event, bool) {
	if l.Status != "error" || l.Provider == "" || l.Model == "" {
		return Event{}, false
	}
	code, retryAfter := l.Failure()
	if code != http.StatusPaymentRequired && code != http.StatusTooManyRequests && !transient(code) {
		return Event{}, false
	}
	provider, model, ok := strings.Cut(cliproxyapi.UserRef(l.Provider, l.Model), "/")
	if !ok || provider == "" || model == "" {
		return Event{}, false
	}
	return Event{
		ID: l.ID, At: l.Timestamp, Target: alias.Target{Provider: provider, Model: model},
		StatusCode: code, RetryAfter: retryAfter, Subscription: l.Provider == bifrost.OAuthProvider,
	}, true
}

// RetryAfter parses a probe response's Retry-After, as delta-seconds or an HTTP date.
func RetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseUint(value, 10, 64); err == nil || errors.Is(err, strconv.ErrRange) {
		return time.Duration(min(seconds, uint64(maxCooldown/time.Second))) * time.Second
	}
	if deadline, err := http.ParseTime(value); err == nil {
		return min(maxCooldown, max(0, deadline.Sub(now)))
	}
	return 0
}

// Run follows the front door's log rows until ctx is done or rows is closed. For each error row it fetches the row's
// details and observes the event; a failed fetch is logged and the row skipped. Every tick (DefaultTick when tick ≤ 0)
// it puts back members whose cooldown ended.
//
// Rows that started before Run did are skipped: the feed replays its recent past when it starts, and a failure from
// before a restart already had its cooldown, so it must not start a new full one. Call Run before /v1 opens.
func (c *Cooler) Run(ctx context.Context, rows <-chan bifrost.Log, detail func(context.Context, string) (bifrost.Log, error), tick time.Duration) error {
	if tick <= 0 {
		tick = DefaultTick
	}
	since := c.now()
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := c.Expire(ctx); err != nil && ctx.Err() == nil {
				c.log.WarnContext(ctx, "cooling: restore members", "err", err)
			}
		case row, ok := <-rows:
			if !ok {
				return ErrFeedClosed
			}
			c.handle(ctx, row, detail, since)
		}
	}
}

// handle observes one row of the feed.
func (c *Cooler) handle(ctx context.Context, row bifrost.Log, detail func(context.Context, string) (bifrost.Log, error), since time.Time) {
	if row.Status != "error" || row.Timestamp.Before(since) {
		return
	}
	full, err := detail(ctx, row.ID)
	if err != nil {
		if ctx.Err() == nil {
			c.log.WarnContext(ctx, "cooling: read log row", "id", row.ID, "err", err)
		}
		return
	}
	// The list row is authoritative for what the detail may leave empty.
	if full.ID == "" {
		full.ID = row.ID
	}
	if full.Timestamp.IsZero() {
		full.Timestamp = row.Timestamp
	}
	if full.Provider == "" {
		full.Provider = row.Provider
	}
	if full.Model == "" {
		full.Model = row.Model
	}
	if full.Status == "" {
		full.Status = row.Status
	}
	e, ok := EventFrom(full)
	if !ok {
		return
	}
	if err := c.Observe(ctx, e); err != nil && ctx.Err() == nil {
		c.log.WarnContext(ctx, "cooling: observe failure", "err", err)
	}
}
