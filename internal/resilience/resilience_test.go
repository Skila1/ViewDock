package resilience

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/backend"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/diagnostics"
	"github.com/viewdock/viewdock/internal/playback"
	"github.com/viewdock/viewdock/internal/reliability"
	"github.com/viewdock/viewdock/internal/storage"
)

const secretErr = "dial tcp 10.0.0.9:5432: password authentication failed for user viewdock"

type fakeStore struct {
	puts atomic.Int64
	err  error
}

func (f *fakeStore) Put(context.Context, string, io.Reader, int64, string) error {
	f.puts.Add(1)
	return f.err
}
func (f *fakeStore) Get(context.Context, string) (storage.Object, error) {
	return storage.Object{}, nil
}
func (f *fakeStore) Delete(context.Context, string) error { return nil }

func openSQLite(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "vd.db"), 1000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return sqlDB
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newService(d Deps) (*Service, *clock) {
	s := New(d)
	c := &clock{t: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)}
	s.now = c.now
	s.timeout = 300 * time.Millisecond
	return s, c
}

func noLeak(t *testing.T, sec Section) {
	t.Helper()
	if strings.Contains(sec.Error, "10.0.0.9") || strings.Contains(sec.Error, "password") {
		t.Fatalf("raw internal error exposed: %q", sec.Error)
	}
}

func TestUnconfiguredSections(t *testing.T) {
	s, _ := newService(Deps{})
	d := s.Snapshot(context.Background())
	for name, sec := range map[string]Section{
		"database": d.Database, "storage": d.Storage, "coordinator": d.Coordinator, "nodes": d.Nodes,
		"progress": d.Progress, "flight": d.Flight, "failovers": d.Failovers, "reliability": d.Reliability,
	} {
		if sec.Status != StatusUnconfigured {
			t.Errorf("%s status = %s", name, sec.Status)
		}
	}
	if d.Control.Status != StatusOK {
		t.Fatalf("control = %+v", d.Control)
	}
}

func TestPartialFailureKeepsOtherSectionsUseful(t *testing.T) {
	closed := openSQLite(t)
	closed.Close()
	flight := diagnostics.New(10, 10)
	flight.Record("s1", "source_failover", map[string]any{"ok": true})
	tr := reliability.New(reliability.Config{})
	tr.Observe(reliability.Outcome{Source: "file-1", Kind: reliability.KindStart, Success: true})
	s, _ := newService(Deps{
		DB:          closed,
		DBDriver:    "sqlite",
		Storage:     &fakeStore{err: errors.New(secretErr)},
		Coordinator: func(context.Context) (CoordinatorStatus, error) { return CoordinatorStatus{}, errors.New(secretErr) },
		Nodes:       func(context.Context) ([]backend.Node, error) { return nil, errors.New(secretErr) },
		Progress:    func() playback.ProgressStats { return playback.ProgressStats{Reports: 600, Writes: 11} },
		Flight:      flight,
		Reliability: tr,
		Checks: map[string]Check{
			"broadcast": func(context.Context) (string, any, error) { panic("boom") },
			"hung": func(ctx context.Context) (string, any, error) {
				time.Sleep(2 * time.Second)
				return StatusOK, nil, nil
			},
		},
	})
	started := time.Now()
	d := s.Snapshot(context.Background())
	if time.Since(started) > 1500*time.Millisecond {
		t.Fatalf("a hung dependency stalled the dashboard for %v", time.Since(started))
	}
	for name, sec := range map[string]Section{"database": d.Database, "storage": d.Storage, "coordinator": d.Coordinator, "nodes": d.Nodes} {
		if sec.Status != StatusDown || sec.Error == "" {
			t.Errorf("%s = %+v", name, sec)
		}
		noLeak(t, sec)
	}
	if d.Extra["broadcast"].Status != StatusDown || d.Extra["hung"].Status != StatusDown || d.Extra["hung"].Error != "the check timed out" {
		t.Fatalf("extra = %+v", d.Extra)
	}
	if d.Progress.Status != StatusOK || d.Progress.Data.(progressData).WritesPerReport != 0.018 {
		t.Fatalf("progress = %+v", d.Progress)
	}
	if d.Failovers.Status != StatusOK || d.Flight.Status != StatusOK || d.Reliability.Status != StatusOK {
		t.Fatalf("in-memory sections should stay ok: %+v %+v %+v", d.Failovers, d.Flight, d.Reliability)
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "password") {
		t.Fatal("serialized dashboard leaks an internal error")
	}
}

