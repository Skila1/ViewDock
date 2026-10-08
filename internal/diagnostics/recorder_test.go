package diagnostics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

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

func newClocked(maxSessions, maxEvents int) (*Recorder, *clock) {
	c := &clock{t: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)}
	r := New(maxSessions, maxEvents)
	r.now = c.now
	return r, c
}

func TestRecorderBoundsEventsAndSessions(t *testing.T) {
	r := New(1, 2)
	r.Record("one", "created", map[string]any{"source": "a"})
	r.Record("one", "buffer", nil)
	r.Record("one", "failed", nil)
	if got := r.Events("one"); len(got) != 2 || got[0].Type != "buffer" {
		t.Fatalf("bounded events = %#v", got)
	}
	r.Record("two", "created", nil)
	if len(r.Events("one")) != 0 || len(r.Events("two")) != 1 {
		t.Fatal("oldest session was not evicted")
	}
}

func TestSourceReliability(t *testing.T) {
	r := New(1, 2)
	r.ObserveSource("primary", true, false, 100*time.Millisecond)
	r.ObserveSource("primary", false, true, 300*time.Millisecond)
	list := r.Sources()
	if len(list) != 1 || list[0].Attempts != 2 || list[0].Failures != 1 || list[0].Stalls != 1 {
		t.Fatalf("source score = %#v", list)
	}
	if list[0].SuccessRate != 0.5 || list[0].AverageMS != 200 {
		t.Fatalf("source metrics = %#v", list[0])
	}
}

func TestEvictsLeastRecentlyActiveSession(t *testing.T) {
	r, c := newClocked(2, 10)
	r.Record("a", "created", nil)
	c.add(time.Second)
	r.Record("b", "created", nil)
	c.add(time.Second)
	r.Record("a", "stall", nil)
	c.add(time.Second)
	r.Record("c", "created", nil)
	if len(r.Events("b")) != 0 {
		t.Fatal("least recently active session b should be evicted")
	}
	if len(r.Events("a")) != 2 || len(r.Events("c")) != 1 {
		t.Fatal("active sessions must be kept")
	}
}

func TestRejectsInvalidEvents(t *testing.T) {
	r := New(10, 10)
	for _, typ := range []string{"", "Upper", "has space", "1leading", string(make([]byte, 60))} {
		if r.RecordEvent(Event{SessionID: "s", Type: typ}) {
			t.Fatalf("type %q accepted", typ)
		}
	}
	if r.RecordEvent(Event{Type: "stall"}) {
		t.Fatal("event without session accepted")
	}
}

func TestRetentionDropsIdleSessions(t *testing.T) {
	r, c := newClocked(10, 10)
	r.SetRetention(time.Hour)
	r.Record("old", "pipeline_failed", nil)
	c.add(2 * time.Hour)
	r.Record("new", "created", nil)
	if len(r.Events("old")) != 0 {
		t.Fatal("idle session outlived retention")
	}
	if len(r.Notable(0)) != 0 {
		t.Fatal("notable events outlived retention")
	}
	if len(r.Events("new")) != 1 {
		t.Fatal("fresh session missing")
	}
}

func TestSamplingThrottlesHighFrequencyTypes(t *testing.T) {
	r, c := newClocked(10, 100)
	r.SetSampling("buffer", 5*time.Second)
	kept := 0
	for i := 0; i < 20; i++ {
		if r.RecordEvent(Event{SessionID: "s", Type: "buffer"}) {
			kept++
		}
		c.add(time.Second)
	}
	if kept != 4 {
		t.Fatalf("kept %d buffer samples over 20 s, want 4", kept)
	}
	if r.Stats().Sampled != 16 {
		t.Fatalf("sampled counter = %d", r.Stats().Sampled)
	}
	if !r.RecordEvent(Event{SessionID: "s", Type: "stall"}) {
		t.Fatal("unsampled type was dropped")
	}
}

