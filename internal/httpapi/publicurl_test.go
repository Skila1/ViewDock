package httpapi

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/viewdock/viewdock/internal/config"
)

type memKV map[string]string

func (m memKV) Get(_ context.Context, key string) (string, error) {
	return m[key], nil
}

func TestResolvePublicURLPrefersSettings(t *testing.T) {
	cfg := config.Config{PublicURL: "https://from-env.example"}
	if got := ResolvePublicURL(context.Background(), cfg, nil); got != "https://from-env.example" {
		t.Fatalf("env fallback %s", got)
	}
	kv := memKV{settingPublicURL: "https://from-ui.example/"}
	if got := ResolvePublicURL(context.Background(), cfg, kv); got != "https://from-ui.example" {
		t.Fatalf("settings win %s", got)
	}
	req := httptest.NewRequest("GET", "http://lan.local:8080/", nil)
	req.Host = "lan.local:8080"
	if got := PublicBase(req, config.Config{}, nil); got != "http://lan.local:8080" {
		t.Fatalf("request host %s", got)
	}
}

func TestPublicOriginChecker(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		cfg    config.Config
		kv     SettingsLookup
		want   bool
	}{
		{name: "same origin", origin: "http://lan.local:8080", want: true},
		{name: "untrusted origin", origin: "https://evil.example", want: false},
		{name: "missing origin", want: false},
		{name: "configured public origin", origin: "https://app.example", cfg: config.Config{PublicURL: "https://app.example"}, want: true},
		{name: "configured origin allowlist", origin: "https://frontend.example", cfg: config.Config{AllowedOrigins: []string{"https://frontend.example"}}, want: true},
		{name: "malformed origin", origin: "https://app.example/path", cfg: config.Config{PublicURL: "https://app.example"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://lan.local:8080/api/v1/watch-together/rooms/r/ws", nil)
			req.Header.Set("Origin", tt.origin)
			if got := PublicOriginChecker(tt.cfg, tt.kv)(req); got != tt.want {
				t.Fatalf("origin %q: got %v, want %v", tt.origin, got, tt.want)
			}
		})
	}
}

func TestPublicOriginCheckerDoesNotTrustForwardedProtoFromDirectClient(t *testing.T) {
	req := httptest.NewRequest("GET", "http://lan.local:8080/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Origin", "https://lan.local:8080")
	if PublicOriginChecker(config.Config{}, nil)(req) {
		t.Fatal("direct client must not control the expected scheme")
	}
}
