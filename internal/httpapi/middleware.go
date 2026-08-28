package httpapi

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/viewdock/viewdock/internal/config"
)

type ctxKey int

const requestIDKey ctxKey = 1

func ClientIP(r *http.Request, cfg config.Config) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if peer == nil || !cfg.TrustedContains(peer) {
		return peer
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.Split(xff, ",")[0])
		if ip := net.ParseIP(first); ip != nil {
			return ip
		}
	}
	return peer
}

func ClientIPString(r *http.Request, cfg config.Config) string {
	if ip := ClientIP(r, cfg); ip != nil {
		return ip.String()
	}
	return ""
}

const settingPublicURL = "app.public_url"

// ResolvePublicURL prefers Admin → Settings, then VD_PUBLIC_URL.
func ResolvePublicURL(ctx context.Context, cfg config.Config, kv SettingsLookup) string {
	if kv != nil {
		if v, err := kv.Get(ctx, settingPublicURL); err == nil {
			if u := strings.TrimRight(strings.TrimSpace(v), "/"); u != "" {
				return u
			}
		}
	}
	return strings.TrimRight(strings.TrimSpace(cfg.PublicURL), "/")
}

// PublicBase is the Admin / .env public origin when set, otherwise the request host.
func PublicBase(r *http.Request, cfg config.Config, kv SettingsLookup) string {
	if u := ResolvePublicURL(r.Context(), cfg, kv); u != "" {
		return u
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "localhost:8080"
	}
	return scheme + "://" + host
}

func proxyHeaders(cfg config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), requestIDKey, r.Header.Get("X-Request-Id"))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func redactQuery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/playback") || strings.HasPrefix(r.URL.Path, "/hls") {
			// Do not log query; handlers must not log stoken.
		}
		next.ServeHTTP(w, r)
	})
}

func noStoreAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
