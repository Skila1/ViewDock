package auth

import (
	"encoding/json"
	"errors"
	"net/http"
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
		httpapi.WriteErr(w, http.StatusUnauthorized, "unauthorized", "invalid credentials")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: raw, Path: "/", HttpOnly: true,
		Secure: httpapi.CookieSecure(r, s.Cfg), SameSite: http.SameSiteLaxMode, Expires: exp,
	})
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
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1})
	httpapi.WriteOK(w)
}

func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	p := FromRequest(r)
	u, err := s.GetUser(r.Context(), p.UserID)
	if err != nil {
		httpapi.WriteErr(w, http.StatusUnauthorized, "unauthorized", "not found")
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
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1})
	httpapi.WriteOK(w)
}

type prefJSON struct {
	AudioLang    string `json:"audio_lang"`
	SubtitleLang string `json:"subtitle_lang"`
	SubtitleMode string `json:"subtitle_mode"`
	Autoplay     bool   `json:"autoplay"`
}

func (s *Service) handleGetPrefs(w http.ResponseWriter, r *http.Request) {
	p := FromRequest(r)
	var pref prefJSON
	var auto int
	err := s.DB.QueryRowContext(r.Context(), `
		SELECT audio_lang, subtitle_lang, subtitle_mode, autoplay FROM user_preferences WHERE user_id = ?
	`, p.UserID).Scan(&pref.AudioLang, &pref.SubtitleLang, &pref.SubtitleMode, &auto)
	if err != nil {
		pref = prefJSON{SubtitleMode: "auto", Autoplay: true}
	} else {
		pref.Autoplay = auto == 1
	}
	httpapi.WriteJSON(w, http.StatusOK, pref)
}

func (s *Service) handlePutPrefs(w http.ResponseWriter, r *http.Request) {
	p := FromRequest(r)
	var pref prefJSON
	if err := json.NewDecoder(r.Body).Decode(&pref); err != nil {
		httpapi.WriteErr(w, 400, "bad_request", "invalid json")
		return
	}
	if pref.SubtitleMode == "" {
		pref.SubtitleMode = "auto"
	}
	a := 0
	if pref.Autoplay {
		a = 1
	}
	_, err := s.DB.ExecContext(r.Context(), `
		INSERT INTO user_preferences(user_id, audio_lang, subtitle_lang, subtitle_mode, autoplay)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			audio_lang=excluded.audio_lang,
			subtitle_lang=excluded.subtitle_lang,
			subtitle_mode=excluded.subtitle_mode,
			autoplay=excluded.autoplay
	`, p.UserID, pref.AudioLang, pref.SubtitleLang, pref.SubtitleMode, a)
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
