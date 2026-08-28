package main

import (
	"database/sql"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/artwork"
	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/collections"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/metadata"
	"github.com/viewdock/viewdock/internal/playback"
	"github.com/viewdock/viewdock/internal/progress"
	"github.com/viewdock/viewdock/internal/scan"
	"github.com/viewdock/viewdock/internal/search"
	"github.com/viewdock/viewdock/internal/settings"
	"github.com/viewdock/viewdock/internal/setup"
	"github.com/viewdock/viewdock/internal/share"
	"github.com/viewdock/viewdock/internal/update"
	"github.com/viewdock/viewdock/internal/upload"
	"github.com/viewdock/viewdock/internal/users"
)

type app struct {
	Auth     *auth.Service
	Playback *playback.API
	Uploads  *upload.Service
}

func wire(srv *httpapi.Server, sqlDB *sql.DB, cfg config.Config, logger *slog.Logger, kv *settings.Store) *app {
	aud := audit.New(sqlDB)
	authSvc := auth.New(sqlDB, cfg, kv, aud)
	ff := ffmpeg.New()
	libs := library.NewService(sqlDB, authSvc.Grants, ff, ff, cfg.CacheDir)
	sc := scan.New(sqlDB, libs, ff)
	libs.SetScan(sc)
	art := artwork.New(sqlDB, cfg.CacheDir, ff, metadata.NewClient(kv))
	meta := metadata.New(sqlDB, kv, art)
	up := upload.New(sqlDB, libs, sc, ff, filepath.Join(cfg.ConfigDir, "uploads"))
	srch := search.New(sqlDB)
	cols := collections.New(sqlDB, libs)
	shareSvc := share.New(sqlDB, libs)
	shareAPI := share.NewAPI(shareSvc, authSvc)
	usersAPI := users.New(sqlDB, authSvc)
	setupAPI := setup.New(authSvc, kv, libs, sc, ff)
	prog := progress.New(sqlDB)
	play := playback.New(playback.Deps{
		Cfg: cfg, DB: sqlDB, Log: logger,
		Locator: libs, Grants: authSvc.Grants, Gate: shareSvc,
		Prober: ff, FF: ff, Progress: prog, Catalog: libs,
		CacheDir: cfg.CacheDir, Slots: 2,
	})

	catalogMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := auth.FromRequest(r)
			if p == nil || !p.IsUser() {
				httpapi.WriteErr(w, http.StatusUnauthorized, "unauthorized", "login required")
				return
			}
			path := r.URL.Path
			write := r.Method != http.MethodGet && r.Method != http.MethodHead
			if write && (strings.HasPrefix(path, "/api/v1/libraries") || strings.HasPrefix(path, "/api/v1/metadata") || strings.HasPrefix(path, "/api/v1/artwork")) && !p.HasPerm(auth.PermLibrariesManage) {
				httpapi.WriteErr(w, http.StatusForbidden, "forbidden", "permission required")
				return
			}
			if strings.HasPrefix(path, "/api/v1/uploads") && !p.IsAdmin {
				httpapi.WriteErr(w, http.StatusForbidden, "forbidden", "admin required")
				return
			}
			ctx := library.WithUserID(r.Context(), p.UserID)
			if !p.IsAdmin {
				ids, err := authSvc.Grants.GrantedLibraryIDs(r.Context(), p.UserID)
				if err != nil || ids == nil {
					ids = []string{}
				}
				ctx = library.WithGrantedIDs(ctx, ids)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	srv.APIMounts = append(srv.APIMounts,
		authSvc.Routes,
		setupAPI.Routes,
		usersAPI.Routes,
		shareAPI.Routes,
		func(r chi.Router) {
			r.Group(func(r chi.Router) {
				r.Use(catalogMW)
				libs.Routes(r)
				sc.Routes(r)
				meta.Routes(r)
				art.Routes(r)
				up.Routes(r)
				srch.Routes(r)
				cols.Routes(r)
			})
		},
		play.Routes,
		update.Routes(kv),
	)
	return &app{Auth: authSvc, Playback: play, Uploads: up}
}
