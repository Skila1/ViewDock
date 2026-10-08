package httpapi

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/config"
)

func realIPServer(t *testing.T, trusted ...string) (http.Handler, *string, *string) {
	t.Helper()
	cfg := config.Config{}
	for _, c := range trusted {
		cfg.TrustedProxies = append(cfg.TrustedProxies, mustCIDR(c))
	}
	s := New(cfg, nil, nil, fstest.MapFS{"index.html": {Data: []byte("<html>ok</html>")}})
	var remote, client string
	s.APIMounts = append(s.APIMounts, func(r chi.Router) {
		r.Get("/whoami", func(w http.ResponseWriter, r *http.Request) {
			remote = r.RemoteAddr
			client = ClientIPString(r, cfg)
			w.WriteHeader(http.StatusNoContent)
		})
	})
	return s.Handler(), &remote, &client
}

func TestRealIPIgnoresSpoofedHeadersFromUntrustedPeer(t *testing.T) {
	h, remote, client := realIPServer(t, "10.0.0.0/8")
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP", "True-Client-IP"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
		req.RemoteAddr = "203.0.113.4:5555"
		req.Header.Set(header, "10.1.2.3")
		h.ServeHTTP(httptest.NewRecorder(), req)
		if *remote != "203.0.113.4:5555" {
			t.Fatalf("%s: RemoteAddr rewritten to %q", header, *remote)
		}
		if *client != "203.0.113.4" {
			t.Fatalf("%s: client IP %q", header, *client)
		}
	}
}

func TestRealIPHonorsTrustedProxy(t *testing.T) {
	h, remote, client := realIPServer(t, "10.0.0.0/8")
	cases := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"xff first entry", map[string]string{"X-Forwarded-For": "198.51.100.7, 10.0.0.2"}, "198.51.100.7"},
		{"x-real-ip before xff", map[string]string{"X-Real-IP": "198.51.100.8", "X-Forwarded-For": "198.51.100.9"}, "198.51.100.8"},
		{"true-client-ip first", map[string]string{"True-Client-IP": "2001:db8::1", "X-Real-IP": "198.51.100.8"}, "2001:db8::1"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
		req.RemoteAddr = "10.0.0.2:4444"
		for k, v := range tc.headers {
			req.Header.Set(k, v)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		if *remote != tc.want {
			t.Fatalf("%s: RemoteAddr %q, want %q", tc.name, *remote, tc.want)
		}
		if *client != tc.want {
			t.Fatalf("%s: client IP %q, want %q", tc.name, *client, tc.want)
		}
	}
}

func TestRealIPTrustedProxyInvalidHeaderKeepsPeer(t *testing.T) {
	h, remote, _ := realIPServer(t, "127.0.0.1/32")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
	req.RemoteAddr = "127.0.0.1:9000"
	req.Header.Set("X-Forwarded-For", "not-an-ip")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if *remote != "127.0.0.1:9000" {
		t.Fatalf("RemoteAddr %q", *remote)
	}
}

func TestForwardedClientIPParsing(t *testing.T) {
	h := http.Header{}
	h.Set("X-Forwarded-For", " 192.0.2.1 ,10.0.0.1")
	if got := forwardedClientIP(h); got != "192.0.2.1" {
		t.Fatalf("got %q", got)
	}
	if ip := peerIP("[2001:db8::2]:80"); !ip.Equal(net.ParseIP("2001:db8::2")) {
		t.Fatalf("peer %v", ip)
	}
}
