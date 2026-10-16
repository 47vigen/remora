package proxy

import (
	"sync"
	"time"
)

// recentWindow is how far back the load signal of the spreading strategies looks.
const recentWindow = 60 * time.Second

const recentBuckets = int64(recentWindow / time.Second)

type recentBucket struct {
	sec int64
	n   int
}

// recentStarts counts starts per key over the last recentWindow in one-second buckets; a bucket is reset when its
// second comes round again. It is safe for concurrent use.
type recentStarts struct {
	mu   sync.Mutex
	now  func() time.Time
	keys map[string]*[recentBuckets]recentBucket
}

func newRecentStarts(now func() time.Time) *recentStarts {
	if now == nil {
		now = time.Now
	}
	return &recentStarts{now: now, keys: map[string]*[recentBuckets]recentBucket{}}
}

// Add records one start for key now.
func (r *recentStarts) Add(key string) {
	sec := r.now().Unix()
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.keys[key]
	if b == nil {
		b = new([recentBuckets]recentBucket)
		r.keys[key] = b
	}
	slot := &b[sec%recentBuckets]
	if slot.sec != sec {
		*slot = recentBucket{sec: sec}
	}
	slot.n++
}

// Count is the number of starts for key in the last recentWindow.
func (r *recentStarts) Count(key string) int {
	sec := r.now().Unix()
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.keys[key]
	if b == nil {
		return 0
	}
	n := 0
	for _, slot := range b {
		if sec-slot.sec < recentBuckets {
			n += slot.n
		}
	}
	return n
}
