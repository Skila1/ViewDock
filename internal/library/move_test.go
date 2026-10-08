package library_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/mediafs"
	"github.com/viewdock/viewdock/internal/scan"
)

type fixture struct {
	t     *testing.T
	ctx   context.Context
	svc   *library.Service
	sc    *scan.Scanner
	db    *sql.DB
	media string
}

func newFixture(t *testing.T) *fixture {
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
	media := filepath.Join(dir, "media")
	if err := os.MkdirAll(media, 0o755); err != nil {
		t.Fatal(err)
	}
	media, _ = filepath.EvalSymlinks(media)
	svc := library.NewService(sqlDB, nil, nil, nil, filepath.Join(dir, "cache"))
	svc.Storage = mediafs.Roots{Paths: []string{media}, UID: 1000, GID: 1000}
	sc := scan.New(sqlDB, svc, nil)
	svc.SetScan(sc)
	return &fixture{t: t, ctx: context.Background(), svc: svc, sc: sc, db: sqlDB, media: media}
}

func (f *fixture) lib(name, contentType string) library.Library {
	f.t.Helper()
	lib, err := f.svc.Create(f.ctx, name, "", contentType)
	if err != nil {
		f.t.Fatalf("create %s: %v", name, err)
	}
	return lib
}

// put writes a file below a library folder and catalogues it when it is a
// video, the way an upload does.
func (f *fixture) put(lib library.Library, rel, body string) string {
	f.t.Helper()
	abs := filepath.Join(lib.RootPath, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
	if library.IsVideoName(abs) {
		if err := f.sc.IngestFile(f.ctx, lib.ID, abs); err != nil {
			f.t.Fatalf("ingest %s: %v", rel, err)
		}
	}
	return abs
}

func (f *fixture) id(table, libraryID, title string) string {
	f.t.Helper()
	var id string
	if err := f.db.QueryRow(`SELECT id FROM `+table+` WHERE library_id = ? AND title = ?`, libraryID, title).Scan(&id); err != nil {
		f.t.Fatalf("%s %q in %s: %v", table, title, libraryID, err)
	}
	return id
}

func (f *fixture) libraryOf(table, id string) string {
	f.t.Helper()
	var lib string
	if err := f.db.QueryRow(`SELECT library_id FROM `+table+` WHERE id = ?`, id).Scan(&lib); err != nil {
		f.t.Fatal(err)
	}
	return lib
}

func (f *fixture) move(req library.MoveRequest) library.MoveJob {
	f.t.Helper()
	job, err := f.svc.StartMove(f.ctx, req, "admin")
	if err != nil {
		f.t.Fatalf("start move: %v", err)
	}
	f.svc.WaitMoves()
	job, err = f.svc.GetMoveJob(f.ctx, job.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return job
}

func (f *fixture) preview(req library.MoveRequest) library.MovePlan {
	f.t.Helper()
	plan, err := f.svc.Preview(f.ctx, req)
	if err != nil {
		f.t.Fatalf("preview: %v", err)
	}
	return plan
}

func mustExist(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s: %v", p, err)
		}
	}
}

func mustNotExist(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Lstat(p); err == nil {
			t.Fatalf("expected %s to be gone", p)
		}
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func movie(id string) library.MoveItemRef {
	return library.MoveItemRef{Kind: library.KindMovie, ID: id}
}
func series(id string) library.MoveItemRef {
	return library.MoveItemRef{Kind: library.KindSeries, ID: id}
}

func TestCreateLibrariesMakeTheirOwnFolders(t *testing.T) {
	f := newFixture(t)
	for _, c := range []struct{ name, ct string }{
		{"Movies", "movies"}, {"Shows", "tv"}, {"Anime", "tv"}, {"Kids Movies", "movies"}, {"Archive", "mixed"}, {"Mixed", "mixed"},
	} {
		lib := f.lib(c.name, c.ct)
		want := filepath.Join(f.media, c.name)
		if lib.RootPath != want || lib.ContentType != c.ct {
			t.Fatalf("%s: root %q type %q", c.name, lib.RootPath, lib.ContentType)
		}
		st, err := os.Stat(want)
		if err != nil || !st.IsDir() || st.Mode().Perm() != mediafs.DirMode {
			t.Fatalf("%s folder: %v %v", c.name, st, err)
		}
		if os.Geteuid() == 0 {
			if uid, gid := owner(t, want); uid != 1000 || gid != 1000 {
				t.Fatalf("%s owner %d:%d", c.name, uid, gid)
			}
		}
		// ViewDock can write immediately, as an upload does.
		if err := mediafs.CheckWritable(want); err != nil {
			t.Fatalf("%s not writable: %v", c.name, err)
		}
		f.put(lib, "probe.txt", "x")
	}
	nested, err := f.svc.Create(f.ctx, "Nested", "collections/kids/cartoons", "tv")
	if err != nil || nested.RootPath != filepath.Join(f.media, "collections", "kids", "cartoons") {
		t.Fatalf("nested relative path: %+v %v", nested, err)
	}
	if _, err := f.svc.Create(f.ctx, "Again", filepath.Join(f.media, "Movies"), "movies"); !errors.Is(err, library.ErrRootInUse) {
		t.Fatalf("same folder twice: %v", err)
	}
}

func TestCreateLibraryRejectsUnsafePaths(t *testing.T) {
	f := newFixture(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(f.media, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/etc", "../x", "a/../../x", filepath.Join(f.media, "..", "x"), outside, "escape/movies", "C:\\Movies"} {
		if _, err := f.svc.Create(f.ctx, "Bad", p, "movies"); err == nil {
			t.Errorf("path %q accepted", p)
		}
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatalf("created outside the media folder: %v", entries)
	}
	var n int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM libraries`).Scan(&n)
	if n != 0 {
		t.Fatalf("rejected libraries were stored: %d", n)
	}
}

func TestLegacyLibraryOutsideRootsKeepsWorking(t *testing.T) {
	f := newFixture(t)
	legacy := t.TempDir()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := f.db.Exec(`INSERT INTO libraries(id, name, root_path, content_type, uploads_enabled, created_at, updated_at) VALUES ('old', 'Old', ?, 'movies', 1, ?, ?)`, legacy, now, now); err != nil {
		t.Fatal(err)
	}
	name := "Renamed"
	same := legacy
	lib, err := f.svc.Update(f.ctx, "old", library.Patch{Name: &name, RootPath: &same})
	if err != nil || lib.Name != "Renamed" || lib.RootPath != legacy {
		t.Fatalf("edit legacy library: %+v %v", lib, err)
	}
	elsewhere := t.TempDir()
	if _, err := f.svc.Update(f.ctx, "old", library.Patch{RootPath: &elsewhere}); err == nil {
		t.Fatal("re-pointing outside the media folder must be refused")
	}
}

func TestLibraryTypeChangeMustFitContent(t *testing.T) {
	f := newFixture(t)
	mixed := f.lib("Mixed", "mixed")
	f.put(mixed, "Heat (1995).mkv", "m")
	f.put(mixed, "Lost/Season 01/Lost S01E01.mkv", "e")
	for _, ct := range []string{"movies", "tv"} {
		ct := ct
		_, err := f.svc.Update(f.ctx, mixed.ID, library.Patch{ContentType: &ct})
		var conflict *library.TypeConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("switch to %s: %v", ct, err)
		}
	}
	movies := f.lib("Movies", "mixed")
	f.put(movies, "Heat (1995).mkv", "m")
	ct := "movies"
	if _, err := f.svc.Update(f.ctx, movies.ID, library.Patch{ContentType: &ct}); err != nil {
		t.Fatalf("mixed library of movies to Movies: %v", err)
	}
}

func TestScannerEnforcesLibraryType(t *testing.T) {
	f := newFixture(t)
	movies := f.lib("Movies", "movies")
	shows := f.lib("Shows", "tv")
	ep := filepath.Join(movies.RootPath, "Lost S01E01.mkv")
	if err := os.WriteFile(ep, []byte("e"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.sc.IngestFile(f.ctx, movies.ID, ep); !errors.Is(err, scan.ErrWrongKind) {
		t.Fatalf("episode into Movies: %v", err)
	}
	film := filepath.Join(shows.RootPath, "Heat (1995).mkv")
	if err := os.WriteFile(film, []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.sc.IngestFile(f.ctx, shows.ID, film); !errors.Is(err, scan.ErrWrongKind) {
		t.Fatalf("movie into TV Shows: %v", err)
	}
	var n int
	_ = f.db.QueryRow(`SELECT (SELECT COUNT(*) FROM movies) + (SELECT COUNT(*) FROM series)`).Scan(&n)
	if n != 0 {
		t.Fatalf("mismatched files were catalogued: %d", n)
	}
	mustExist(t, ep, film) // rejected files are left exactly where they are
}

func seedUserState(t *testing.T, f *fixture, kind, id string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, q := range []string{
		`INSERT INTO users(id, username, password_hash, created_at, updated_at) VALUES ('u1', 'u1', 'x', '` + now + `', '` + now + `')`,
		`INSERT INTO playback_progress(user_id, item_kind, item_id, position_ms, duration_ms, updated_at) VALUES ('u1', '` + kind + `', '` + id + `', 60000, 7200000, '` + now + `')`,
		`INSERT INTO watch_history(id, user_id, item_kind, item_id, watched_at, position_ms) VALUES ('w1', 'u1', '` + kind + `', '` + id + `', '` + now + `', 60000)`,
		`INSERT INTO favourites(user_id, item_kind, item_id, created_at) VALUES ('u1', '` + kind + `', '` + id + `', '` + now + `')`,
		`INSERT INTO collections(id, name, created_at) VALUES ('c1', 'Best', '` + now + `')`,
		`INSERT INTO collection_items(collection_id, item_kind, item_id, position) VALUES ('c1', '` + kind + `', '` + id + `', 1)`,
		`INSERT INTO artwork(id, item_kind, item_id, kind, path, source, locked) VALUES ('a1', '` + kind + `', '` + id + `', 'poster', 'artwork/poster/x.jpg', 'tmdb', 1)`,
		`INSERT INTO metadata_locks(item_kind, item_id, field) VALUES ('` + kind + `', '` + id + `', 'title')`,
	} {
		if _, err := f.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func assertUserState(t *testing.T, f *fixture, kind, id string) {
	t.Helper()
	for _, table := range []string{"playback_progress", "watch_history", "favourites", "collection_items", "artwork", "metadata_locks"} {
		var n int
		if err := f.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE item_kind = ? AND item_id = ?`, kind, id).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s lost after move: n=%d err=%v", table, n, err)
		}
	}
}

