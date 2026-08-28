package auth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/httpapi"
)

func (s *Service) DiscordCallbackURL(r *http.Request) string {
	return strings.TrimRight(httpapi.PublicBase(r, s.Cfg, s.Settings), "/") + "/api/v1/auth/discord/callback"
}

func (s *Service) handleDiscordStart(w http.ResponseWriter, r *http.Request) {
	oauth := s.LoadDiscord(r.Context())
	if !oauth.Ready() {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "disabled", "Discord sign-in is off")
		return
	}
	state, err := RandomToken(24)
	if err != nil {
		httpapi.WriteErr(w, 500, "oauth", err.Error())
		return
	}
	ver, ch := pkce()
	linkUser := ""
	if p := FromRequest(r); p != nil && p.IsUser() && r.URL.Query().Get("link") == "1" {
		linkUser = p.UserID
	}
	if err := s.StoreLoginState(r.Context(), state, ver, linkUser); err != nil {
		httpapi.WriteErr(w, 500, "oauth", err.Error())
		return
	}
	http.Redirect(w, r, discordAuthURL(oauth.ClientID, s.DiscordCallbackURL(r), state, ch), http.StatusFound)
}

func (s *Service) handleDiscordCallback(w http.ResponseWriter, r *http.Request) {
	fail := func(msg string) {
		http.Redirect(w, r, "/login?error="+url.QueryEscape(msg), http.StatusFound)
	}
	oauth := s.LoadDiscord(r.Context())
	if !oauth.Ready() {
		fail("disabled")
		return
	}
	if r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
		fail("oauth_denied")
		return
	}
	ver, linkUser, err := s.TakeLoginState(r.Context(), r.URL.Query().Get("state"))
	if err != nil || ver == "" {
		fail("invalid_state")
		return
	}
	prof, err := exchangeDiscordCode(r.Context(), oauth.ClientID, oauth.Secret, s.DiscordCallbackURL(r), r.URL.Query().Get("code"), ver)
	if err != nil {
		fail("token_exchange")
		return
	}
	if linkUser != "" {
		if existing, e := s.userByDiscord(r.Context(), prof.ID); e == nil && existing.ID != linkUser {
			http.Redirect(w, r, "/settings/connected?error="+url.QueryEscape("already_linked"), http.StatusFound)
			return
		}
		if err := s.linkDiscord(r.Context(), linkUser, prof); err != nil {
			http.Redirect(w, r, "/settings/connected?error="+url.QueryEscape(err.Error()), http.StatusFound)
			return
		}
		http.Redirect(w, r, "/settings/connected?linked=1", http.StatusFound)
		return
	}
	u, err := s.UpsertDiscordUser(r.Context(), prof)
	if err != nil {
		fail(err.Error())
		return
	}
	if u.Disabled {
		fail("disabled")
		return
	}
	raw, exp, err := s.Sessions.Create(r.Context(), u.ID, httpapi.ClientIPString(r, s.Cfg), r.UserAgent())
	if err != nil {
		fail("session")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: raw, Path: "/", HttpOnly: true,
		Secure: httpapi.CookieSecure(r, s.Cfg), SameSite: http.SameSiteLaxMode, Expires: exp,
	})
	_, _ = IssueCSRF(w, r, s.Cfg)
	s.Audit.Event(r.Context(), u.ID, "login.discord", prof.ID, httpapi.ClientIPString(r, s.Cfg), "")
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Service) handleIdentities(w http.ResponseWriter, r *http.Request) {
	p := FromRequest(r)
	list, err := s.ListIdentities(r.Context(), p.UserID)
	if err != nil {
		httpapi.WriteErr(w, 500, "identities", err.Error())
		return
	}
	httpapi.WriteJSON(w, 200, list)
}

func (s *Service) handleUnlinkDiscord(w http.ResponseWriter, r *http.Request) {
	p := FromRequest(r)
	if err := s.UnlinkDiscord(r.Context(), p.UserID); err != nil {
		httpapi.WriteErr(w, 400, "identities", err.Error())
		return
	}
	httpapi.WriteOK(w)
}

func (s *Service) handleAdminDiscordGet(w http.ResponseWriter, r *http.Request) {
	cfg := s.LoadDiscord(r.Context())
	cfg.RedirectURI = s.DiscordCallbackURL(r)
	cfg.Secret = ""
	httpapi.WriteJSON(w, 200, cfg)
}

func (s *Service) handleAdminDiscordPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LoginEnabled        *bool   `json:"login_enabled"`
		ClientID            *string `json:"client_id"`
		ClientSecret        *string `json:"client_secret"`
		RegistrationEnabled *bool   `json:"registration_enabled"`
		AdminDiscordIDs     *string `json:"admin_discord_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteErr(w, 400, "bad_request", "invalid json")
		return
	}
	cur := s.LoadDiscord(r.Context())
	if body.LoginEnabled != nil {
		cur.LoginEnabled = *body.LoginEnabled
	}
	if body.ClientID != nil {
		cur.ClientID = *body.ClientID
	}
	if body.RegistrationEnabled != nil {
		cur.RegistrationEnabled = *body.RegistrationEnabled
	}
	if body.AdminDiscordIDs != nil {
		cur.AdminDiscordIDs = *body.AdminDiscordIDs
	}
	updateSecret := body.ClientSecret != nil && strings.TrimSpace(*body.ClientSecret) != ""
	if updateSecret {
		cur.Secret = strings.TrimSpace(*body.ClientSecret)
	}
	if err := s.SaveDiscord(r.Context(), cur, updateSecret); err != nil {
		httpapi.WriteErr(w, 500, "discord", err.Error())
		return
	}
	out := s.LoadDiscord(r.Context())
	out.Secret = ""
	out.RedirectURI = s.DiscordCallbackURL(r)
	httpapi.WriteJSON(w, 200, out)
}

func mountAdminRBAC(s *Service, r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(RequirePerm(PermRolesManage))
		r.Get("/admin/permissions", s.handleListPermissions)
		r.Get("/admin/roles", s.handleListRoles)
		r.Post("/admin/roles", s.handleCreateRole)
		r.Get("/admin/roles/{id}", s.handleGetRole)
		r.Patch("/admin/roles/{id}", s.handlePatchRole)
		r.Delete("/admin/roles/{id}", s.handleDeleteRole)
		r.Post("/admin/roles/{id}/members", s.handleAddMembers)
		r.Delete("/admin/roles/{id}/members/{userID}", s.handleRemoveMember)
	})
	r.Group(func(r chi.Router) {
		r.Use(RequirePerm(PermUsersManage))
		r.Get("/admin/libraries/{id}/grants", s.handleListLibGrants)
		r.Post("/admin/libraries/{id}/grants", s.handleSetLibGrant)
		r.Delete("/admin/libraries/{id}/grants", s.handleDeleteLibGrant)
	})
	r.Group(func(r chi.Router) {
		r.Use(RequirePerm(PermSettingsManage))
		r.Get("/admin/settings", s.handleAdminSiteGet)
		r.Put("/admin/settings", s.handleAdminSitePut)
		r.Get("/admin/integrations/discord", s.handleAdminDiscordGet)
		r.Put("/admin/integrations/discord", s.handleAdminDiscordPut)
	})
}
