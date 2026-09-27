package reliability

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/diagnostics"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

func newTracker(cfg Config) (*Tracker, *fakeClock) {
	c := &fakeClock{t: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)}
	t := New(cfg)
	t.now = c.now
	return t, c
}

func starts(t *Tracker, source string, n int, success bool, region, device string) {
	for i := 0; i < n; i++ {
		t.Observe(Outcome{Source: source, Kind: KindStart, Success: success, LatencyMS: 500, Region: region, DeviceClass: device})
	}
}

func globalScore(t *testing.T, tr *Tracker, source string) ScopeScore {
	t.Helper()
	for _, r := range tr.Report() {
		if r.Source == source {
			return r.Score
		}
	}
	t.Fatalf("source %s missing from report", source)
	return ScopeScore{}
}

func TestDecayHalvesWeightPerHalfLife(t *testing.T) {
	s := stats{attempts: 8, successes: 4, stalls: 2, latSum: 800, latN: 8}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.updated = start
	s.decayTo(start.Add(time.Hour), time.Hour)
	if math.Abs(s.attempts-4) > 1e-9 || math.Abs(s.successes-2) > 1e-9 || math.Abs(s.stalls-1) > 1e-9 {
		t.Fatalf("one half-life: %+v", s)
	}
	s.decayTo(start.Add(3*time.Hour), time.Hour)
	if math.Abs(s.attempts-1) > 1e-9 || math.Abs(s.latN-1) > 1e-9 {
		t.Fatalf("three half-lives: %+v", s)
	}
	before := s
	s.decayTo(start, time.Hour)
	if s != before {
		t.Fatal("decay must not run backwards")
	}
}

func TestNeutralPriorAndBounds(t *testing.T) {
	cfg := Config{}.withDefaults()
	n := cfg.score(stats{})
	if n.Score <= 0 || n.Score >= 100 || n.SuccessRate != cfg.PriorSuccess || n.Evidence != 0 {
		t.Fatalf("neutral score = %+v", n)
	}
	perfect := cfg.score(stats{attempts: 1e6, successes: 1e6, latSum: 1, latN: 1e6, ttffSum: 1, ttffN: 1e6})
	if perfect.Score > 100 || perfect.Score < 99 {
		t.Fatalf("perfect score = %v", perfect.Score)
	}
	worst := cfg.score(stats{attempts: 1e6, failures: 1e6, stalls: 1e9, latSum: 1e15, latN: 1e6, ttffSum: 1e15, ttffN: 1e6})
	if worst.Score < 0 || worst.Score > 1 {
		t.Fatalf("worst score = %v", worst.Score)
	}
}

func TestFailuresLowerScoreAndAreListed(t *testing.T) {
	tr, _ := newTracker(Config{})
	starts(tr, "good", 20, true, "", "")
	starts(tr, "bad", 10, true, "", "")
	for i := 0; i < 10; i++ {
		tr.Observe(Outcome{Source: "bad", Kind: KindFailure, Reason: "ffmpeg exit\nstatus 1", SessionID: "s1", RequestID: "r1"})
	}
	good, bad := globalScore(t, tr, "good"), globalScore(t, tr, "bad")
	if good.Score <= bad.Score+15 {
		t.Fatalf("good %.1f should clearly beat bad %.1f", good.Score, bad.Score)
	}
	var rep SourceReport
	for _, r := range tr.Report() {
		if r.Source == "bad" {
			rep = r
		}
	}
	if len(rep.RecentFailures) != 10 || rep.LastFailure == nil {
		t.Fatalf("recent failures = %d", len(rep.RecentFailures))
	}
	if strings.Contains(rep.RecentFailures[0].Reason, "\n") || rep.RecentFailures[0].RequestID != "r1" {
		t.Fatalf("failure record = %+v", rep.RecentFailures[0])
	}
}

