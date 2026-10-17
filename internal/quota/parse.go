package quota

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/47vigen/remora/internal/store"
)

var errNotObject = errors.New("quota answer is not a JSON object")

// opt decodes a value leniently: null, a missing field or a value of the wrong type leaves OK false instead of
// failing the whole answer, so one changed field never hides the others.
type opt[T any] struct {
	V  T
	OK bool
}

func (o *opt[T]) UnmarshalJSON(b []byte) error {
	o.OK = !bytes.Equal(bytes.TrimSpace(b), []byte("null")) && json.Unmarshal(b, &o.V) == nil
	return nil
}

func (o opt[T]) ptr() *T {
	if !o.OK {
		return nil
	}
	return &o.V
}

// decode fills v from body; err only when body is not a JSON object.
func decode(body []byte, v any) error {
	var probe map[string]json.RawMessage
	if json.Unmarshal(body, &probe) != nil || probe == nil {
		return errNotObject
	}
	_ = json.Unmarshal(body, v)
	return nil
}

// fraction clamps v to 0..1 and rounds it to four places; nil stays nil.
func fraction(v *float64) *float64 {
	if v == nil || math.IsNaN(*v) {
		return nil
	}
	r := math.Round(min(max(*v, 0), 1)*1e4) / 1e4
	return &r
}

// leftOfPercent turns a percentage used into the fraction left.
func leftOfPercent(used *float64) *float64 {
	if used == nil {
		return nil
	}
	return fraction(new(1 - *used/100))
}

func parseRFC3339(o opt[string]) *time.Time {
	if !o.OK {
		return nil
	}
	t, err := time.Parse(time.RFC3339, o.V)
	if err != nil {
		return nil
	}
	return new(t.UTC())
}

func unixTime(secs float64) *time.Time { return new(time.Unix(int64(secs), 0).UTC()) }

// finish sets LimitReached when any window is known to be at 0.
func finish(q store.AccountQuota) store.AccountQuota {
	if q.Windows == nil {
		q.Windows = []store.QuotaWindow{}
	}
	for _, w := range q.Windows {
		if w.Remaining != nil && *w.Remaining == 0 {
			q.LimitReached = true
		}
	}
	return q
}

// windowLabel names a window by its length: about five hours is "5h", six to eight days "weekly", else minutes.
func windowLabel(seconds int64) string {
	const fiveHours, day = 5 * 3600, 24 * 3600
	switch {
	case seconds >= fiveHours*9/10 && seconds <= fiveHours*11/10:
		return "5h"
	case seconds >= 6*day && seconds <= 8*day:
		return "weekly"
	}
	return strconv.FormatInt(seconds/60, 10)
}

type agRemaining struct {
	RemainingFraction opt[float64] `json:"remainingFraction"`
	ResetTime         opt[string]  `json:"resetTime"`
}

type agBucket struct {
	BucketID    opt[string]      `json:"bucketId"`
	DisplayName opt[string]      `json:"displayName"`
	Window      opt[string]      `json:"window"`
	Disabled    opt[bool]        `json:"disabled"`
	Remaining   opt[agRemaining] `json:"remaining"`
	agRemaining
}

type agGroup struct {
	GroupID     opt[string]          `json:"groupId"`
	ID          opt[string]          `json:"id"`
	DisplayName opt[string]          `json:"displayName"`
	Buckets     opt[[]opt[agBucket]] `json:"buckets"`
}

