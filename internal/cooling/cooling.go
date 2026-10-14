// Package cooling takes alias members that keep failing out of their chains for a while. A 402 cools a member at
// once; repeated 429s within a window cool it too. A cooling member is left out when aliases are compiled, and put
// back when its cooldown ends. The state lives in memory only.
package cooling

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
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
)

// Config tunes when a member cools and for how long. Zero fields take the defaults above.
type Config struct {
	Threshold int           // 429s within Window that cool a member
	Window    time.Duration // how close together those 429s must be
	Default   time.Duration // cooldown when the upstream sent no retry hint
}

// Event is one failed upstream attempt. Target is in user names (provider and model as the user wrote them).
type Event struct {
	ID         string
	At         time.Time // when the request started
	Target     alias.Target
	StatusCode int
	RetryAfter time.Duration // 0 when the upstream gave no hint
}

// Cooling is one member that is out of its chains until Until. Reason is the status code that cooled it, "402" or
// "429".
type Cooling struct {
	Target alias.Target
	Until  time.Time
	Reason string
}

// Cooler holds which members are cooling. apply recompiles the aliases (it reads Filter), and runs whenever the set
// of cooling members changes.
type Cooler struct {
	cfg   Config
	apply func(context.Context) error
	now   func() time.Time
	log   *slog.Logger

	mu      sync.Mutex
	cooling map[alias.Target]Cooling
	hits    map[alias.Target][]time.Time // recent 429s by Event.At, sorted
	dirty   bool                         // the set changed and apply has not yet succeeded since
	gen     uint64                       // bumped on every change of the set
}

// New returns a Cooler. A nil now uses time.Now; a nil log discards.
func New(cfg Config, apply func(context.Context) error, now func() time.Time, log *slog.Logger) *Cooler {
	if cfg.Threshold <= 0 {
		cfg.Threshold = DefaultThreshold
	}
	if cfg.Window <= 0 {
		cfg.Window = DefaultWindow
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
	}
}

// Observe records one failed attempt. A 402 cools its member at once; a 429 cools it once Threshold of them fall
// within Window. Other status codes are ignored. When the set of cooling members changes, Observe calls apply and
// returns its error; the member stays cooling either way.
func (c *Cooler) Observe(ctx context.Context, e Event) error {
	c.mu.Lock()
	switch e.StatusCode {
	case http.StatusPaymentRequired:
		c.coolLocked(e)
	case http.StatusTooManyRequests:
		if c.count429Locked(e.Target, e.At) {
			c.coolLocked(e)
		}
	}
	c.mu.Unlock()
	return c.flush(ctx)
}

// Expire puts back every member whose cooldown has ended, and calls apply when any came back or an earlier apply
// failed.
func (c *Cooler) Expire(ctx context.Context) error {
	now := c.now()
	c.mu.Lock()
	for t, s := range c.cooling {
		if now.Before(s.Until) {
			continue
		}
		delete(c.cooling, t)
		delete(c.hits, t)
		c.changedLocked()
		c.log.InfoContext(ctx, "alias member restored", "provider", t.Provider, "model", t.Model)
	}
	c.mu.Unlock()
	return c.flush(ctx)
}

// Filter returns chain without its cooling members. When every member is cooling it returns chain unchanged, so an
// alias never loses its route.
func (c *Cooler) Filter(chain []alias.Target) []alias.Target {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]alias.Target, 0, len(chain))
	for _, t := range chain {
		if _, ok := c.cooling[t]; !ok {
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

// count429Locked records a 429 at `at` and reports whether Threshold of the member's 429s now fall within Window.
// Events can arrive out of order (a long request's row is written late), so every window is checked, not only the
// one ending at `at`.
func (c *Cooler) count429Locked(t alias.Target, at time.Time) bool {
	hits := c.hits[t]
	i, _ := slices.BinarySearchFunc(hits, at, func(x, y time.Time) int { return x.Compare(y) })
	hits = slices.Insert(hits, i, at)
	// Keep only what a window ending at the newest hit can still reach.
	floor := hits[len(hits)-1].Add(-c.cfg.Window)
	for len(hits) > 0 && hits[0].Before(floor) {
		hits = hits[1:]
	}
	c.hits[t] = hits
	n := c.cfg.Threshold
	for j := 0; j+n-1 < len(hits); j++ {
		if hits[j+n-1].Sub(hits[j]) <= c.cfg.Window {
			return true
		}
	}
	return false
}

// coolLocked cools e.Target from now for its retry hint, or the default cooldown. A member already cooling is
// extended only when the new deadline is later.
func (c *Cooler) coolLocked(e Event) {
	d := e.RetryAfter
	if d <= 0 {
		d = c.cfg.Default
	}
	until := c.now().Add(d)
	reason := strconv.Itoa(e.StatusCode)
	cur, cooling := c.cooling[e.Target]
	if cooling && !until.After(cur.Until) {
		return
	}
	c.cooling[e.Target] = Cooling{Target: e.Target, Until: until, Reason: reason}
	if cooling {
		return // a later deadline changes no chain
	}
	c.changedLocked()
	c.log.Info("alias member cooling", "provider", e.Target.Provider, "model", e.Target.Model,
		"reason", reason, "until", until)
}

func (c *Cooler) changedLocked() {
	c.dirty = true
	c.gen++
}

// flush runs apply, outside the lock, when the set changed since the last successful apply.
func (c *Cooler) flush(ctx context.Context) error {
	c.mu.Lock()
	if !c.dirty || c.apply == nil {
		c.mu.Unlock()
		return nil
	}
	gen := c.gen
	c.mu.Unlock()
	if err := c.apply(ctx); err != nil {
		return fmt.Errorf("cooling: recompile aliases: %w", err)
	}
	c.mu.Lock()
	if c.gen == gen {
		c.dirty = false
	}
	c.mu.Unlock()
	return nil
}
