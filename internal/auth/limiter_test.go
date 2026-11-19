package auth

import (
	"testing"
	"time"
)

func TestLimiterBackoff(t *testing.T) {
	l := newLimiter()
	now := newClock().now()
	const ip = "192.0.2.1"
	for failures, seconds := range []int{0, 0, 0, 0, 0, 1, 2, 4, 8, 16, 30, 30, 30} {
		if got, want := l.delay(ip, now), time.Duration(seconds)*time.Second; got != want {
			t.Errorf("delay after %d failures = %v, want %v", failures, got, want)
		}
		l.fail(ip, now)
	}
	if got := l.delay("192.0.2.2", now); got != 0 {
		t.Errorf("other IP delay = %v, want none", got)
	}
	if got := l.delay(ip, now.Add(15*time.Minute)); got != 0 {
		t.Errorf("delay after window = %v, want none", got)
	}
}
