package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/backup"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/discordbot/interactions"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/log"
	"github.com/viewdock/viewdock/internal/nodeauth"
	"github.com/viewdock/viewdock/internal/secrets"
	"github.com/viewdock/viewdock/internal/settings"
	"github.com/viewdock/viewdock/internal/setup"
	"github.com/viewdock/viewdock/internal/update"
	"github.com/viewdock/viewdock/internal/version"
	"github.com/viewdock/viewdock/internal/watchtogether"
	"github.com/viewdock/viewdock/web"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "update-swap" {
		if err := update.RunSwap(); err != nil {
			_, _ = os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "backup" {
		os.Exit(backup.RunCLI(context.Background(), os.Args[2:], config.Load(), os.Stdout, os.Stderr))
	}

	cfg := config.Load()
	logger := log.New(cfg.LogLevel)
	slog.SetDefault(logger)
	if !config.ValidRole(cfg.Role) {
		logger.Error("invalid VD_ROLE", "role", cfg.Role, "hint", "use all, control, worker, frontend or coordinator")
		os.Exit(1)
	}
	if cfg.Role == config.RoleFrontend {
		os.Exit(runFrontend(cfg, logger))
	}
	if cfg.Role == config.RoleWorker && len(cfg.NodeSecret) < 32 {
		logger.Error("VD_NODE_SECRET is required for VD_ROLE=worker", "hint", "issue one under Admin, Nodes, Credential")
		os.Exit(1)
	}
	worker := cfg.Role == config.RoleWorker
	coordinator := cfg.Role == config.RoleCoordinator
	controlPlane := !worker && !coordinator
	if coordinator || (controlPlane && cfg.CoordinatorURL != "") {
		if len(cfg.CoordinatorSecret) < 32 {
			logger.Error("VD_COORDINATOR_SECRET of at least 32 characters is required", "role", cfg.Role,
				"hint", "use the same value on the coordinator and every control plane, for example from openssl rand -base64 32")
			os.Exit(1)
		}
	}
	if controlPlane && cfg.CoordinatorURL != "" {
		if _, err := httpapi.ParseUpstreamURL("VD_COORDINATOR_URL", cfg.CoordinatorURL); err != nil {
			logger.Error("invalid VD_COORDINATOR_URL", "err", err, "hint", "for example http://viewdock-coordinator:8080")
			os.Exit(1)
		}
	}

	for _, dir := range []string{cfg.ConfigDir, cfg.CacheDir, cfg.TranscodeDir, cfg.MediaDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			logger.Error("mkdir", "dir", dir, "err", err)
			os.Exit(1)
		}
	}

	provider, err := db.OpenProvider(context.Background(), db.ProviderConfig{
		Dialect:       db.Dialect(cfg.DatabaseDriver),
		SQLitePath:    cfg.DatabasePath,
		PostgresURL:   cfg.DatabaseURL,
		BusyTimeoutMS: cfg.BusyTimeoutMS,
	})
	if err != nil {
		logger.Error("db open", "err", err)
		os.Exit(1)
	}
	defer provider.SQL.Close()
	sqlDB := provider.SQL

	webFS, err := web.FS()
	if err != nil {
		logger.Error("web embed", "err", err)
		os.Exit(1)
	}

	kv := settings.New(provider)
	cipher, err := secrets.Load(cfg.ConfigDir, os.Getenv("VD_MASTER_KEY"))
	if err != nil {
		logger.Error("master key", "err", err, "hint", "set VD_MASTER_KEY or make VD_CONFIG_DIR writable")
		os.Exit(1)
	}
	kv.UseCipher(cipher)
	srv := httpapi.New(cfg, sqlDB, logger, webFS)
	srv.Settings = kv
	app := wire(srv, sqlDB, cfg, logger, kv)
	logger = log.Tee(logger, app.Logs, cfg.LogLevel)
	slog.SetDefault(logger)
	app.Playback.Log = logger
	srv.Log = logger
	if controlPlane {
		app.Auth.SyncDiscordEnv()
		n, _ := app.Auth.UserCount(context.Background())
		if err := setup.EnsureBootstrap(context.Background(), kv, cfg, logger, n); err != nil {
			logger.Error("setup bootstrap", "err", err)
			os.Exit(1)
		}
		if app.Uploads != nil {
			app.Uploads.Sweep(context.Background())
		}
	}
	defer app.Playback.Close()
	if app.Labs != nil {
		defer app.Labs.Close()
	}
	if app.Backend != nil {
		defer app.Backend.Close()
	}

	root := chi.NewRouter()
	root.Use(httpapi.OutageAware(sqlDB))
	if worker {
		root.Mount("/", srv.WorkerHandler(app.Worker.Guard, app.Worker.Routes, app.Playback.HLSHandler()))
	} else if coordinator {
		hub := app.Playback.WT
		guard := watchtogether.CoordinatorGuard(nodeauth.NewVerifier(cfg.CoordinatorSecret), logger)
		authed := func(next http.Handler) http.Handler { return app.Auth.Middleware(app.Auth.CSRF(next)) }
		root.Mount("/", srv.CoordinatorHandler(guard, hub.InternalRoutes, authed, hub.Routes))
	} else {
		// Relayed session traffic is authenticated by stream tokens, not cookies.
		root.Mount("/mesh", srv.Edge(app.Mesh.Relay()))
		// Discord interactions are authenticated by Ed25519 signatures, not cookies.
		root.Method(http.MethodPost, interactions.Path, srv.Edge(app.Interactions.Handler()))
		root.Group(func(r chi.Router) {
			r.Use(app.Auth.Middleware)
			r.Use(app.Auth.SetupGate)
			r.Use(app.Auth.CSRF)
			r.Mount("/hls", app.Playback.HLSHandler())
			r.Mount("/", srv.Handler())
		})
	}

	hs := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       0,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("listen", "addr", cfg.HTTPAddr, "version", version.Version, "role", cfg.Role)
		if err := hs.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http", "err", err)
			os.Exit(1)
		}
	}()
	waitListen(cfg.HTTPAddr)
	if controlPlane && setup.BootstrapPending(context.Background(), kv) {
		setup.AnnounceToken(cfg, logger)
	}

	go func() {
		if !controlPlane {
			return
		}
		t := time.NewTicker(15 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				update.Tick(context.Background(), kv)
				if app != nil && app.Uploads != nil {
					app.Uploads.Sweep(context.Background())
				}
				if app != nil && app.Logs != nil {
					app.Logs.Sweep(context.Background())
				}
				if app != nil && app.Users != nil {
					if n := app.Users.SweepExpiredGuests(context.Background()); n > 0 {
						logger.Info("guest accounts expired", "count", n)
					}
				}
			}
		}
	}()

	<-ctx.Done()
	srv.Draining = true
	shCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownWait)
	defer cancel()
	_ = hs.Shutdown(shCtx)
}