func TestMoveMovieBetweenMovieLibrariesKeepsEverything(t *testing.T) {
	f := newFixture(t)
	oldLib := f.lib("Old Movies", "movies")
	newLib := f.lib("New Movies", "movies")
	f.put(oldLib, "The Matrix (1999)/The Matrix (1999).mkv", "movie")
	f.put(oldLib, "The Matrix (1999)/The Matrix (1999).en.srt", "subs")
	f.put(oldLib, "The Matrix (1999)/poster.jpg", "art")
	f.put(oldLib, "The Matrix (1999)/Featurettes/Making Of.mkv", "extra")
	id := f.id("movies", oldLib.ID, "The Matrix")
	seedUserState(t, f, "movie", id)
	var filesBefore int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM media_files WHERE movie_id = ?`, id).Scan(&filesBefore)

	plan := f.preview(library.MoveRequest{DestinationLibraryID: newLib.ID, Items: []library.MoveItemRef{movie(id)}})
	if len(plan.Eligible) != 1 || len(plan.Ineligible) != 0 || plan.Eligible[0].Target != "The Matrix (1999)" {
		t.Fatalf("plan %+v", plan)
	}
	job := f.move(library.MoveRequest{DestinationLibraryID: newLib.ID, Items: []library.MoveItemRef{movie(id)}})
	if job.Status != "done" || job.Moved != 1 || job.Failed != 0 {
		t.Fatalf("job %+v", job)
	}
	dst := filepath.Join(newLib.RootPath, "The Matrix (1999)")
	mustExist(t, filepath.Join(dst, "The Matrix (1999).mkv"), filepath.Join(dst, "The Matrix (1999).en.srt"),
		filepath.Join(dst, "poster.jpg"), filepath.Join(dst, "Featurettes", "Making Of.mkv"))
	mustNotExist(t, filepath.Join(oldLib.RootPath, "The Matrix (1999)"))
	if readFile(t, filepath.Join(dst, "The Matrix (1999).mkv")) != "movie" {
		t.Fatal("content changed")
	}
	if got := f.libraryOf("movies", id); got != newLib.ID {
		t.Fatalf("movie library %s", got)
	}
	rows, err := f.db.Query(`SELECT library_id, rel_path, abs_path FROM media_files WHERE movie_id = ?`, id)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for rows.Next() {
		var lib, rel, abs string
		_ = rows.Scan(&lib, &rel, &abs)
		n++
		if lib != newLib.ID || !strings.HasPrefix(rel, "The Matrix (1999)/") || abs != filepath.Join(newLib.RootPath, filepath.FromSlash(rel)) {
			t.Fatalf("file row %s %s %s", lib, rel, abs)
		}
		mustExist(t, abs)
	}
	rows.Close()
	if n != filesBefore || n < 2 {
		t.Fatalf("files %d, before %d", n, filesBefore)
	}
	loc, err := f.svc.LocateItem(f.ctx, "movie", id)
	if err != nil || loc.LibraryID != newLib.ID || f.svc.Contains(newLib.ID, loc.AbsPath) != nil {
		t.Fatalf("locate after move: %+v %v", loc, err)
	}
	assertUserState(t, f, "movie", id)
	// A rescan of either library finds nothing new and loses nothing.
	for _, lib := range []library.Library{oldLib, newLib} {
		run, err := f.sc.StartScan(f.ctx, lib.ID)
		if err != nil {
			t.Fatal(err)
		}
		waitScan(t, f.db, run)
	}
	var movies int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM movies`).Scan(&movies)
	if movies != 1 {
		t.Fatalf("movies after rescans: %d", movies)
	}
}

