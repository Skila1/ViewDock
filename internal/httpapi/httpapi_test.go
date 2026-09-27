package httpapi

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/db"
)

func TestHealthz(t *testing.T) {
	s := New(config.Config{}, nil, nil, fstest.MapFS{"index.html": {Data: []byte("<html>ok</html>")}})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Fatalf("healthz %d %q", rec.Code, rec.Body.String())
	}
}

func TestSPADoesNotServeAPI(t *testing.T) {
	s := New(config.Config{}, nil, nil, fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<html>") {
		t.Fatal("served spa html for unknown api")
	}
}

func TestSPAFallback(t *testing.T) {
	s := New(config.Config{}, nil, nil, fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/movies", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "<html>") {
		t.Fatalf("body %q", body)
	}
}

func TestClientIPTrustedProxy(t *testing.T) {
	cfg := config.Load()
	cfg.TrustedProxies = []*net.IPNet{mustCIDR("127.0.0.1/32")}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:9"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	ip := ClientIP(req, cfg)
	if ip.String() != "203.0.113.9" {
		t.Fatalf("got %s", ip)
	}
}

func TestClientIPUntrustedIgnoresXFF(t *testing.T) {
	cfg := config.Load()
	cfg.TrustedProxies = []*net.IPNet{mustCIDR("127.0.0.1/32")}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.4:9"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	ip := ClientIP(req, cfg)
	if ip.String() != "203.0.113.4" {
		t.Fatalf("got %s", ip)
	}
}

func TestAllowedOrigins(t *testing.T) {
	s := New(config.Config{
		PublicURL:      "https://app.example/",
		AllowedOrigins: []string{"https://frontend.example", "https://app.example/"},
	}, nil, nil, fstest.MapFS{"index.html": {Data: []byte("<html>ok</html>")}})
	got := s.allowedOrigins()
	if strings.Join(got, ",") != "https://app.example,https://frontend.example" {
		t.Fatalf("origins %v", got)
	}
}

func TestIdempotencyReplaysMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "viewdock.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	s := New(config.Config{}, sqlDB, nil, fstest.MapFS{"index.html": {Data: []byte("<html>ok</html>")}})
	count := 0
	s.APIMounts = append(s.APIMounts, func(r chi.Router) {
		r.Post("/idempotent", func(w http.ResponseWriter, _ *http.Request) {
			count++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"count":1}`))
		})
	})
	handler := s.Handler()
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/idempotent", strings.NewReader("{}"))
		req.Header.Set("Idempotency-Key", "mutation-1")
		req.Header.Set("Authorization", "Bearer test-session")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated || rec.Body.String() != `{"count":1}` {
			t.Fatalf("attempt %d: %d %q", i, rec.Code, rec.Body.String())
		}
	}
	if count != 1 {
		t.Fatalf("handler executed %d times", count)
	}
}

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}