// runFrontend serves the web app and relays API traffic to the control
// plane. It opens no database and holds no secrets.
func runFrontend(cfg config.Config, logger *slog.Logger) int {
	control, err := httpapi.ParseControlURL(cfg.ControlURL)
	if err != nil {
		logger.Error("VD_CONTROL_URL is required for VD_ROLE=frontend", "err", err, "hint", "for example http://viewdock-control:8080")
		return 1
	}
	webFS, err := web.FS()
	if err != nil {
		logger.Error("web embed", "err", err)
		return 1
	}
	srv := httpapi.New(cfg, nil, logger, webFS)
	hs := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.FrontendHandler(control),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() {
		logger.Info("listen", "addr", cfg.HTTPAddr, "version", version.Version, "role", cfg.Role, "control", control.String())
		errc <- hs.ListenAndServe()
	}()
	select {
	case err := <-errc:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("http", "err", err)
			return 1
		}
		return 0
	case <-ctx.Done():
	}
	shCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownWait)
	defer cancel()
	_ = hs.Shutdown(shCtx)
	return 0
}

func waitListen(addr string) {
	host := addr
	switch {
	case strings.HasPrefix(addr, ":"):
		host = "127.0.0.1" + addr
	case strings.HasPrefix(addr, "0.0.0.0:"):
		host = "127.0.0.1" + strings.TrimPrefix(addr, "0.0.0.0")
	case strings.HasPrefix(addr, "[::]:"):
		host = "[::1]" + strings.TrimPrefix(addr, "[::]")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", host, 50*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
