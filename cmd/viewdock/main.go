package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/log"
	"github.com/viewdock/viewdock/internal/settings"
	"github.com/viewdock/viewdock/internal/version"
	"github.com/viewdock/viewdock/web"
)

func main() {
	cfg := config.Load()
	logger := log.New(cfg.LogLevel)
	slog.SetDefault(logger)

	for _, dir := range []string{cfg.ConfigDir, cfg.CacheDir, cfg.TranscodeDir, cfg.MediaDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			logger.Error("mkdir", "dir", dir, "err", err)
			os.Exit(1)
		}
	}

	if err := db.Migrate(cfg.DatabasePath); err != nil {
		logger.Error("migrate", "err", err)
		os.Exit(1)
	}
	sqlDB, err := db.Open(cfg.DatabasePath, cfg.BusyTimeoutMS)
	if err != nil {
		logger.Error("db open", "err", err)
		os.Exit(1)
	}
	defer sqlDB.Close()

	webFS, err := web.FS()
	if err != nil {
		logger.Error("web embed", "err", err)
		os.Exit(1)
	}

	kv := settings.New(sqlDB)
	srv := httpapi.New(cfg, sqlDB, logger, webFS)
	srv.Settings = kv
	app := wire(srv, sqlDB, cfg, logger, kv)
	app.Auth.SyncDiscordEnv()
	defer app.Playback.Close()

	root := chi.NewRouter()
	root.Use(app.Auth.Middleware)
	root.Use(app.Auth.SetupGate)
	root.Use(app.Auth.CSRF)
	root.Mount("/hls", app.Playback.HLSHandler())
	root.Mount("/", srv.Handler())

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
		logger.Info("listen", "addr", cfg.HTTPAddr, "version", version.Version)
		if err := hs.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	srv.Draining = true
	shCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownWait)
	defer cancel()
	_ = hs.Shutdown(shCtx)
}