func TestAdmitTokenBucket(t *testing.T) {
	r, c := newClocked(10, 100)
	r.SetIngestBudget(10, 2)
	if got := r.Admit("s", 25); got != 10 {
		t.Fatalf("first admit = %d, want burst 10", got)
	}
	if got := r.Admit("s", 5); got != 0 {
		t.Fatalf("empty bucket admitted %d", got)
	}
	c.add(3 * time.Second)
	if got := r.Admit("s", 10); got != 6 {
		t.Fatalf("refilled admit = %d, want 6", got)
	}
	c.add(time.Hour)
	if got := r.Admit("s", 100); got != 10 {
		t.Fatalf("refill must cap at burst, got %d", got)
	}
	if got := r.Admit("other", 3); got != 3 {
		t.Fatal("budgets must be per session")
	}
	if r.Stats().Dropped != 15+5+4+90 {
		t.Fatalf("dropped = %d", r.Stats().Dropped)
	}
}

func TestNotableFeedAndSessionSummaries(t *testing.T) {
	r, c := newClocked(10, 100)
	r.Record("a", "session_created", nil)
	c.add(time.Second)
	r.Record("a", "source_failover", map[string]any{"ok": true})
	c.add(time.Second)
	r.RecordEvent(Event{SessionID: "b", Type: "error", Origin: OriginClient, Data: map[string]any{"fatal": true}})
	c.add(time.Second)
	r.RecordEvent(Event{SessionID: "b", Type: "error", Origin: OriginClient, Data: map[string]any{"fatal": false}})
	feed := r.Notable(10)
	if len(feed) != 2 || feed[0].SessionID != "b" || feed[1].Type != "source_failover" {
		t.Fatalf("notable feed = %#v", feed)
	}
	sums := r.Sessions(0)
	if len(sums) != 2 || sums[0].SessionID != "b" || sums[0].Events != 2 || sums[0].Notable != 1 || sums[1].Notable != 1 {
		t.Fatalf("summaries = %#v", sums)
	}
	if got := r.Sessions(1); len(got) != 1 {
		t.Fatal("limit ignored")
	}
}

func TestEventsSortedByTimeAndIsolated(t *testing.T) {
	r, _ := newClocked(10, 100)
	base := time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC)
	r.RecordEvent(Event{SessionID: "s", Type: "stall", At: base.Add(2 * time.Second), Data: map[string]any{"k": 1}})
	r.RecordEvent(Event{SessionID: "s", Type: "first_frame", At: base})
	got := r.Events("s")
	if got[0].Type != "first_frame" || got[1].Type != "stall" {
		t.Fatalf("order = %#v", got)
	}
	got[1].Data["k"] = 99
	if r.Events("s")[1].Data["k"] != 1 {
		t.Fatal("returned data aliases recorder state")
	}
}

type recObserver struct {
	events  []Event
	sources []SourceObservation
}

func (o *recObserver) FlightEvent(e Event)                { o.events = append(o.events, e) }
func (o *recObserver) SourceObserved(s SourceObservation) { o.sources = append(o.sources, s) }

func TestObserversReceiveStoredEventsOnly(t *testing.T) {
	r, _ := newClocked(10, 100)
	o := &recObserver{}
	r.AddObserver(o)
	r.SetSampling("buffer", time.Minute)
	r.Record("s", "buffer", nil)
	r.Record("s", "buffer", nil)
	r.Record("s", "Bad Type", nil)
	r.ObserveSource("file-1", false, true, 50*time.Millisecond)
	if len(o.events) != 1 || len(o.sources) != 1 || o.sources[0].Source != "file-1" || o.sources[0].Success {
		t.Fatalf("observer saw events=%v sources=%v", o.events, o.sources)
	}
}

func TestRequestIDCarriedFromContext(t *testing.T) {
	r := New(10, 10)
	var got string
	h := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.RecordContext(req.Context(), "s", "session_created", nil)
		got = r.Events("s")[0].RequestID
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "abc-123")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got != "abc-123" {
		t.Fatalf("request id = %q", got)
	}
	if RequestID(context.Background()) != "" {
		t.Fatal("empty context produced a request id")
	}
	if CleanRequestID("bad id\n") != "" || CleanRequestID("<script>") != "" {
		t.Fatal("unsafe request id accepted")
	}
}
