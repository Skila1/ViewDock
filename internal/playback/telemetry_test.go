package playback

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/capability"
	"github.com/viewdock/viewdock/internal/diagnostics"
	"github.com/viewdock/viewdock/internal/reliability"
)

func telemetryFixture(t *testing.T) (*API, *Session, *auth.Principal) {
	t.Helper()
	a := testAPI(t, &mockLocator{}, nil)
	owner := &auth.Principal{Kind: "user", UserID: "u1"}
	s := &Session{
		ID: "sess-1", Kind: "user", UserID: "u1", Owner: owner, MediaFileID: "file-9",
		Stoken: "tok", StokenExp: time.Now().Add(time.Minute), Created: time.Now(), LastPing: time.Now(),
		Client: capability.Profile{UserAgent: "Mozilla/5.0 (SMART-TV; Linux; Tizen 6.0)"},
	}
	a.Reg.Put(s)
	return a, s, owner
}

func postTelemetry(a *API, p *auth.Principal, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	a.SessionRoutes(r)
	var h http.Handler = r
	if p != nil {
		h = withUser(p, r)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeResult(t *testing.T, rec *httptest.ResponseRecorder) telemetryResult {
	t.Helper()
	var res telemetryResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return res
}

func TestTelemetryRecordsSanitizedEvents(t *testing.T) {
	a, _, owner := telemetryFixture(t)
	now := time.Now().UnixMilli()
	body := fmt.Sprintf(`{"correlation_id":"attempt-7","events":[
		{"type":"first_frame","at":%d,"data":{"ttff_ms":1234,"source":"spoofed","region":"mars","auth_token":"x","nested":{"a":1},"list":[1],"url":"/hls/sess-1/seg1.m4s?stoken=secret","note":"line\nbreak"}},
		{"type":"stall","at":1,"data":{"buffer_ms":0}},
		{"type":"made_up","data":{}},
		{"type":"","data":{}}
	]}`, now)
	rec := postTelemetry(a, owner, "/playback/sessions/sess-1/telemetry", body, map[string]string{"X-Request-Id": "req-42"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if res := decodeResult(t, rec); res.Accepted != 2 || res.Rejected != 2 || res.Dropped != 0 {
		t.Fatalf("result = %+v", res)
	}
	events := a.Flight.Events("sess-1")
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	var ff, stall diagnostics.Event
	for _, e := range events {
		switch e.Type {
		case "first_frame":
			ff = e
		case "stall":
			stall = e
		}
	}
	if ff.Origin != diagnostics.OriginClient || ff.RequestID != "req-42" || ff.CorrelationID != "attempt-7" {
		t.Fatalf("correlation = %+v", ff)
	}
	if ff.At.UnixMilli() != now {
		t.Fatalf("client time within skew should be kept: %v", ff.At)
	}
	d := ff.Data
	if d["source"] != "file-9" || d["device_class"] != "tv" || d["region"] != nil {
		t.Fatalf("server-owned fields = %+v", d)
	}
	if _, ok := d["auth_token"]; ok {
		t.Fatal("sensitive key kept")
	}
	if _, ok := d["nested"]; ok {
		t.Fatal("nested value kept")
	}
	if _, ok := d["list"]; ok {
		t.Fatal("array value kept")
	}
	if d["url"] != "/hls/sess-1/seg1.m4s" {
		t.Fatalf("url not stripped: %v", d["url"])
	}
	if d["note"] != "linebreak" || d["ttff_ms"] != float64(1234) {
		t.Fatalf("values = %+v", d)
	}
	if stall.Data["clock_adjusted"] != true {
		t.Fatalf("skewed client time should be replaced: %+v", stall)
	}
}

func TestTelemetryRejectsBadRequests(t *testing.T) {
	a, _, owner := telemetryFixture(t)
	path := "/playback/sessions/sess-1/telemetry"
	many := make([]string, telemetryMaxEvents+1)
	for i := range many {
		many[i] = `{"type":"seek"}`
	}
	cases := []struct {
		name string
		body string
		code int
	}{
		{"invalid json", `{"events":`, http.StatusBadRequest},
		{"no events", `{"events":[]}`, http.StatusBadRequest},
		{"only invalid events", `{"events":[{"type":"nope"}]}`, http.StatusBadRequest},
		{"too many events", `{"events":[` + strings.Join(many, ",") + `]}`, http.StatusRequestEntityTooLarge},
		{"body too large", `{"events":[{"type":"seek","data":{"x":"` + strings.Repeat("a", telemetryMaxBody) + `"}}]}`, http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		if rec := postTelemetry(a, owner, path, c.body, nil); rec.Code != c.code {
			t.Errorf("%s: status %d, want %d (%s)", c.name, rec.Code, c.code, rec.Body)
		}
	}
	if n := len(a.Flight.Events("sess-1")); n != 0 {
		t.Fatalf("rejected requests recorded %d events", n)
	}
}

func TestTelemetryAuthorization(t *testing.T) {
	a, _, owner := telemetryFixture(t)
	body := `{"events":[{"type":"seek","data":{"to_ms":5000}}]}`
	other := &auth.Principal{Kind: "user", UserID: "u2"}
	if rec := postTelemetry(a, other, "/playback/sessions/sess-1/telemetry", body, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("other user status %d", rec.Code)
	}
	if rec := postTelemetry(a, nil, "/playback/sessions/sess-1/telemetry", body, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status %d", rec.Code)
	}
	if rec := postTelemetry(a, nil, "/playback/sessions/sess-1/telemetry?stoken=wrong", body, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad token status %d", rec.Code)
	}
	if rec := postTelemetry(a, owner, "/playback/sessions/sess-1/telemetry?stoken=wrong", body, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("owner with bad token on a write must be refused, got %d", rec.Code)
	}
	if rec := postTelemetry(a, nil, "/playback/sessions/sess-1/telemetry?stoken=tok", body, nil); rec.Code != http.StatusAccepted {
		t.Fatalf("stream token status %d", rec.Code)
	}
	if rec := postTelemetry(a, owner, "/playback/sessions/gone/telemetry", body, nil); rec.Code != http.StatusGone {
		t.Fatalf("unknown session status %d", rec.Code)
	}
	if n := len(a.Flight.Events("sess-1")); n != 1 {
		t.Fatalf("recorded %d events, want 1", n)
	}
}

func TestTelemetryRateLimit(t *testing.T) {
	a, _, owner := telemetryFixture(t)
	a.Flight.SetIngestBudget(3, 0.0001)
	path := "/playback/sessions/sess-1/telemetry"
	body := `{"events":[{"type":"seek"},{"type":"seek"},{"type":"seek"},{"type":"seek"},{"type":"seek"}]}`
	rec := postTelemetry(a, owner, path, body, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d", rec.Code)
	}
	if res := decodeResult(t, rec); res.Accepted != 3 || res.Dropped != 2 {
		t.Fatalf("result = %+v", res)
	}
	rec = postTelemetry(a, owner, path, `{"events":[{"type":"seek"}]}`, nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("status %d, retry-after %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if n := len(a.Flight.Events("sess-1")); n != 3 {
		t.Fatalf("recorded %d events", n)
	}
}

func TestTelemetryNeverTouchesDatabase(t *testing.T) {
	a, _, owner := telemetryFixture(t)
	before := a.ProgressStats()
	for i := 0; i < 10; i++ {
		postTelemetry(a, owner, "/playback/sessions/sess-1/telemetry", `{"events":[{"type":"stall"}]}`, nil)
	}
	if a.ProgressStats() != before {
		t.Fatal("telemetry changed persistence counters")
	}
}

func TestTelemetryFeedsReliability(t *testing.T) {
	a, _, owner := telemetryFixture(t)
	tr := reliability.New(reliability.Config{})
	a.Flight.AddObserver(reliability.FlightObserver{Recorder: tr})
	body := `{"events":[{"type":"first_frame","data":{"ttff_ms":900}},{"type":"stall"},{"type":"error","data":{"fatal":true,"code":"DECODE"}}]}`
	if rec := postTelemetry(a, owner, "/playback/sessions/sess-1/telemetry", body, map[string]string{"X-Request-Id": "rq-1"}); rec.Code != http.StatusAccepted {
		t.Fatalf("status %d", rec.Code)
	}
	rep := tr.Report()
	if len(rep) != 1 || rep[0].Source != "file-9" || rep[0].Score.TTFFMS != 900 {
		t.Fatalf("report = %+v", rep)
	}
	if len(rep[0].RecentFailures) != 1 || rep[0].RecentFailures[0].RequestID != "rq-1" || rep[0].RecentFailures[0].DeviceClass != "tv" {
		t.Fatalf("failures = %+v", rep[0].RecentFailures)
	}
	if len(a.Flight.Notable(10)) != 1 {
		t.Fatal("fatal client error missing from the notable feed")
	}
}

func TestTelemetryStringSanitizer(t *testing.T) {
	cases := map[string]any{
		"https://x.test/a?b=1":         "https://x.test/a",
		"plain ? question":             "plain ? question",
		"stoken=abc":                   nil,
		strings.Repeat("é", 250):       strings.Repeat("é", telemetryMaxStringLen),
		"  tab\tand\x00null  ":         "tabandnull",
		"blob:https://x/uuid?stoken=1": "blob:https://x/uuid",
	}
	for in, want := range cases {
		got, ok := telemetryString(in)
		if want == nil {
			if ok {
				t.Errorf("%q should be dropped, got %v", in, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("telemetryString(%q) = %v, want %v", in, got, want)
		}
	}
}
