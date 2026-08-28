package media

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/ffmpeg"
)

type stubProber struct {
	n    int
	info *ffmpeg.MediaInfo
	err  error
}

func (s *stubProber) ProbeFile(ctx context.Context, path string) (*ffmpeg.MediaInfo, error) {
	s.n++
	return s.info, s.err
}

func TestSkipReprobeWhenSizeMtimeUnchanged(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "t.db")
	if err := db.Migrate(dbPath); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(dbPath, 20000)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	media := filepath.Join(dir, "a.mkv")
	if err := os.WriteFile(media, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(media)
	now := time.Now().UTC().Format(time.RFC3339)
	libID, fileID := uuid.NewString(), uuid.NewString()
	_, err = sqlDB.Exec(`
		INSERT INTO libraries(id, name, root_path, content_type, uploads_enabled, created_at, updated_at)
		VALUES (?, 'L', ?, 'movies', 0, ?, ?)
	`, libID, dir, now, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = sqlDB.Exec(`
		INSERT INTO media_files(id, library_id, rel_path, abs_path, size_bytes, inode, mtime, kind,
			extra_kind, probe_status, probe_error, probed_at, availability, duration_ms, container,
			video_codec, audio_codec, width, height, created_at, updated_at)
		VALUES (?, ?, 'a.mkv', ?, ?, '', ?, 'movie', '', 'ok', '', ?, 'online', 1000, 'mkv', 'h264', 'aac', 1920, 1080, ?, ?)
	`, fileID, libID, media, st.Size(), st.ModTime().UTC().Format(time.RFC3339), now, now, now)
	if err != nil {
		t.Fatal(err)
	}
	p := &stubProber{info: &ffmpeg.MediaInfo{DurationMS: 9}}
	if err := PersistProbe(context.Background(), sqlDB, p, fileID); err != nil {
		t.Fatal(err)
	}
	if p.n != 0 {
		t.Fatalf("probed %d times, want skip", p.n)
	}
}

func TestNASOutageIsOffline(t *testing.T) {
	missingRoot := filepath.Join(t.TempDir(), "gone-root")
	got := ClassifyAvailability(filepath.Join(missingRoot, "a.mkv"), missingRoot, os.ErrNotExist)
	if got != Offline {
		t.Fatalf("got %s want offline", got)
	}
	root := t.TempDir()
	got = ClassifyAvailability(filepath.Join(root, "missing.mkv"), root, os.ErrNotExist)
	if got != Missing {
		t.Fatalf("got %s want missing", got)
	}
}
