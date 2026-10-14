package auth

import (
	"sync"
	"time"
)

// Login limits: after limiterMax failed logins from one IP within limiterWindow, every login from that IP is
// refused until the window that began with the first failure ends.
const (
	limiterMax    = 5
	limiterWindow = 15 * time.Minute
)

type attempts struct {
	start time.Time
	fails int
}

// limiter counts failed logins per remote IP.
type limiter struct {
	mu  sync.Mutex
	ips map[string]*attempts
}

func newLimiter() *limiter { return &limiter{ips: map[string]*attempts{}} }

// allow reports whether ip may try to log in at now.
func (l *limiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.ips[ip]
	if !ok {
		return true
	}
	if !now.Before(a.start.Add(limiterWindow)) {
		delete(l.ips, ip)
		return true
	}
	return a.fails < limiterMax
}

// fail records a failed login from ip at now.
func (l *limiter) fail(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(now)
	a, ok := l.ips[ip]
	if !ok || !now.Before(a.start.Add(limiterWindow)) {
		a = &attempts{start: now}
		l.ips[ip] = a
	}
	a.fails++
}

// succeed forgets ip's failures.
func (l *limiter) succeed(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.ips, ip)
}

// prune drops expired windows so the map does not grow without bound. Called with mu held.
func (l *limiter) prune(now time.Time) {
	for ip, a := range l.ips {
		if !now.Before(a.start.Add(limiterWindow)) {
			delete(l.ips, ip)
		}
	}
}
