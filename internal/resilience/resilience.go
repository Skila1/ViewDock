// Package resilience serves the Connection Resilience Dashboard: an admin
// view where each subsystem is checked independently, so one offline
// service never hides the state of the others.
package resilience

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/viewdock/viewdock/internal/backend"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/diagnostics"
	"github.com/viewdock/viewdock/internal/playback"
	"github.com/viewdock/viewdock/internal/reliability"
	"github.com/viewdock/viewdock/internal/storage"
	"github.com/viewdock/viewdock/internal/version"
)

// Section statuses.
const (
	StatusOK           = "ok"
	StatusDegraded     = "degraded"
	StatusDown         = "down"
	StatusUnconfigured = "unconfigured"
)

// Section is one independently evaluated part of the dashboard. Error is a
// safe, generic message; details go to the server log.
type Section struct {
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
	Stale     bool      `json:"stale,omitempty"`
	Data      any       `json:"data,omitempty"`
}

type Dashboard struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Control     Section            `json:"control"`
	Database    Section            `json:"database"`
	Storage     Section            `json:"storage"`
	Coordinator Section            `json:"coordinator"`
	Nodes       Section            `json:"nodes"`
	Progress    Section            `json:"progress"`
	Flight      Section            `json:"flight_recorder"`
	Failovers   Section            `json:"failovers"`
	Reliability Section            `json:"reliability"`
	Extra       map[string]Section `json:"extra,omitempty"`
}

// CoordinatorStatus is reported by the party coordinator at wiring time.
type CoordinatorStatus struct {
	Available    bool           `json:"available"`
	Rooms        int            `json:"rooms"`
	Participants int            `json:"participants"`
	Details      map[string]any `json:"details,omitempty"`
}

// Check is an additional named section, for example an experimental
// broadcast pipeline. It returns a status constant and display data.
type Check func(ctx context.Context) (status string, data any, err error)

// Deps are supplied at wiring time. Every field is optional; a missing
// dependency yields an "unconfigured" section instead of an error.
type Deps struct {
	DB       *sql.DB
	DBDriver string
	Role     string
	// Storage is probed with a small write and delete at most once per
	// storageTTL. StorageProvider labels it in the UI.
	Storage         storage.Store
	StorageProvider string
	// StorageFn, when set, resolves the store at probe time so a changed
	// runtime setting is picked up. It takes precedence over Storage.
	StorageFn   func() (store storage.Store, provider string, err error)
	Coordinator func(ctx context.Context) (CoordinatorStatus, error)
	Nodes       func(ctx context.Context) ([]backend.Node, error)
	Progress    func() playback.ProgressStats
	Flight      *diagnostics.Recorder
	// RemoteSessions, when set, adds session summaries held by media workers
	// to the session list.
	RemoteSessions func(ctx context.Context) []diagnostics.SessionSummary
	Reliability    *reliability.Tracker
	Checks         map[string]Check
	Log            *slog.Logger
}

const (
	checkTimeout   = 3 * time.Second
	dbTTL          = 5 * time.Second
	storageTTL     = 60 * time.Second
	coordinatorTTL = 5 * time.Second
	nodesTTL       = 5 * time.Second
	extraTTL       = 15 * time.Second
	dbPingTimeout  = 1500 * time.Millisecond
	recentSessions = 25
	recentFailover = 50
	reportLimit    = 200
	storageProbe   = "viewdock-healthcheck/probe"
)

type Service struct {
	deps    Deps
	started time.Time
	now     func() time.Time
	timeout time.Duration

	db          *cached
	storage     *cached
	coordinator *cached
	nodes       *cached
	extra       map[string]*cached

	lastNodesMu sync.Mutex
	lastNodes   *nodesData
}

func New(d Deps) *Service {
	s := &Service{deps: d, started: time.Now(), now: time.Now, timeout: checkTimeout, extra: map[string]*cached{}}
	s.db = &cached{name: "database", ttl: dbTTL}
	s.storage = &cached{name: "storage", ttl: storageTTL}
	s.coordinator = &cached{name: "coordinator", ttl: coordinatorTTL}
	s.nodes = &cached{name: "nodes", ttl: nodesTTL}
	for name := range d.Checks {
		s.extra[name] = &cached{name: "check " + name, ttl: extraTTL}
	}
	return s
}

// cached memoizes one section for ttl. Concurrent callers wait for the
// in-progress evaluation rather than starting another probe.
type cached struct {
	name string
	ttl  time.Duration

	mu     sync.Mutex
	at     time.Time
	result Section
	last   string
}

