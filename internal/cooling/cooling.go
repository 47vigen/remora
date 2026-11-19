// Package cooling takes alias members that keep failing out of their chains for a while. A 402 cools a member at
// once; repeated 429s or transient failures also cool it. A cooling member is left out of its compiled chains.
// A member that tripped the circuit breaker returns only after a successful probe.
package cooling

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/47vigen/remora/internal/alias"
)

// Defaults for the zero fields of Config.
const (
	DefaultThreshold = 3
	DefaultWindow    = 60 * time.Second
	DefaultCooldown  = 5 * time.Minute
	// DefaultSubscriptionCooldown is shorter: a subscription member stands for a pool of accounts that the OAuth engine
	// rotates and cools itself, so the member as a whole is back within seconds.
	DefaultSubscriptionCooldown = 30 * time.Second
)

const (
	breakerThreshold = 5
	breakerWindow    = 60 * time.Second
	breakerCooldown  = 30 * time.Second
	maxCooldown      = 24 * time.Hour
)

// Config tunes when a member cools and for how long. Zero fields take the defaults above.
type Config struct {
	Threshold int           // 429s within Window that cool a member
	Window    time.Duration // how close together those 429s must be
	Default   time.Duration // cooldown when the upstream sent no retry hint
	// SubscriptionDefault replaces Default for a subscription member's failure without a retry hint.
	SubscriptionDefault time.Duration
}

// Event is one failed upstream attempt. Target is in user names (provider and model as the user wrote them).
type Event struct {
	ID         string
	At         time.Time // when the request started
	Target     alias.Target
	StatusCode int
	RetryAfter time.Duration // 0 when the upstream gave no hint
	// Subscription marks a failure of a subscription member, served through the OAuth engine.
	Subscription bool
}

// Cooling is one member out of its chains. Until is when it can return or be probed; Reason is the status code.
type Cooling struct {
	Target  alias.Target
	Until   time.Time
	Reason  string
	Backoff time.Duration // nonzero for a tripped breaker; the base delay before its next probe
}

// Storage persists the active cooldowns, not the recent failure counts or in-flight probes.
type Storage interface {
	LoadCooling(context.Context, time.Time) ([]Cooling, error)
	SaveCooling(context.Context, []Cooling) error
}

// Cooler holds which members are cooling. apply recompiles the aliases (it reads Filter), and runs whenever the set
// of cooling members changes.
type Cooler struct {
	cfg   Config
	apply func(context.Context) error
	now   func() time.Time
	log   *slog.Logger

	mu       sync.Mutex
	cooling  map[alias.Target]Cooling
	hits     map[alias.Target][]time.Time // recent 429s by Event.At, sorted
	failures map[alias.Target][]time.Time // recent 5xx by Event.At, sorted
	probing  map[alias.Target]bool
	breakers int
	dirty    bool // the set changed and apply has not yet succeeded since
	gen      uint64

	flushMu    sync.Mutex
	storage    Storage
	persist    bool
	persistGen uint64
}

// New returns a Cooler. A nil now uses time.Now; a nil log discards.
func New(cfg Config, apply func(context.Context) error, now func() time.Time, log *slog.Logger) *Cooler {
	if cfg.Threshold <= 0 {
		cfg.Threshold = DefaultThreshold
	}
	if cfg.Window <= 0 {
		cfg.Window = DefaultWindow
	}
	if cfg.SubscriptionDefault <= 0 {
		cfg.SubscriptionDefault = DefaultSubscriptionCooldown
	}
	if cfg.Default <= 0 {
		cfg.Default = DefaultCooldown
	}
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Cooler{
		cfg: cfg, apply: apply, now: now, log: log,
		cooling: map[alias.Target]Cooling{}, hits: map[alias.Target][]time.Time{},
		failures: map[alias.Target][]time.Time{}, probing: map[alias.Target]bool{},
	}
}

// Restore loads persisted cooldowns and tripped breakers before the first reconcile and before serving requests.
func (c *Cooler) Restore(ctx context.Context, storage Storage) error {
	rows, err := storage.LoadCooling(ctx, c.now())
	if err != nil {
		return fmt.Errorf("cooling: restore: %w", err)
	}
	c.storage = storage
	for _, row := range rows {
		c.cooling[row.Target] = row
		if row.Backoff > 0 {
			c.breakers++
		}
	}
	return nil
}