func TestHealthySections(t *testing.T) {
	st := &fakeStore{}
	s, _ := newService(Deps{
		DB: openSQLite(t), DBDriver: "sqlite", Storage: st, StorageProvider: "local",
		Coordinator: func(context.Context) (CoordinatorStatus, error) {
			return CoordinatorStatus{Available: true, Rooms: 2, Participants: 5}, nil
		},
		Nodes: func(context.Context) ([]backend.Node, error) {
			return []backend.Node{
				{ID: "a", Name: "a", Scheme: "https", Host: "w1", Port: 8080, Enabled: true, Status: "healthy", Priority: 10, Health: `{"sessions":3}`},
				{ID: "b", Name: "b", Scheme: "https", Host: "w2", Port: 8080, Enabled: true, Status: "healthy", Priority: 5, Health: "ok"},
			}, nil
		},
	})
	d := s.Snapshot(context.Background())
	if d.Database.Status != StatusOK || d.Storage.Status != StatusOK || d.Coordinator.Status != StatusOK || d.Nodes.Status != StatusOK {
		t.Fatalf("dashboard = %+v", d)
	}
	nodes := d.Nodes.Data.(*nodesData)
	if nodes.Healthy != 2 || nodes.Items[0].ID != "a" || string(nodes.Items[0].Health) != `{"sessions":3}` || nodes.Items[1].Health != nil {
		t.Fatalf("nodes = %+v", nodes)
	}
	if st.puts.Load() != 1 {
		t.Fatalf("storage probes = %d", st.puts.Load())
	}
}

func TestStorageResolvedFromRuntimeSettings(t *testing.T) {
	st := &fakeStore{}
	s, _ := newService(Deps{StorageFn: func() (storage.Store, string, error) { return st, "s3", nil }})
	sec := s.Snapshot(context.Background()).Storage
	if sec.Status != StatusOK || sec.Data.(map[string]any)["provider"] != "s3" || st.puts.Load() != 1 {
		t.Fatalf("storage = %+v", sec)
	}
	s, _ = newService(Deps{StorageFn: func() (storage.Store, string, error) {
		return nil, "s3", errors.New("secret=hunter2 bucket missing")
	}})
	sec = s.Snapshot(context.Background()).Storage
	if sec.Status != StatusDown || strings.Contains(sec.Error, "hunter2") {
		t.Fatalf("misconfigured storage = %+v", sec)
	}
}

func TestExtraCheckReported(t *testing.T) {
	s, _ := newService(Deps{Checks: map[string]Check{
		"broadcast": func(context.Context) (string, any, error) {
			return StatusDegraded, map[string]any{"state": "backoff"}, nil
		},
	}})
	if sec := s.Snapshot(context.Background()).Extra["broadcast"]; sec.Status != StatusDegraded {
		t.Fatalf("broadcast = %+v", sec)
	}
}

func TestCoordinatorReportsUnavailable(t *testing.T) {
	s, _ := newService(Deps{Coordinator: func(context.Context) (CoordinatorStatus, error) {
		return CoordinatorStatus{Available: false}, nil
	}})
	if sec := s.Snapshot(context.Background()).Coordinator; sec.Status != StatusDown || sec.Error == "" {
		t.Fatalf("coordinator = %+v", sec)
	}
}

