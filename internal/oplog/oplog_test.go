package oplog

import (
	"context"
	"path/filepath"
	"testing"

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