func (c *cached) get(ctx context.Context, s *Service, eval func(context.Context) Section) Section {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && s.now().Sub(c.at) < c.ttl {
		return c.result
	}
	res := s.safely(ctx, c.name, eval)
	if res.Status != c.last && s.deps.Log != nil {
		lvl := slog.LevelInfo
		if res.Status == StatusDown || res.Status == StatusDegraded {
			lvl = slog.LevelWarn
		}
		s.deps.Log.Log(ctx, lvl, "resilience status changed", "category", "resilience", "section", c.name, "from", c.last, "to", res.Status)
	}
	c.at, c.result, c.last = s.now(), res, res.Status
	return res
}

// safely runs eval with a deadline and turns a panic or a hung dependency
// into a down section, so one faulty service cannot stall the dashboard.
func (s *Service) safely(ctx context.Context, name string, eval func(context.Context) Section) Section {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.timeout)
	defer cancel()
	done := make(chan Section, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				s.logErr(name, fmt.Errorf("panic: %v\n%s", p, debug.Stack()))
				done <- Section{Status: StatusDown, Error: "the check failed", CheckedAt: s.now().UTC()}
			}
		}()
		done <- eval(ctx)
	}()
	select {
	case out := <-done:
		return out
	case <-ctx.Done():
		s.logErr(name, ctx.Err())
		return Section{Status: StatusDown, Error: "the check timed out", CheckedAt: s.now().UTC()}
	}
}

func (s *Service) logErr(section string, err error) {
	if s.deps.Log != nil && err != nil {
		s.deps.Log.Warn("resilience check", "category", "resilience", "section", section, "err", err.Error())
	}
}

func (s *Service) section(status string, data any) Section {
	return Section{Status: status, CheckedAt: s.now().UTC(), Data: data}
}

func (s *Service) failed(section, status, msg string, err error) Section {
	s.logErr(section, err)
	return Section{Status: status, Error: msg, CheckedAt: s.now().UTC()}
}

// Snapshot evaluates every section concurrently. It never fails as a whole.
func (s *Service) Snapshot(ctx context.Context) Dashboard {
	d := Dashboard{GeneratedAt: s.now().UTC()}
	var wg sync.WaitGroup
	run := func(dst *Section, fn func(context.Context) Section) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			*dst = fn(ctx)
		}()
	}
	run(&d.Database, func(ctx context.Context) Section { return s.db.get(ctx, s, s.checkDatabase) })
	run(&d.Storage, func(ctx context.Context) Section { return s.storage.get(ctx, s, s.checkStorage) })
	run(&d.Coordinator, func(ctx context.Context) Section { return s.coordinator.get(ctx, s, s.checkCoordinator) })
	run(&d.Nodes, func(ctx context.Context) Section { return s.nodes.get(ctx, s, s.checkNodes) })
	extra := make(map[string]*Section, len(s.extra))
	for name, c := range s.extra {
		name, c := name, c
		sec := new(Section)
		extra[name] = sec
		check := s.deps.Checks[name]
		run(sec, func(ctx context.Context) Section {
			return c.get(ctx, s, func(ctx context.Context) Section { return s.checkExtra(ctx, name, check) })
		})
	}
	d.Control = s.checkControl()
	d.Progress = s.safely(ctx, "progress", s.checkProgress)
	d.Flight = s.safely(ctx, "flight recorder", s.checkFlight)
	d.Failovers = s.safely(ctx, "failovers", s.checkFailovers)
	d.Reliability = s.safely(ctx, "reliability", s.checkReliability)
	wg.Wait()
	if len(extra) > 0 {
		d.Extra = make(map[string]Section, len(extra))
		for name, sec := range extra {
			d.Extra[name] = *sec
		}
	}
	return d
}

func (s *Service) checkControl() Section {
	role := s.deps.Role
	if role == "" {
		role = "all"
	}
	return s.section(StatusOK, map[string]any{
		"version": version.Version, "role": role,
		"uptime_seconds": int64(s.now().Sub(s.started).Seconds()),
	})
}

type poolStats struct {
	MaxOpen        int   `json:"max_open"`
	Open           int   `json:"open"`
	InUse          int   `json:"in_use"`
	Idle           int   `json:"idle"`
	WaitCount      int64 `json:"wait_count"`
	WaitDurationMS int64 `json:"wait_duration_ms"`
}

func pool(sqlDB *sql.DB) poolStats {
	st := sqlDB.Stats()
	return poolStats{
		MaxOpen: st.MaxOpenConnections, Open: st.OpenConnections, InUse: st.InUse, Idle: st.Idle,
		WaitCount: st.WaitCount, WaitDurationMS: st.WaitDuration.Milliseconds(),
	}
}

// checkDatabase reads the breaker watcher when one exists, which already
// pings every second, and only pings directly for unwatched pools.
func (s *Service) checkDatabase(ctx context.Context) Section {
	if s.deps.DB == nil {
		return s.section(StatusUnconfigured, nil)
	}
	h := db.HealthOf(s.deps.DB)
	data := map[string]any{
		"driver": s.deps.DBDriver, "watched": h.Watched, "trips": h.Trips, "pool": pool(s.deps.DB),
	}
	if !h.Since.IsZero() {
		data["since"] = h.Since
	}
	available := h.Available
	var pingErr error
	if !h.Watched {
		pctx, cancel := context.WithTimeout(ctx, dbPingTimeout)
		pingErr = s.deps.DB.PingContext(pctx)
		cancel()
		available = pingErr == nil
	}
	data["available"] = available
	if !available {
		sec := s.failed("database", StatusDown, "the database is unavailable", pingErr)
		sec.Data = data
		return sec
	}
	return s.section(StatusOK, data)
}

func (s *Service) checkStorage(ctx context.Context) Section {
	store, provider := s.deps.Storage, s.deps.StorageProvider
	if s.deps.StorageFn != nil {
		var err error
		store, provider, err = s.deps.StorageFn()
		if err != nil {
			sec := s.failed("storage", StatusDown, "object storage is misconfigured", err)
			sec.Data = map[string]any{"provider": provider}
			return sec
		}
	}
	if store == nil {
		return s.section(StatusUnconfigured, map[string]any{"provider": provider})
	}
	data := map[string]any{"provider": provider}
	started := s.now()
	if err := probeStore(ctx, store); err != nil {
		sec := s.failed("storage", StatusDown, "object storage is unreachable", err)
		sec.Data = data
		return sec
	}
	data["latency_ms"] = s.now().Sub(started).Milliseconds()
	return s.section(StatusOK, data)
}

// probeStore writes and removes a tiny object, proving both connectivity
// and write permission without leaving data behind.
func probeStore(ctx context.Context, st storage.Store) error {
	body := []byte("ok")
	if err := st.Put(ctx, storageProbe, bytes.NewReader(body), int64(len(body)), "text/plain"); err != nil {
		return err
	}
	return st.Delete(ctx, storageProbe)
}

func (s *Service) checkCoordinator(ctx context.Context) Section {
	if s.deps.Coordinator == nil {
		return s.section(StatusUnconfigured, nil)
	}
	st, err := s.deps.Coordinator(ctx)
	if err != nil {
		return s.failed("coordinator", StatusDown, "the party coordinator is unreachable", err)
	}
	if !st.Available {
		sec := s.section(StatusDown, st)
		sec.Error = "the party coordinator reports it is unavailable"
		return sec
	}
	return s.section(StatusOK, st)
}

type nodeView struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Endpoint      string          `json:"endpoint"`
	Role          string          `json:"role"`
	Region        string          `json:"region,omitempty"`
	Status        string          `json:"status"`
	Enabled       bool            `json:"enabled"`
	Draining      bool            `json:"draining"`
	Priority      int             `json:"priority"`
	Weight        int             `json:"weight"`
	Capacity      int             `json:"capacity"`
	LatencyMS     int64           `json:"latency_ms"`
	Failures      int             `json:"failures"`
	LastFailureAt string          `json:"last_failure_at,omitempty"`
	LastError     string          `json:"last_error,omitempty"`
	UpdatedAt     string          `json:"updated_at"`
	Health        json.RawMessage `json:"health,omitempty"`
}

type nodesData struct {
	Total     int        `json:"total"`
	Healthy   int        `json:"healthy"`
	Unhealthy int        `json:"unhealthy"`
	Draining  int        `json:"draining"`
	Disabled  int        `json:"disabled"`
	Items     []nodeView `json:"items"`
}

func summarizeNodes(nodes []backend.Node) *nodesData {
	out := &nodesData{Items: make([]nodeView, 0, len(nodes))}
	for _, n := range nodes {
		v := nodeView{
			ID: n.ID, Name: n.Name, Endpoint: n.BaseURL(), Role: n.Role, Region: n.Region, Status: n.Status,
			Enabled: n.Enabled, Draining: n.Draining, Priority: n.Priority, Weight: n.Weight, Capacity: n.Capacity,
			LatencyMS: n.LatencyMS, Failures: n.Failures, LastFailureAt: n.LastFailureAt, LastError: n.LastError,
			UpdatedAt: n.UpdatedAt,
		}
		if h := strings.TrimSpace(n.Health); strings.HasPrefix(h, "{") && json.Valid([]byte(h)) {
			v.Health = json.RawMessage(h)
		}
		out.Total++
		switch {
		case !n.Enabled:
			out.Disabled++
		case n.Draining:
			out.Draining++
		case n.Status == "healthy":
			out.Healthy++
		default:
			out.Unhealthy++
		}
		out.Items = append(out.Items, v)
	}
	sort.SliceStable(out.Items, func(i, j int) bool {
		if out.Items[i].Priority != out.Items[j].Priority {
			return out.Items[i].Priority > out.Items[j].Priority
		}
		return out.Items[i].Name < out.Items[j].Name
	})
	return out
}

