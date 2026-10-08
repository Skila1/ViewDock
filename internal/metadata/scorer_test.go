package metadata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScoreUniqueWinner(t *testing.T) {
	cands := []SearchResult{
		{ID: 1, Title: "The Matrix", ReleaseDate: "1999-03-31"},
		{ID: 2, Title: "The Matrix Reloaded", ReleaseDate: "2003-05-15"},
		{ID: 3, Title: "Matrixxx", ReleaseDate: "2010-01-01"},
	}
	scored := Score("The Matrix", 1999, cands)
	if len(scored) == 0 || scored[0].Candidate.ID != 1 {
		t.Fatalf("top %+v", scored)
	}
	win, ok := UniqueWinner(scored)
	if !ok || win.ID != 1 {
		t.Fatalf("winner %+v ok=%v", win, ok)
	}
}

func TestScoreClearWinnerDespiteSecondAboveThreshold(t *testing.T) {
	cands := []SearchResult{
		{ID: 1, Title: "Dune", ReleaseDate: "2021-10-22"},
		{ID: 2, Title: "Dune", ReleaseDate: "2020-01-01"},
	}
	scored := Score("Dune", 2021, cands)
	win, ok := UniqueWinner(scored)
	if !ok || win.ID != 1 {
		t.Fatalf("want 2021 Dune as unique winner, got %+v ok=%v scored=%+v", win, ok, scored)
	}
}

func TestScoreNoUniqueWhenTie(t *testing.T) {
	cands := []SearchResult{
		{ID: 1, Title: "Foo Bar", ReleaseDate: "2020-01-01"},
		{ID: 2, Title: "Foo Bar", ReleaseDate: "2020-02-02"},
	}
	scored := Score("Foo Bar", 2020, cands)
	if _, ok := UniqueWinner(scored); ok {
		t.Fatal("expected no unique winner")
	}
}

func TestScorerHTTPTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/search/movie" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"id": 603, "title": "The Matrix", "release_date": "1999-03-31", "overview": "wake up"},
				{"id": 604, "title": "The Matrix Reloaded", "release_date": "2003-05-15"},
			},
		})
	}))
	t.Cleanup(srv.Close)

	c := NewTestClient(srv.Client(), srv.URL)
	c.Keys = staticKey("test")
	cands, err := c.Search(context.Background(), "movie", "The Matrix", 1999)
	if err != nil {
		t.Fatal(err)
	}
	scored := Score("The Matrix", 1999, cands)
	win, ok := UniqueWinner(scored)
	if !ok || win.ID != 603 {
		t.Fatalf("winner %+v ok=%v scored=%+v", win, ok, scored)
	}
}

type staticKey string

func (s staticKey) Get(ctx context.Context, key string) (string, error) {
	return string(s), nil
}

func TestRejectsUserURL(t *testing.T) {
	c := NewClient(nil)
	if _, err := c.resolve("https://api.themoviedb.org", "https://evil.example/x", nil); err == nil {
		t.Fatal("absolute path should fail")
	}
}
