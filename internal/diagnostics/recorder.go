// Package diagnostics holds the playback flight recorder: a bounded,
// in-memory, per-session timeline. Nothing here is written to the database.
package diagnostics

import (
	"context"
	"regexp"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// Event origins.
const (
	OriginServer = "server"
	OriginClient = "client"
)

type Event struct {
	At            time.Time      `json:"at"`
	SessionID     string         `json:"session_id"`
	Type          string         `json:"type"`
	Origin        string         `json:"origin,omitempty"`
	RequestID     string         `json:"request_id,omitempty"`
	CorrelationID string         `json:"correlation_id,omitempty"`
	Data          map[string]any `json:"data,omitempty"`
	// Node names the media worker that recorded the event, when it was not
	// recorded by this process.
	Node string `json:"node,omitempty"`
}

type SourceScore struct {
	Source      string    `json:"source"`
	Attempts    int64     `json:"attempts"`
	Successes   int64     `json:"successes"`
	Failures    int64     `json:"failures"`
	Stalls      int64     `json:"stalls"`
	SuccessRate float64   `json:"success_rate"`
	AverageMS   float64   `json:"average_ms"`
	LastFailure time.Time `json:"last_failure,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SourceObservation is one server-side source outcome passed to observers.
type SourceObservation struct {
	Source  string
	Success bool
	Stall   bool
	Latency time.Duration
	At      time.Time
}

// Observer receives recorded events and source outcomes. Calls happen
// synchronously on the recording goroutine, outside the recorder lock, so
// implementations must be fast and must not call back into the recorder.
type Observer interface {
	FlightEvent(Event)
	SourceObserved(SourceObservation)
}

// SessionSummary describes one retained session timeline.
type SessionSummary struct {
	SessionID string    `json:"session_id"`
	FirstAt   time.Time `json:"first_at"`
	LastAt    time.Time `json:"last_at"`
	Events    int       `json:"events"`
	LastType  string    `json:"last_type"`
	Notable   int       `json:"notable"`
	Node      string    `json:"node,omitempty"`
}

// Stats reports recorder occupancy and ingest counters since start.
type Stats struct {
	Sessions         int     `json:"sessions"`
	MaxSessions      int     `json:"max_sessions"`
	MaxEvents        int     `json:"max_events"`
	RetentionSeconds int64   `json:"retention_seconds"`
	Recorded         int64   `json:"recorded"`
	Dropped          int64   `json:"dropped"`
	Sampled          int64   `json:"sampled"`
	BudgetBurst      int     `json:"budget_burst"`
	BudgetPerSecond  float64 `json:"budget_per_second"`
}

const (
	defaultRetention   = 24 * time.Hour
	defaultNotableKeep = 200
	defaultBurst       = 60
	defaultPerSecond   = 1.0
	sweepEvery         = time.Minute
	maxTypeLen         = 48
	maxRequestIDLen    = 128
)

var (
	typeRE      = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,47}$`)
	requestIDRE = regexp.MustCompile(`^[A-Za-z0-9._:/+=-]{1,128}$`)
)

// notableTypes mark events shown in the global failover and incident feed.
var notableTypes = map[string]bool{
	"source_failover":  true,
	"pipeline_failed":  true,
	"failover":         true,
	"startup_failed":   true,
	"codec_error":      true,
	"reconnect":        true,
	"node_unavailable": true,
}

func notable(e Event) bool {
	if notableTypes[e.Type] {
		return true
	}
	if e.Type == "error" {
		fatal, _ := e.Data["fatal"].(bool)
		return fatal
	}
	return false
}

type sourceState struct {
	SourceScore
	latencyTotal float64
}

type sessionLog struct {
	events   []Event
	firstAt  time.Time
	lastAt   time.Time
	notable  int
	sampled  map[string]time.Time
	tokens   float64
	refillAt time.Time
}