func waitScan(t *testing.T, sqlDB *sql.DB, runID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		_ = sqlDB.QueryRow(`SELECT status FROM scan_runs WHERE id = ?`, runID).Scan(&status)
		if status == "ok" {
			return
		}
		if status == "failed" {
			t.Fatal("scan failed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("scan timeout")
}

func TestMoveCompatibilityRules(t *testing.T) {
	f := newFixture(t)
	movies := f.lib("Movies", "movies")
	shows := f.lib("Shows", "tv")
	shows2 := f.lib("More Shows", "tv")
	mixed := f.lib("Mixed", "mixed")
	moviePath := f.put(movies, "Heat (1995)/Heat (1995).mkv", "m")
	f.put(shows, "Lost/Season 01/Lost S01E01.mkv", "e1")
	f.put(shows, "Lost/Season 01/Lost S01E01.en.srt", "s1")
	f.put(shows, "Lost/Season 02/Lost S02E01.mkv", "e2")
	movieID := f.id("movies", movies.ID, "Heat")
	showID := f.id("series", shows.ID, "Lost")

	// Movies -> TV Shows and TV Shows -> Movies are refused, and nothing moves.
	for _, c := range []struct {
		ref  library.MoveItemRef
		dest string
	}{{movie(movieID), shows.ID}, {series(showID), movies.ID}} {
		plan := f.preview(library.MoveRequest{DestinationLibraryID: c.dest, Items: []library.MoveItemRef{c.ref}})
		if len(plan.Eligible) != 0 || len(plan.Ineligible) != 1 || plan.Ineligible[0].Reason != library.ReasonIncompatible {
			t.Fatalf("%s -> %s plan %+v", c.ref.Kind, c.dest, plan)
		}
		job := f.move(library.MoveRequest{DestinationLibraryID: c.dest, Items: []library.MoveItemRef{c.ref}})
		if job.Moved != 0 || job.Skipped != 1 || job.Items[0].Status != "skipped" {
			t.Fatalf("incompatible job %+v", job)
		}
	}
	mustExist(t, moviePath)
	if f.libraryOf("movies", movieID) != movies.ID || f.libraryOf("series", showID) != shows.ID {
		t.Fatal("incompatible move changed the catalogue")
	}

	// TV Shows -> TV Shows keeps the show and season folders.
	job := f.move(library.MoveRequest{DestinationLibraryID: shows2.ID, Items: []library.MoveItemRef{series(showID)}})
	if job.Moved != 1 {
		t.Fatalf("tv -> tv %+v", job)
	}
	mustExist(t, filepath.Join(shows2.RootPath, "Lost", "Season 01", "Lost S01E01.mkv"),
		filepath.Join(shows2.RootPath, "Lost", "Season 01", "Lost S01E01.en.srt"),
		filepath.Join(shows2.RootPath, "Lost", "Season 02", "Lost S02E01.mkv"))
	var eps int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM episodes WHERE series_id = ?`, showID).Scan(&eps)
	if eps != 2 {
		t.Fatalf("episodes %d", eps)
	}

	// Movies -> Mixed and TV Shows -> Mixed are allowed.
	job = f.move(library.MoveRequest{DestinationLibraryID: mixed.ID, Items: []library.MoveItemRef{movie(movieID), series(showID)}})
	if job.Moved != 2 || job.Failed != 0 {
		t.Fatalf("-> mixed %+v", job)
	}
	mustExist(t, filepath.Join(mixed.RootPath, "Heat (1995)", "Heat (1995).mkv"), filepath.Join(mixed.RootPath, "Lost", "Season 02", "Lost S02E01.mkv"))
	if f.libraryOf("movies", movieID) != mixed.ID || f.libraryOf("series", showID) != mixed.ID {
		t.Fatal("catalogue not updated")
	}
}

func TestMixedLibrarySplitsByActualContentType(t *testing.T) {
	f := newFixture(t)
	mixed := f.lib("Mixed", "mixed")
	movies := f.lib("Movies", "movies")
	shows := f.lib("Shows", "tv")
	f.put(mixed, "Heat (1995)/Heat (1995).mkv", "m1")
	f.put(mixed, "Alien (1979).mkv", "m2")
	f.put(mixed, "Alien (1979).en.srt", "s")
	showFile := f.put(mixed, "Lost/Season 01/Lost S01E01.mkv", "e1")
	showID := f.id("series", mixed.ID, "Lost")

	all := library.MoveRequest{SourceLibraryID: mixed.ID, DestinationLibraryID: movies.ID, All: true}
	plan := f.preview(all)
	if len(plan.Eligible) != 2 || len(plan.Ineligible) != 1 {
		t.Fatalf("mixed -> movies plan: %d eligible, %d ineligible", len(plan.Eligible), len(plan.Ineligible))
	}
	if bad := plan.Ineligible[0]; bad.Kind != library.KindSeries || bad.Reason != library.ReasonIncompatible || bad.Message == "" {
		t.Fatalf("ineligible %+v", bad)
	}
	job := f.move(all)
	if job.Moved != 2 || job.Skipped != 1 || job.Failed != 0 {
		t.Fatalf("job %+v", job)
	}
	mustExist(t, filepath.Join(movies.RootPath, "Heat (1995)", "Heat (1995).mkv"),
		filepath.Join(movies.RootPath, "Alien (1979).mkv"), filepath.Join(movies.RootPath, "Alien (1979).en.srt"), showFile)
	if f.libraryOf("series", showID) != mixed.ID {
		t.Fatal("the show must stay in the Mixed library")
	}

	job = f.move(library.MoveRequest{SourceLibraryID: mixed.ID, DestinationLibraryID: shows.ID, All: true})
	if job.Moved != 1 || job.Skipped != 0 {
		t.Fatalf("mixed -> tv %+v", job)
	}
	mustExist(t, filepath.Join(shows.RootPath, "Lost", "Season 01", "Lost S01E01.mkv"))
	var left int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM media_files WHERE library_id = ?`, mixed.ID).Scan(&left)
	if left != 0 {
		t.Fatalf("files left in mixed: %d", left)
	}
}

func TestWholeLibraryMigration(t *testing.T) {
	f := newFixture(t)
	oldMovies := f.lib("Old Movies", "movies")
	oldShows := f.lib("Old Shows", "tv")
	newMovies := f.lib("New Movies", "movies")
	everything := f.lib("Everything", "mixed")
	for _, rel := range []string{"Heat (1995).mkv", "Alien (1979)/Alien (1979).mkv", "Films/Up (2009).mkv", "Films/Cars (2006).mkv"} {
		f.put(oldMovies, rel, rel)
	}
	f.put(oldShows, "Lost/Season 01/Lost S01E01.mkv", "e")
	f.put(oldShows, "Dark/S01/Dark S01E01.mkv", "d")

	job := f.move(library.MoveRequest{SourceLibraryID: oldMovies.ID, DestinationLibraryID: newMovies.ID, All: true})
	if job.Total != 4 || job.Moved != 4 {
		t.Fatalf("movies migration %+v", job)
	}
	// A loose movie stays loose; one in a shared folder gets its own folder.
	mustExist(t, filepath.Join(newMovies.RootPath, "Heat (1995).mkv"),
		filepath.Join(newMovies.RootPath, "Alien (1979)", "Alien (1979).mkv"),
		filepath.Join(newMovies.RootPath, "Up (2009)", "Up (2009).mkv"),
		filepath.Join(newMovies.RootPath, "Cars (2006)", "Cars (2006).mkv"))
	mustNotExist(t, filepath.Join(oldMovies.RootPath, "Films"))
	mustExist(t, oldMovies.RootPath) // the old library folder itself stays

	job = f.move(library.MoveRequest{SourceLibraryID: newMovies.ID, DestinationLibraryID: everything.ID, All: true})
	if job.Moved != 4 {
		t.Fatalf("movies -> mixed %+v", job)
	}
	job = f.move(library.MoveRequest{SourceLibraryID: oldShows.ID, DestinationLibraryID: everything.ID, All: true})
	if job.Moved != 2 {
		t.Fatalf("shows -> mixed %+v", job)
	}
	mustExist(t, filepath.Join(everything.RootPath, "Lost", "Season 01", "Lost S01E01.mkv"), filepath.Join(everything.RootPath, "Dark", "S01", "Dark S01E01.mkv"))
	var n int
	_ = f.db.QueryRow(`SELECT (SELECT COUNT(*) FROM movies WHERE library_id = ?) + (SELECT COUNT(*) FROM series WHERE library_id = ?)`, everything.ID, everything.ID).Scan(&n)
	if n != 6 {
		t.Fatalf("titles in mixed: %d", n)
	}
}

func TestMigrateOutOfLibraryAtStorageRoot(t *testing.T) {
	f := newFixture(t)
	// Older installs point one library at the whole media folder.
	legacy, err := f.svc.Create(f.ctx, "Library", f.media, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	f.put(legacy, "Heat (1995).mkv", "m")
	movies := f.lib("Movies", "movies") // inside the legacy library's folder
	job := f.move(library.MoveRequest{SourceLibraryID: legacy.ID, DestinationLibraryID: movies.ID, All: true})
	if job.Moved != 1 {
		t.Fatalf("job %+v", job)
	}
	mustExist(t, filepath.Join(movies.RootPath, "Heat (1995).mkv"))
	run, err := f.sc.StartScan(f.ctx, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitScan(t, f.db, run)
	var n int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM media_files`).Scan(&n)
	if n != 1 {
		t.Fatalf("the outer library re-catalogued the nested library's file: %d files", n)
	}
}

func TestCollisionsNeverOverwrite(t *testing.T) {
	f := newFixture(t)
	src := f.lib("Source", "movies")
	dest := f.lib("Dest", "movies")
	f.put(src, "The Matrix (1999)/The Matrix (1999).mkv", "new")
	f.put(src, "Heat (1995).mkv", "new heat")
	f.put(src, "Heat (1995).en.srt", "new subs")
	f.put(src, "Up (2009).mkv", "up")
	// Untracked files already in the destination.
	keep := filepath.Join(dest.RootPath, "The Matrix (1999)", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(keep), 0o755); err != nil {
		t.Fatal(err)
	}
	for p, body := range map[string]string{keep: "keep", filepath.Join(dest.RootPath, "Heat (1995).mkv"): "old heat"} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The same film already catalogued in the destination.
	f.put(dest, "Up (2009)/Up (2009).mkv", "existing up")

	plan := f.preview(library.MoveRequest{SourceLibraryID: src.ID, DestinationLibraryID: dest.ID, All: true})
	if len(plan.Eligible) != 2 || len(plan.Ineligible) != 1 || plan.Ineligible[0].Reason != library.ReasonDuplicate {
		t.Fatalf("plan %+v", plan)
	}
	for _, it := range plan.Eligible {
		if !it.Renamed {
			t.Fatalf("%s should be renamed: %+v", it.Title, it)
		}
	}
	job := f.move(library.MoveRequest{SourceLibraryID: src.ID, DestinationLibraryID: dest.ID, All: true})
	if job.Moved != 2 || job.Skipped != 1 {
		t.Fatalf("job %+v", job)
	}
	if readFile(t, keep) != "keep" || readFile(t, filepath.Join(dest.RootPath, "Heat (1995).mkv")) != "old heat" {
		t.Fatal("an existing file was overwritten")
	}
	if readFile(t, filepath.Join(dest.RootPath, "The Matrix (1999) (2)", "The Matrix (1999).mkv")) != "new" {
		t.Fatal("renamed folder missing")
	}
	if readFile(t, filepath.Join(dest.RootPath, "Heat (1995) (2).mkv")) != "new heat" ||
		readFile(t, filepath.Join(dest.RootPath, "Heat (1995) (2).en.srt")) != "new subs" {
		t.Fatal("renamed file or its subtitles missing")
	}
	if readFile(t, filepath.Join(src.RootPath, "Up (2009).mkv")) != "up" {
		t.Fatal("the duplicate must stay where it was")
	}
}

func TestFailedMoveLeavesFilesAndCatalogueUnchanged(t *testing.T) {
	f := newFixture(t)
	src := f.lib("Mixed", "mixed")
	dest := f.lib("Shows", "tv")
	// Loose episodes: the show is moved file by file, so a failure can
	// happen after some files already moved.
	e1 := f.put(src, "Lost S01E01.mkv", "e1")
	e2 := f.put(src, "Lost S01E02.mkv", "e2")
	sub := f.put(src, "Lost S01E01.en.srt", "s1")
	showID := f.id("series", src.ID, "Lost")
	before := snapshot(t, f.db, showID)

	f.svc.BeforeEachMove(func(dsts []string) {
		// Something appears at the last destination between planning and moving.
		last := dsts[len(dsts)-1]
		_ = os.MkdirAll(filepath.Dir(last), 0o755)
		_ = os.WriteFile(last, []byte("intruder"), 0o644)
	})
	job := f.move(library.MoveRequest{DestinationLibraryID: dest.ID, Items: []library.MoveItemRef{series(showID)}})
	if job.Moved != 0 || job.Failed != 1 || job.Items[0].Status != "failed" || job.Items[0].Message == "" {
		t.Fatalf("job %+v", job)
	}
	for p, body := range map[string]string{e1: "e1", e2: "e2", sub: "s1"} {
		if readFile(t, p) != body {
			t.Fatalf("%s not restored", p)
		}
	}
	if after := snapshot(t, f.db, showID); after != before {
		t.Fatalf("catalogue changed:\n%s\n%s", before, after)
	}
	var intruders []string
	_ = filepath.WalkDir(dest.RootPath, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			intruders = append(intruders, filepath.Base(p))
		}
		return nil
	})
	if len(intruders) != 1 || readFile(t, filepath.Join(dest.RootPath, "Lost", "Season 01", intruders[0])) != "intruder" {
		t.Fatalf("destination after rollback: %v", intruders)
	}

	// A catalogue failure after the files moved puts the files back too.
	f.svc.BeforeEachMove(func([]string) {})
	if _, err := f.db.Exec(`CREATE TRIGGER fail_move BEFORE UPDATE OF library_id ON series BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	other := f.lib("Other", "tv")
	job = f.move(library.MoveRequest{DestinationLibraryID: other.ID, Items: []library.MoveItemRef{series(showID)}})
	if job.Failed != 1 || !strings.Contains(job.Items[0].Message, "catalogue") {
		t.Fatalf("db failure job %+v", job)
	}
	mustExist(t, e1, e2, sub)
	if after := snapshot(t, f.db, showID); after != before {
		t.Fatal("catalogue changed after a failed update")
	}
	entries, _ := os.ReadDir(other.RootPath)
	if len(entries) != 0 {
		t.Fatalf("files left in the destination: %v", entries)
	}
}

func snapshot(t *testing.T, sqlDB *sql.DB, seriesID string) string {
	t.Helper()
	var lib string
	_ = sqlDB.QueryRow(`SELECT library_id FROM series WHERE id = ?`, seriesID).Scan(&lib)
	rows, err := sqlDB.Query(`
		SELECT DISTINCT mf.library_id, mf.rel_path, mf.abs_path FROM media_files mf
		JOIN media_file_episodes mfe ON mfe.media_file_id = mf.id JOIN episodes e ON e.id = mfe.episode_id
		WHERE e.series_id = ?`, seriesID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []string{lib}
	for rows.Next() {
		var a, b, c string
		_ = rows.Scan(&a, &b, &c)
		out = append(out, a+"|"+b+"|"+c)
	}
	sort.Strings(out[1:])
	return strings.Join(out, "\n")
}

func TestCrossFilesystemMoveCopiesVerifiesAndRemoves(t *testing.T) {
	f := newFixture(t)
	src := f.lib("A", "movies")
	dest := f.lib("B", "movies")
	f.put(src, "Heat (1995)/Heat (1995).mkv", "movie bytes")
	f.put(src, "Heat (1995)/Subs/Heat.en.srt", "subs")
	id := f.id("movies", src.ID, "Heat")
	restore := library.ForceCrossDevice()
	defer restore()
	job := f.move(library.MoveRequest{DestinationLibraryID: dest.ID, Items: []library.MoveItemRef{movie(id)}})
	if job.Moved != 1 {
		t.Fatalf("job %+v", job)
	}
	if readFile(t, filepath.Join(dest.RootPath, "Heat (1995)", "Heat (1995).mkv")) != "movie bytes" ||
		readFile(t, filepath.Join(dest.RootPath, "Heat (1995)", "Subs", "Heat.en.srt")) != "subs" {
		t.Fatal("copy incomplete")
	}
	mustNotExist(t, filepath.Join(src.RootPath, "Heat (1995)"))
	entries, _ := os.ReadDir(dest.RootPath)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".vd-move-") {
			t.Fatalf("partial copy left behind: %s", e.Name())
		}
	}
}

func TestRecoverInterruptedMoves(t *testing.T) {
	f := newFixture(t)
	src := f.lib("A", "movies")
	dest := f.lib("B", "movies")
	heat := f.put(src, "Heat (1995).mkv", "heat")
	heatSub := f.put(src, "Heat (1995).en.srt", "subs")
	up := f.put(src, "Up (2009).mkv", "up")
	heatID := f.id("movies", src.ID, "Heat")
	upID := f.id("movies", src.ID, "Up")
	var heatFile, upFile string
	_ = f.db.QueryRow(`SELECT id FROM media_files WHERE movie_id = ?`, heatID).Scan(&heatFile)
	_ = f.db.QueryRow(`SELECT id FROM media_files WHERE movie_id = ?`, upID).Scan(&upFile)

	// Crash after the video moved but before its subtitles: roll back.
	heatDst := filepath.Join(dest.RootPath, "Heat (1995).mkv")
	if err := os.Rename(heat, heatDst); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.InsertMoveJournal("j1", "i1", "movie", heatID, dest.ID, "moving",
		[][2]string{{heat, heatDst}, {heatSub, filepath.Join(dest.RootPath, "Heat (1995).en.srt")}},
		[][3]string{{heatFile, "Heat (1995).mkv", heatDst}}); err != nil {
		t.Fatal(err)
	}
	// Crash after every file moved but before the catalogue: finish.
	upDst := filepath.Join(dest.RootPath, "Up (2009).mkv")
	if err := os.Rename(up, upDst); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.InsertMoveJournal("j2", "i2", "movie", upID, dest.ID, "fs_done",
		[][2]string{{up, upDst}}, [][3]string{{upFile, "Up (2009).mkv", upDst}}); err != nil {
		t.Fatal(err)
	}

	f.svc.RecoverMoves(f.ctx)

	mustExist(t, heat, heatSub)
	mustNotExist(t, heatDst)
	if f.libraryOf("movies", heatID) != src.ID {
		t.Fatal("rolled back title must stay in its library")
	}
	mustExist(t, upDst)
	if f.libraryOf("movies", upID) != dest.ID {
		t.Fatal("finished title must be in the destination")
	}
	var abs string
	_ = f.db.QueryRow(`SELECT abs_path FROM media_files WHERE id = ?`, upFile).Scan(&abs)
	if abs != upDst {
		t.Fatalf("abs_path %s", abs)
	}
	for id, want := range map[string]string{"j1": "rolled_back", "j2": "moved"} {
		job, err := f.svc.GetMoveJob(f.ctx, id)
		if err != nil || job.Status != "interrupted" || job.Items[0].Status != want {
			t.Fatalf("%s: %+v %v", id, job, err)
		}
	}
}

func TestMoveEndpointsRequireLibraryManagement(t *testing.T) {
	f := newFixture(t)
	a := f.lib("A", "movies")
	b := f.lib("B", "mixed")
	f.put(a, "Heat (1995).mkv", "m")
	r := chi.NewRouter()
	f.svc.Routes(r)
	call := func(p *auth.Principal, method, path string, body any) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		req := httptest.NewRequest(method, path, &buf)
		if p != nil {
			req = req.WithContext(auth.WithPrincipal(req.Context(), p))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	body := map[string]any{"source_library_id": a.ID, "destination_library_id": b.ID, "all": true}
	viewer := &auth.Principal{Kind: auth.KindUser, UserID: "viewer"}
	for _, path := range []string{"/library-moves/preview", "/library-moves"} {
		if rec := call(viewer, http.MethodPost, path, body); rec.Code != http.StatusForbidden {
			t.Fatalf("%s as viewer: %d", path, rec.Code)
		}
	}
	manager := &auth.Principal{Kind: auth.KindUser, UserID: "mgr", Permissions: []string{auth.PermLibrariesManage}}
	rec := call(manager, http.MethodPost, "/library-moves/preview", body)
	var plan library.MovePlan
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &plan) != nil || len(plan.Eligible) != 1 {
		t.Fatalf("preview %d %s", rec.Code, rec.Body.String())
	}
	rec = call(manager, http.MethodPost, "/library-moves", body)
	var job library.MoveJob
	if rec.Code != http.StatusAccepted || json.Unmarshal(rec.Body.Bytes(), &job) != nil {
		t.Fatalf("start %d %s", rec.Code, rec.Body.String())
	}
	f.svc.WaitMoves()
	if rec := call(viewer, http.MethodGet, "/library-moves/"+job.ID, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("job as viewer: %d", rec.Code)
	}
	rec = call(manager, http.MethodGet, "/library-moves/"+job.ID, nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &job) != nil || job.Moved != 1 {
		t.Fatalf("job %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(manager, http.MethodPost, "/library-moves/preview", map[string]any{"destination_library_id": "nope", "all": true, "source_library_id": a.ID}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown destination: %d", rec.Code)
	}
}

func TestMoveRejectsTitlesOfRemoteLibraries(t *testing.T) {
	f := newFixture(t)
	dest := f.lib("Movies", "movies")
	now := time.Now().UTC().Format(time.RFC3339)
	for _, q := range []string{
		`INSERT INTO libraries(id, name, root_path, content_type, uploads_enabled, created_at, updated_at) VALUES ('jf', 'Jellyfin', '', 'movies', 0, '` + now + `', '` + now + `')`,
		`INSERT INTO media_sources(id, library_id, name, url, username, created_at, updated_at) VALUES ('src', 'jf', 'JF', 'http://jf', 'u', '` + now + `', '` + now + `')`,
		`INSERT INTO movies(id, library_id, title, sort_title, created_at, updated_at) VALUES ('rm', 'jf', 'Remote', 'remote', '` + now + `', '` + now + `')`,
	} {
		if _, err := f.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	plan := f.preview(library.MoveRequest{DestinationLibraryID: dest.ID, Items: []library.MoveItemRef{movie("rm")}})
	if len(plan.Ineligible) != 1 || plan.Ineligible[0].Reason != library.ReasonRemote {
		t.Fatalf("plan %+v", plan)
	}
}

func owner(t *testing.T, path string) (int, int) {
	t.Helper()
	return fileOwner(t, path)
}

func TestCreateRefusesFolderAnotherLibraryAlreadyLists(t *testing.T) {
	f := newFixture(t)
	legacy, err := f.svc.Create(f.ctx, "Library", f.media, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	f.put(legacy, "movies/Heat (1995).mkv", "m")
	if _, err := f.svc.Create(f.ctx, "Movies", "movies", "movies"); err == nil || !strings.Contains(err.Error(), "Move content") {
		t.Fatalf("expected refusal, got %v", err)
	}
	// A fresh folder is fine, and the title can then be moved into it.
	movies := f.lib("Movies", "movies")
	job := f.move(library.MoveRequest{SourceLibraryID: legacy.ID, DestinationLibraryID: movies.ID, All: true})
	if job.Moved != 1 {
		t.Fatalf("job %+v", job)
	}
}

func TestRunningMoveReportsItsProgress(t *testing.T) {
	f := newFixture(t)
	src := f.lib("A", "movies")
	dest := f.lib("B", "movies")
	f.put(src, "Heat (1995)/Heat (1995).mkv", strings.Repeat("h", 4000))
	f.put(src, "Ronin (1998)/Ronin (1998).mkv", strings.Repeat("r", 6000))
	restore := library.ForceCrossDevice()
	defer restore()
	var seen []library.MoveJob
	f.svc.BeforeEachMove(func([]string) {
		jobs, err := f.svc.ActiveMoves(f.ctx)
		if err != nil || len(jobs) != 1 {
			t.Errorf("active moves during the move: %v %v", jobs, err)
			return
		}
		seen = append(seen, jobs[0])
	})
	job := f.move(library.MoveRequest{SourceLibraryID: src.ID, DestinationLibraryID: dest.ID, All: true})
	if job.Moved != 2 || len(seen) != 2 {
		t.Fatalf("job %+v, progress seen %d times", job, len(seen))
	}
	if seen[0].BytesTotal != 10000 || seen[0].BytesDone != 0 || seen[0].Current == "" {
		t.Fatalf("progress before the first title: %+v", seen[0])
	}
	if seen[1].BytesDone == 0 || seen[1].BytesDone >= seen[1].BytesTotal || seen[1].Current == seen[0].Current {
		t.Fatalf("progress before the second title: %+v", seen[1])
	}
	if jobs, _ := f.svc.ActiveMoves(f.ctx); len(jobs) != 0 {
		t.Fatalf("a finished move is still listed: %+v", jobs)
	}
	if job.BytesTotal != 0 || job.Current != "" {
		t.Fatalf("a finished job reports live progress: %+v", job)
	}
}
