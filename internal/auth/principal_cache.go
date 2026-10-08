package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// principalCache avoids three database reads per API request. Entries live for
// principalTTL and are discarded whenever any authentication, user, role or
// admin mutation succeeds, so revocations take effect on the next request.
// During a database outage an entry up to principalStaleTTL old keeps an
// already-signed-in client usable; new sign-ins still require the database.
const (
	principalTTL      = 5 * time.Second
	principalStaleTTL = 15 * time.Minute
	principalMax      = 5000
)

type cachedPrincipal struct {
	p   Principal
	at  time.Time
	gen int64
}

type principalCache struct {
	mu      sync.Mutex
	entries map[string]cachedPrincipal
	gen     atomic.Int64
}

func newPrincipalCache() *principalCache {
	return &principalCache{entries: map[string]cachedPrincipal{}}
}

func cacheKey(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:16])
}

func (c *principalCache) get(tok string, maxAge time.Duration) (*Principal, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[cacheKey(tok)]
	if !ok || time.Since(e.at) > maxAge {
		return nil, false
	}
	if maxAge == principalTTL && e.gen != c.gen.Load() {
		return nil, false
	}
	p := e.p
	return &p, true
}

func (c *principalCache) put(tok string, p *Principal) {
	if c == nil || p == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= principalMax {
		cut := time.Now().Add(-principalStaleTTL)
		for k, e := range c.entries {
			if e.at.Before(cut) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= principalMax {
			c.entries = map[string]cachedPrincipal{}
		}
	}
	c.entries[cacheKey(tok)] = cachedPrincipal{p: *p, at: time.Now(), gen: c.gen.Load()}
}

// invalidate forces every cached principal to be re-read from the database.
func (c *principalCache) invalidate() {
	if c == nil {
		return
	}
	c.gen.Add(1)
	c.mu.Lock()
	c.entries = map[string]cachedPrincipal{}
	c.mu.Unlock()
}

// InvalidatePrincipals drops cached sessions after permission-affecting changes.
func (s *Service) InvalidatePrincipals() { s.principals.invalidate() }

// mutatesIdentity reports requests that can change who a principal is or what
// it may do. Successful ones invalidate the principal cache.
func mutatesIdentity(r *http.Request) bool {
	if SafeMethod(r.Method) {
		return false
	}
	p := r.URL.Path
	for _, prefix := range []string{"/api/v1/auth", "/api/v1/me", "/api/v1/users", "/api/v1/admin", "/api/v1/invites", "/api/v1/household", "/api/v1/setup", "/api/v1/guests"} {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }
