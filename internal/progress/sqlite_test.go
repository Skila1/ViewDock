package progress

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/viewdock/viewdock/internal/db"
)

func testStore(t *testing.T) (*SQLite, context.Context) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if _, err := sqlDB.Exec(`INSERT INTO users(id, username, password_hash, display_name, email, is_admin, disabled, pin_hash, created_at, updated_at)
		VALUES ('u1','u','x','U','',0,0,'','t','t')`); err != nil {
		t.Fatal(err)
	}
	return New(sqlDB), context.Background()
}

func TestPutGetContinue(t *testing.T) {
	s, ctx := testStore(t)
	if err := s.Put(ctx, "u1", "movie", "m1", "f1", 60_000, 3_600_000); err != nil {
		t.Fatal(err)
	}
	rec, err := s.Get(ctx, "u1", "movie", "m1")
	if err != nil {
		t.Fatal(err)
	}
	if rec.ResumeMS != 60_000 || rec.Completed {
		t.Fatalf("%+v", rec)
	}
	list, err := s.Continue(ctx, "u1", 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("continue %v %d", err, len(list))
	}
	if err := s.Put(ctx, "u1", "movie", "m1", "f1", 3_590_000, 3_600_000); err != nil {
		t.Fatal(err)
	}
	rec, _ = s.Get(ctx, "u1", "movie", "m1")
	if !rec.Completed || rec.ResumeMS != 0 {
		t.Fatalf("completed %+v", rec)
	}
	list, _ = s.Continue(ctx, "u1", 10)
	if len(list) != 0 {
		t.Fatalf("completed should drop from continue: %d", len(list))
	}
}

func TestGuestNotInStore(t *testing.T) {
	s, ctx := testStore(t)
	if err := s.Put(ctx, "", "movie", "m1", "f1", 10, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "", "movie", "m1"); err != ErrNotFound {
		t.Fatalf("empty user must not write: %v", err)
	}
}
