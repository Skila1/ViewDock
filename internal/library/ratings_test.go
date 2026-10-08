package library

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
)

type ratedFixture struct {
	svc   *Service
	libID string
}

func exec(t *testing.T, s *Service, q string, args ...any) {
	t.Helper()
	if _, err := s.DB.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func addUser(t *testing.T, s *Service, id string, ageLimit int) {
	t.Helper()
	exec(t, s, `INSERT INTO users(id, username, password_hash, display_name, email, is_admin, disabled, pin_hash, created_at, updated_at, content_age_limit)
		VALUES (?, ?, 'x', ?, '', 0, 0, '', 't', 't', ?)`, id, id, id, ageLimit)
}

func addMovie(t *testing.T, s *Service, libID, id, title string, age any) {
	t.Helper()
	now := nowUTC()
	exec(t, s, `INSERT INTO movies(id, library_id, title, sort_title, created_at, updated_at, rating_age) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, libID, title, title, now, now, age)
	exec(t, s, `INSERT INTO media_files(id, library_id, rel_path, abs_path, kind, movie_id, created_at, updated_at) VALUES (?, ?, ?, ?, 'movie', ?, ?, ?)`,
		"f-"+id, libID, id+".mkv", "/x/"+id+".mkv", id, now, now)
}

func newRatedFixture(t *testing.T) *ratedFixture {
	t.Helper()
	svc, _ := testDB(t)
	lib, err := svc.Create(context.Background(), "L", t.TempDir(), "mixed")
	if err != nil {
		t.Fatal(err)
	}
	addMovie(t, svc, lib.ID, "m-pg", "Family", 10)
	addMovie(t, svc, lib.ID, "m-r", "Grim", 17)
	addMovie(t, svc, lib.ID, "m-nr", "Home Video", nil)
	now := nowUTC()
	exec(t, svc, `INSERT INTO series(id, library_id, title, sort_title, created_at, updated_at, rating_age) VALUES ('s-ma', ?, 'Late Show', 'late show', ?, ?, 17)`, lib.ID, now, now)
	exec(t, svc, `INSERT INTO seasons(id, series_id, number) VALUES ('se1', 's-ma', 1)`)
	exec(t, svc, `INSERT INTO episodes(id, series_id, season_id, season, number, title) VALUES ('e1', 's-ma', 'se1', 1, 1, 'Pilot')`)
	exec(t, svc, `INSERT INTO media_files(id, library_id, rel_path, abs_path, kind, created_at, updated_at) VALUES ('f-e1', ?, 'e1.mkv', '/x/e1.mkv', 'episode', ?, ?)`, lib.ID, now, now)
	exec(t, svc, `INSERT INTO media_file_episodes(media_file_id, episode_id) VALUES ('f-e1', 'e1')`)

	addUser(t, svc, "owner", 0)
	addUser(t, svc, "child", 0)
	addUser(t, svc, "adult", 0)
	exec(t, svc, `INSERT INTO households(id, name, owner_id, created_at) VALUES ('h1', 'Home', 'owner', ?)`, now)
	exec(t, svc, `INSERT INTO household_members(household_id, user_id, role, age_limit, joined_at) VALUES ('h1', 'owner', 'owner', 0, ?)`, now)
	exec(t, svc, `INSERT INTO household_members(household_id, user_id, role, age_limit, joined_at) VALUES ('h1', 'child', 'child', 12, ?)`, now)
	t.Cleanup(func() { SetBlockUnrated(true) })
	return &ratedFixture{svc: svc, libID: lib.ID}
}

func movieIDs(list []Movie) map[string]bool {
	out := map[string]bool{}
	for _, m := range list {
		out[m.ID] = true
	}
	return out
}

func TestMigrationAddsRatingColumns(t *testing.T) {
	svc, _ := testDB(t)
	for _, q := range []string{
		`SELECT content_rating, rating_age, rating_source FROM movies LIMIT 1`,
		`SELECT content_rating, rating_age, rating_source FROM series LIMIT 1`,
		`SELECT content_age_limit FROM users LIMIT 1`,
	} {
		rows, err := svc.DB.Query(q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		rows.Close()
	}
}

func TestRestrictionResolution(t *testing.T) {
	f := newRatedFixture(t)
	ctx := context.Background()
	rest, err := RestrictionFor(ctx, f.svc.DB, "child")
	if err != nil || rest.MaxAge != 12 || rest.HouseholdLimit != 12 {
		t.Fatalf("household limit: %+v %v", rest, err)
	}
	exec(t, f.svc, `UPDATE users SET content_age_limit = 7 WHERE id = 'child'`)
	if rest, _ = RestrictionFor(ctx, f.svc.DB, "child"); rest.MaxAge != 7 {
		t.Fatalf("lowest limit must win: %+v", rest)
	}
	exec(t, f.svc, `UPDATE users SET content_age_limit = 16 WHERE id = 'child'`)
	if rest, _ = RestrictionFor(ctx, f.svc.DB, "child"); rest.MaxAge != 12 {
		t.Fatalf("household limit must still apply: %+v", rest)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	exec(t, f.svc, `UPDATE household_members SET expires_at = ? WHERE user_id = 'child'`, past)
	if rest, _ = RestrictionFor(ctx, f.svc.DB, "child"); rest.MaxAge != 16 {
		t.Fatalf("expired membership must not apply: %+v", rest)
	}
	if rest, _ = RestrictionFor(ctx, f.svc.DB, "adult"); rest.Active() {
		t.Fatalf("adult restricted: %+v", rest)
	}
	if rest, _ = RestrictionFor(ctx, f.svc.DB, ""); rest.Active() {
		t.Fatalf("anonymous restricted: %+v", rest)
	}
}

func TestListingsHideRestrictedTitles(t *testing.T) {
	f := newRatedFixture(t)
	child := WithUserID(context.Background(), "child")
	got, err := f.svc.ListMovies(child, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ids := movieIDs(got); len(ids) != 1 || !ids["m-pg"] {
		t.Fatalf("child with unrated blocked: %v", ids)
	}
	SetBlockUnrated(false)
	got, _ = f.svc.ListMovies(child, nil)
	if ids := movieIDs(got); len(ids) != 2 || !ids["m-pg"] || !ids["m-nr"] {
		t.Fatalf("child with unrated allowed: %v", ids)
	}
	adult, _ := f.svc.ListMovies(WithUserID(context.Background(), "adult"), nil)
	if len(adult) != 3 {
		t.Fatalf("adult must see everything: %d", len(adult))
	}
	series, _ := f.svc.ListSeries(child, nil)
	if len(series) != 0 {
		t.Fatalf("MA series listed for child: %+v", series)
	}
	granted := WithGrantedIDs(WithUserID(context.Background(), "child"), []string{f.libID})
	if got, _ := f.svc.ListMovies(granted, nil); len(got) != 2 {
		t.Fatalf("grants and ratings combined: %d", len(got))
	}
}

func TestDirectIDLookupsAreEnforced(t *testing.T) {
	f := newRatedFixture(t)
	child := WithUserID(context.Background(), "child")
	if _, err := f.svc.GetMovie(child, "m-pg"); err != nil {
		t.Fatalf("allowed movie: %v", err)
	}
	if _, err := f.svc.GetMovie(child, "m-r"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restricted movie by id: %v", err)
	}
	if _, err := f.svc.GetMovie(child, "m-nr"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unrated movie with block policy: %v", err)
	}
	if _, err := f.svc.GetSeries(child, "s-ma"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restricted series by id: %v", err)
	}
	if _, err := f.svc.GetEpisode(child, "e1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("episode inherits series rating: %v", err)
	}
	if _, err := f.svc.NextEpisode(context.Background(), "s-ma", "child"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("next episode with explicit user: %v", err)
	}
	if _, err := f.svc.GetEpisode(WithUserID(context.Background(), "adult"), "e1"); err != nil {
		t.Fatalf("adult episode: %v", err)
	}
	other := WithGrantedIDs(WithUserID(context.Background(), "adult"), []string{"another-library"})
	if _, err := f.svc.GetMovie(other, "m-pg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ungranted library by id: %v", err)
	}
	SetBlockUnrated(false)
	if _, err := f.svc.GetMovie(child, "m-nr"); err != nil {
		t.Fatalf("unrated movie with allow policy: %v", err)
	}
}

func TestPlaybackPermitted(t *testing.T) {
	f := newRatedFixture(t)
	ctx := context.Background()
	cases := []struct {
		user, file string
		want       bool
	}{
		{"child", "f-m-pg", true},
		{"child", "f-m-r", false},
		{"child", "f-m-nr", false},
		{"child", "f-e1", false},
		{"child", "missing", false},
		{"adult", "f-m-r", true},
		{"adult", "f-e1", true},
	}
	for _, c := range cases {
		if got := PlaybackPermitted(ctx, f.svc.DB, c.user, c.file); got != c.want {
			t.Errorf("%s/%s = %v, want %v", c.user, c.file, got, c.want)
		}
	}
	SetBlockUnrated(false)
	if !PlaybackPermitted(ctx, f.svc.DB, "child", "f-m-nr") {
		t.Error("unrated file must play when unrated content is allowed")
	}
}

func TestPlaybackPermittedFailsClosed(t *testing.T) {
	f := newRatedFixture(t)
	if err := f.svc.DB.Close(); err != nil {
		t.Fatal(err)
	}
	if PlaybackPermitted(context.Background(), f.svc.DB, "child", "f-m-pg") {
		t.Fatal("database failure must deny playback")
	}
	if _, err := f.svc.GetMovie(WithUserID(context.Background(), "child"), "m-pg"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("detail on database failure: %v", err)
	}
}

func ratingRequest(t *testing.T, svc *Service, p *auth.Principal, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	svc.Routes(r)
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	ctx := req.Context()
	if p != nil {
		ctx = WithUserID(auth.WithPrincipal(ctx, p), p.UserID)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func TestRatingOverrideRequiresPermission(t *testing.T) {
	f := newRatedFixture(t)
	user := &auth.Principal{Kind: auth.KindUser, UserID: "adult"}
	if rec := ratingRequest(t, f.svc, user, http.MethodPut, "/movies/m-nr/rating", map[string]any{"content_rating": "G"}); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin override: %d", rec.Code)
	}
	manager := &auth.Principal{Kind: auth.KindUser, UserID: "owner", Permissions: []string{auth.PermLibrariesManage}}
	if rec := ratingRequest(t, f.svc, manager, http.MethodPut, "/movies/m-nr/rating", map[string]any{"content_rating": "Mystery"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown rating without age: %d", rec.Code)
	}
	if rec := ratingRequest(t, f.svc, manager, http.MethodPut, "/movies/m-nr/rating", map[string]any{"content_rating": "G"}); rec.Code != http.StatusOK {
		t.Fatalf("override: %d %s", rec.Code, rec.Body.String())
	}
	var age sql.NullInt64
	var source string
	_ = f.svc.DB.QueryRow(`SELECT rating_age, rating_source FROM movies WHERE id = 'm-nr'`).Scan(&age, &source)
	if !age.Valid || age.Int64 != 0 || source != "admin" {
		t.Fatalf("stored override: %v %q", age, source)
	}
	var n int
	_ = f.svc.DB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'content.rating.set'`).Scan(&n)
	if n != 1 {
		t.Fatalf("override not audited: %d", n)
	}
	if _, err := f.svc.GetMovie(WithUserID(context.Background(), "child"), "m-nr"); err != nil {
		t.Fatalf("child after G override: %v", err)
	}
	if rec := ratingRequest(t, f.svc, manager, http.MethodPut, "/movies/m-nr/rating", map[string]any{"content_rating": "Custom", "rating_age": 40}); rec.Code != http.StatusBadRequest {
		t.Fatalf("out of range age: %d", rec.Code)
	}
	if rec := ratingRequest(t, f.svc, manager, http.MethodPut, "/movies/missing/rating", map[string]any{"content_rating": "G"}); rec.Code != http.StatusNotFound {
		t.Fatalf("missing title: %d", rec.Code)
	}
	if rec := ratingRequest(t, f.svc, manager, http.MethodDelete, "/movies/m-nr/rating", nil); rec.Code != http.StatusOK {
		t.Fatalf("reset: %d", rec.Code)
	}
	_ = f.svc.DB.QueryRow(`SELECT rating_age, rating_source FROM movies WHERE id = 'm-nr'`).Scan(&age, &source)
	if age.Valid || source != "" {
		t.Fatalf("reset left %v %q", age, source)
	}
}

func TestArtworkHonoursRestriction(t *testing.T) {
	f := newRatedFixture(t)
	child := &auth.Principal{Kind: auth.KindUser, UserID: "child"}
	if rec := ratingRequest(t, f.svc, child, http.MethodGet, "/artwork/poster/movie/m-r", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("restricted artwork: %d", rec.Code)
	}
	if rec := ratingRequest(t, f.svc, child, http.MethodGet, "/movies/m-r", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("restricted detail over HTTP: %d", rec.Code)
	}
	if rec := ratingRequest(t, f.svc, child, http.MethodGet, "/movies/m-pg", nil); rec.Code != http.StatusOK {
		t.Fatalf("allowed detail over HTTP: %d", rec.Code)
	}
}

func TestCertificationAge(t *testing.T) {
	cases := []struct {
		country, cert string
		age           int
		ok            bool
	}{
		{"US", "PG-13", 13, true},
		{"US", "TV-MA", 17, true},
		{"US", "NR", 0, false},
		{"GB", "12A", 12, true},
		{"GB", "PG", 8, true},
		{"AU", "MA15+", 15, true},
		{"DE", "16", 16, true},
		{"DE", "FSK 12", 12, true},
		{"", "R", 17, true},
		{"", "", 0, false},
		{"", "Unrated", 0, false},
		{"FR", "U", 0, true},
		{"", "99", 0, false},
	}
	for _, c := range cases {
		age, ok := CertificationAge(c.country, c.cert)
		if age != c.age || ok != c.ok {
			t.Errorf("%s %q = %d %v, want %d %v", c.country, c.cert, age, ok, c.age, c.ok)
		}
	}
}

func TestVisibilityAppliesGrantsAndRatings(t *testing.T) {
	f := newRatedFixture(t)
	ctx := context.Background()
	other, err := f.svc.Create(ctx, "Other", t.TempDir(), "mixed")
	if err != nil {
		t.Fatal(err)
	}
	addMovie(t, f.svc, other.ID, "m-other", "Elsewhere", 0)

	check := func(ctx context.Context, want map[string]bool) {
		t.Helper()
		v, err := NewVisibility(ctx, f.svc.DB)
		if err != nil {
			t.Fatal(err)
		}
		for id, exp := range want {
			kind := "movie"
			if id == "s-ma" {
				kind = "series"
			}
			if id == "e1" {
				kind = "episode"
			}
			got, err := v.Item(ctx, kind, id)
			if err != nil || got != exp {
				t.Errorf("%s visible=%v err=%v, want %v", id, got, err, exp)
			}
		}
	}
	check(ctx, map[string]bool{"m-pg": true, "m-r": true, "m-other": true, "missing": false})
	child := WithGrantedIDs(WithUserID(ctx, "child"), []string{f.libID})
	check(child, map[string]bool{"m-pg": true, "m-r": false, "m-nr": false, "s-ma": false, "e1": false, "m-other": false})
	check(WithGrantedIDs(WithUserID(ctx, "adult"), []string{}), map[string]bool{"m-pg": false})
}
