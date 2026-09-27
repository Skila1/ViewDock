package search

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/library"
)

func ratedSearchDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	stmts := []string{
		`INSERT INTO libraries(id, name, root_path, content_type, created_at, updated_at) VALUES ('lib', 'L', '/x', 'movies', 't', 't')`,
		`INSERT INTO movies(id, library_id, title, sort_title, created_at, updated_at, rating_age) VALUES ('m-pg', 'lib', 'Space Family', 's', 't', 't', 10)`,
		`INSERT INTO movies(id, library_id, title, sort_title, created_at, updated_at, rating_age) VALUES ('m-r', 'lib', 'Space Horror', 's', 't', 't', 17)`,
		`INSERT INTO users(id, username, password_hash, display_name, email, is_admin, disabled, pin_hash, created_at, updated_at, content_age_limit)
			VALUES ('child', 'child', 'x', 'C', '', 0, 0, '', 't', 't', 12)`,
	}
	for _, s := range stmts {
		if _, err := sqlDB.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	for _, id := range []string{"m-pg", "m-r"} {
		title := "Space Family"
		if id == "m-r" {
			title = "Space Horror"
		}
		if err := library.UpsertFTS(context.Background(), sqlDB, "movie", id, title, 2001, ""); err != nil {
			t.Fatal(err)
		}
	}
	return sqlDB
}

func TestSearchHidesRestrictedTitles(t *testing.T) {
	sqlDB := ratedSearchDB(t)
	svc := New(sqlDB)
	all, err := svc.Query(context.Background(), "Space", nil)
	if err != nil || len(all) != 2 {
		t.Fatalf("unrestricted: %+v %v", all, err)
	}
	child := library.WithUserID(context.Background(), "child")
	hits, err := svc.Query(child, "Space", nil)
	if err != nil || len(hits) != 1 || hits[0].ItemID != "m-pg" {
		t.Fatalf("child hits: %+v %v", hits, err)
	}
	smart, err := svc.Smart(child, "space 2000s", nil, "child")
	if err != nil || len(smart) != 1 || smart[0].ItemID != "m-pg" {
		t.Fatalf("child smart hits: %+v %v", smart, err)
	}
}

func TestSearchHandlerUsesPrincipalWithoutCatalogueContext(t *testing.T) {
	svc := New(ratedSearchDB(t))
	req := httptest.NewRequest(http.MethodGet, "/search?q=Space", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), &auth.Principal{Kind: auth.KindUser, UserID: "child"}))
	rec := httptest.NewRecorder()
	svc.handle(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "m-r") {
		t.Fatalf("restricted hit leaked: %d %s", rec.Code, rec.Body.String())
	}
}
