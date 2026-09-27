package mesh

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/backend"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/diagnostics"
	"github.com/viewdock/viewdock/internal/nodeauth"
	"github.com/viewdock/viewdock/internal/playback"
	"github.com/viewdock/viewdock/internal/reliability"
)

func ids(nodes []backend.Node) string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.ID
	}
	return strings.Join(out, ",")
}

func tierNodes() []backend.Node {
	return []backend.Node{
		{ID: "a", Priority: 10, Weight: 1},
		{ID: "b", Priority: 10, Weight: 1},
		{ID: "c", Priority: 10, Weight: 1},
		{ID: "s", Priority: 5, Weight: 1},
	}
}

func fail(tr *reliability.Tracker, id string, n int) {
	for i := 0; i < n; i++ {
		tr.Observe(reliability.Outcome{Source: "node:" + id, Kind: reliability.KindStart, Success: false, Reason: "placement failed"})
	}
}

func succeed(tr *reliability.Tracker, id string, n int) {
	for i := 0; i < n; i++ {
		tr.Observe(reliability.Outcome{Source: "node:" + id, Kind: reliability.KindStart, Success: true, LatencyMS: 200})
	}
}

func TestRankKeepsWeightedOrderWithoutEvidence(t *testing.T) {
	d := NewDispatcher(nil, config.Config{}, nil)
	d.Ranker = reliability.New(reliability.Config{HalfLife: time.Hour})
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if got := ids(d.rankCandidates(req, tierNodes())); got != "a,b,c,s" {
		t.Fatalf("order changed without evidence: %s", got)
	}
}

func TestRankDemotesUnreliableWorkerWithinTier(t *testing.T) {
	tr := reliability.New(reliability.Config{HalfLife: time.Hour})
	fail(tr, "a", 12)
	succeed(tr, "b", 12)
	succeed(tr, "c", 12)
	succeed(tr, "s", 12)
	d := NewDispatcher(nil, config.Config{}, nil)
	d.Ranker = tr
	got := ids(d.rankCandidates(httptest.NewRequest(http.MethodPost, "/", nil), tierNodes()))
	if got != "b,c,a,s" {
		t.Fatalf("unreliable worker not demoted within its tier, or tiers mixed: %s", got)
	}
}

func TestRankSmallDifferenceKeepsBalancing(t *testing.T) {
	tr := reliability.New(reliability.Config{HalfLife: time.Hour})
	succeed(tr, "a", 20)
	fail(tr, "a", 1)
	succeed(tr, "b", 20)
	d := NewDispatcher(nil, config.Config{}, nil)
	d.Ranker = tr
	got := ids(d.rankCandidates(httptest.NewRequest(http.MethodPost, "/", nil), tierNodes()))
	if got != "a,b,c,s" {
		t.Fatalf("a small score gap overrode weighted balancing: %s", got)
	}
}

func TestRankOverrides(t *testing.T) {
	tr := reliability.New(reliability.Config{HalfLife: time.Hour})
	for src, mode := range map[string]string{"node:c": reliability.ModePrefer, "node:a": reliability.ModeAvoid, "node:s": reliability.ModeExclude} {
		o, err := reliability.Override{Source: src, Mode: mode}.Normalize()
		if err != nil {
			t.Fatal(err)
		}
		tr.SetOverride(o)
	}
	d := NewDispatcher(nil, config.Config{}, nil)
	d.Ranker = tr
	got := ids(d.rankCandidates(httptest.NewRequest(http.MethodPost, "/", nil), tierNodes()))
	if got != "c,b,a" {
		t.Fatalf("overrides not applied: %s", got)
	}
}

func TestWorkerTimelineThroughControlPlane(t *testing.T) {
	store, router := testRouter(t)
	var secret string
	at := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	worker := fakeWorker(t, &secret, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		switch r.URL.Path {
		case nodeDiagTimelinePath + "s1":
			_ = json.NewEncoder(w).Encode([]diagnostics.Event{{At: at, SessionID: "s1", Type: "stall"}})
		case nodeDiagTimelinePath + "other":
			_, _ = w.Write([]byte(`[]`))
		case nodeDiagSessionsPath:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []diagnostics.SessionSummary{{SessionID: "s1", LastAt: at, Events: 1, LastType: "stall"}}})
		default:
			http.NotFound(w, r)
		}
	})
	defer worker.Close()
	secret = addNode(t, store, "w1", worker.URL, 10)
	d := NewDispatcher(router, config.Config{}, nil)

	events := d.Timeline(context.Background(), "s1")
	if len(events) != 1 || events[0].Type != "stall" || events[0].Node != "w1" {
		t.Fatalf("timeline %+v", events)
	}
	if got := d.Timeline(context.Background(), "other"); len(got) != 0 {
		t.Fatalf("empty timeline returned %+v", got)
	}
	if got := d.Timeline(context.Background(), "../etc"); got != nil {
		t.Fatal("invalid session id forwarded")
	}
	sessions := d.RemoteSessions(context.Background())
	if len(sessions) != 1 || sessions[0].Node != "w1" {
		t.Fatalf("sessions %+v", sessions)
	}

	local := []diagnostics.SessionSummary{{SessionID: "s1", FirstAt: at.Add(-time.Minute), LastAt: at.Add(-time.Minute), Events: 2, LastType: "session_created"}}
	merged := diagnostics.MergeSessions(local, sessions, 10)
	if len(merged) != 1 || merged[0].Events != 3 || merged[0].Node != "w1" || merged[0].LastType != "stall" {
		t.Fatalf("merged %+v", merged)
	}
}

func TestWorkerDiagnosticsNeedSignature(t *testing.T) {
	store, router := testRouter(t)
	flight := diagnostics.New(10, 10)
	flight.Record("s1", "session_created", nil)
	var handler http.Handler
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer worker.Close()
	secret := addNode(t, store, "w1", worker.URL, 10)
	wk := &Worker{Verifier: nodeauth.NewVerifier(secret), Play: &playback.API{Flight: flight}}
	root := chi.NewRouter()
	root.Route("/api/v1", func(r chi.Router) {
		r.Use(wk.Guard)
		wk.Routes(r)
	})
	handler = root

	resp, err := http.Get(worker.URL + nodeDiagTimelinePath + "s1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned diagnostics request answered %d", resp.StatusCode)
	}
	if got := NewDispatcher(router, config.Config{}, nil).Timeline(context.Background(), "s1"); len(got) != 1 {
		t.Fatalf("signed request failed: %+v", got)
	}
}