type Recorder struct {
	mu          sync.RWMutex
	maxSessions int
	maxEvents   int
	retention   time.Duration
	burst       int
	perSecond   float64
	sampling    map[string]time.Duration
	sessions    map[string]*sessionLog
	sources     map[string]*sourceState
	notable     []Event
	notableKeep int
	lastSweep   time.Time
	observers   []Observer
	now         func() time.Time

	recorded atomic.Int64
	dropped  atomic.Int64
	sampledN atomic.Int64
}

func New(maxSessions, maxEvents int) *Recorder {
	if maxSessions <= 0 {
		maxSessions = 1000
	}
	if maxEvents <= 0 {
		maxEvents = 256
	}
	return &Recorder{
		maxSessions: maxSessions, maxEvents: maxEvents,
		retention: defaultRetention, burst: defaultBurst, perSecond: defaultPerSecond,
		sampling:    map[string]time.Duration{"buffer": 5 * time.Second, "bitrate_change": 2 * time.Second},
		sessions:    map[string]*sessionLog{},
		sources:     map[string]*sourceState{},
		notableKeep: defaultNotableKeep,
		now:         time.Now,
	}
}

// SetRetention drops session timelines idle for longer than d. Zero or
// negative restores the default.
func (r *Recorder) SetRetention(d time.Duration) {
	if r == nil {
		return
	}
	if d <= 0 {
		d = defaultRetention
	}
	r.mu.Lock()
	r.retention = d
	r.mu.Unlock()
}

// SetIngestBudget sets the per-session token bucket used by Admit.
func (r *Recorder) SetIngestBudget(burst int, perSecond float64) {
	if r == nil || burst <= 0 || perSecond <= 0 {
		return
	}
	r.mu.Lock()
	r.burst, r.perSecond = burst, perSecond
	r.mu.Unlock()
}

// SetSampling keeps at most one event of typ per session per interval.
// A zero interval disables sampling for typ.
func (r *Recorder) SetSampling(typ string, interval time.Duration) {
	if r == nil || typ == "" {
		return
	}
	r.mu.Lock()
	if interval <= 0 {
		delete(r.sampling, typ)
	} else {
		r.sampling[typ] = interval
	}
	r.mu.Unlock()
}

// AddObserver registers o for every stored event and source outcome.
func (r *Recorder) AddObserver(o Observer) {
	if r == nil || o == nil {
		return
	}
	r.mu.Lock()
	r.observers = append(append([]Observer(nil), r.observers...), o)
	r.mu.Unlock()
}

// ValidType reports whether typ is an acceptable event type name.
func ValidType(typ string) bool { return len(typ) <= maxTypeLen && typeRE.MatchString(typ) }

// CleanRequestID returns id when it is a safe correlation identifier, or "".
func CleanRequestID(id string) string {
	if len(id) > maxRequestIDLen || !requestIDRE.MatchString(id) {
		return ""
	}
	return id
}

// RequestID returns the sanitized request ID attached by the HTTP stack.
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	return CleanRequestID(middleware.GetReqID(ctx))
}

func (r *Recorder) Record(sessionID, typ string, data map[string]any) {
	r.RecordEvent(Event{SessionID: sessionID, Type: typ, Data: data})
}

// RecordContext records a server event carrying the request's correlation ID.
func (r *Recorder) RecordContext(ctx context.Context, sessionID, typ string, data map[string]any) {
	r.RecordEvent(Event{SessionID: sessionID, Type: typ, RequestID: RequestID(ctx), Data: data})
}

