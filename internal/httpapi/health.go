package httpapi

import (
	"context"
	"net/http"
	"os/exec"

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
	sqlite := true
	if s.DB != nil {
		if err := s.DB.PingContext(r.Context()); err != nil {
			sqlite = false
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
		"sqlite":  sqlite,
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
	tmdb := s.Cfg.TMDBAPIKey != "" || s.setting(r.Context(), "tmdb.api_key") != ""
	setupNeeded := s.setting(r.Context(), "setup.complete") != "1"
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
		"name":               "ViewDock",
		"version":            version.Version,
		"api_version":        "v1",
		"tmdb_configured":    tmdb,
		"setup_needed":       setupNeeded,
		"media_dir":          s.Cfg.MediaDir,
		"discord_login":      discordLogin,
		"discord_configured": discordConfigured,
		"public_url":         s.Cfg.PublicURL,
	})
}
