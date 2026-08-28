package setup

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
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/settings"
)

func setupAPI(t *testing.T, token string) (*API, *settings.Store, config.Config) {
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
	cfg := config.Load()
	cfg.ConfigDir = t.TempDir()
	kv := settings.New(sqlDB)
	a := auth.New(sqlDB, cfg, kv, audit.New(sqlDB))
	t.Setenv("VD_SETUP_TOKEN", token)
	if err := EnsureBootstrap(context.Background(), kv, cfg, nil, 0); err != nil {
		t.Fatal(err)
	}
	return New(a, kv, nil, nil, nil), kv, cfg
}

func postAdmin(t *testing.T, api *API, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	api.Routes(r)
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/setup/admin", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestSetupAdminRejectsMissingToken(t *testing.T) {
	api, _, _ := setupAPI(t, "correct-token-value")
	rec := postAdmin(t, api, map[string]string{"username": "admin", "password": "secret12"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestSetupAdminRejectsWrongToken(t *testing.T) {
	api, _, _ := setupAPI(t, "correct-token-value")
	rec := postAdmin(t, api, map[string]string{
		"username": "admin", "password": "secret12", "bootstrap_token": "nope",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestSetupAdminTokenReuseAndClosed(t *testing.T) {
	api, kv, _ := setupAPI(t, "correct-token-value")
	rec := postAdmin(t, api, map[string]string{
		"username": "admin", "password": "secret12", "bootstrap_token": "correct-token-value",
	})
	if rec.Code != 200 {
		t.Fatalf("first create %d %s", rec.Code, rec.Body.String())
	}
	rec = postAdmin(t, api, map[string]string{
		"username": "other", "password": "secret12", "bootstrap_token": "correct-token-value",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("reuse/closed got %d %s", rec.Code, rec.Body.String())
	}
	if !AdminCreated(context.Background(), kv) || BootstrapPending(context.Background(), kv) {
		t.Fatal("bootstrap should be consumed")
	}
}

func TestSetupStatusDoesNotLeakToken(t *testing.T) {
	api, _, _ := setupAPI(t, "correct-token-value")
	r := chi.NewRouter()
	api.Routes(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/setup/status", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if _, ok := body["bootstrap_token"]; ok {
		t.Fatal("status leaked bootstrap_token")
	}
	if body["bootstrap_required"] != true {
		t.Fatalf("expected bootstrap_required, got %#v", body["bootstrap_required"])
	}
	raw, _ := json.Marshal(body)
	if bytes.Contains(raw, []byte("correct-token-value")) {
		t.Fatal("token value present in status")
	}
}

func TestSetupAdminAfterCreateAdminRejected(t *testing.T) {
	api, _, _ := setupAPI(t, "correct-token-value")
	if _, err := api.Auth.CreateAdmin(context.Background(), "admin", "secret12", "Admin"); err != nil {
		t.Fatal(err)
	}
	rec := postAdmin(t, api, map[string]string{
		"username": "late", "password": "secret12", "bootstrap_token": "correct-token-value",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}
