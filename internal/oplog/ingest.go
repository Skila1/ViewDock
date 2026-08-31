package oplog

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
)

const (
	maxIngestBody   = 64 << 10
	maxIngestBatch  = 40
	maxDetailBytes  = 4 << 10
	ingestPerMinute = 120
)

var eventNameRe = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

type ingestEvent struct {
	Name    string         `json:"name"`
	T       int64          `json:"t"`
	Details map[string]any `json:"details"`
}

type ingestBody struct {
	Events []ingestEvent `json:"events"`
}

type ingestLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newIngestLimiter() *ingestLimiter {
	return &ingestLimiter{hits: map[string][]time.Time{}}
}

func (l *ingestLimiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := now.Add(-time.Minute)
	arr := l.hits[key]
	kept := arr[:0]
	for _, t := range arr {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= ingestPerMinute {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}

var ingestLimit = newIngestLimiter()

func SanitizeEventName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if !eventNameRe.MatchString(name) {
		return ""
	}
	return name
}

func ingestKey(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		return strings.TrimSpace(strings.Split(xf, ",")[0])
	}
	return r.RemoteAddr
}

func (s *Store) handleIngest(w http.ResponseWriter, r *http.Request) {
	if !ingestLimit.allow(ingestKey(r)) {
		httpapi.WriteErr(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxIngestBody+1))
	if err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid body")
		return
	}
	if len(raw) > maxIngestBody {
		httpapi.WriteErr(w, http.StatusRequestEntityTooLarge, "too_large", "payload too large")
		return
	}
	var body ingestBody
	if err := json.Unmarshal(raw, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	if len(body.Events) == 0 || len(body.Events) > maxIngestBatch {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "events required")
		return
	}
	actor := ""
	if p := auth.FromRequest(r); p != nil && p.IsUser() {
		actor = p.UserID
	}
	ua := r.UserAgent()
	if len(ua) > 160 {
		ua = ua[:160]
	}
	accepted := 0
	for _, ev := range body.Events {
		name := SanitizeEventName(ev.Name)
		if name == "" {
			continue
		}
		details := ev.Details
		if details == nil {
			details = map[string]any{}
		}
		if ev.T > 0 {
			details["client_t"] = ev.T
		}
		details["ua"] = ua
		if b, err := json.Marshal(details); err == nil && len(b) > maxDetailBytes {
			details = map[string]any{"truncated": true, "client_t": ev.T, "ua": ua}
		}
		s.Write(r.Context(), Entry{
			Level:    "info",
			Category: "journey",
			Message:  name,
			Details:  details,
			ActorID:  actor,
		})
		accepted++
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "accepted": accepted})
}
