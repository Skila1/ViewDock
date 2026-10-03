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

func TestContinueHidesRestrictedTitles(t *testing.T) {
	s, ctx := testStore(t)
	sqlDB := s.DB
	for _, q := range []string{
		`INSERT INTO libraries(id, name, root_path, content_type, created_at, updated_at) VALUES ('lib', 'L', '/x', 'mixed', 't', 't')`,
		`INSERT INTO movies(id, library_id, title, sort_title, created_at, updated_at, rating_age) VALUES ('m-pg', 'lib', 'A', 'a', 't', 't', 10)`,
		`INSERT INTO movies(id, library_id, title, sort_title, created_at, updated_at, rating_age) VALUES ('m-r', 'lib', 'B', 'b', 't', 't', 17)`,
		`INSERT INTO series(id, library_id, title, sort_title, created_at, updated_at, rating_age) VALUES ('s-ma', 'lib', 'S', 's', 't', 't', 17)`,
		`INSERT INTO seasons(id, series_id, number) VALUES ('se1', 's-ma', 1)`,
		`INSERT INTO episodes(id, series_id, season_id, season, number) VALUES ('e1', 's-ma', 'se1', 1, 1)`,
	} {
		if _, err := sqlDB.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, item := range [][2]string{{"movie", "m-pg"}, {"movie", "m-r"}, {"episode", "e1"}} {
		if err := s.Put(ctx, "u1", item[0], item[1], "f", 60_000, 3_600_000); err != nil {
			t.Fatal(err)
		}
	}
	if list, err := s.Continue(ctx, "u1", 10); err != nil || len(list) != 3 {
		t.Fatalf("unrestricted continue: %d %v", len(list), err)
	}
	if _, err := sqlDB.ExecContext(ctx, `UPDATE users SET content_age_limit = 12 WHERE id = 'u1'`); err != nil {
		t.Fatal(err)
	}
	list, err := s.Continue(ctx, "u1", 10)
	if err != nil || len(list) != 1 || list[0].ItemID != "m-pg" {
		t.Fatalf("restricted continue: %+v %v", list, err)
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

func TestCompletionThreshold(t *testing.T) {
	defer SetCompletion(92, 180)
	SetCompletion(92, 180)
	twoHours := int64(2 * 60 * 60 * 1000)
	if !Completed(twoHours-170_000, twoHours) {
		t.Fatal("a film in its last three minutes of credits is finished")
	}
	if Completed(twoHours/2, twoHours) {
		t.Fatal("half a film is not finished")
	}
	if Completed(60_000, 15*60_000) || !Completed(14*60_000+40_000, 15*60_000) {
		t.Fatal("short titles use the percent and the 30 second rule only")
	}
	SetCompletion(80, 0)
	if !Completed(twoHours*81/100, twoHours) {
		t.Fatal("a lower percent finishes earlier")
	}
}

func TestHistoryRecordsStartAndFinishOnce(t *testing.T) {
	s, ctx := testStore(t)
	hour := int64(3_600_000)
	for _, pos := range []int64{10_000, 60_000, hour - 60_000, hour - 50_000, hour - 40_000, hour - 30_000} {
		if err := s.Put(ctx, "u1", "movie", "m1", "", pos, hour); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM watch_history WHERE user_id = 'u1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("history rows %d, want 2: the start and the finish, not one per report after finishing", n)
	}
}