func nodesStatus(d *nodesData) string {
	active := d.Total - d.Disabled - d.Draining
	switch {
	case d.Total == 0:
		return StatusUnconfigured
	case d.Healthy == 0 && active > 0:
		return StatusDown
	case d.Unhealthy > 0 || d.Healthy == 0:
		return StatusDegraded
	}
	return StatusOK
}

// checkNodes serves the last known node list, marked stale, while the
// registry cannot be read.
func (s *Service) checkNodes(ctx context.Context) Section {
	if s.deps.Nodes == nil {
		return s.section(StatusUnconfigured, nil)
	}
	list, err := s.deps.Nodes(ctx)
	if err != nil {
		s.lastNodesMu.Lock()
		last := s.lastNodes
		s.lastNodesMu.Unlock()
		sec := s.failed("nodes", StatusDown, "the node registry is unavailable", err)
		if last != nil {
			sec.Status, sec.Stale, sec.Data = StatusDegraded, true, last
			sec.Error = "the node registry is unavailable; showing the last known state"
		}
		return sec
	}
	d := summarizeNodes(list)
	s.lastNodesMu.Lock()
	s.lastNodes = d
	s.lastNodesMu.Unlock()
	return s.section(nodesStatus(d), d)
}

func (s *Service) checkExtra(ctx context.Context, name string, check Check) Section {
	if check == nil {
		return s.section(StatusUnconfigured, nil)
	}
	status, data, err := check(ctx)
	if err != nil {
		return s.failed("check "+name, StatusDown, name+" is unavailable", err)
	}
	switch status {
	case StatusOK, StatusDegraded, StatusDown, StatusUnconfigured:
	default:
		status = StatusDegraded
	}
	return s.section(status, data)
}

type progressData struct {
	Reports int64 `json:"reports"`
	Writes  int64 `json:"writes"`
	// WritesPerReport shows the checkpoint budget; per-report writes are 1.
	WritesPerReport float64 `json:"writes_per_report"`
}

func (s *Service) checkProgress(context.Context) Section {
	if s.deps.Progress == nil {
		return s.section(StatusUnconfigured, nil)
	}
	st := s.deps.Progress()
	d := progressData{Reports: st.Reports, Writes: st.Writes}
	if st.Reports > 0 {
		d.WritesPerReport = math.Round(float64(st.Writes)/float64(st.Reports)*1000) / 1000
	}
	return s.section(StatusOK, d)
}

func (s *Service) checkFlight(context.Context) Section {
	if s.deps.Flight == nil {
		return s.section(StatusUnconfigured, nil)
	}
	return s.section(StatusOK, map[string]any{
		"stats":    s.deps.Flight.Stats(),
		"sessions": s.deps.Flight.Sessions(recentSessions),
	})
}

func (s *Service) checkFailovers(context.Context) Section {
	if s.deps.Flight == nil {
		return s.section(StatusUnconfigured, nil)
	}
	return s.section(StatusOK, map[string]any{"events": s.deps.Flight.Notable(recentFailover)})
}

// ReliabilityData is the dashboard and API view of reliability scores.
type ReliabilityData struct {
	HalfLifeSeconds int64                      `json:"half_life_seconds"`
	MinEvidence     float64                    `json:"min_evidence"`
	Sources         []reliability.SourceReport `json:"sources"`
	Overrides       []reliability.Override     `json:"overrides"`
	Truncated       bool                       `json:"truncated,omitempty"`
}

// lowScore marks a well-evidenced source as degrading the dashboard.
const lowScore = 60

func (s *Service) reliabilityData() (ReliabilityData, bool) {
	tr := s.deps.Reliability
	cfg := tr.Config()
	rep := tr.Report()
	d := ReliabilityData{
		HalfLifeSeconds: int64(cfg.HalfLife / time.Second), MinEvidence: cfg.MinEvidence,
		Overrides: tr.Overrides(),
	}
	low := false
	for _, r := range rep {
		if r.Score.Evidence >= cfg.MinEvidence && r.Score.Score < lowScore {
			low = true
		}
	}
	if len(rep) > reportLimit {
		rep, d.Truncated = rep[:reportLimit], true
	}
	d.Sources = rep
	return d, low
}

func (s *Service) checkReliability(context.Context) Section {
	if s.deps.Reliability == nil {
		return s.section(StatusUnconfigured, nil)
	}
	d, low := s.reliabilityData()
	status := StatusOK
	if low {
		status = StatusDegraded
	}
	return s.section(status, d)
}

var errNoTracker = errors.New("reliability tracking is not configured")
