package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/mediafs"
)

func testDB(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "t.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	svc := NewService(sqlDB, nil, nil, nil, filepath.Join(dir, "cache"))
	// Tests place library folders in their own temp dirs.
	svc.Storage = mediafs.Roots{Paths: []string{os.TempDir()}}
	return svc, dir
}

func TestCreateRequiresContentType(t *testing.T) {
	svc, _ := testDB(t)
	root := t.TempDir()
	if _, err := svc.Create(context.Background(), "Movies", root, ""); err == nil {
		t.Fatal("expected content_type error")
	}
	if _, err := svc.Create(context.Background(), "Movies", root, "music"); err == nil {
		t.Fatal("expected invalid content_type")
	}
	lib, err := svc.Create(context.Background(), "Movies", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(lib.RootPath) {
		t.Fatalf("root not abs: %s", lib.RootPath)
	}
	got, err := svc.Get(context.Background(), lib.ID)
	if err != nil || got.ContentType != "movies" {
		t.Fatalf("get %#v %v", got, err)
	}
}

func TestContainsRejectsEscape(t *testing.T) {
	svc, _ := testDB(t)
	root := t.TempDir()
	lib, err := svc.Create(context.Background(), "L", root, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "a", "b.mkv")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.Contains(lib.ID, inside); err != nil {
		t.Fatalf("inside: %v", err)
	}
	outside := filepath.Join(root, "..", "escape.mkv")
	if err := svc.Contains(lib.ID, outside); err == nil {
		t.Fatal("expected escape rejected")
	}
	if err := ContainsPath(root, filepath.Join(root, "..", "x")); err == nil {
		t.Fatal("rel .. should fail")
	}
}

func TestGrantedFilterNilListsAll(t *testing.T) {
	svc, _ := testDB(t)
	root := t.TempDir()
	lib, err := svc.Create(context.Background(), "L", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = svc.DB.ExecContext(ctx, `
		INSERT INTO movies(id, library_id, title, year, sort_title, overview, metadata_source, unmatched, needs_review, hint_mismatch, created_at, updated_at)
		VALUES ('m1', ?, 'The Matrix', 1999, 'matrix, the', '', 'filename', 1, 0, 0, ?, ?)
	`, lib.ID, nowUTC(), nowUTC())
	if err != nil {
		t.Fatal(err)
	}
	all, err := svc.ListMovies(ctx, nil)
	if err != nil || len(all) != 1 {
		t.Fatalf("nil grants: %d %v", len(all), err)
	}
	none, err := svc.ListMovies(ctx, []string{"no-such-lib"})
	if err != nil || len(none) != 0 {
		t.Fatalf("filtered: %d %v", len(none), err)
	}
	ctx2 := WithGrantedIDs(ctx, []string{lib.ID})
	got, err := svc.ListMovies(ctx2, nil)
	if err != nil || len(got) != 1 {
		t.Fatalf("ctx grants: %d %v", len(got), err)
	}
}

func TestRemoteTitlesPlayTheirCopyOnThisServer(t *testing.T) {
	svc, _ := testDB(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO libraries(id, name, root_path, content_type, uploads_enabled, created_at, updated_at) VALUES ('jf', 'Jellyfin', '', 'movies', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO media_sources(id, library_id, name, url, username, created_at, updated_at) VALUES ('src', 'jf', 'JF', 'http://jf', 'u', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO remote_items(source_id, remote_id, item_kind, item_id, duration_ms) VALUES ('src', 'r1', 'movie', 'm1', 6000000)`,
	} {
		if _, err := svc.DB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	loc, err := svc.LocateItem(ctx, "movie", "m1")
	if err != nil || loc.AbsPath != "" || loc.Availability != "remote" {
		t.Fatalf("without a copy: %+v %v", loc, err)
	}
	svc.RemoteCopy = func(_ context.Context, sourceID, remoteID string) (string, string, int64, bool) {
		if sourceID != "src" || remoteID != "r1" {
			return "", "", 0, false
		}
		return "/cache/jellyfin-media/abc.media", "mkv", 42, true
	}
	loc, err = svc.LocateItem(ctx, "movie", "m1")
	if err != nil || loc.AbsPath != "/cache/jellyfin-media/abc.media" || loc.Container != "mkv" || loc.Size != 42 || loc.Availability != "online" || loc.DurationMS != 6000000 {
		t.Fatalf("with a copy: %+v %v", loc, err)
	}
}
