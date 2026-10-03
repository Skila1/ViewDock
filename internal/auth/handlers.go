package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/httpapi"
)

func (s *Service) Routes(r chi.Router) {
	r.Get("/auth/csrf", s.handleCSRF)
	r.With(RateLimit(s.Cfg, 10, time.Minute)).Post("/auth/login", s.handleLogin)
	r.Post("/auth/logout", s.handleLogout)
	r.With(RequireUser).Post("/auth/logout-all", s.handleLogoutAll)
	r.Get("/auth/discord", s.handleDiscordStart)
	r.Get("/auth/discord/callback", s.handleDiscordCallback)
	r.Get("/auth/discord/activity", s.handleActivityConfig)
	r.With(RateLimit(s.Cfg, 10, time.Minute)).Post("/auth/discord/activity", s.handleActivitySignIn)
	r.With(RequireUser).Get("/me", s.handleMe)
	r.With(RequireUser).Patch("/me", s.handlePatchMe)
	r.With(RequireUser).Get("/me/preferences", s.handleGetPrefs)
	r.With(RequireUser).Put("/me/preferences", s.handlePutPrefs)
	r.With(RequireUser).Post("/me/password", s.handlePassword)
	r.With(RequireUser).Post("/me/pin", s.handleSetPIN)
	r.With(RequireUser).Delete("/me/pin", s.handleClearPIN)
	r.With(RequireUser).Post("/me/pin/unlock", s.handleUnlockPIN)
	r.With(RequireUser).Get("/me/sessions", s.handleSessions)
	r.With(RequireUser).Delete("/me/sessions/{id}", s.handleDeleteSession)
	r.With(RequireUser).Get("/me/identities", s.handleIdentities)
	r.With(RequireUser).Delete("/me/identities/discord", s.handleUnlinkDiscord)
	mountAdminRBAC(s, r)
	s.mountAPIKeys(r)
}

func (s *Service) handleCSRF(w http.ResponseWriter, r *http.Request) {
	tok, err := IssueCSRF(w, r, s.Cfg)
	if err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "csrf", "failed")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]string{"token": tok})
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	raw, exp, u, err := s.Login(r.Context(), body.Username, body.Password, httpapi.ClientIPString(r, s.Cfg), r.UserAgent())
	if err != nil {
		if errors.Is(err, ErrLocalLoginDisabled) {
			httpapi.WriteErr(w, http.StatusForbidden, "local_login_disabled", err.Error())
			return
		}
		if errors.Is(err, ErrSessionLimit) {
			httpapi.WriteErr(w, http.StatusTooManyRequests, "session_limit", "this account is signed in on the maximum number of devices")
			return
		}
		if !errors.Is(err, ErrInvalidCredentials) && !errors.Is(err, ErrDisabled) {
			httpapi.WriteErr(w, http.StatusServiceUnavailable, "database_unavailable", "sign-in is temporarily unavailable")
			return
		}
		httpapi.WriteErr(w, http.StatusUnauthorized, "unauthorized", "invalid credentials")
		return
	}
	s.setSessionCookie(w, r, raw, exp)
	if _, err := IssueCSRF(w, r, s.Cfg); err != nil {
		httpapi.WriteErr(w, 500, "csrf", "failed")
		return
	}
	s.Audit.Event(r.Context(), u.ID, "login", u.Username, httpapi.ClientIPString(r, s.Cfg), "")
	httpapi.WriteJSON(w, http.StatusOK, meJSON(u, false))
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil {
		s.Sessions.Delete(r.Context(), c.Value)
	}
	s.clearSessionCookie(w, r)
	httpapi.WriteOK(w)
}

func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	p := FromRequest(r)
	u, err := s.GetUser(r.Context(), p.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		httpapi.WriteErr(w, http.StatusUnauthorized, "unauthorized", "not found")
		return
	}
	if err != nil {
		w.Header().Set("Retry-After", "5")
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "database_unavailable", "the database is temporarily unavailable")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, meJSON(u, p.PINLocked))
}

func meJSON(u User, pin bool) map[string]any {
	perms := u.Permissions
	if perms == nil {
		perms = []string{}
	}
	roles := u.Roles
	if roles == nil {
		roles = []string{}
	}
	isSA := false
	for _, r := range roles {
		if r == "Superadmin" {
			isSA = true
			break
		}
	}
	if !isSA {
		for _, p := range perms {
			if p == PermSuperadmin {
				isSA = true
				break
			}
		}
	}
	return map[string]any{
		"id": u.ID, "username": u.Username, "display_name": u.DisplayName,
		"is_admin": u.IsAdmin, "is_superadmin": isSA, "kind": KindUser, "pin_locked": pin,
		"has_password": u.HasPassword, "has_pin": u.PINHash != "",
		"permissions": perms, "roles": roles,
	}
}

