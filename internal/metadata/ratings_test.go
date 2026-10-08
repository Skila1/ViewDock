package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viewdock/viewdock/internal/db"
)

func TestCertificationPrefersCountryAndTheatrical(t *testing.T) {
	var r SearchResult
	body := `{"release_dates":{"results":[{"iso_3166_1":"US","release_dates":[
		{"certification":"","type":1},{"certification":"NR","type":4},{"certification":"R","type":3}]}]}}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	if cert, from := r.Certification("GB"); cert != "R" || from != "US" {
		t.Fatalf("fallback to US theatrical: %q %q", cert, from)
	}
	tv := SearchResult{ContentRatings: &ContentRatings{}}
	if tv.HasCertifications("movie") || !tv.HasCertifications("series") {
		t.Fatal("HasCertifications must follow the item kind")
	}
}

func TestBackfillRatingsFromTMDB(t *testing.T) {
	t.Setenv("VD_TMDB_API_KEY", "test-key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("append_to_response") == "" {
			http.Error(w, "missing append_to_response", http.StatusBadRequest)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/3/movie/404"):
			http.NotFound(w, r)
		case strings.HasPrefix(r.URL.Path, "/3/movie/"):
			_, _ = w.Write([]byte(`{"id":1,"title":"A","release_dates":{"results":[{"iso_3166_1":"US","release_dates":[{"certification":"PG-13","type":3}]}]}}`))
		case strings.HasPrefix(r.URL.Path, "/3/tv/"):
			_, _ = w.Write([]byte(`{"id":2,"name":"S","content_ratings":{"results":[{"iso_3166_1":"US","rating":"TV-MA"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "t.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	for _, q := range []string{
		`INSERT INTO libraries(id, name, root_path, content_type, created_at, updated_at) VALUES ('lib', 'L', '/x', 'mixed', 't', 't')`,
		`INSERT INTO movies(id, library_id, title, sort_title, unmatched, tmdb_id, created_at, updated_at) VALUES ('m1', 'lib', 'A', 'a', 0, 1, 't', 't')`,
		`INSERT INTO movies(id, library_id, title, sort_title, unmatched, tmdb_id, created_at, updated_at, content_rating, rating_age, rating_source) VALUES ('m2', 'lib', 'B', 'b', 0, 1, 't', 't', 'G', 0, 'admin')`,
		`INSERT INTO movies(id, library_id, title, sort_title, unmatched, tmdb_id, created_at, updated_at) VALUES ('m3', 'lib', 'C', 'c', 0, 404, 't', 't')`,
		`INSERT INTO series(id, library_id, title, sort_title, unmatched, tmdb_id, created_at, updated_at) VALUES ('s1', 'lib', 'S', 's', 0, 2, 't', 't')`,
	} {
		if _, err := sqlDB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	svc := NewWithClient(sqlDB, NewTestClient(srv.Client(), srv.URL), nil)
	svc.backfillRatings(context.Background())

	check := func(table, id, wantCert string, wantAge sql.NullInt64, wantSource string) {
		t.Helper()
		var cert, source string
		var age sql.NullInt64
		if err := sqlDB.QueryRow(`SELECT content_rating, rating_age, rating_source FROM `+table+` WHERE id = ?`, id).Scan(&cert, &age, &source); err != nil {
			t.Fatal(err)
		}
		if cert != wantCert || age != wantAge || source != wantSource {
			t.Fatalf("%s/%s = %q %v %q, want %q %v %q", table, id, cert, age, source, wantCert, wantAge, wantSource)
		}
	}
	check("movies", "m1", "PG-13", sql.NullInt64{Int64: 13, Valid: true}, "tmdb")
	check("movies", "m2", "G", sql.NullInt64{Int64: 0, Valid: true}, "admin")
	check("movies", "m3", "", sql.NullInt64{}, "tmdb")
	check("series", "s1", "TV-MA", sql.NullInt64{Int64: 17, Valid: true}, "tmdb")
}