type agSummary struct {
	Groups       opt[[]opt[agGroup]] `json:"groups"`
	QuotaSummary opt[struct {
		Groups opt[[]opt[agGroup]] `json:"groups"`
	}] `json:"quotaSummary"`
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func agScope(g agGroup) string {
	id := g.GroupID.V
	if id == "" {
		id = g.ID.V
	}
	text := strings.ToLower(id + " " + g.DisplayName.V)
	switch {
	case strings.Contains(text, "gemini"):
		return "gemini"
	case strings.Contains(text, "claude"), strings.Contains(text, "gpt"):
		return "claude-gpt"
	}
	name := id
	if name == "" {
		name = g.DisplayName.V
	}
	return strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

func agWindow(b agBucket) string {
	switch strings.ToLower(b.Window.V) {
	case "5h", "session":
		return "5h"
	case "weekly", "7d":
		return "weekly"
	}
	text := strings.ToLower(b.BucketID.V + " " + b.DisplayName.V)
	switch {
	case strings.Contains(text, "session"), strings.Contains(text, "5h"), strings.Contains(text, "5 hour"):
		return "5h"
	case strings.Contains(text, "week"), strings.Contains(text, "7d"):
		return "weekly"
	}
	return ""
}

func parseAntigravitySummary(body []byte) (store.AccountQuota, error) {
	var s agSummary
	if err := decode(body, &s); err != nil {
		return store.AccountQuota{}, err
	}
	groups := s.Groups.V
	if !s.Groups.OK {
		groups = s.QuotaSummary.V.Groups.V
	}
	var q store.AccountQuota
	seen := map[[2]string]bool{}
	for _, g := range groups {
		scope := agScope(g.V)
		for _, b := range g.V.Buckets.V {
			window := agWindow(b.V)
			key := [2]string{scope, window}
			if !b.OK || b.V.Disabled.V || window == "" || seen[key] {
				continue
			}
			seen[key] = true
			r := b.V.agRemaining
			if b.V.Remaining.OK {
				r = b.V.Remaining.V
			}
			w := store.QuotaWindow{Scope: scope, Window: window, Remaining: fraction(r.RemainingFraction.ptr()), ResetsAt: parseRFC3339(r.ResetTime)}
			q.Windows = append(q.Windows, w)
		}
	}
	return finish(q), nil
}

// parseAntigravitySuspension reads a refusal that names a terms-of-service suspension, and the provider's message.
func parseAntigravitySuspension(body []byte) (string, bool) {
	var s struct {
		Error struct {
			Message string `json:"message"`
			Details []struct {
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &s) != nil {
		return "", false
	}
	for _, d := range s.Error.Details {
		if d.Reason == "TOS_VIOLATION" {
			msg := strings.TrimSpace(s.Error.Message)
			if len(msg) > 300 {
				msg = msg[:300]
			}
			return msg, true
		}
	}
	return "", false
}

// parseAntigravityPlan reads the plan name from a loadCodeAssist answer; "" when none.
func parseAntigravityPlan(body []byte) string {
	type tier struct {
		ID   opt[string] `json:"id"`
		Name opt[string] `json:"name"`
	}
	var s struct {
		PaidTier    opt[tier] `json:"paidTier"`
		CurrentTier opt[tier] `json:"currentTier"`
	}
	if decode(body, &s) != nil {
		return ""
	}
	for _, v := range []string{s.PaidTier.V.Name.V, s.PaidTier.V.ID.V, s.CurrentTier.V.Name.V, s.CurrentTier.V.ID.V} {
		if v != "" {
			return v
		}
	}
	return ""
}

type codexWindow struct {
	UsedPercent        opt[float64] `json:"used_percent"`
	LimitWindowSeconds opt[float64] `json:"limit_window_seconds"`
	ResetAt            opt[float64] `json:"reset_at"`
	ResetAfterSeconds  opt[float64] `json:"reset_after_seconds"`
}

type codexRateLimit struct {
	LimitReached opt[bool]        `json:"limit_reached"`
	Primary      opt[codexWindow] `json:"primary_window"`
	Secondary    opt[codexWindow] `json:"secondary_window"`
}

func codexWindows(scope string, rl codexRateLimit, now time.Time) []store.QuotaWindow {
	var out []store.QuotaWindow
	for _, slot := range []opt[codexWindow]{rl.Primary, rl.Secondary} {
		if !slot.OK {
			continue
		}
		w := slot.V
		var reset *time.Time
		switch {
		case w.ResetAt.OK:
			reset = unixTime(w.ResetAt.V)
		case w.ResetAfterSeconds.OK:
			reset = new(now.Add(time.Duration(w.ResetAfterSeconds.V) * time.Second).UTC())
		}
		out = append(out, store.QuotaWindow{
			Scope: scope, Window: windowLabel(int64(w.LimitWindowSeconds.V)),
			Remaining: leftOfPercent(w.UsedPercent.ptr()), ResetsAt: reset,
		})
	}
	return out
}

func parseCodexUsage(body []byte, now time.Time) (store.AccountQuota, error) {
	var s struct {
		PlanType   opt[string]         `json:"plan_type"`
		RateLimit  opt[codexRateLimit] `json:"rate_limit"`
		Additional opt[[]opt[struct {
			LimitName opt[string]         `json:"limit_name"`
			RateLimit opt[codexRateLimit] `json:"rate_limit"`
		}]] `json:"additional_rate_limits"`
	}
	if err := decode(body, &s); err != nil {
		return store.AccountQuota{}, err
	}
	q := store.AccountQuota{Plan: s.PlanType.V, LimitReached: s.RateLimit.V.LimitReached.V}
	q.Windows = codexWindows("", s.RateLimit.V, now)
	for _, a := range s.Additional.V {
		q.Windows = append(q.Windows, codexWindows(a.V.LimitName.V, a.V.RateLimit.V, now)...)
	}
	return finish(q), nil
}

func parseClaudeUsage(body []byte) (store.AccountQuota, error) {
	type window struct {
		Utilization opt[float64] `json:"utilization"`
		ResetsAt    opt[string]  `json:"resets_at"`
	}
	var s struct {
		FiveHour       opt[window] `json:"five_hour"`
		SevenDay       opt[window] `json:"seven_day"`
		SevenDayOpus   opt[window] `json:"seven_day_opus"`
		SevenDaySonnet opt[window] `json:"seven_day_sonnet"`
	}
	if err := decode(body, &s); err != nil {
		return store.AccountQuota{}, err
	}
	var q store.AccountQuota
	for _, w := range []struct {
		scope, window string
		v             opt[window]
	}{{"", "5h", s.FiveHour}, {"", "weekly", s.SevenDay}, {"opus", "weekly", s.SevenDayOpus}, {"sonnet", "weekly", s.SevenDaySonnet}} {
		if w.v.OK {
			q.Windows = append(q.Windows, store.QuotaWindow{
				Scope: w.scope, Window: w.window, Remaining: leftOfPercent(w.v.V.Utilization.ptr()), ResetsAt: parseRFC3339(w.v.V.ResetsAt),
			})
		}
	}
	return finish(q), nil
}