func (s *Service) handlePatchMe(w http.ResponseWriter, r *http.Request) {
	p := FromRequest(r)
	var body struct {
		DisplayName *string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteErr(w, 400, "bad_request", "invalid json")
		return
	}
	if body.DisplayName != nil {
		if err := s.UpdateDisplayName(r.Context(), p.UserID, *body.DisplayName); err != nil {
			httpapi.WriteErr(w, 400, "me", err.Error())
			return
		}
	}
	u, err := s.GetUser(r.Context(), p.UserID)
	if err != nil {
		httpapi.WriteErr(w, 401, "unauthorized", "not found")
		return
	}
	httpapi.WriteJSON(w, 200, meJSON(u, p.PINLocked))
}

func (s *Service) handleSessions(w http.ResponseWriter, r *http.Request) {
	p := FromRequest(r)
	list, err := s.Sessions.ListForUser(r.Context(), p.UserID, p.SessionID)
	if err != nil {
		httpapi.WriteErr(w, 500, "sessions", err.Error())
		return
	}
	httpapi.WriteJSON(w, 200, list)
}

func (s *Service) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	p := FromRequest(r)
	id := chi.URLParam(r, "id")
	if id == p.SessionID {
		httpapi.WriteErr(w, 400, "sessions", "cannot revoke the current session")
		return
	}
	_ = s.Sessions.DeleteID(r.Context(), p.UserID, id)
	httpapi.WriteOK(w)
}

func (s *Service) handleLogoutAll(w http.ResponseWriter, r *http.Request) {
	p := FromRequest(r)
	s.Sessions.DeleteAllForUser(r.Context(), p.UserID)
	s.clearSessionCookie(w, r)
	httpapi.WriteOK(w)
}

type prefJSON struct {
	AudioLang    string `json:"audio_lang"`
	SubtitleLang string `json:"subtitle_lang"`
	SubtitleMode string `json:"subtitle_mode"`
	Autoplay     bool   `json:"autoplay"`
	// PlaybackRate, Quality and UpNextSeconds are the profile's player
	// defaults; SourcePref is "", "local" or "remote"; HomeRows orders the
	// home page rows (empty means the default set).
	PlaybackRate  float64  `json:"playback_rate"`
	Quality       string   `json:"quality"`
	UpNextSeconds int      `json:"upnext_seconds"`
	SourcePref    string   `json:"source_pref"`
	HomeRows      []string `json:"home_rows"`
}

var homeRowID = regexp.MustCompile(`^[a-z0-9_:-]{1,64}$`)

func defaultPrefs() prefJSON {
	return prefJSON{SubtitleMode: "auto", Autoplay: true, PlaybackRate: 1, UpNextSeconds: 10, HomeRows: []string{}}
}

func (s *Service) loadPrefs(ctx context.Context, userID string) prefJSON {
	pref := defaultPrefs()
	var auto int
	var rows string
	err := s.DB.QueryRowContext(ctx, `
		SELECT audio_lang, subtitle_lang, subtitle_mode, autoplay, playback_rate, quality, upnext_seconds, source_pref, home_rows
		FROM user_preferences WHERE user_id = ?
	`, userID).Scan(&pref.AudioLang, &pref.SubtitleLang, &pref.SubtitleMode, &auto, &pref.PlaybackRate, &pref.Quality, &pref.UpNextSeconds, &pref.SourcePref, &rows)
	if err != nil {
		return defaultPrefs()
	}
	pref.Autoplay = auto == 1
	if rows != "" {
		pref.HomeRows = strings.Split(rows, ",")
	}
	return pref
}

func (s *Service) handleGetPrefs(w http.ResponseWriter, r *http.Request) {
	httpapi.WriteJSON(w, http.StatusOK, s.loadPrefs(r.Context(), FromRequest(r).UserID))
}