// RecordEvent stores e and reports whether it was kept. A zero At is set to
// the current time and an empty Origin defaults to server.
func (r *Recorder) RecordEvent(e Event) bool {
	if r == nil || e.SessionID == "" || !ValidType(e.Type) {
		return false
	}
	now := r.now().UTC()
	if e.At.IsZero() {
		e.At = now
	}
	e.At = e.At.UTC()
	if e.Origin == "" {
		e.Origin = OriginServer
	}
	e.RequestID = CleanRequestID(e.RequestID)
	e.CorrelationID = CleanRequestID(e.CorrelationID)
	e.Data = clone(e.Data)

	r.mu.Lock()
	r.sweepLocked(now)
	s := r.sessionLocked(e.SessionID, now)
	if every, ok := r.sampling[e.Type]; ok {
		if last, seen := s.sampled[e.Type]; seen && now.Sub(last) < every {
			r.mu.Unlock()
			r.sampledN.Add(1)
			return false
		}
		if s.sampled == nil {
			s.sampled = map[string]time.Time{}
		}
		s.sampled[e.Type] = now
	}
	s.events = append(s.events, e)
	if len(s.events) > r.maxEvents {
		s.events = append([]Event(nil), s.events[len(s.events)-r.maxEvents:]...)
	}
	if s.firstAt.IsZero() || e.At.Before(s.firstAt) {
		s.firstAt = e.At
	}
	s.lastAt = now
	if notable(e) {
		s.notable++
		r.notable = append(r.notable, e)
		if len(r.notable) > r.notableKeep {
			r.notable = append([]Event(nil), r.notable[len(r.notable)-r.notableKeep:]...)
		}
	}
	observers := r.observers
	r.mu.Unlock()
	r.recorded.Add(1)
	for _, o := range observers {
		o.FlightEvent(copyEvent(e))
	}
	return true
}

