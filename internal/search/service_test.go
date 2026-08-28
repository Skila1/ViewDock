package search

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/library"
)

func TestFTSSearch(t *testing.T) {
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
	if err := library.UpsertFTS(context.Background(), sqlDB, "movie", "m1", "The Matrix", 1999, ""); err != nil {
		t.Fatal(err)
	}
	hits, err := New(sqlDB).Query(context.Background(), "Matrix", nil)
	if err != nil || len(hits) != 1 || hits[0].ItemID != "m1" {
		t.Fatalf("hits=%+v err=%v", hits, err)
	}
}