// handlePutPrefs merges the fields sent over the stored preferences, so a
// client that knows only some of them does not reset the rest.
func (s *Service) handlePutPrefs(w http.ResponseWriter, r *http.Request) {
	p := FromRequest(r)
	pref := s.loadPrefs(r.Context(), p.UserID)
	if err := json.NewDecoder(r.Body).Decode(&pref); err != nil {
		httpapi.WriteErr(w, 400, "bad_request", "invalid json")
		return
	}
	if pref.SubtitleMode == "" {
		pref.SubtitleMode = "auto"
	}
	if pref.PlaybackRate < 0.25 || pref.PlaybackRate > 3 {
		pref.PlaybackRate = 1
	}
	switch pref.Quality {
	case "", "auto", "1080", "720", "480":
	default:
		httpapi.WriteErr(w, 400, "bad_request", "quality must be auto, 1080, 720 or 480")
		return
	}
	if pref.UpNextSeconds < 0 || pref.UpNextSeconds > 60 {
		httpapi.WriteErr(w, 400, "bad_request", "upnext_seconds must be between 0 and 60")
		return
	}
	switch pref.SourcePref {
	case "", "local", "remote":
	default:
		httpapi.WriteErr(w, 400, "bad_request", "source_pref must be local or remote")
		return
	}
	if len(pref.HomeRows) > 30 {
		httpapi.WriteErr(w, 400, "bad_request", "at most 30 home rows")
		return
	}
	for _, id := range pref.HomeRows {
		if !homeRowID.MatchString(id) {
			httpapi.WriteErr(w, 400, "bad_request", "invalid home row id")
			return
		}
	}
	if pref.HomeRows == nil {
		pref.HomeRows = []string{}
	}
	a := 0
	if pref.Autoplay {
		a = 1
	}
	_, err := s.DB.ExecContext(r.Context(), `
		INSERT INTO user_preferences(user_id, audio_lang, subtitle_lang, subtitle_mode, autoplay, playback_rate, quality, upnext_seconds, source_pref, home_rows)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			audio_lang=excluded.audio_lang,
			subtitle_lang=excluded.subtitle_lang,
			subtitle_mode=excluded.subtitle_mode,
			autoplay=excluded.autoplay,
			playback_rate=excluded.playback_rate,
			quality=excluded.quality,
			upnext_seconds=excluded.upnext_seconds,
			source_pref=excluded.source_pref,
			home_rows=excluded.home_rows
	`, p.UserID, pref.AudioLang, pref.SubtitleLang, pref.SubtitleMode, a, pref.PlaybackRate, pref.Quality, pref.UpNextSeconds, pref.SourcePref, strings.Join(pref.HomeRows, ","))
	if err != nil {
		httpapi.WriteErr(w, 500, "prefs", err.Error())
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, pref)
}

func (s *Service) handlePassword(w http.ResponseWriter, r *http.Request) {
	p := FromRequest(r)
	var body struct {
		Current string `json:"current"`
		Next    string `json:"next"`
		New     string `json:"new"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Next == "" {
		body.Next = body.New
	}
	if err := s.ChangePassword(r.Context(), p.UserID, body.Current, body.Next); err != nil {
		httpapi.WriteErr(w, 400, "password", err.Error())
		return
	}
	httpapi.WriteOK(w)
}

func (s *Service) handleSetPIN(w http.ResponseWriter, r *http.Request) {
	p := FromRequest(r)
	var body struct {
		PIN string `json:"pin"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := s.SetPIN(r.Context(), p.UserID, body.PIN); err != nil {
		httpapi.WriteErr(w, 400, "pin", err.Error())
		return
	}
	httpapi.WriteOK(w)
}

func (s *Service) handleClearPIN(w http.ResponseWriter, r *http.Request) {
	_ = s.ClearPIN(r.Context(), FromRequest(r).UserID)
	httpapi.WriteOK(w)
}

func (s *Service) handleUnlockPIN(w http.ResponseWriter, r *http.Request) {
	p := FromRequest(r)
	var body struct {
		PIN string `json:"pin"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if !s.VerifyPIN(r.Context(), p.UserID, body.PIN) {
		httpapi.WriteErr(w, 401, "pin", "invalid pin")
		return
	}
	if tok := cookieOrBearer(r, SessionCookie); tok != "" {
		if row, err := s.Sessions.Lookup(r.Context(), tok); err == nil {
			s.Sessions.Touch(r.Context(), row.ID)
		}
	}
	httpapi.WriteOK(w)
}

func CookieSecureCfg(r *http.Request, cfg config.Config) bool {
	return httpapi.CookieSecure(r, cfg)
}
