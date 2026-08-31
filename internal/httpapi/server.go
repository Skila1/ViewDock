package httpapi

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/viewdock/viewdock/internal/config"
)

// RouteMount is a domain package hook. Only cmd/viewdock appends these.
type RouteMount func(r chi.Router)

type SettingsLookup interface {
	Get(ctx context.Context, key string) (string, error)
}

type Server struct {
	Cfg       config.Config
	DB        *sql.DB
	Log       *slog.Logger
	Web       fs.FS
	Draining  bool
	Settings  SettingsLookup
	APIMounts []RouteMount
}

func New(cfg config.Config, sqlDB *sql.DB, logger *slog.Logger, web fs.FS) *Server {
	return &Server{Cfg: cfg, DB: sqlDB, Log: logger, Web: web}
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(s.accessLog)
	r.Use(middleware.Recoverer)
	r.Use(secureHeaders)
	r.Use(noStoreAPI)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "X-Request-Id"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/healthz", s.healthz)
	r.Get("/readyz", s.readyz)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/system", s.systemInfo)
		for _, mount := range s.APIMounts {
			if mount != nil {
				mount(r)
			}
		}
	})

	r.NotFound(s.spa().ServeHTTP)
	return r
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		if s.Log == nil {
			return
		}
		path := r.URL.Path
		if path == "/api/v1/client-logs" {
			return
		}
		query := r.URL.RawQuery
		if strings.HasPrefix(path, "/api/v1/playback") || strings.HasPrefix(path, "/hls") || strings.HasPrefix(path, "/api/v1/watch-together") {
			query = ""
		}
		s.Log.Info("http",
			"method", r.Method,
			"path", path,
			"query", query,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"ms", time.Since(start).Milliseconds(),
			"ip", ClientIPString(r, s.Cfg),
		)
	})
}

func CookieSecure(r *http.Request, cfg config.Config) bool {
	if cfg.CookieSecure {
		return true
	}
	if r.TLS != nil {
		return true
	}
	if !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && cfg.TrustedContains(ip)
}
