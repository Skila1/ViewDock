package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/viewdock/viewdock/internal/db"
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
	return NewService(sqlDB, nil, nil, nil, filepath.Join(dir, "cache")), dir
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
