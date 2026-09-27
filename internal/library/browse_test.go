package library

import (
	"context"
	"reflect"
	"testing"
)

func TestIsAnime(t *testing.T) {
	if !IsAnime([]string{"Action", "Anime"}, "", "") {
		t.Fatal("an Anime genre marks a title as anime")
	}
	if !IsAnime(nil, "Anime Shows", "") || !IsAnime(nil, "", "anime") {
		t.Fatal("a library named anime marks its titles as anime")
	}
	if IsAnime([]string{"Animation"}, "Cartoons", "Kids") {
		t.Fatal("animation alone is not anime")
	}
}

func TestEncodeGenresCleansList(t *testing.T) {
	if got := EncodeGenres([]string{" Drama", "drama", "", "Sci-Fi & Fantasy"}); got != `["Drama","Sci-Fi \u0026 Fantasy"]` {
		t.Fatalf("got %s", got)
	}
	if got := EncodeGenres(nil); got != "[]" {
		t.Fatalf("nil genres should encode as an empty list, got %s", got)
	}
	if got := ParseGenres(""); len(got) != 0 {
		t.Fatalf("unlooked-up genres should parse empty, got %v", got)
	}
}

func TestBrowseSignals(t *testing.T) {
	svc, _ := testDB(t)
	ctx := context.Background()
	lib, err := svc.Create(ctx, "Anime", t.TempDir(), "mixed")
	if err != nil {
		t.Fatal(err)
	}
	addUser(t, svc, "u1", -1)
	addUser(t, svc, "u2", -1)
	addMovie(t, svc, lib.ID, "watched", "Seen", nil)
	addMovie(t, svc, lib.ID, "same-genre", "Similar", nil)
	addMovie(t, svc, lib.ID, "popular", "Popular", nil)
	exec(t, svc, `UPDATE movies SET genres_json = '["Horror","Thriller"]' WHERE id IN ('watched', 'same-genre')`)
	exec(t, svc, `UPDATE movies SET genres_json = '["Comedy"]' WHERE id = 'popular'`)
	exec(t, svc, `INSERT INTO playback_progress(user_id, item_kind, item_id, position_ms, duration_ms, updated_at) VALUES ('u1', 'movie', 'watched', 10, 100, 't')`)
	exec(t, svc, `INSERT INTO playback_progress(user_id, item_kind, item_id, position_ms, duration_ms, updated_at) VALUES ('u2', 'movie', 'popular', 10, 100, 't')`)
	exec(t, svc, `INSERT INTO playback_progress(user_id, item_kind, item_id, position_ms, duration_ms, updated_at) VALUES ('u2', 'movie', 'hidden', 10, 100, 't')`)

	sig, err := svc.BrowseSignals(WithUserID(ctx, "u1"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sig.Watched, []string{"movie:watched"}) {
		t.Fatalf("watched = %v", sig.Watched)
	}
	if sig.Views["movie:popular"] != 1 || sig.Views["movie:watched"] != 1 {
		t.Fatalf("views = %v", sig.Views)
	}
	if _, ok := sig.Views["movie:hidden"]; ok {
		t.Fatal("views must only cover titles the viewer can see")
	}
	if len(sig.Recommended) < 2 || sig.Recommended[0] != "movie:same-genre" {
		t.Fatalf("a title sharing watched genres should be recommended first, got %v", sig.Recommended)
	}
	for _, k := range sig.Recommended {
		if k == "movie:watched" {
			t.Fatal("watched titles are not recommended")
		}
	}

	movies, err := svc.ListMovies(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range movies {
		if !m.Anime {
			t.Fatalf("%s is in a library named Anime", m.ID)
		}
	}

	denied, err := svc.BrowseSignals(WithGrantedIDs(WithUserID(ctx, "u1"), []string{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(denied.Views) != 0 || len(denied.Watched) != 0 || len(denied.Recommended) != 0 {
		t.Fatalf("a viewer without library access sees no signals, got %+v", denied)
	}
}
