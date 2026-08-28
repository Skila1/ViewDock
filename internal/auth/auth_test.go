package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/settings"
)

func testSvc(t *testing.T) *Service {
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
	kv := settings.New(sqlDB)
	_ = kv.Set(context.Background(), "setup.complete", "1")
	return New(sqlDB, config.Load(), kv, audit.New(sqlDB))
}

func TestHashPassword(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct horse") || VerifyPassword(h, "wrong") {
		t.Fatal("verify")
	}
}

func TestLoginCookieAndCSRF(t *testing.T) {
	s := testSvc(t)
	_, err := s.CreateAdmin(context.Background(), "admin", "secret12", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Use(s.Middleware)
	r.Use(s.CSRF)
	s.Routes(r)
	r.With(RequireUser).Get("/me-check", func(w http.ResponseWriter, req *http.Request) {
		httpapi.WriteOK(w)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/csrf", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("csrf %d", rec.Code)
	}
	var csrf struct{ Token string }
	_ = json.Unmarshal(rec.Body.Bytes(), &csrf)
	csrfCookie := rec.Result().Cookies()

	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret12"})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf.Token)
	for _, c := range csrfCookie {
		req.AddCookie(c)
	}
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	var session *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookie {
			session = c
		}
	}
	if session == nil {
		t.Fatal("no session cookie")
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/me-check", nil)
	req.AddCookie(session)
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("me %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(session)
	r.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("logout without csrf should 403, got %d", rec.Code)
	}
}

func TestDummy401(t *testing.T) {
	s := testSvc(t)
	r := chi.NewRouter()
	r.Use(s.Middleware)
	r.With(RequireUser).Get("/me", s.handleMe)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/me", nil))
	if rec.Code != 401 {
		t.Fatalf("got %d", rec.Code)
	}
}