func transient(code int) bool { return code >= 500 && code < 600 }

// Observe records an upstream failure and recompiles the aliases if it changes which members are cooling.
func (c *Cooler) Observe(ctx context.Context, e Event) error {
	c.mu.Lock()
	switch {
	case e.StatusCode == http.StatusPaymentRequired:
		c.coolLocked(e, c.cooling[e.Target].Backoff)
	case e.StatusCode == http.StatusTooManyRequests:
		if countHit(c.hits, e.Target, e.At, c.cfg.Threshold, c.cfg.Window) {
			c.coolLocked(e, c.cooling[e.Target].Backoff)
		}
	case transient(e.StatusCode):
		if _, open := c.cooling[e.Target]; !open && countHit(c.failures, e.Target, e.At, breakerThreshold, breakerWindow) {
			c.coolLocked(e, breakerCooldown)
		}
	}
	c.mu.Unlock()
	return c.flush(ctx)
}

// HasBreakers lets the proxy skip compiling a request's full alias chain on the normal path.
func (c *Cooler) HasBreakers() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.breakers != 0
}

// Admit reserves one expired breaker for a probe. With no probe, the request can use the normal chain only if it
// has a member outside the breaker; credit and rate-limit cooldowns retain their existing fallback behavior.
func (c *Cooler) Admit(chain []alias.Target) (probe alias.Target, allowed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for _, t := range chain {
		row := c.cooling[t]
		if row.Backoff == 0 {
			allowed = true
		} else if !now.Before(row.Until) && !c.probing[t] {
			c.probing[t] = true
			return t, true
		}
	}
	return alias.Target{}, allowed
}

// CompleteProbe closes the breaker on success, or reopens it with backoff on an upstream failure. A canceled
// request or client-side error is inconclusive and lets the next request probe.
func (c *Cooler) CompleteProbe(ctx context.Context, t alias.Target, code int, retry time.Duration) error {
	return c.completeProbe(ctx, t, code, retry, false)
}

// FailProbe reopens the breaker for an explicit upstream failure in an otherwise successful response.
func (c *Cooler) FailProbe(ctx context.Context, t alias.Target, code int, retry time.Duration) error {
	return c.completeProbe(ctx, t, code, retry, true)
}

func (c *Cooler) completeProbe(ctx context.Context, t alias.Target, code int, retry time.Duration, failed bool) error {
	c.mu.Lock()
	if !c.probing[t] {
		c.mu.Unlock()
		return nil
	}
	delete(c.probing, t)
	row := c.cooling[t]
	switch {
	case !failed && code >= 200 && code < 300 && !c.now().Before(row.Until):
		c.removeLocked(ctx, t, row)
	case failed || transient(code) || code == http.StatusPaymentRequired || code == http.StatusTooManyRequests:
		backoff := min(row.Backoff*2, maxCooldown)
		c.coolLocked(Event{Target: t, StatusCode: code, RetryAfter: max(retry, backoff)}, backoff)
	}
	c.mu.Unlock()
	return c.flush(ctx)
}

// Expire restores members cooling on credit or rate limits and retries pending changes. An expired breaker still
// needs a successful probe.
func (c *Cooler) Expire(ctx context.Context) error {
	now := c.now()
	c.mu.Lock()
	for t, s := range c.cooling {
		if s.Backoff == 0 && !now.Before(s.Until) {
			c.removeLocked(ctx, t, s)
		}
	}
	c.mu.Unlock()
	return c.flush(ctx)
}

