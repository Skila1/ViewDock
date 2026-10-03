package library

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// profileServer serves the library routes as user, the way the catalogue
// middleware does in production.
func profileServer(t *testing.T, svc *Service, user string) func(method, path, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(WithUserID(req.Context(), user)))
		})
	})
	r.Route("/api/v1", svc.Routes)
	return func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body)))
		return rec
	}
}

func TestProfileLists(t *testing.T) {
	svc, _ := testDB(t)
	ctx := context.Background()
	lib, err := svc.Create(ctx, "Movies", t.TempDir(), "mixed")
	if err != nil {
		t.Fatal(err)
	}
	addUser(t, svc, "u1", -1)
	addUser(t, svc, "u2", -1)
	addMovie(t, svc, lib.ID, "m1", "One", nil)
	addMovie(t, svc, lib.ID, "m2", "Two", nil)
	call := profileServer(t, svc, "u1")
	other := profileServer(t, svc, "u2")

	t.Run("my list belongs to the profile", func(t *testing.T) {
		if rec := call("PUT", "/me/watchlist/movie/m1", ""); rec.Code != 200 {
			t.Fatalf("add: %d %s", rec.Code, rec.Body)
		}
		if rec := call("PUT", "/me/watchlist/movie/missing", ""); rec.Code != 404 {
			t.Fatalf("unknown titles are refused: %d", rec.Code)
		}
		var list []listEntry
		_ = json.Unmarshal(call("GET", "/me/watchlist", "").Body.Bytes(), &list)
		if len(list) != 1 || list[0].ID != "m1" {
			t.Fatalf("list %+v", list)
		}
		_ = json.Unmarshal(other("GET", "/me/watchlist", "").Body.Bytes(), &list)
		if len(list) != 0 {
			t.Fatalf("another profile sees this list: %+v", list)
		}
		call("DELETE", "/me/watchlist/movie/m1", "")
		_ = json.Unmarshal(call("GET", "/me/watchlist", "").Body.Bytes(), &list)
		if len(list) != 0 {
			t.Fatalf("removed title still listed: %+v", list)
		}
	})

	t.Run("playlists", func(t *testing.T) {
		rec := call("POST", "/me/playlists", `{"name":"Friday night"}`)
		var p playlist
		if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &p) != nil || p.ID == "" {
			t.Fatalf("create: %d %s", rec.Code, rec.Body)
		}
		if rec := call("POST", "/me/playlists/"+p.ID+"/items", `{"kind":"movie","id":"m2"}`); rec.Code != 200 {
			t.Fatalf("add item: %d %s", rec.Code, rec.Body)
		}
		if rec := other("POST", "/me/playlists/"+p.ID+"/items", `{"kind":"movie","id":"m1"}`); rec.Code != 404 {
			t.Fatalf("another profile edited the playlist: %d", rec.Code)
		}
		var lists []playlist
		_ = json.Unmarshal(call("GET", "/me/playlists", "").Body.Bytes(), &lists)
		if len(lists) != 1 || lists[0].Name != "Friday night" || len(lists[0].Items) != 1 || lists[0].Items[0].ID != "m2" {
			t.Fatalf("playlists %+v", lists)
		}
		if rec := call("POST", "/me/playlists", `{"name":""}`); rec.Code != 400 {
			t.Fatalf("empty name: %d", rec.Code)
		}
		call("DELETE", "/me/playlists/"+p.ID, "")
		_ = json.Unmarshal(call("GET", "/me/playlists", "").Body.Bytes(), &lists)
		if len(lists) != 0 {
			t.Fatalf("deleted playlist listed: %+v", lists)
		}
	})

	t.Run("continue dismissal and history", func(t *testing.T) {
		exec(t, svc, `INSERT INTO playback_progress(user_id, item_kind, item_id, position_ms, duration_ms, updated_at) VALUES ('u1', 'movie', 'm1', 60000, 6000000, '2026-01-01T00:00:00Z')`)
		exec(t, svc, `INSERT INTO watch_history(id, user_id, item_kind, item_id, watched_at, position_ms) VALUES ('h1', 'u1', 'movie', 'm1', '2026-01-01T00:00:00Z', 60000)`)
		exec(t, svc, `INSERT INTO watch_history(id, user_id, item_kind, item_id, watched_at, position_ms) VALUES ('h2', 'u1', 'movie', 'm1', '2026-01-01T00:00:10Z', 70000)`)
		if rec := call("PUT", "/titles/movie/m1/continue", `{"dismissed":true}`); rec.Code != 200 {
			t.Fatalf("dismiss: %d", rec.Code)
		}
		var dismissed int
		_ = svc.DB.QueryRow(`SELECT dismissed FROM playback_progress WHERE user_id = 'u1' AND item_id = 'm1'`).Scan(&dismissed)
		if dismissed != 1 {
			t.Fatal("not dismissed")
		}
		var hist []historyEntry
		_ = json.Unmarshal(call("GET", "/me/history", "").Body.Bytes(), &hist)
		if len(hist) != 1 || hist[0].Title != "One" || hist[0].PositionMS != 60000 || hist[0].ID != "movie:m1" || hist[0].WatchedAt != "2026-01-01T00:00:10Z" {
			t.Fatalf("one entry per title, with the latest viewing and current progress: %+v", hist)
		}
		if rec := other("DELETE", "/me/history/movie:m1", ""); rec.Code != 200 {
			t.Fatalf("delete: %d", rec.Code)
		}
		_ = json.Unmarshal(call("GET", "/me/history", "").Body.Bytes(), &hist)
		if len(hist) != 1 {
			t.Fatal("another profile deleted this history entry")
		}
		call("DELETE", "/me/history/movie:m1", "")
		_ = json.Unmarshal(call("GET", "/me/history", "").Body.Bytes(), &hist)
		if len(hist) != 0 {
			t.Fatalf("deleted entry listed: %+v", hist)
		}
	})

	t.Run("remembered tracks", func(t *testing.T) {
		if rec := call("PUT", "/titles/movie/m1/tracks", `{"audio_index":2,"subtitle_index":-1}`); rec.Code != 200 {
			t.Fatalf("tracks: %d %s", rec.Code, rec.Body)
		}
		call("PUT", "/titles/movie/m1/tracks", `{"subtitle_index":4}`)
		if a, s := ProfileTracks(ctx, svc.DB, "u1", "movie", "m1"); a != 2 || s != 4 {
			t.Fatalf("audio %d subtitle %d, want 2 and 4 (a later subtitle pick keeps the audio)", a, s)
		}
		if a, s := ProfileTracks(ctx, svc.DB, "u2", "movie", "m1"); a != -1 || s != -2 {
			t.Fatalf("another profile inherits tracks: %d %d", a, s)
		}
	})

	t.Run("not interested hides without counting against the genre", func(t *testing.T) {
		if rec := call("PUT", "/titles/movie/m2/reaction", `{"reaction":"not_interested"}`); rec.Code != 200 {
			t.Fatalf("reaction: %d %s", rec.Code, rec.Body)
		}
		sig, err := svc.BrowseSignals(WithUserID(ctx, "u1"))
		if err != nil {
			t.Fatal(err)
		}
		if len(sig.NotInterested) != 1 || len(sig.Disliked) != 0 {
			t.Fatalf("signals %+v", sig)
		}
		for _, k := range sig.Recommended {
			if k == "movie:m2" {
				t.Fatal("not interested title recommended")
			}
		}
	})
}