func TestOldOutageDoesNotPermanentlyPenalize(t *testing.T) {
	tr, clk := newTracker(Config{HalfLife: time.Hour})
	for i := 0; i < 50; i++ {
		tr.Observe(Outcome{Source: "node-a", Kind: KindStart, Success: false})
	}
	outage := globalScore(t, tr, "node-a").Score
	neutral := tr.neutral().Score
	if outage >= neutral-20 {
		t.Fatalf("outage score %.1f should be far below neutral %.1f", outage, neutral)
	}
	clk.add(12 * time.Hour)
	idle := globalScore(t, tr, "node-a").Score
	if math.Abs(idle-neutral) > 1 {
		t.Fatalf("after 12 half-lives without traffic score %.1f should return to neutral %.1f", idle, neutral)
	}
	clk.add(-6 * time.Hour)
	starts(tr, "node-a", 20, true, "", "")
	recovered := globalScore(t, tr, "node-a")
	if recovered.SuccessRate < 0.9 {
		t.Fatalf("recent successes should dominate a decayed outage: %+v", recovered)
	}
}

func TestRankingSeparatesRegionAndDevice(t *testing.T) {
	tr, _ := newTracker(Config{})
	starts(tr, "a", 10, true, "eu", "tv")
	starts(tr, "b", 10, true, "eu", "desktop")
	for i := 0; i < 10; i++ {
		tr.Observe(Outcome{Source: "a", Kind: KindFailure, Region: "eu", DeviceClass: "desktop"})
		tr.Observe(Outcome{Source: "b", Kind: KindFailure, Region: "eu", DeviceClass: "tv"})
	}
	starts(tr, "a", 10, true, "eu", "desktop")
	starts(tr, "b", 10, true, "eu", "tv")

	tv := tr.Rank(RankRequest{Candidates: []string{"b", "a"}, Region: "EU", DeviceClass: "tv"})
	if tv.Selected != "a" || tv.Ranking[0].Metrics.Scope != "region:eu|device:tv" {
		t.Fatalf("tv ranking = %+v", tv)
	}
	desk := tr.Rank(RankRequest{Candidates: []string{"a", "b"}, Region: "eu", DeviceClass: "desktop"})
	if desk.Selected != "b" {
		t.Fatalf("desktop ranking = %+v", desk)
	}
	if !strings.Contains(tv.Explanation, "selected a") || !strings.Contains(tv.Explanation, "success rate") {
		t.Fatalf("explanation = %q", tv.Explanation)
	}
}

func TestRankFallsBackToBroaderScopeWithoutEvidence(t *testing.T) {
	tr, _ := newTracker(Config{MinEvidence: 5})
	starts(tr, "a", 20, true, "us", "")
	starts(tr, "a", 1, false, "eu", "mobile")
	d := tr.Rank(RankRequest{Candidates: []string{"a"}, Region: "eu", DeviceClass: "mobile"})
	if d.Ranking[0].Metrics.Scope != "global" {
		t.Fatalf("sparse scope should fall back to global, got %s", d.Ranking[0].Metrics.Scope)
	}
	unknown := tr.Rank(RankRequest{Candidates: []string{"never-seen"}})
	if unknown.Ranking[0].Metrics.Evidence != 0 || !strings.Contains(unknown.Ranking[0].Explanation, "neutral prior") {
		t.Fatalf("unknown source = %+v", unknown.Ranking[0])
	}
}

func TestRankTieKeepsCallerOrderAndDedupes(t *testing.T) {
	tr, _ := newTracker(Config{})
	d := tr.Rank(RankRequest{Candidates: []string{"x", "y", "x", "", "z"}})
	if len(d.Ranking) != 3 || d.Ranking[0].Source != "x" || d.Ranking[1].Source != "y" || d.Ranking[2].Source != "z" {
		t.Fatalf("ranking = %+v", d.Ranking)
	}
	if !strings.Contains(d.Explanation, "configured order") {
		t.Fatalf("explanation = %q", d.Explanation)
	}
}

