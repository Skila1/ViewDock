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
