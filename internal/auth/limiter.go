package auth

import (
	"sync"
	"time"
)

const (
	limiterThreshold = 5
	limiterWindow    = 15 * time.Minute
	limiterMaxDelay  = 30 * time.Second
	limiterMaxFails  = limiterThreshold + 5 // By 2^5 seconds the delay has reached its cap.
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

// delay returns how long the next login from ip should wait before checking the password.
func (l *limiter) delay(ip string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.ips[ip]
	if !ok {
		return 0
	}
	if !now.Before(a.start.Add(limiterWindow)) {
		delete(l.ips, ip)
		return 0
	}
	if a.fails < limiterThreshold {
		return 0
	}
	return min(time.Second<<(a.fails-limiterThreshold), limiterMaxDelay)
}

// fail records a failed login from ip at now.
func (l *limiter) fail(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(now)
	a, ok := l.ips[ip]
	if !ok {
		a = &attempts{start: now}
		l.ips[ip] = a
	}
	a.fails = min(a.fails+1, limiterMaxFails)
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