func TestProbesAreCachedAndDeduplicated(t *testing.T) {
	st := &fakeStore{}
	var nodeCalls atomic.Int64
	s, c := newService(Deps{Storage: st, Nodes: func(context.Context) ([]backend.Node, error) {
		nodeCalls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return nil, nil
	}})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Snapshot(context.Background())
		}()
	}
	wg.Wait()
	if st.puts.Load() != 1 || nodeCalls.Load() != 1 {
		t.Fatalf("concurrent snapshots probed storage %d and nodes %d times", st.puts.Load(), nodeCalls.Load())
	}
	c.add(nodesTTL + time.Second)
	s.Snapshot(context.Background())
	if nodeCalls.Load() != 2 || st.puts.Load() != 1 {
		t.Fatalf("after node TTL: nodes %d storage %d", nodeCalls.Load(), st.puts.Load())
	}
	c.add(storageTTL)
	s.Snapshot(context.Background())
	if st.puts.Load() != 2 {
		t.Fatalf("storage should be probed again after its TTL, got %d", st.puts.Load())
	}
}

func TestNodesServeStaleDataDuringRegistryOutage(t *testing.T) {
	fail := false
	s, c := newService(Deps{Nodes: func(context.Context) ([]backend.Node, error) {
		if fail {
			return nil, errors.New(secretErr)
		}
		return []backend.Node{{ID: "a", Name: "a", Scheme: "https", Host: "w1", Port: 1, Enabled: true, Status: "healthy"}}, nil
	}})
	if sec := s.Snapshot(context.Background()).Nodes; sec.Status != StatusOK || sec.Stale {
		t.Fatalf("nodes = %+v", sec)
	}
	fail = true
	c.add(nodesTTL + time.Second)
	sec := s.Snapshot(context.Background()).Nodes
	if sec.Status != StatusDegraded || !sec.Stale || sec.Data.(*nodesData).Total != 1 {
		t.Fatalf("stale nodes = %+v", sec)
	}
	noLeak(t, sec)
}

func TestNodesStatus(t *testing.T) {
	mk := func(status string, enabled, draining bool) backend.Node {
		return backend.Node{Status: status, Enabled: enabled, Draining: draining, Scheme: "https", Host: "h", Port: 1}
	}
	cases := []struct {
		nodes []backend.Node
		want  string
	}{
		{nil, StatusUnconfigured},
		{[]backend.Node{mk("healthy", true, false)}, StatusOK},
		{[]backend.Node{mk("healthy", true, false), mk("unhealthy", true, false)}, StatusDegraded},
		{[]backend.Node{mk("unhealthy", true, false)}, StatusDown},
		{[]backend.Node{mk("healthy", true, false), mk("unhealthy", false, false)}, StatusOK},
		{[]backend.Node{mk("healthy", true, true)}, StatusDegraded},
	}
	for i, c := range cases {
		if got := nodesStatus(summarizeNodes(c.nodes)); got != c.want {
			t.Errorf("case %d: %s, want %s", i, got, c.want)
		}
	}
}

func TestReliabilityDegradesOnLowWellEvidencedScore(t *testing.T) {
	tr := reliability.New(reliability.Config{})
	for i := 0; i < 20; i++ {
		tr.Observe(reliability.Outcome{Source: "bad", Kind: reliability.KindStart, Success: false})
	}
	s, _ := newService(Deps{Reliability: tr})
	if sec := s.Snapshot(context.Background()).Reliability; sec.Status != StatusDegraded {
		t.Fatalf("reliability = %+v", sec)
	}
}

type memOverrides struct {
	items map[string]reliability.Override
	err   error
}

func (m *memOverrides) List(context.Context) ([]reliability.Override, error) {
	out := []reliability.Override{}
	for _, o := range m.items {
		out = append(out, o)
	}
	return out, m.err
}
func (m *memOverrides) Put(_ context.Context, o reliability.Override) error {
	if m.err != nil {
		return m.err
	}
	m.items[o.Source] = o
	return nil
}
func (m *memOverrides) Delete(_ context.Context, source string) error {
	if m.err != nil {
		return m.err
	}
	delete(m.items, source)
	return nil
}

