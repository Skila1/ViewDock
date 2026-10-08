package httpapi

import (
	"context"
	"net/http"
	"os/exec"
	"time"

	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/version"
)

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if s.Draining {
		WriteErr(w, http.StatusServiceUnavailable, "draining", "shutting down")
		return
	}
	ok := true
	database := true
	if s.DB != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		err := s.DB.PingContext(ctx)
		cancel()
		if err != nil {
			database = false
			ok = false
		}
	}
	_, err := exec.LookPath("ffmpeg")
	ffmpeg := err == nil
	status := http.StatusOK
	if !ok {
		status = http.StatusServiceUnavailable
	}
	WriteJSON(w, status, map[string]any{
		"ok":      ok,
		"version": version.Version,
		"ffmpeg":  ffmpeg,
		// sqlite is retained for existing probes; it reports the configured database.
		"sqlite":          database,
		"database":        database,
		"database_driver": s.Cfg.DatabaseDriver,
		"database_health": db.HealthOf(s.DB),
	})
}

func (s *Server) setting(ctx context.Context, key string) string {
	if s.Settings == nil {
		return ""
	}
	v, _ := s.Settings.Get(ctx, key)
	return v
}

func (s *Server) systemInfo(w http.ResponseWriter, r *http.Request) {
	setupNeeded := true
	if s.Settings != nil {
		v, err := s.Settings.Get(r.Context(), "setup.complete")
		if err != nil {
			w.Header().Set("Retry-After", "5")
			WriteErr(w, http.StatusServiceUnavailable, "database_unavailable", "the database is temporarily unavailable")
			return
		}
		setupNeeded = v != "1"
	}
	tmdb := s.Cfg.TMDBAPIKey != "" || s.setting(r.Context(), "tmdb.api_key") != ""
	features := map[string]bool{}
	if s.Features != nil {
		features = s.Features()
	}
	discordLogin := s.setting(r.Context(), "discord.login") == "1"
	discordConfigured := false
	if s.DB != nil {
		var login int
		var cid, sec string
		_ = s.DB.QueryRowContext(r.Context(), `SELECT login_enabled, client_id, client_secret FROM discord_settings WHERE id = 1`).Scan(&login, &cid, &sec)
		discordLogin = login == 1
		discordConfigured = login == 1 && cid != "" && sec != ""
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"name":                 "ViewDock",
		"version":              version.Version,
		"api_version":          "v1",
		"tmdb_configured":      tmdb,
		"setup_needed":         setupNeeded,
		"media_dir":            s.Cfg.MediaDir,
		"discord_login":        discordLogin,
		"discord_configured":   discordConfigured,
		"local_login_disabled": discordConfigured,
		"public_url":           ResolvePublicURL(r.Context(), s.Cfg, s.Settings),
		"features":             features,
	})
}