func TestOverridesTakePrecedence(t *testing.T) {
	tr, _ := newTracker(Config{})
	starts(tr, "best", 30, true, "", "")
	starts(tr, "worst", 5, false, "", "")
	starts(tr, "mid", 10, true, "", "")
	tr.Observe(Outcome{Source: "mid", Kind: KindFailure})

	if _, err := tr.SetOverride(Override{Source: "worst", Mode: "PREFER", Note: "new uplink"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.SetOverride(Override{Source: "best", Mode: ModeExclude}); err != nil {
		t.Fatal(err)
	}
	d := tr.Rank(RankRequest{Candidates: []string{"best", "mid", "worst"}})
	if d.Selected != "worst" || d.Ranking[0].Override != ModePrefer || d.Ranking[1].Source != "mid" {
		t.Fatalf("ranking = %+v", d.Ranking)
	}
	last := d.Ranking[2]
	if last.Source != "best" || !last.Excluded || last.Rank != 0 {
		t.Fatalf("excluded candidate = %+v", last)
	}
	if !strings.Contains(d.Explanation, "administrator preference") || !strings.Contains(d.Explanation, "1 excluded") {
		t.Fatalf("explanation = %q", d.Explanation)
	}
	if !strings.Contains(d.Ranking[0].Explanation, "new uplink") {
		t.Fatalf("note missing: %q", d.Ranking[0].Explanation)
	}

	tr.ClearOverride("worst")
	if _, err := tr.SetOverride(Override{Source: "mid", Mode: ModeAvoid}); err != nil {
		t.Fatal(err)
	}
	d = tr.Rank(RankRequest{Candidates: []string{"mid", "worst"}})
	if d.Selected != "worst" {
		t.Fatalf("avoided source should rank last: %+v", d.Ranking)
	}

	tr.SetOverride(Override{Source: "worst", Mode: ModeExclude})
	tr.SetOverride(Override{Source: "mid", Mode: ModeExclude})
	d = tr.Rank(RankRequest{Candidates: []string{"mid", "worst"}})
	if d.Selected != "" || !strings.Contains(d.Explanation, "every candidate is excluded") {
		t.Fatalf("all excluded = %+v", d)
	}
}

func TestOverrideValidation(t *testing.T) {
	tr, _ := newTracker(Config{})
	if _, err := tr.SetOverride(Override{Source: "a", Mode: "boost"}); err != ErrInvalidMode {
		t.Fatalf("mode err = %v", err)
	}
	if _, err := tr.SetOverride(Override{Source: " ", Mode: ModePrefer}); err != ErrInvalidSource {
		t.Fatalf("source err = %v", err)
	}
	if _, err := tr.SetOverride(Override{Source: strings.Repeat("x", 200), Mode: ModePrefer}); err != ErrInvalidSource {
		t.Fatalf("long source err = %v", err)
	}
	o, err := tr.SetOverride(Override{Source: "a", Mode: ModeAvoid, Note: strings.Repeat("é", 300)})
	if err != nil || len([]rune(o.Note)) != maxNoteLen {
		t.Fatalf("note not truncated: %d %v", len([]rune(o.Note)), err)
	}
	reps := tr.Report()
	if len(reps) != 1 || reps[0].Override == nil || reps[0].Score.Evidence != 0 {
		t.Fatalf("override-only source should be reported: %+v", reps)
	}
}

func TestObserveIgnoresInvalidInput(t *testing.T) {
	tr, _ := newTracker(Config{})
	tr.Observe(Outcome{Source: "", Kind: KindStart})
	tr.Observe(Outcome{Source: "a", Kind: "bogus"})
	tr.Observe(Outcome{Source: "bad\x00id", Kind: KindStart})
	if len(tr.Report()) != 0 {
		t.Fatal("invalid outcomes were recorded")
	}
	tr.Observe(Outcome{Source: "a", Kind: KindStart, Success: true, Region: "Not A Region!", DeviceClass: "TV"})
	rep := tr.Report()[0]
	if len(rep.Scopes) != 1 || rep.Scopes[0].Scope != "device:tv" {
		t.Fatalf("scopes = %+v", rep.Scopes)
	}
}

func TestBoundedSourcesAndScopes(t *testing.T) {
	tr, clk := newTracker(Config{MaxSources: 3, MaxScopes: 3})
	for _, s := range []string{"a", "b", "c", "d"} {
		starts(tr, s, 1, true, "", "")
		clk.add(time.Second)
	}
	if len(tr.Report()) != 3 {
		t.Fatalf("sources = %d", len(tr.Report()))
	}
	for _, s := range tr.Report() {
		if s.Source == "a" {
			t.Fatal("least recently seen source should be evicted")
		}
	}
	for _, region := range []string{"r1", "r2", "r3", "r4"} {
		starts(tr, "d", 1, true, region, "")
		clk.add(time.Second)
	}
	for _, s := range tr.Report() {
		if s.Source == "d" && len(s.Scopes)+1 > 3 {
			t.Fatalf("scopes not bounded: %d", len(s.Scopes))
		}
	}
}

func TestFlightObserverMapsEvents(t *testing.T) {
	tr, _ := newTracker(Config{})
	f := FlightObserver{Recorder: tr}
	f.SourceObserved(diagnostics.SourceObservation{Source: "file-1", Success: true, Latency: 800 * time.Millisecond})
	f.FlightEvent(diagnostics.Event{Origin: diagnostics.OriginClient, SessionID: "s", Type: "first_frame", Data: map[string]any{"source": "file-1", "ttff_ms": float64(1500), "device_class": "tv"}})
	f.FlightEvent(diagnostics.Event{Origin: diagnostics.OriginClient, SessionID: "s", Type: "stall", Data: map[string]any{"source": "file-1", "device_class": "tv"}})
	f.FlightEvent(diagnostics.Event{Origin: diagnostics.OriginClient, SessionID: "s", Type: "error", Data: map[string]any{"source": "file-1", "fatal": false}})
	f.FlightEvent(diagnostics.Event{Origin: diagnostics.OriginClient, SessionID: "s", Type: "error", RequestID: "req-9", Data: map[string]any{"source": "file-1", "fatal": true, "code": "MEDIA_ERR_DECODE"}})
	f.FlightEvent(diagnostics.Event{Origin: diagnostics.OriginServer, SessionID: "s", Type: "stall", Data: map[string]any{"source": "file-1"}})
	f.FlightEvent(diagnostics.Event{Origin: diagnostics.OriginClient, SessionID: "s", Type: "stall"})
	rep := tr.Report()[0]
	g := rep.Score
	if g.LatencyMS != 800 || g.TTFFMS != 1500 || g.Evidence != 2 {
		t.Fatalf("global = %+v", g)
	}
	if len(rep.RecentFailures) != 1 || rep.RecentFailures[0].Reason != "error: MEDIA_ERR_DECODE" || rep.RecentFailures[0].RequestID != "req-9" {
		t.Fatalf("failures = %+v", rep.RecentFailures)
	}
	if len(rep.Scopes) != 1 || rep.Scopes[0].DeviceClass != "tv" || rep.Scopes[0].StallsPerSession <= 0 {
		t.Fatalf("scopes = %+v", rep.Scopes)
	}
}

func TestDeviceClass(t *testing.T) {
	cases := map[string]string{
		"": "",
		"Mozilla/5.0 (SMART-TV; Linux; Tizen 6.0)":                                                "tv",
		"Mozilla/5.0 (Web0S; Linux/SmartTV)":                                                      "tv",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Mobile/15E148":                    "mobile",
		"Mozilla/5.0 (Linux; Android 14; Pixel 8) Mobile Safari/537.36":                           "mobile",
		"Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X)":                                           "tablet",
		"Mozilla/5.0 (Linux; Android 13; SM-X700) Safari/537.36":                                  "tablet",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/128.0 Safari/537.36": "desktop",
	}
	for ua, want := range cases {
		if got := DeviceClass(ua); got != want {
			t.Errorf("DeviceClass(%q) = %q, want %q", ua, got, want)
		}
	}
}

func TestSQLStoreRoundTripAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vd.db")
	if err := db.Migrate(path); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, err := db.Open(path, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	ctx := context.Background()
	store := NewSQLStore(sqlDB)
	if err := store.Put(ctx, Override{Source: "a", Mode: ModePrefer, Note: "one", ActorID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, Override{Source: "a", Mode: ModeAvoid, Note: "two", ActorID: "u2"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, Override{Source: "b", Mode: "nope"}); err != ErrInvalidMode {
		t.Fatalf("invalid mode stored: %v", err)
	}
	if err := store.Put(ctx, Override{Source: "c", Mode: ModeExclude}); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	list, err := store.List(ctx)
	if err != nil || len(list) != 1 || list[0].Mode != ModeAvoid || list[0].Note != "two" || list[0].ActorID != "u2" || list[0].UpdatedAt.IsZero() {
		t.Fatalf("list = %+v, %v", list, err)
	}
	tr, _ := newTracker(Config{})
	tr.SetOverride(Override{Source: "stale", Mode: ModePrefer})
	if err := tr.LoadOverrides(ctx, store); err != nil {
		t.Fatal(err)
	}
	if ovs := tr.Overrides(); len(ovs) != 1 || ovs[0].Source != "a" {
		t.Fatalf("loaded overrides = %+v", ovs)
	}
}
