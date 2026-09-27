package jellyfin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/decision"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/secrets"
)

func fakeJellyfin(t *testing.T) *httptest.Server {
	t.Helper()
	authed := func(r *http.Request) bool { return strings.Contains(r.Header.Get("Authorization"), `Token="tok"`) }
	mux := http.NewServeMux()
	mux.HandleFunc("/System/Info/Public", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ServerName":"Home","Version":"10.10.0"}`))
	})
	mux.HandleFunc("/Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"AccessToken":"tok","User":{"Id":"u1"}}`))
	})
	mux.HandleFunc("/Sessions/Logout", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
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
				"MediaSources":[{"Id":"m1","Container":"mkv","MediaStreams":[{"Type":"Video","Codec":"hevc"}]}]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"TotalRecordCount":4,"Items":[
			{"Id":"m1","Name":"Dune","Type":"Movie","ProductionYear":2021,"ProviderIds":{"Tmdb":"438631"},"OfficialRating":"PG-13"},
			{"Id":"m2","Name":"Only Remote","Type":"Movie","ProductionYear":2020,"ImageTags":{"Primary":"t1"}},
			{"Id":"s1","Name":"Show","Type":"Series","ProductionYear":2019},
			{"Id":"e1","Name":"Pilot","Type":"Episode","SeriesId":"s1","ParentIndexNumber":1,"IndexNumber":1}]}`))
	})
	mux.HandleFunc("/Items/m2/Images/Primary", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	})
	mux.HandleFunc("/Videos/m1/master.m3u8", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) || r.URL.Query().Get("api_key") != "" {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte("#EXTM3U\nmain.m3u8\n"))
	})
	mux.HandleFunc("/Videos/ActiveEncodings", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
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

	stream, options, err := svc.Resolve(ctx, "movie", "dune", "", true)
	if err != nil || stream != nil || len(options) != 2 {
		t.Fatalf("local preferred: %v %v %v", stream, options, err)
	}
	stream, _, err = svc.Resolve(ctx, "movie", "dune", options[1].ID, true)
	if err != nil || stream == nil || stream.Delivery != decision.DeliveryHLS || stream.DurationMS != 6_000_000 {
		t.Fatalf("remote pick: %+v %v", stream, err)
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
	if rec := get(stream.URL); rec.Code != http.StatusGone {
		t.Fatalf("revoked grant: %d", rec.Code)
	}
	if rec := get("/api/v1/admin/media-sources"); rec.Code == 200 {
		t.Fatalf("admin list without admin: %d", rec.Code)
	}
}
