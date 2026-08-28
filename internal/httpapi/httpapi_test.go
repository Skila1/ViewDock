package httpapi

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/viewdock/viewdock/internal/config"
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

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}
