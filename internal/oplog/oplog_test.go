package oplog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/viewdock/viewdock/internal/db"
)

func TestWriteListAndRedact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(sqlDB)
	s.Write(context.Background(), Entry{
		Level: "error", Category: "playback", Message: "ffmpeg exit",
		Details: map[string]any{"stderr": "ok", "stoken": "secret"},
	})
	s.insert(context.Background(), Entry{
		ID: "x", CreatedAt: "2026-01-01T00:00:00Z", Level: "error", Category: "playback",
		Message: Redact("Authorization: Bearer vd_abc"), Details: redactDetails(map[string]any{"stoken": "x"}),
	})
	list, err := s.List(context.Background(), Filter{Category: "playback", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 1 {
		t.Fatal("expected rows")
	}
	if Redact("Bearer vd_secret123") == "Bearer vd_secret123" {
		t.Fatal("bearer should redact")
	}
}

func TestRedactStoken(t *testing.T) {
	got := Redact("url /hls/x/index.m3u8?stoken=abc123")
	if got == "url /hls/x/index.m3u8?stoken=abc123" {
		t.Fatalf("stoken still present: %s", got)
	}
}

func TestSanitizeEventName(t *testing.T) {
	if SanitizeEventName("play.heartbeat") != "play.heartbeat" {
		t.Fatal("valid name")
	}
	if SanitizeEventName("DROP TABLE") != "" || SanitizeEventName("") != "" {
		t.Fatal("invalid names must be rejected")
	}
}

func TestIngestJourney(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(sqlDB)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/client-logs", strings.NewReader(`{
		"events":[
			{"name":"land","t":1,"details":{"path":"/"}},
			{"name":"play.drift_while_paused","t":2,"details":{"from":10.1,"to":12.4}},
			{"name":"not a name","t":3}
		]
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ViewDockTest/1.0")
	s.handleIngest(rec, req)
	if rec.Code != 200 {
		t.Fatalf("ingest %d %s", rec.Code, rec.Body.String())
	}

	// Write is async via channel; wait for the worker.
	deadline := time.Now().Add(2 * time.Second)
	var list []Entry
	for time.Now().Before(deadline) {
		list, err = s.List(context.Background(), Filter{Category: "journey", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(list) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 journey rows, got %d", len(list))
	}
	names := map[string]bool{}
	for _, e := range list {
		names[e.Message] = true
		if e.Category != "journey" {
			t.Fatalf("category %s", e.Category)
		}
	}
	if !names["land"] || !names["play.drift_while_paused"] {
		t.Fatalf("missing events: %+v", names)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/client-logs", strings.NewReader(`{"events":[]}`))
	s.handleIngest(rec, req)
	if rec.Code != 400 {
		t.Fatalf("empty batch should 400, got %d", rec.Code)
	}
}
