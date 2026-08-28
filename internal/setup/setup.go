package setup

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/settings"
)

type API struct {
	Auth     *auth.Service
	Settings *settings.Store
	Libs     library.LibrarySetup
	Scan     library.ScanStart
	FF       ffmpeg.Detector
}

func New(a *auth.Service, kv *settings.Store, libs library.LibrarySetup, scan library.ScanStart, ff ffmpeg.Detector) *API {
	return &API{Auth: a, Settings: kv, Libs: libs, Scan: scan, FF: ff}
}

func (a *API) Routes(r chi.Router) {
	r.Route("/setup", func(r chi.Router) {
		r.Get("/status", a.status)
		r.Post("/admin", a.admin)
		r.Post("/library", a.library)
		r.Get("/ffmpeg", a.ffmpeg)
		r.Post("/tmdb", a.tmdb)
		r.Post("/scan", a.scan)
		r.Post("/complete", a.complete)
	})
}

func (a *API) status(w http.ResponseWriter, r *http.Request) {
	needed := !a.Auth.SetupComplete(r.Context())
	step := "done"
	if needed {
		n, _ := a.Auth.UserCount(r.Context())
		switch {
		case n == 0:
			step = "admin"
		case a.Settings != nil && !a.Settings.Bool(r.Context(), "setup.library"):
			step = "library"
		case a.Settings != nil && !a.Settings.Bool(r.Context(), "setup.ffmpeg"):
			step = "ffmpeg"
		default:
			step = "tmdb"
		}
	}
	media := ""
	if a.Auth != nil {
		media = a.Auth.Cfg.MediaDir
	}
	discord := false
	configured := false
	if a.Auth != nil {
		cfg := a.Auth.LoadDiscord(r.Context())
		discord = cfg.LoginEnabled
		configured = cfg.Ready()
	}
	httpapi.WriteJSON(w, 200, map[string]any{
		"needed":             needed,
		"step":               step,
		"media_dir":          media,
		"discord_enabled":    discord,
		"discord_configured": configured,
	})
}

func (a *API) admin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	u, err := a.Auth.CreateAdmin(r.Context(), body.Username, body.Password, body.DisplayName)
	if err != nil {
		httpapi.WriteErr(w, 400, "setup", err.Error())
		return
	}
	raw, exp, err := a.Auth.Sessions.Create(r.Context(), u.ID, httpapi.ClientIPString(r, a.Auth.Cfg), r.UserAgent())
	if err != nil {
		httpapi.WriteErr(w, 500, "setup", err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: auth.SessionCookie, Value: raw, Path: "/", HttpOnly: true,
		Secure: httpapi.CookieSecure(r, a.Auth.Cfg), SameSite: http.SameSiteLaxMode, Expires: exp,
	})
	_, _ = auth.IssueCSRF(w, r, a.Auth.Cfg)
	httpapi.WriteJSON(w, 200, map[string]any{"id": u.ID, "username": u.Username})
}

func (a *API) library(w http.ResponseWriter, r *http.Request) {
	if auth.FromRequest(r) == nil || !auth.FromRequest(r).IsAdmin {
		n, _ := a.Auth.UserCount(r.Context())
		if n > 0 {
			httpapi.WriteErr(w, 401, "unauthorized", "login required")
			return
		}
	}
	var body struct {
		Name        string `json:"name"`
		Path        string `json:"path"`
		ContentType string `json:"content_type"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if a.Libs == nil {
		httpapi.WriteErr(w, 503, "setup", "library not wired")
		return
	}
	if strings.TrimSpace(body.Path) == "" && a.Auth != nil {
		body.Path = a.Auth.Cfg.MediaDir
	}
	if body.Name == "" {
		body.Name = "Library"
	}
	lib, err := a.Libs.Create(r.Context(), body.Name, body.Path, body.ContentType)
	if err != nil {
		httpapi.WriteErr(w, 400, "setup", err.Error())
		return
	}
	_ = a.Settings.Set(r.Context(), "setup.library", "1")
	httpapi.WriteJSON(w, 200, lib)
}

func (a *API) ffmpeg(w http.ResponseWriter, r *http.Request) {
	if a.FF == nil {
		httpapi.WriteJSON(w, 200, ffmpeg.DetectResult{})
		return
	}
	d := a.FF.Detect()
	_ = a.Settings.Set(r.Context(), "setup.ffmpeg", "1")
	httpapi.WriteJSON(w, 200, d)
}

func (a *API) tmdb(w http.ResponseWriter, r *http.Request) {
	var body struct {
		APIKey string `json:"api_key"`
		Skip   bool   `json:"skip"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if !body.Skip && body.APIKey != "" {
		_ = a.Settings.Set(r.Context(), "tmdb.api_key", body.APIKey)
	}
	_ = a.Settings.Set(r.Context(), "setup.tmdb", "1")
	httpapi.WriteOK(w)
}

func (a *API) scan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LibraryID string `json:"library_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if a.Scan == nil {
		httpapi.WriteErr(w, 503, "setup", "scan not wired")
		return
	}
	id, err := a.Scan.StartScan(r.Context(), body.LibraryID)
	if err != nil {
		httpapi.WriteErr(w, 400, "setup", err.Error())
		return
	}
	httpapi.WriteJSON(w, 202, map[string]string{"scan_run_id": id})
}

func (a *API) complete(w http.ResponseWriter, r *http.Request) {
	_ = a.Settings.Set(r.Context(), "setup.complete", "1")
	httpapi.WriteOK(w)
}
