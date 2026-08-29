package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/httpapi"
)

func TestAPIKeyBearerSkipsCSRFAndReadsLogsPerm(t *testing.T) {
	s := testSvc(t)
	if _, err := s.CreateAdmin(context.Background(), "admin", "secret12", "Admin"); err != nil {
		t.Fatal(err)
	}

	r := chi.NewRouter()
	r.Use(s.Middleware)
	r.Use(s.CSRF)
	s.Routes(r)
	r.With(RequirePerm(PermLogsRead)).Get("/admin/logs-check", func(w http.ResponseWriter, _ *http.Request) {
		httpapi.WriteOK(w)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/csrf", nil)
	r.ServeHTTP(rec, req)
	var csrf struct{ Token string }
	_ = json.Unmarshal(rec.Body.Bytes(), &csrf)
	jar := map[string]*http.Cookie{}
	putCookies := func(cs []*http.Cookie) {
		for _, c := range cs {
			jar[c.Name] = c
		}
	}
	addCookies := func(req *http.Request) {
		for _, c := range jar {
			req.AddCookie(c)
		}
	}
	putCookies(rec.Result().Cookies())

	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret12"})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf.Token)
	addCookies(req)
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	putCookies(rec.Result().Cookies())

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/auth/csrf", nil)
	addCookies(req)
	r.ServeHTTP(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &csrf)
	putCookies(rec.Result().Cookies())

	createBody, _ := json.Marshal(map[string]any{"name": "agent", "scopes": []string{"logs.read"}})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/admin/api-keys", bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf.Token)
	addCookies(req)
	r.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("create key %d %s", rec.Code, rec.Body.String())
	}
	var created APIKey
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Secret == "" || len(created.Secret) < 3 || created.Secret[:3] != "vd_" {
		t.Fatalf("secret %q", created.Secret)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/admin/logs-check", nil)
	req.Header.Set("Authorization", "Bearer "+created.Secret)
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("bearer logs %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/admin/api-keys", bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+created.Secret)
	r.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("logs-only key must not create keys, got %d %s", rec.Code, rec.Body.String())
	}
}
