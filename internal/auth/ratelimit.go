package auth

import (
	"net/http"
	"sync"
	"time"

	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/httpapi"
)

type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLimiter() *limiter { return &limiter{hits: map[string][]time.Time{}} }

func (l *limiter) allow(key string, n int, window time.Duration) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := now.Add(-window)
	arr := l.hits[key]
	kept := arr[:0]
	for _, t := range arr {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= n {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}

func RateLimit(cfg config.Config, n int, window time.Duration) func(http.Handler) http.Handler {
	lim := newLimiter()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := httpapi.ClientIPString(r, cfg)
			if !lim.allow(ip+r.URL.Path, n, window) {
				httpapi.WriteErr(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
