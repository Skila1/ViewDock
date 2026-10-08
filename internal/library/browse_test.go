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

func TestPreferencesShapeRecommendations(t *testing.T) {
	svc, _ := testDB(t)
	ctx := context.Background()
	lib, err := svc.Create(ctx, "Movies", t.TempDir(), "mixed")
	if err != nil {
		t.Fatal(err)
	}
	addUser(t, svc, "u1", -1)
	addMovie(t, svc, lib.ID, "liked", "Liked", nil)
	addMovie(t, svc, lib.ID, "action2", "More Action", nil)
	addMovie(t, svc, lib.ID, "hated", "Hated", nil)
	addMovie(t, svc, lib.ID, "horror2", "More Horror", nil)
	exec(t, svc, `UPDATE movies SET genres_json = '["Action"]' WHERE id IN ('liked', 'action2')`)
	exec(t, svc, `UPDATE movies SET genres_json = '["Horror"]' WHERE id IN ('hated', 'horror2')`)
	user := WithUserID(ctx, "u1")

	if err := svc.SetReaction(user, "u1", "movie", "liked", ReactionLike); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetReaction(user, "u1", "movie", "hated", ReactionDislike); err != nil {
		t.Fatal(err)
	}
	sig, err := svc.BrowseSignals(user)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sig.Liked, []string{"movie:liked"}) || !reflect.DeepEqual(sig.Disliked, []string{"movie:hated"}) {
		t.Fatalf("liked %v disliked %v", sig.Liked, sig.Disliked)
	}
	if len(sig.Recommended) == 0 || sig.Recommended[0] == "movie:horror2" {
		t.Fatalf("a liked genre should outrank a disliked one: %v", sig.Recommended)
	}
	for _, k := range sig.Recommended {
		if k == "movie:hated" {
			t.Fatalf("disliked titles are never recommended: %v", sig.Recommended)
		}
	}
	if sig.Recommended[len(sig.Recommended)-1] != "movie:horror2" {
		t.Fatalf("the disliked genre should rank last: %v", sig.Recommended)
	}

	// Marking watched finishes the title; unwatching clears it again.
	if err := svc.SetWatched(user, "u1", "movie", "action2", true); err != nil {
		t.Fatal(err)
	}
	sig, _ = svc.BrowseSignals(user)
	if !reflect.DeepEqual(sig.Finished, []string{"movie:action2"}) || !reflect.DeepEqual(sig.Watched, []string{"movie:action2"}) {
		t.Fatalf("finished %v watched %v", sig.Finished, sig.Watched)
	}
	if err := svc.SetWatched(user, "u1", "movie", "action2", false); err != nil {
		t.Fatal(err)
	}
	sig, _ = svc.BrowseSignals(user)
	if len(sig.Finished) != 0 || len(sig.Watched) != 0 {
		t.Fatalf("unwatched title still listed: finished %v watched %v", sig.Finished, sig.Watched)
	}
	if err := svc.SetReaction(user, "u1", "movie", "liked", 0); err != nil {
		t.Fatal(err)
	}
	if sig, _ = svc.BrowseSignals(user); len(sig.Liked) != 0 {
		t.Fatalf("cleared like still listed: %v", sig.Liked)
	}
}
