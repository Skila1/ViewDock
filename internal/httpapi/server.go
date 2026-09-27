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
	// Features reports administrator feature flags to clients.
	Features func() map[string]bool
}

func New(cfg config.Config, sqlDB *sql.DB, logger *slog.Logger, web fs.FS) *Server {
	return &Server{Cfg: cfg, DB: sqlDB, Log: logger, Web: web}
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(s.realIP)
	r.Use(s.accessLog)
	r.Use(middleware.Recoverer)
	r.Use(secureHeaders)
	r.Use(noStoreAPI)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   s.allowedOrigins(),
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "X-Request-Id"},
		AllowCredentials: true,
		MaxAge:           300,
	}))
	r.Use(s.idempotency)

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

// Edge applies request IDs, access logging, panic recovery and security
// headers to a handler mounted outside Handler.
func (s *Server) Edge(h http.Handler) http.Handler {
	return middleware.RequestID(s.accessLog(middleware.Recoverer(secureHeaders(h))))
}

// WorkerHandler serves a media worker: health endpoints, the routes added by
// mount under /api/v1, and HLS. There is no SPA and no general API; guard
// runs before everything except /healthz.
func (s *Server) WorkerHandler(guard func(http.Handler) http.Handler, mount RouteMount, hls http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(s.accessLog)
	r.Use(middleware.Recoverer)
	r.Use(secureHeaders)
	r.Use(noStoreAPI)
	r.Get("/healthz", s.healthz)
	r.Group(func(r chi.Router) {
		r.Use(guard)
		r.Get("/readyz", s.readyz)
		r.Route("/api/v1", mount)
		r.Mount("/hls", hls)
	})
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		WriteErr(w, http.StatusNotFound, "not_found", "not found")
	})
	return r
}

// CoordinatorHandler serves a watch party coordinator: health endpoints, the
// signed internal API for control planes under /api/v1/coordinator, and the
// public watch party routes (reached through a control plane relay) behind
// authed. Everything else is 404.
func (s *Server) CoordinatorHandler(guard func(http.Handler) http.Handler, internal RouteMount, authed func(http.Handler) http.Handler, public RouteMount) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(s.realIP)
	r.Use(s.accessLog)
	r.Use(middleware.Recoverer)
	r.Use(secureHeaders)
	r.Use(noStoreAPI)
	r.Get("/healthz", s.healthz)
	r.Get("/readyz", s.readyz)
	r.Route("/api/v1", func(r chi.Router) {
		r.With(guard).Route("/coordinator", internal)
		r.Group(func(r chi.Router) {
			r.Use(authed)
			public(r)
		})
	})
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		WriteErr(w, http.StatusNotFound, "not_found", "not found")
	})
	return r
}

func (s *Server) allowedOrigins() []string {
	seen := map[string]struct{}{}
	var origins []string
	add := func(origin string) {
		origin = strings.TrimRight(strings.TrimSpace(origin), "/")
		if origin == "" {
			return
		}
		key := strings.ToLower(origin)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		origins = append(origins, origin)
	}
	add(s.Cfg.PublicURL)
	add(ResolvePublicURL(context.Background(), s.Cfg, s.Settings))
	for _, origin := range s.Cfg.AllowedOrigins {
		add(origin)
	}
	return origins
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
		if strings.HasPrefix(path, "/api/v1/playback") || strings.HasPrefix(path, "/hls") || strings.HasPrefix(path, "/mesh/") || strings.HasPrefix(path, "/api/v1/watch-together") {
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