func serve(a *API, p *auth.Principal, method, target, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	a.Routes(r)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if p != nil {
		req = req.WithContext(auth.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestRoutesRequireAdmin(t *testing.T) {
	s, _ := newService(Deps{})
	a := &API{Service: s}
	user := &auth.Principal{Kind: auth.KindUser, UserID: "u"}
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "a", IsAdmin: true}
	for _, path := range []string{"/admin/resilience", "/admin/resilience/sessions", "/admin/resilience/reliability"} {
		if rec := serve(a, nil, http.MethodGet, path, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: %d", path, rec.Code)
		}
		if rec := serve(a, user, http.MethodGet, path, ""); rec.Code != http.StatusForbidden {
			t.Errorf("%s user: %d", path, rec.Code)
		}
	}
	if rec := serve(a, admin, http.MethodGet, "/admin/resilience", ""); rec.Code != http.StatusOK {
		t.Fatalf("admin dashboard: %d", rec.Code)
	}
	if rec := serve(a, admin, http.MethodGet, "/admin/resilience/reliability", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("reliability without tracker: %d", rec.Code)
	}
	if rec := serve(a, user, http.MethodPut, "/admin/resilience/reliability/overrides", `{"source":"a","mode":"prefer"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("user override: %d", rec.Code)
	}
}

func TestOverrideEndpoints(t *testing.T) {
	tr := reliability.New(reliability.Config{})
	store := &memOverrides{items: map[string]reliability.Override{}}
	s, _ := newService(Deps{Reliability: tr})
	a := &API{Service: s, Overrides: store}
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "admin-1", IsAdmin: true}

	if rec := serve(a, admin, http.MethodPut, "/admin/resilience/reliability/overrides", `{"source":"a","mode":"boost"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid mode: %d", rec.Code)
	}
	if rec := serve(a, admin, http.MethodPut, "/admin/resilience/reliability/overrides", `{bad`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid json: %d", rec.Code)
	}
	rec := serve(a, admin, http.MethodPut, "/admin/resilience/reliability/overrides", `{"source":"node-a","mode":"exclude","note":"maintenance"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"persisted":true`) {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	if store.items["node-a"].ActorID != "admin-1" || len(tr.Overrides()) != 1 {
		t.Fatalf("override not applied: %+v %+v", store.items, tr.Overrides())
	}

	rec = serve(a, admin, http.MethodGet, "/admin/resilience/reliability/rank?candidates=node-a,node-b&region=eu", "")
	var dec reliability.Decision
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &dec) != nil || dec.Selected != "node-b" || !dec.Ranking[1].Excluded {
		t.Fatalf("rank: %d %s", rec.Code, rec.Body)
	}
	if rec := serve(a, admin, http.MethodGet, "/admin/resilience/reliability/rank", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("rank without candidates: %d", rec.Code)
	}

	store.err = errors.New(secretErr)
	rec = serve(a, admin, http.MethodPut, "/admin/resilience/reliability/overrides", `{"source":"node-b","mode":"prefer"}`)
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("store failure: %d %s", rec.Code, rec.Body)
	}
	if len(tr.Overrides()) != 1 {
		t.Fatal("override applied in memory although it was not persisted")
	}
	if rec := serve(a, admin, http.MethodDelete, "/admin/resilience/reliability/overrides?source=node-a", ""); rec.Code != http.StatusServiceUnavailable || len(tr.Overrides()) != 1 {
		t.Fatalf("delete during outage: %d", rec.Code)
	}
	store.err = nil
	if rec := serve(a, admin, http.MethodDelete, "/admin/resilience/reliability/overrides?source=node-a", ""); rec.Code != http.StatusNoContent || len(tr.Overrides()) != 0 || len(store.items) != 0 {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := serve(a, admin, http.MethodDelete, "/admin/resilience/reliability/overrides", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("delete without source: %d", rec.Code)
	}
}

func TestInMemoryOverridesWithoutStore(t *testing.T) {
	tr := reliability.New(reliability.Config{})
	s, _ := newService(Deps{Reliability: tr})
	a := &API{Service: s}
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "a", IsAdmin: true}
	rec := serve(a, admin, http.MethodPut, "/admin/resilience/reliability/overrides", `{"source":"x","mode":"avoid"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"persisted":false`) || len(tr.Overrides()) != 1 {
		t.Fatalf("in-memory override: %d %s", rec.Code, rec.Body)
	}
}
