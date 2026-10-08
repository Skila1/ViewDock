// Package reliability keeps exponentially decaying, in-memory reliability
// scores for playback sources and nodes, ranks candidates per region and
// device class, applies administrator overrides and explains each decision.
package reliability

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Kind classifies an outcome.
type Kind string

const (
	// KindStart is a playback start attempt; Success reports whether it began.
	KindStart Kind = "start"
	// KindFirstFrame carries a time-to-first-frame measurement only.
	KindFirstFrame Kind = "first_frame"
	// KindStall is one rebuffering stall during playback.
	KindStall Kind = "stall"
	// KindFailure is playback failing after it started, or a failover away.
	KindFailure Kind = "failure"
)

// Outcome is one observation about a source. Region and DeviceClass are
// optional; when set the observation also feeds the scoped rankings.
type Outcome struct {
	Source      string
	Region      string
	DeviceClass string
	Kind        Kind
	Success     bool
	LatencyMS   int64
	TTFFMS      int64
	Reason      string
	SessionID   string
	RequestID   string
	At          time.Time
}

// Recorder is the narrow interface producers use to feed scores.
type Recorder interface {
	Observe(Outcome)
}

// Config tunes the scoring model. Zero fields take defaults.
type Config struct {
	// HalfLife is how long it takes an observation to lose half its weight.
	HalfLife time.Duration
	// MinEvidence is the weighted session count a region or device scope
	// needs before it is preferred over a broader scope.
	MinEvidence float64
	// PriorWeight and PriorSuccess pull sparse data toward a neutral score.
	PriorWeight  float64
	PriorSuccess float64
	LatencyRefMS float64
	TTFFRefMS    float64
	MaxSources   int
	MaxScopes    int
	// RecentFailures is how many failures are kept per source for display.
	RecentFailures int
}

func (c Config) withDefaults() Config {
	if c.HalfLife <= 0 {
		c.HalfLife = time.Hour
	}
	if c.MinEvidence <= 0 {
		c.MinEvidence = 3
	}
	if c.PriorWeight <= 0 {
		c.PriorWeight = 2
	}
	if c.PriorSuccess <= 0 || c.PriorSuccess > 1 {
		c.PriorSuccess = 0.9
	}
	if c.LatencyRefMS <= 0 {
		c.LatencyRefMS = 2000
	}
	if c.TTFFRefMS <= 0 {
		c.TTFFRefMS = 4000
	}
	if c.MaxSources <= 0 {
		c.MaxSources = 2000
	}
	if c.MaxScopes <= 0 {
		c.MaxScopes = 64
	}
	if c.RecentFailures <= 0 {
		c.RecentFailures = 10
	}
	return c
}

// Score weights. They sum to 1 so scores stay within 0 to 100.
const (
	weightSuccess = 0.6
	weightStalls  = 0.2
	weightLatency = 0.1
	weightTTFF    = 0.1
)

const (
	maxSourceLen = 128
	maxReasonLen = 120
	// Decayed sums below this are treated as no data for averages.
	minSamples = 0.05
	// Failures older than this are not shown as recent.
	recentWindow = 24 * time.Hour
)

var labelRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,31}$`)

// stats are exponentially decayed sums. decayTo must run before any update
// or read so every term shares the same reference time.
type stats struct {
	attempts  float64
	successes float64
	failures  float64
	stalls    float64
	latSum    float64
	latN      float64
	ttffSum   float64
	ttffN     float64
	updated   time.Time
}

func (s *stats) decayTo(now time.Time, halfLife time.Duration) {
	if s.updated.IsZero() {
		s.updated = now
		return
	}
	dt := now.Sub(s.updated)
	if dt <= 0 {
		return
	}
	f := math.Exp2(-float64(dt) / float64(halfLife))
	s.attempts *= f
	s.successes *= f
	s.failures *= f
	s.stalls *= f
	s.latSum *= f
	s.latN *= f
	s.ttffSum *= f
	s.ttffN *= f
	s.updated = now
}

func (s *stats) add(o Outcome) {
	switch o.Kind {
	case KindStart:
		s.attempts++
		if o.Success {
			s.successes++
		}
		if o.LatencyMS > 0 {
			s.latSum += float64(o.LatencyMS)
			s.latN++
		}
	case KindFailure:
		s.failures++
	case KindStall:
		s.stalls++
	}
	if o.TTFFMS > 0 {
		s.ttffSum += float64(o.TTFFMS)
		s.ttffN++
	}
}

// ScopeScore is the computed score for one source within one scope.
type ScopeScore struct {
	Scope            string  `json:"scope"`
	Region           string  `json:"region,omitempty"`
	DeviceClass      string  `json:"device_class,omitempty"`
	Score            float64 `json:"score"`
	SuccessRate      float64 `json:"success_rate"`
	StallsPerSession float64 `json:"stalls_per_session"`
	LatencyMS        float64 `json:"latency_ms"`
	TTFFMS           float64 `json:"ttff_ms"`
	// Evidence is the decayed number of sessions behind the score.
	Evidence float64 `json:"evidence"`
}

func (c Config) score(s stats) ScopeScore {
	pw := c.PriorWeight
	sessions := s.attempts + s.failures
	success := (s.successes + pw*c.PriorSuccess) / (sessions + pw)
	stallRate := s.stalls / (s.attempts + pw)
	latency := c.LatencyRefMS
	if s.latN >= minSamples {
		latency = (s.latSum + pw*c.LatencyRefMS) / (s.latN + pw)
	}
	ttff := c.TTFFRefMS
	if s.ttffN >= minSamples {
		ttff = (s.ttffSum + pw*c.TTFFRefMS) / (s.ttffN + pw)
	}
	stallPenalty := stallRate / (stallRate + 1)
	latPenalty := latency / (latency + c.LatencyRefMS)
	ttffPenalty := ttff / (ttff + c.TTFFRefMS)
	score := 100 * (weightSuccess*success + weightStalls*(1-stallPenalty) + weightLatency*(1-latPenalty) + weightTTFF*(1-ttffPenalty))
	out := ScopeScore{
		Score: round(score, 1), SuccessRate: round(success, 4), StallsPerSession: round(stallRate, 3),
		Evidence: round(sessions, 2),
	}
	if s.latN >= minSamples {
		out.LatencyMS = math.Round(s.latSum / s.latN)
	}
	if s.ttffN >= minSamples {
		out.TTFFMS = math.Round(s.ttffSum / s.ttffN)
	}
	return out
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

// Failure is a recent failure kept for diagnosis.
type Failure struct {
	At          time.Time `json:"at"`
	Kind        Kind      `json:"kind"`
	Reason      string    `json:"reason,omitempty"`
	Region      string    `json:"region,omitempty"`
	DeviceClass string    `json:"device_class,omitempty"`
	SessionID   string    `json:"session_id,omitempty"`
	RequestID   string    `json:"request_id,omitempty"`
}

type scopeEntry struct {
	region, device string
	stats          stats
}

type sourceEntry struct {
	scopes   map[string]*scopeEntry
	failures []Failure
	lastSeen time.Time
	lastFail time.Time
}

// Tracker is safe for concurrent use.
type Tracker struct {
	cfg       Config
	now       func() time.Time
	mu        sync.RWMutex
	sources   map[string]*sourceEntry
	overrides map[string]Override
}

func New(cfg Config) *Tracker {
	return &Tracker{cfg: cfg.withDefaults(), now: time.Now, sources: map[string]*sourceEntry{}, overrides: map[string]Override{}}
}

// Config returns the effective configuration.
func (t *Tracker) Config() Config { return t.cfg }

func scopeKey(region, device string) string {
	switch {
	case region != "" && device != "":
		return "region:" + region + "|device:" + device
	case region != "":
		return "region:" + region
	case device != "":
		return "device:" + device
	}
	return "global"
}

// NormalizeLabel lowercases a region or device class and returns "" when it
// is not a short identifier.
func NormalizeLabel(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if !labelRE.MatchString(v) {
		return ""
	}
	return v
}

func cleanSource(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > maxSourceLen || strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return ""
	}
	return v
}

func cleanReason(v string) string { return cleanText(v, maxReasonLen) }

// cleanText replaces control characters and truncates to max runes.
func cleanText(v string, max int) string {
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, strings.TrimSpace(v))
	if r := []rune(v); len(r) > max {
		v = string(r[:max])
	}
	return v
}

// Observe records one outcome. Invalid outcomes are ignored.
func (t *Tracker) Observe(o Outcome) {
	if t == nil {
		return
	}
	o.Source = cleanSource(o.Source)
	if o.Source == "" {
		return
	}
	switch o.Kind {
	case KindStart, KindFirstFrame, KindStall, KindFailure:
	default:
		return
	}
	o.Region, o.DeviceClass = NormalizeLabel(o.Region), NormalizeLabel(o.DeviceClass)
	now := t.now()
	if o.At.IsZero() || o.At.After(now) {
		o.At = now
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.sources[o.Source]
	if e == nil {
		t.evictSourceLocked()
		e = &sourceEntry{scopes: map[string]*scopeEntry{}}
		t.sources[o.Source] = e
	}
	e.lastSeen = now
	keys := [][2]string{{"", ""}}
	if o.Region != "" {
		keys = append(keys, [2]string{o.Region, ""})
	}
	if o.DeviceClass != "" {
		keys = append(keys, [2]string{"", o.DeviceClass})
	}
	if o.Region != "" && o.DeviceClass != "" {
		keys = append(keys, [2]string{o.Region, o.DeviceClass})
	}
	for _, k := range keys {
		key := scopeKey(k[0], k[1])
		sc := e.scopes[key]
		if sc == nil {
			t.evictScopeLocked(e, now)
			sc = &scopeEntry{region: k[0], device: k[1]}
			e.scopes[key] = sc
		}
		sc.stats.decayTo(now, t.cfg.HalfLife)
		sc.stats.add(o)
	}
	if o.Kind == KindFailure || (o.Kind == KindStart && !o.Success) {
		e.lastFail = o.At
		e.failures = append(e.failures, Failure{
			At: o.At.UTC(), Kind: o.Kind, Reason: cleanReason(o.Reason), Region: o.Region, DeviceClass: o.DeviceClass,
			SessionID: cleanSource(o.SessionID), RequestID: cleanSource(o.RequestID),
		})
		if len(e.failures) > t.cfg.RecentFailures {
			e.failures = append([]Failure(nil), e.failures[len(e.failures)-t.cfg.RecentFailures:]...)
		}
	}
}

func (t *Tracker) evictSourceLocked() {
	if len(t.sources) < t.cfg.MaxSources {
		return
	}
	oldest := ""
	var at time.Time
	for id, e := range t.sources {
		if oldest == "" || e.lastSeen.Before(at) {
			oldest, at = id, e.lastSeen
		}
	}
	delete(t.sources, oldest)
}

func (t *Tracker) evictScopeLocked(e *sourceEntry, now time.Time) {
	if len(e.scopes) < t.cfg.MaxScopes {
		return
	}
	oldest := ""
	var at time.Time
	for key, sc := range e.scopes {
		if key == "global" {
			continue
		}
		if oldest == "" || sc.stats.updated.Before(at) {
			oldest, at = key, sc.stats.updated
		}
	}
	if oldest != "" {
		delete(e.scopes, oldest)
	}
}

// scopeScoreLocked returns the decayed score for one scope without mutating
// stored state, so readers only need the read lock.
func (t *Tracker) scopeScoreLocked(sc *scopeEntry, now time.Time) ScopeScore {
	s := sc.stats
	s.decayTo(now, t.cfg.HalfLife)
	out := t.cfg.score(s)
	out.Scope, out.Region, out.DeviceClass = scopeKey(sc.region, sc.device), sc.region, sc.device
	return out
}

func (t *Tracker) neutral() ScopeScore {
	out := t.cfg.score(stats{})
	out.Scope = "global"
	return out
}

// SourceReport is the dashboard view of one source.
type SourceReport struct {
	Source         string       `json:"source"`
	Score          ScopeScore   `json:"score"`
	Scopes         []ScopeScore `json:"scopes"`
	RecentFailures []Failure    `json:"recent_failures"`
	LastSeen       *time.Time   `json:"last_seen,omitempty"`
	LastFailure    *time.Time   `json:"last_failure,omitempty"`
	Override       *Override    `json:"override,omitempty"`
}

// Report lists every tracked or overridden source, best score first.
func (t *Tracker) Report() []SourceReport {
	if t == nil {
		return []SourceReport{}
	}
	now := t.now()
	t.mu.RLock()
	out := make([]SourceReport, 0, len(t.sources)+len(t.overrides))
	for id, e := range t.sources {
		rep := SourceReport{Source: id, Scopes: []ScopeScore{}, RecentFailures: []Failure{}}
		for key, sc := range e.scopes {
			s := t.scopeScoreLocked(sc, now)
			if key == "global" {
				rep.Score = s
				continue
			}
			rep.Scopes = append(rep.Scopes, s)
		}
		if rep.Score.Scope == "" {
			rep.Score = t.neutral()
		}
		sort.Slice(rep.Scopes, func(i, j int) bool { return rep.Scopes[i].Scope < rep.Scopes[j].Scope })
		cutoff := now.Add(-recentWindow)
		for i := len(e.failures) - 1; i >= 0; i-- {
			if e.failures[i].At.After(cutoff) {
				rep.RecentFailures = append(rep.RecentFailures, e.failures[i])
			}
		}
		seen := e.lastSeen.UTC()
		rep.LastSeen = &seen
		if !e.lastFail.IsZero() {
			lf := e.lastFail.UTC()
			rep.LastFailure = &lf
		}
		if ov, ok := t.overrides[id]; ok {
			rep.Override = &ov
		}
		out = append(out, rep)
	}
	for id, ov := range t.overrides {
		if _, ok := t.sources[id]; ok {
			continue
		}
		ov := ov
		out = append(out, SourceReport{Source: id, Score: t.neutral(), Scopes: []ScopeScore{}, RecentFailures: []Failure{}, Override: &ov})
	}
	t.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score.Score != out[j].Score.Score {
			return out[i].Score.Score > out[j].Score.Score
		}
		return out[i].Source < out[j].Source
	})
	return out
}