// Admit takes up to n events from the session's ingest budget and returns
// how many were granted. The remainder should be dropped by the caller.
func (r *Recorder) Admit(sessionID string, n int) int {
	if r == nil || sessionID == "" || n <= 0 {
		return 0
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.sessionLocked(sessionID, now)
	if s.refillAt.IsZero() {
		s.tokens, s.refillAt = float64(r.burst), now
	} else if elapsed := now.Sub(s.refillAt).Seconds(); elapsed > 0 {
		s.tokens += elapsed * r.perSecond
		if s.tokens > float64(r.burst) {
			s.tokens = float64(r.burst)
		}
		s.refillAt = now
	}
	granted := int(s.tokens)
	if granted > n {
		granted = n
	}
	s.tokens -= float64(granted)
	if denied := n - granted; denied > 0 {
		r.dropped.Add(int64(denied))
	}
	return granted
}

// sessionLocked returns the log for id, evicting the least recently active
// session when the recorder is full.
func (r *Recorder) sessionLocked(id string, now time.Time) *sessionLog {
	if s, ok := r.sessions[id]; ok {
		return s
	}
	if len(r.sessions) >= r.maxSessions {
		oldestID := ""
		var oldest time.Time
		for sid, s := range r.sessions {
			if oldestID == "" || s.lastAt.Before(oldest) {
				oldestID, oldest = sid, s.lastAt
			}
		}
		delete(r.sessions, oldestID)
	}
	s := &sessionLog{lastAt: now.UTC()}
	r.sessions[id] = s
	return s
}

func (r *Recorder) sweepLocked(now time.Time) {
	if now.Sub(r.lastSweep) < sweepEvery {
		return
	}
	r.lastSweep = now
	cutoff := now.Add(-r.retention)
	for id, s := range r.sessions {
		if s.lastAt.Before(cutoff) {
			delete(r.sessions, id)
		}
	}
	kept := r.notable[:0]
	for _, e := range r.notable {
		if !e.At.Before(cutoff) {
			kept = append(kept, e)
		}
	}
	r.notable = kept
}

// Events returns the session timeline ordered by event time.
func (r *Recorder) Events(sessionID string) []Event {
	if r == nil {
		return []Event{}
	}
	r.mu.RLock()
	var list []Event
	if s := r.sessions[sessionID]; s != nil {
		list = s.events
	}
	out := make([]Event, len(list))
	for i := range list {
		out[i] = copyEvent(list[i])
	}
	r.mu.RUnlock()
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// Sessions lists retained timelines, most recently active first.
func (r *Recorder) Sessions(limit int) []SessionSummary {
	if r == nil {
		return []SessionSummary{}
	}
	r.mu.RLock()
	out := make([]SessionSummary, 0, len(r.sessions))
	for id, s := range r.sessions {
		if len(s.events) == 0 {
			continue
		}
		out = append(out, SessionSummary{
			SessionID: id, FirstAt: s.firstAt, LastAt: s.lastAt, Events: len(s.events),
			LastType: s.events[len(s.events)-1].Type, Notable: s.notable,
		})
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastAt.Equal(out[j].LastAt) {
			return out[i].LastAt.After(out[j].LastAt)
		}
		return out[i].SessionID < out[j].SessionID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Notable returns recent failover, failure and reconnect events across all
// sessions, newest first.
func (r *Recorder) Notable(limit int) []Event {
	if r == nil {
		return []Event{}
	}
	r.mu.RLock()
	out := make([]Event, 0, len(r.notable))
	for i := len(r.notable) - 1; i >= 0; i-- {
		out = append(out, copyEvent(r.notable[i]))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	r.mu.RUnlock()
	return out
}

// Merge combines timelines of one session recorded by different processes,
// ordered by event time.
func Merge(a, b []Event) []Event {
	out := make([]Event, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// MergeSessions combines summaries from this process with those reported by
// media workers. A session known to both keeps the worker label and the
// combined counts. The result is most recently active first.
func MergeSessions(local, remote []SessionSummary, limit int) []SessionSummary {
	byID := make(map[string]int, len(local)+len(remote))
	out := make([]SessionSummary, 0, len(local)+len(remote))
	for _, s := range local {
		byID[s.SessionID] = len(out)
		out = append(out, s)
	}
	for _, s := range remote {
		i, ok := byID[s.SessionID]
		if !ok {
			byID[s.SessionID] = len(out)
			out = append(out, s)
			continue
		}
		cur := &out[i]
		cur.Events += s.Events
		cur.Notable += s.Notable
		cur.Node = s.Node
		if s.FirstAt.Before(cur.FirstAt) {
			cur.FirstAt = s.FirstAt
		}
		if s.LastAt.After(cur.LastAt) {
			cur.LastAt, cur.LastType = s.LastAt, s.LastType
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].LastAt.Equal(out[j].LastAt) {
			return out[i].LastAt.After(out[j].LastAt)
		}
		return out[i].SessionID < out[j].SessionID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (r *Recorder) Stats() Stats {
	if r == nil {
		return Stats{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return Stats{
		Sessions: len(r.sessions), MaxSessions: r.maxSessions, MaxEvents: r.maxEvents,
		RetentionSeconds: int64(r.retention / time.Second),
		Recorded:         r.recorded.Load(), Dropped: r.dropped.Load(), Sampled: r.sampledN.Load(),
		BudgetBurst: r.burst, BudgetPerSecond: r.perSecond,
	}
}

func (r *Recorder) ObserveSource(source string, success, stall bool, latency time.Duration) {
	if r == nil || source == "" {
		return
	}
	now := r.now().UTC()
	r.mu.Lock()
	s := r.sources[source]
	if s == nil {
		s = &sourceState{SourceScore: SourceScore{Source: source}}
		r.sources[source] = s
	}
	s.Attempts++
	if success {
		s.Successes++
	} else {
		s.Failures++
		s.LastFailure = now
	}
	if stall {
		s.Stalls++
	}
	s.latencyTotal += float64(latency.Milliseconds())
	s.SuccessRate = float64(s.Successes) / float64(s.Attempts)
	s.AverageMS = s.latencyTotal / float64(s.Attempts)
	s.UpdatedAt = now
	observers := r.observers
	r.mu.Unlock()
	for _, o := range observers {
		o.SourceObserved(SourceObservation{Source: source, Success: success, Stall: stall, Latency: latency, At: now})
	}
}

func (r *Recorder) Sources() []SourceScore {
	if r == nil {
		return []SourceScore{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SourceScore, 0, len(r.sources))
	for _, state := range r.sources {
		out = append(out, state.SourceScore)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SuccessRate > out[j].SuccessRate })
	return out
}

func copyEvent(e Event) Event {
	e.Data = clone(e.Data)
	return e
}

func clone(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
