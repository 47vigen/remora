package proxy

import (
	"testing"
	"time"
)

func TestRecentStartsWindow(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	now := t0
	r := newRecentStarts(func() time.Time { return now })
	for range 3 {
		r.Add("a")
	}
	now = t0.Add(30 * time.Second)
	r.Add("a")
	for _, tc := range []struct {
		at   time.Duration
		want int
	}{{59 * time.Second, 4}, {61 * time.Second, 1}, {91 * time.Second, 0}} {
		now = t0.Add(tc.at)
		if got := r.Count("a"); got != tc.want {
			t.Errorf("Count at t0+%v = %d, want %d", tc.at, got, tc.want)
		}
	}
	if got := r.Count("b"); got != 0 {
		t.Errorf("Count(b) = %d, want 0", got)
	}
	// A bucket is reset when its second comes round again.
	now = t0.Add(120 * time.Second)
	r.Add("a")
	if got := r.Count("a"); got != 1 {
		t.Errorf("Count after a full turn = %d, want 1", got)
	}
}
