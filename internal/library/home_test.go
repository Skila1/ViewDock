package library_test

import (
	"testing"
	"time"

	"github.com/viewdock/viewdock/internal/library"
)

func TestEpisodeLabel(t *testing.T) {
	for _, c := range []struct {
		season, number int
		title, want    string
	}{
		{1, 5, "Chapter 5: Inquisition", "S1:E5 - Chapter 5: Inquisition"},
		{2, 1, "Episode 1", "S2:E1"},
		{3, 12, "", "S3:E12"},
	} {
		if got := library.EpisodeLabel(c.season, c.number, c.title); got != c.want {
			t.Errorf("EpisodeLabel(%d, %d, %q) = %q, want %q", c.season, c.number, c.title, got, c.want)
		}
	}
}

func TestNextUp(t *testing.T) {
	f := newFixture(t)
	shows := f.lib("Shows", "tv")
	for _, rel := range []string{
		"Lost/Season 01/Lost S01E01.mkv", "Lost/Season 01/Lost S01E02.mkv", "Lost/Season 01/Lost S01E03.mkv",
		"Lost/Specials/Lost S00E01.mkv",
		"Dark/Season 01/Dark S01E01.mkv", "Dark/Season 01/Dark S01E02.mkv",
		"Done/Season 01/Done S01E01.mkv",
		"Fresh/Season 01/Fresh S01E01.mkv",
	} {
		f.put(shows, rel, rel)
	}
	now := time.Now().UTC()
	if _, err := f.db.Exec(`INSERT INTO users(id, username, password_hash, created_at, updated_at) VALUES ('u1', 'u1', 'x', ?, ?)`, now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	ep := func(show string, season, number int) string {
		var id string
		if err := f.db.QueryRow(`SELECT e.id FROM episodes e JOIN series s ON s.id = e.series_id WHERE s.title = ? AND e.season = ? AND e.number = ?`, show, season, number).Scan(&id); err != nil {
			t.Fatalf("%s S%dE%d: %v", show, season, number, err)
		}
		return id
	}
	watch := func(id string, completed bool, posMS int64, ago time.Duration) {
		c := 0
		if completed {
			c = 1
		}
		if _, err := f.db.Exec(`INSERT INTO playback_progress(user_id, item_kind, item_id, position_ms, duration_ms, completed, updated_at) VALUES ('u1', 'episode', ?, ?, 3600000, ?, ?)`,
			id, posMS, c, now.Add(-ago).Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
	}
	watch(ep("Lost", 1, 1), true, 3600000, 3*time.Hour)  // finished E1: E2 is next up
	watch(ep("Dark", 1, 1), false, 600000, time.Hour)     // E1 under way: Continue Watching, not Next Up
	watch(ep("Done", 1, 1), true, 3600000, 2*time.Hour)   // nothing left
	watch(ep("Fresh", 1, 1), false, 2000, 30*time.Minute) // barely started: still next up

	cards, err := f.svc.NextUp(library.WithUserID(f.ctx, "u1"), "u1", 20)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]library.Card{}
	for _, c := range cards {
		got[c.Title] = c
	}
	if len(cards) != 2 {
		t.Fatalf("next up %+v", cards)
	}
	lost, ok := got["Lost"]
	if !ok || lost.ID != ep("Lost", 1, 2) || lost.Kind != "episode" || lost.Subtitle != "S1:E2" || lost.SeriesID == "" {
		t.Fatalf("Lost: %+v", lost)
	}
	if fresh := got["Fresh"]; fresh.ID != ep("Fresh", 1, 1) {
		t.Fatalf("Fresh: %+v", fresh)
	}
	if cards[0].Title != "Fresh" {
		t.Fatalf("most recently watched show first, got %s", cards[0].Title)
	}
	// Finishing E2 moves Lost on to E3, never to the special.
	watch(ep("Lost", 1, 2), true, 3600000, 0)
	cards, _ = f.svc.NextUp(library.WithUserID(f.ctx, "u1"), "u1", 20)
	if cards[0].Title != "Lost" || cards[0].ID != ep("Lost", 1, 3) {
		t.Fatalf("after E2: %+v", cards[0])
	}
}

func TestItemCardForMovie(t *testing.T) {
	f := newFixture(t)
	movies := f.lib("Movies", "movies")
	f.put(movies, "Heat (1995).mkv", "m")
	id := f.id("movies", movies.ID, "Heat")
	c, err := f.svc.ItemCard(f.ctx, "movie", id)
	if err != nil || c.Title != "Heat" || c.Subtitle != "1995" || c.Kind != "movie" {
		t.Fatalf("card %+v %v", c, err)
	}
}
