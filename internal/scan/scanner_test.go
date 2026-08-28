package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/library"
)

type stubProber struct{}

func (stubProber) ProbeFile(ctx context.Context, path string) (*ffmpeg.MediaInfo, error) {
	return &ffmpeg.MediaInfo{DurationMS: 1000, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080}, nil
}

func TestScanCataloguesMovieAndExtra(t *testing.T) {
	prev := SizeStable
	SizeStable = 0
	t.Cleanup(func() { SizeStable = prev })

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "t.db")
	if err := db.Migrate(dbPath); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(dbPath, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	root := filepath.Join(dir, "media")
	if err := os.MkdirAll(filepath.Join(root, "Featurettes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "The Matrix (1999).mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Featurettes", "Bonus.mkv"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	libs := library.NewService(sqlDB, nil, nil, nil, "")
	lib, err := libs.Create(context.Background(), "Movies", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	sc := New(sqlDB, libs, stubProber{})
	runID, err := sc.StartScan(context.Background(), lib.ID)
	if err != nil || runID == "" {
		t.Fatalf("start %q %v", runID, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var status string
		_ = sqlDB.QueryRow(`SELECT status FROM scan_runs WHERE id = ?`, runID).Scan(&status)
		if status == "ok" || status == "failed" {
			if status != "ok" {
				t.Fatalf("scan %s", status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout")
		}
		time.Sleep(20 * time.Millisecond)
	}
	var movies int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM movies`).Scan(&movies); err != nil || movies < 1 {
		t.Fatalf("movies=%d err=%v", movies, err)
	}
	var extras int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM media_files WHERE extra_kind != ''`).Scan(&extras); err != nil || extras < 1 {
		t.Fatalf("extras=%d err=%v", extras, err)
	}
	var fts int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM media_fts`).Scan(&fts); err != nil || fts < 1 {
		t.Fatalf("fts=%d err=%v", fts, err)
	}
}
