package jellyfin

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/capability"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/decision"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/playback"
	"github.com/viewdock/viewdock/internal/search"
	"github.com/viewdock/viewdock/internal/secrets"
)

type fakeCalls struct {
	images, logouts, transcodes int
}

func fakeJellyfin(t *testing.T) *httptest.Server {
	srv, _ := fakeJellyfinCalls(t)
	return srv
}

func fakeJellyfinCalls(t *testing.T) (*httptest.Server, *fakeCalls) {
	t.Helper()
	calls := &fakeCalls{}
	authed := func(r *http.Request) bool {
		h := r.Header.Get("Authorization")
		return strings.Contains(h, `Token="tok"`) || strings.Contains(h, `Token="key1"`)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/Users", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`[{"Id":"u1","Name":"viewdock","Policy":{"IsAdministrator":false}},{"Id":"u2","Name":"owner","Policy":{"IsAdministrator":true}}]`))
	})
	mux.HandleFunc("/System/ActivityLog/Entries", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`{"Items":[
			{"Date":"2026-09-27T10:00:00Z","Type":"VideoPlayback","Name":"viewdock is playing Dune","Severity":"Information","UserId":"u1","ShortOverview":"IP address: 10.0.0.9"},
			{"Date":"2026-09-27T10:01:00Z","Type":"SessionStarted","Name":"owner signed in","Severity":"Information","UserId":"u2"}]}`))
	})
	mux.HandleFunc("/System/Info/Public", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ServerName":"Home","Version":"10.10.0"}`))
	})
	mux.HandleFunc("/Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"AccessToken":"tok","User":{"Id":"u1"}}`))
	})
	mux.HandleFunc("/Sessions/Logout", func(w http.ResponseWriter, r *http.Request) {
		calls.logouts++
		w.WriteHeader(204)
	})
	mux.HandleFunc("/Users/u1/Views", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`{"Items":[{"Id":"v1","Name":"Movies","CollectionType":"movies"}]}`))
	})
	mux.HandleFunc("/Items", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			w.WriteHeader(401)
			return
		}
		if r.URL.Query().Get("Ids") != "" {
			_, _ = w.Write([]byte(`{"Items":[{"Id":"m1","Type":"Movie","RunTimeTicks":60000000000,
				"MediaSources":[{"Id":"m1","Container":"mkv","MediaStreams":[{"Type":"Video","Codec":"hevc","Height":2160}]}]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"TotalRecordCount":4,"Items":[
			{"Id":"m1","Name":"Dune","Type":"Movie","ProductionYear":2021,"ProviderIds":{"Tmdb":"438631"},"OfficialRating":"PG-13"},
			{"Id":"m2","Name":"Only Remote","Type":"Movie","ProductionYear":2020,"ImageTags":{"Primary":"t1"},"BackdropImageTags":["b1"],"Genres":["Comedy","comedy"]},
			{"Id":"s1","Name":"Show","Type":"Series","ProductionYear":2019,"Genres":["Anime"]},
			{"Id":"e1","Name":"Pilot","Type":"Episode","SeriesId":"s1","ParentIndexNumber":1,"IndexNumber":1}]}`))
	})
	mux.HandleFunc("/Items/m2/Images/Primary", func(w http.ResponseWriter, r *http.Request) {
		calls.images++
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	})
	mux.HandleFunc("/Items/m2/Images/Backdrop/0", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("tag") != "b1" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpg"))
	})
	mux.HandleFunc("/Videos/m1/master.m3u8", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) || r.URL.Query().Get("api_key") != "" {
			w.WriteHeader(401)
			return
		}
		calls.transcodes++
		_, _ = w.Write([]byte("#EXTM3U\nmain.m3u8\n"))
	})
	mux.HandleFunc("/Videos/ActiveEncodings", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, calls
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	path := t.TempDir() + "/viewdock.db"
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return sqlDB
}

