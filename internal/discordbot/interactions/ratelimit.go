package interactions

import (
	"sync"
	"time"
)

// limiter is a fixed-window counter keyed by caller. It is bounded: stale
// keys are pruned whenever the map grows past maxKeys.
type limiter struct {
	mu   sync.Mutex
	hits map[string]*window
}

type window struct {
	start time.Time
	n     int
}

const maxKeys = 50_000

func newLimiter() *limiter { return &limiter{hits: map[string]*window{}} }

// exceeded reports whether key has used its budget without counting a hit.
func (l *limiter) exceeded(key string, n int, per time.Duration, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.hits[key]
	return w != nil && now.Sub(w.start) < per && w.n >= n
}

func (l *limiter) allow(key string, n int, per time.Duration, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits) >= maxKeys {
		for k, w := range l.hits {
			if now.Sub(w.start) >= per {
				delete(l.hits, k)
			}
		}
		if len(l.hits) >= maxKeys {
			return false
		}
	}
	w := l.hits[key]
	if w == nil || now.Sub(w.start) >= per {
		l.hits[key] = &window{start: now, n: 1}
		return true
	}
	if w.n >= n {
		return false
	}
	w.n++
	return true
}