// Filter returns chain without its cooling members. When no live member remains, prefer the legacy cooldowns
// to open breakers. If every member is behind a breaker, keep the rule installed; Admit gates the request.
func (c *Cooler) Filter(chain []alias.Target) []alias.Target {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]alias.Target, 0, len(chain))
	for _, t := range chain {
		if _, ok := c.cooling[t]; !ok {
			out = append(out, t)
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, t := range chain {
		if c.cooling[t].Backoff == 0 {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return slices.Clone(chain)
	}
	return out
}

// Snapshot lists the cooling members, soonest back first.
func (c *Cooler) Snapshot() []Cooling {
	c.mu.Lock()
	out := make([]Cooling, 0, len(c.cooling))
	for _, s := range c.cooling {
		out = append(out, s)
	}
	c.mu.Unlock()
	slices.SortFunc(out, func(x, y Cooling) int {
		return cmp.Or(x.Until.Compare(y.Until),
			cmp.Compare(x.Target.Provider, y.Target.Provider), cmp.Compare(x.Target.Model, y.Target.Model))
	})
	return out
}

// countHit checks the window ending at the latest event, even when attempts complete out of order.
func countHit(counts map[alias.Target][]time.Time, t alias.Target, at time.Time, threshold int, window time.Duration) bool {
	hits := counts[t]
	i, _ := slices.BinarySearchFunc(hits, at, func(x, y time.Time) int { return x.Compare(y) })
	hits = slices.Insert(hits, i, at)
	// Keep only what a window ending at the newest hit can still reach.
	floor := hits[len(hits)-1].Add(-window)
	for len(hits) > 0 && hits[0].Before(floor) {
		hits = hits[1:]
	}
	counts[t] = hits
	return len(hits) >= threshold
}

// coolLocked extends the deadline, but never shortens an active cooldown.
func (c *Cooler) coolLocked(e Event, backoff time.Duration) {
	hint := min(e.RetryAfter, maxCooldown)
	var d time.Duration
	switch {
	case transient(e.StatusCode):
		d = max(hint, backoff)
	case hint > 0:
		d = hint
	case e.Subscription:
		d = c.cfg.SubscriptionDefault
	default:
		d = c.cfg.Default
	}
	until := c.now().Add(d)
	reason := strconv.Itoa(e.StatusCode)
	cur, cooling := c.cooling[e.Target]
	if cooling && !until.After(cur.Until) {
		return
	}
	c.cooling[e.Target] = Cooling{Target: e.Target, Until: until, Reason: reason, Backoff: backoff}
	if backoff > 0 && cur.Backoff == 0 {
		c.breakers++
		delete(c.failures, e.Target)
	}
	c.persist, c.persistGen = true, c.persistGen+1
	if cooling {
		return // a later deadline changes no chain
	}
	c.changedLocked()
	c.log.Info("alias member cooling", "provider", e.Target.Provider, "model", e.Target.Model,
		"reason", reason, "until", until)
}

func (c *Cooler) removeLocked(ctx context.Context, t alias.Target, row Cooling) {
	delete(c.cooling, t)
	delete(c.hits, t)
	delete(c.failures, t)
	if row.Backoff > 0 {
		c.breakers--
	}
	c.persist, c.persistGen = true, c.persistGen+1
	c.changedLocked()
	c.log.InfoContext(ctx, "alias member restored", "provider", t.Provider, "model", t.Model)
}

func (c *Cooler) changedLocked() {
	c.dirty = true
	c.gen++
}

// flush serializes writes so a slower save cannot overwrite newer state. Either failed operation is retried on
// the next tick, without holding the admission lock during I/O.
func (c *Cooler) flush(ctx context.Context) error {
	c.mu.Lock()
	pending := c.dirty && c.apply != nil || c.persist && c.storage != nil
	c.mu.Unlock()
	if !pending {
		return nil
	}
	c.flushMu.Lock()
	defer c.flushMu.Unlock()
	c.mu.Lock()
	var rows []Cooling
	save, apply := c.persist && c.storage != nil, c.dirty && c.apply != nil
	gen, persistGen := c.gen, c.persistGen
	if save {
		rows = slices.Collect(maps.Values(c.cooling))
	}
	c.mu.Unlock()
	var saveErr, applyErr error
	if save {
		if err := c.storage.SaveCooling(ctx, rows); err != nil {
			saveErr = fmt.Errorf("cooling: save: %w", err)
		}
	}
	if apply {
		if err := c.apply(ctx); err != nil {
			applyErr = fmt.Errorf("cooling: recompile aliases: %w", err)
		}
	}
	c.mu.Lock()
	if save && saveErr == nil && c.persistGen == persistGen {
		c.persist = false
	}
	if apply && applyErr == nil && c.gen == gen {
		c.dirty = false
	}
	c.mu.Unlock()
	return errors.Join(saveErr, applyErr)
}