func waitSynced(t *testing.T, svc *Service) Source {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		list, _ := svc.list(context.Background())
		if len(list) == 1 && list[0].Status != "pending" && list[0].Status != "syncing" && !list[0].Syncing {
			return list[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("sync did not finish: %+v", list)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAPIKeySourceHonoursUsageRestrictions(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	cipher, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	jf, calls := fakeJellyfinCalls(t)
	svc := New(sqlDB, func() *secrets.Cipher { return cipher }, t.TempDir(), nil)

	rec := httptest.NewRecorder()
	svc.handleTest(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"url":"`+jf.URL+`","auth_mode":"api_key","api_key":"key1"}`)))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"name":"viewdock"`) || strings.Contains(rec.Body.String(), "key1") {
		t.Fatalf("api key test: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	body := `{"url":"` + jf.URL + `","auth_mode":"api_key","api_key":"key1","remote_user_id":"u1",
		"policy":{"images":false,"stream":true,"transcode":false,"activity_log":true,"max_streams":1}}`
	svc.handleCreate(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), "key1") {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	src := waitSynced(t, svc)
	if src.Status != "ok" || src.AuthMode != AuthAPIKey || src.RemoteName != "viewdock" || src.Policy.Images {
		t.Fatalf("source: %+v", src)
	}
	if calls.images != 0 || calls.logouts != 0 {
		t.Fatalf("restricted or revoking calls were sent: %+v", calls)
	}

	var remoteDune string
	_ = sqlDB.QueryRow(`SELECT item_id FROM remote_items WHERE remote_id = 'm1'`).Scan(&remoteDune)
	if _, _, err := svc.Resolve(ctx, "movie", remoteDune, "", "", false, capability.Profile{}); err == nil || calls.transcodes != 0 || !strings.Contains(err.Error(), "transcoding is turned off") {
		t.Fatalf("a file needing transcoding streamed with transcoding off: %v %+v", err, calls)
	}

	r := chi.NewRouter()
	r.Get("/activity/{id}", svc.handleActivity)
	r.Get("/events/{id}", svc.handleEvents)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/activity/"+src.ID, nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "viewdock is playing") ||
		strings.Contains(rec.Body.String(), "owner signed in") || strings.Contains(rec.Body.String(), "10.0.0.9") {
		t.Fatalf("activity: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/events/"+src.ID, nil))
	log := rec.Body.String()
	if !strings.Contains(log, "transcoding, which is not allowed") || !strings.Contains(log, `"kind":"sync"`) || strings.Contains(log, "key1") {
		t.Fatalf("usage log: %s", log)
	}
}

func TestSourceSyncMergeAndStream(t *testing.T) {
	path := t.TempDir() + "/viewdock.db"
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := sqlDB.Exec(`INSERT INTO libraries(id, name, root_path, content_type, created_at, updated_at) VALUES ('local', 'Local', '/media', 'movies', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO movies(id, library_id, title, year, sort_title, created_at, updated_at) VALUES ('dune', 'local', 'Dune', 2021, 'dune', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	cipher, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	jf := fakeJellyfin(t)
	svc := New(sqlDB, func() *secrets.Cipher { return cipher }, t.TempDir(), nil)

	rec := httptest.NewRecorder()
	body := `{"url":"` + jf.URL + `","username":"viewdock","password":"pw"}`
	svc.handleCreate(rec, httptest.NewRequest(http.MethodPost, "/admin/media-sources", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"pw"`) || strings.Contains(rec.Body.String(), "tok") {
		t.Fatalf("credentials leaked: %s", rec.Body.String())
	}
	var src Source
	deadline := time.Now().Add(10 * time.Second)
	for {
		list, _ := svc.list(ctx)
		if len(list) == 1 && list[0].Status == "ok" && !list[0].Syncing {
			src = list[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sync did not finish: %+v", list)
		}
		time.Sleep(50 * time.Millisecond)
	}
	var backdrops int
	_ = sqlDB.QueryRow(`SELECT COUNT(*) FROM artwork a JOIN remote_items ri ON ri.item_id = a.item_id
		WHERE a.kind = 'backdrop' AND ri.remote_id = 'm2' AND a.source = 'jellyfin'`).Scan(&backdrops)
	if backdrops != 1 {
		t.Fatalf("Jellyfin backdrop not stored: %d", backdrops)
	}
	if src.ItemCount != 3 {
		t.Fatalf("item count %d", src.ItemCount)
	}

	granted, err := auth.NewGrantStore(sqlDB).GrantedLibraryIDs(ctx, "someone")
	if err != nil || len(granted) != 1 || granted[0] != src.LibraryID {
		t.Fatalf("remote library not shared with every user: %v %v", granted, err)
	}
	libs := library.NewService(sqlDB, nil, nil, nil, "")
	movies, err := libs.ListMovies(ctx, append(granted, "local"))
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 2 || movies[0].ID != "dune" || movies[1].Title != "Only Remote" || movies[1].PosterURL == nil {
		t.Fatalf("merged listing: %+v", movies)
	}
	if list, _ := libs.List(ctx); len(list) != 1 {
		t.Fatalf("remote library listed with local libraries: %+v", list)
	}
	if g := movies[1].Genres; len(g) != 1 || g[0] != "Comedy" || movies[1].Anime {
		t.Fatalf("remote genres: %+v", movies[1])
	}
	shows, err := libs.ListSeries(ctx, granted)
	if err != nil || len(shows) != 1 || !shows[0].Anime {
		t.Fatalf("remote anime series: %+v %v", shows, err)
	}
	hits, err := search.New(sqlDB).Query(ctx, "remote", nil)
	if err != nil || len(hits) != 1 || hits[0].Title != "Only Remote" {
		t.Fatalf("remote titles are searchable: %+v %v", hits, err)
	}

	stream, options, err := svc.Resolve(ctx, "movie", "dune", "", "", true, capability.Profile{})
	if err != nil || stream != nil || len(options) != 2 {
		t.Fatalf("local preferred: %v %v %v", stream, options, err)
	}
	stream, _, err = svc.Resolve(ctx, "movie", "dune", options[1].ID, "", true, capability.Profile{})
	if err != nil || stream == nil || stream.Delivery != decision.DeliveryHLS || stream.DurationMS != 6_000_000 {
		t.Fatalf("remote pick: %+v %v", stream, err)
	}
	if strings.Contains(options[1].Label, ":") || options[1].Label == "" {
		t.Fatalf("source label should be the server name alone: %q", options[1].Label)
	}
	if u, _ := url.Parse(stream.URL); u.Query().Get("VideoBitrate") != "20000000" || u.Query().Get("MaxHeight") != "1080" || u.Query().Get("VideoCodec") != "h264" {
		t.Fatalf("a device that cannot decode the 4K HEVC source gets a 1080p H.264 re-encode: %s", stream.URL)
	}
	if got := strings.Join(stream.Qualities, ","); got != "auto,1080,720,480" {
		t.Fatalf("qualities for a 2160p source: %s", got)
	}
	stream.Stop()
	capped, _, err := svc.Resolve(ctx, "movie", "dune", options[1].ID, "720", true, capability.Profile{})
	if err != nil || capped == nil {
		t.Fatalf("720 pick: %v", err)
	}
	if u, _ := url.Parse(capped.URL); u.Query().Get("MaxHeight") != "720" || u.Query().Get("VideoBitrate") != "5000000" {
		t.Fatalf("720 preset: %s", capped.URL)
	}
	capped.Stop()
	stream, _, err = svc.Resolve(ctx, "movie", "dune", options[1].ID, "", true, capability.Profile{})
	if err != nil || stream == nil {
		t.Fatalf("remote pick again: %v", err)
	}

	r := chi.NewRouter()
	r.Route("/api/v1", svc.Routes)
	get := func(u string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, u, nil))
		return rec
	}
	if rec := get(stream.URL + "&api_key=other"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "#EXTM3U") {
		t.Fatalf("proxy: %d %s", rec.Code, rec.Body.String())
	}
	for _, bad := range []string{"/Videos/m2/", "/Videos/m1/%2e%2e/%2e%2e/System/"} {
		if rec := get(strings.Replace(stream.URL, "/Videos/m1/", bad, 1)); rec.Code != http.StatusNotFound {
			t.Fatalf("grant escaped its item via %s: %d", bad, rec.Code)
		}
	}
	stream.Stop()
	if got := autoVideoBitrate(mediaSource{Bitrate: 12_000_000, MediaStreams: []mediaStream{{Type: "Video", BitRate: 8_000_000}}}); got != 8_000_000 {
		t.Fatalf("auto bitrate %d, want the video stream bitrate", got)
	}
	if rec := get(stream.URL); rec.Code != http.StatusGone {
		t.Fatalf("revoked grant: %d", rec.Code)
	}
	if rec := get("/api/v1/admin/media-sources"); rec.Code == 200 {
		t.Fatalf("admin list without admin: %d", rec.Code)
	}
}

func TestStreamLimitReplacesTheSameViewer(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	cipher, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	jf := fakeJellyfin(t)
	svc := New(sqlDB, func() *secrets.Cipher { return cipher }, t.TempDir(), nil)
	rec := httptest.NewRecorder()
	body := `{"url":"` + jf.URL + `","username":"viewdock","password":"pw","policy":{"images":true,"stream":true,"transcode":true,"max_streams":1}}`
	svc.handleCreate(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	_ = waitSynced(t, svc)
	var remoteDune string
	if err := sqlDB.QueryRow(`SELECT item_id FROM remote_items WHERE remote_id = 'm1'`).Scan(&remoteDune); err != nil {
		t.Fatal(err)
	}
	mine := playback.WithStreamOwner(ctx, "viewer-a")
	theirs := playback.WithStreamOwner(ctx, "viewer-b")
	first, _, err := svc.Resolve(mine, "movie", remoteDune, "", "", false, capability.Profile{})
	if err != nil || first == nil {
		t.Fatalf("first stream: %v", err)
	}
	second, _, err := svc.Resolve(mine, "movie", remoteDune, "", "", false, capability.Profile{})
	if err != nil || second == nil {
		t.Fatalf("same viewer should replace their own stream: %v", err)
	}
	r := chi.NewRouter()
	r.Route("/api/v1", svc.Routes)
	get := func(u string) int {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, u, nil))
		return rec.Code
	}
	if get(first.URL) != http.StatusGone {
		t.Fatalf("replaced stream still open")
	}
	if _, _, err := svc.Resolve(theirs, "movie", remoteDune, "", "", false, capability.Profile{}); err == nil || !strings.Contains(err.Error(), "ViewDock allows 1 stream at once") || !strings.Contains(err.Error(), "Maximum concurrent streams") {
		t.Fatalf("another viewer should still hit the limit, named as ViewDock's own setting: %v", err)
	}
	svc.mu.Lock()
	for _, g := range svc.grants {
		g.created = time.Now().Add(-time.Minute)
		g.lastHit = time.Time{}
	}
	svc.mu.Unlock()
	taken, _, err := svc.Resolve(theirs, "movie", remoteDune, "", "", false, capability.Profile{})
	if err != nil || taken == nil {
		t.Fatalf("an unread stream should free the slot: %v", err)
	}
	taken.Stop()

	// A viewer's overlapping starts (a party seek racing a quality change)
	// replace each other rather than one refusing the next.
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := svc.Resolve(mine, "movie", remoteDune, "", "", false, capability.Profile{}); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("overlapping starts by one viewer: %v", err)
	}
	svc.mu.Lock()
	n := len(svc.grants)
	svc.mu.Unlock()
	if n != 1 {
		t.Fatalf("viewer holds %d streams, want 1", n)
	}
}

// A watch party opens one stream per member. With no ViewDock cap
// (max_streams 0, the default) every member plays at the same time.
func TestWatchPartyMembersEachStreamWithoutCap(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	cipher, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	jf := fakeJellyfin(t)
	svc := New(sqlDB, func() *secrets.Cipher { return cipher }, t.TempDir(), nil)
	rec := httptest.NewRecorder()
	body := `{"url":"` + jf.URL + `","username":"viewdock","password":"pw","policy":{"images":true,"stream":true,"transcode":true,"max_streams":0}}`
	svc.handleCreate(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	_ = waitSynced(t, svc)
	var remoteDune string
	if err := sqlDB.QueryRow(`SELECT item_id FROM remote_items WHERE remote_id = 'm1'`).Scan(&remoteDune); err != nil {
		t.Fatal(err)
	}
	for _, member := range []string{"host", "guest-1", "guest-2", "guest-3", "guest-4"} {
		st, _, err := svc.Resolve(playback.WithStreamOwner(ctx, member), "movie", remoteDune, "", "", false, capability.Profile{})
		if err != nil || st == nil {
			t.Fatalf("%s could not stream: %v", member, err)
		}
	}
	svc.mu.Lock()
	n := len(svc.grants)
	svc.mu.Unlock()
	if n != 5 {
		t.Fatalf("party holds %d streams, want 5", n)
	}
}

func TestSyncIsDue(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) string { return now.Add(-ago).Format(time.RFC3339) }
	cases := []struct {
		name string
		src  Source
		want bool
	}{
		{"never synced", Source{Enabled: true, Status: "pending"}, true},
		{"disabled", Source{Enabled: false, Status: "ok", LastSyncAt: at(time.Hour)}, false},
		{"recent", Source{Enabled: true, Status: "ok", LastSyncAt: at(29 * time.Minute)}, false},
		{"half hour", Source{Enabled: true, Status: "ok", LastSyncAt: at(30 * time.Minute)}, true},
		{"already syncing", Source{Enabled: true, Status: "syncing", Syncing: true, LastSyncAt: at(time.Hour)}, false},
		{"error just failed", Source{Enabled: true, Status: "error", UpdatedAt: at(5 * time.Minute)}, false},
		{"error retry", Source{Enabled: true, Status: "error", UpdatedAt: at(15 * time.Minute)}, true},
	}
	for _, c := range cases {
		if got := syncIsDue(c.src, now); got != c.want {
			t.Errorf("%s: syncIsDue = %v, want %v", c.name, got, c.want)
		}
	}
}
