package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/viewdock/viewdock/internal/discordbot"
	"github.com/viewdock/viewdock/internal/httpapi"
)

// EmbedHeader is sent by the web app when it runs inside a Discord Activity.
// Cookies issued for such requests are SameSite=None, Secure and
// Partitioned, which is the only form a browser keeps inside Discord's
// iframe. The header changes cookie attributes only, never authorization.
const EmbedHeader = "X-ViewDock-Embed"

// EmbedDiscordActivity is the EmbedHeader value for a Discord Activity.
const EmbedDiscordActivity = "discord-activity"

const activityOriginTTL = 10 * time.Second

func embeddedRequest(r *http.Request) bool {
	return r.Header.Get(EmbedHeader) == EmbedDiscordActivity
}

// cookieAttrs returns the Secure, SameSite and Partitioned attributes for a
// cookie set in response to r.
func cookieAttrs(r *http.Request, secure bool) (bool, http.SameSite, bool) {
	if embeddedRequest(r) {
		return true, http.SameSiteNoneMode, true
	}
	return secure, http.SameSiteLaxMode, false
}

func (s *Service) setSessionCookie(w http.ResponseWriter, r *http.Request, raw string, exp time.Time) {
	secure, same, partitioned := cookieAttrs(r, httpapi.CookieSecure(r, s.Cfg))
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: raw, Path: "/", HttpOnly: true,
		Secure: secure, SameSite: same, Partitioned: partitioned, Expires: exp,
	})
}

func (s *Service) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	if embeddedRequest(r) {
		http.SetCookie(w, &http.Cookie{
			Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
			Secure: true, SameSite: http.SameSiteNoneMode, Partitioned: true,
		})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1})
}

type activityOriginCache struct {
	mu     sync.Mutex
	at     time.Time
	origin string
}

// activityClient returns the sign-in application when the Discord Activity
// is enabled and that application can exchange codes.
func (s *Service) activityClient(ctx context.Context) (DiscordOAuthConfig, bool) {
	if s.ActivityEnabled == nil || !s.ActivityEnabled() {
		return DiscordOAuthConfig{}, false
	}
	oauth := s.LoadDiscord(ctx)
	if !discordbot.ValidSnowflake(oauth.ClientID) || oauth.Secret == "" {
		return DiscordOAuthConfig{}, false
	}
	return oauth, true
}

// ActivityOrigin is the origin the web app has inside the Discord Activity
// (https://<client id>.discordsays.com), or "" when the Activity is off. It
// is cached briefly because it is consulted for every response.
func (s *Service) ActivityOrigin(ctx context.Context) string {
	c := &s.activityOrigin
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < activityOriginTTL {
		return c.origin
	}
	c.origin = ""
	if oauth, ok := s.activityClient(ctx); ok {
		c.origin = "https://" + oauth.ClientID + ".discordsays.com"
	}
	c.at = time.Now()
	return c.origin
}

// ResetActivityOrigin drops the cached origin after a configuration change.
func (s *Service) ResetActivityOrigin() {
	s.activityOrigin.mu.Lock()
	s.activityOrigin.at = time.Time{}
	s.activityOrigin.mu.Unlock()
}

// ActivityOriginAllowed reports whether r comes from the Discord Activity.
func (s *Service) ActivityOriginAllowed(r *http.Request) bool {
	origin := s.ActivityOrigin(r.Context())
	return origin != "" && strings.EqualFold(strings.TrimRight(strings.TrimSpace(r.Header.Get("Origin")), "/"), origin)
}

// FrameAncestors lists the origins that may embed the web app: Discord's
// clients and the Activity's own proxy origin. Empty when the Activity is off.
func (s *Service) FrameAncestors(ctx context.Context) []string {
	origin := s.ActivityOrigin(ctx)
	if origin == "" {
		return nil
	}
	return []string{"https://discord.com", "https://*.discord.com", origin}
}

func (s *Service) handleActivityConfig(w http.ResponseWriter, r *http.Request) {
	oauth, ok := s.activityClient(r.Context())
	if !ok {
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"enabled":   true,
		"client_id": oauth.ClientID,
		"scopes":    strings.Fields(DiscordLoginScope(oauth.DiscordRegistration)),
	})
}

// handleActivitySignIn exchanges an Embedded App SDK authorization code for
// a ViewDock session. It applies the same account rules as the browser
// sign-in; with Discord sign-in off, only accounts already linked to Discord
// can sign in. The Discord access token is returned because the SDK needs it
// to authenticate the Activity.
func (s *Service) handleActivitySignIn(w http.ResponseWriter, r *http.Request) {
	oauth, ok := s.activityClient(r.Context())
	if !ok {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "disabled", "the Discord Activity is off")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || strings.TrimSpace(body.Code) == "" || len(body.Code) > 512 {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "an authorization code is required")
		return
	}
	prof, access, err := exchangeDiscordCode(r.Context(), oauth.ClientID, oauth.Secret, "", strings.TrimSpace(body.Code), "")
	if err != nil {
		httpapi.WriteErr(w, http.StatusUnauthorized, "token_exchange", "Discord did not accept the sign-in")
		return
	}
	if !oauth.LoginEnabled {
		if _, err := s.userByDiscord(r.Context(), prof.ID); err != nil {
			httpapi.WriteErr(w, http.StatusForbidden, "not_linked", "link Discord to your ViewDock account under Settings, Connected accounts first")
			return
		}
	}
	u, err := s.discordAccount(r.Context(), oauth, prof, access)
	if err != nil {
		httpapi.WriteErr(w, http.StatusForbidden, "denied", err.Error())
		return
	}
	raw, exp, err := s.Sessions.Create(r.Context(), u.ID, httpapi.ClientIPString(r, s.Cfg), r.UserAgent())
	if err != nil {
		if errors.Is(err, ErrSessionLimit) {
			httpapi.WriteErr(w, http.StatusTooManyRequests, "session_limit", "this account is signed in on the maximum number of devices")
			return
		}
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "session", "sign-in is temporarily unavailable")
		return
	}
	s.setSessionCookie(w, r, raw, exp)
	if _, err := IssueCSRF(w, r, s.Cfg); err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "csrf", "failed")
		return
	}
	s.Audit.Event(r.Context(), u.ID, "login.discord_activity", prof.ID, httpapi.ClientIPString(r, s.Cfg), "")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"access_token": access, "user": meJSON(u, false)})
}

// discordAccount applies the Discord sign-in rules to a verified profile:
// new accounts must pass the server and role checks (administrators listed
// by Discord ID are exempt) and registration must be on; disabled accounts
// are refused.
func (s *Service) discordAccount(ctx context.Context, oauth DiscordOAuthConfig, prof DiscordProfile, access string) (User, error) {
	if _, err := s.userByDiscord(ctx, prof.ID); err != nil && !isAdminDiscordID(prof.ID, oauth.AdminDiscordIDs) {
		if err := CheckDiscordRegistration(ctx, access, oauth.DiscordRegistration); err != nil {
			return User{}, err
		}
	}
	u, err := s.UpsertDiscordUser(ctx, prof)
	if err != nil {
		return User{}, err
	}
	if u.Disabled {
		return User{}, errors.New("disabled")
	}
	return u, nil
}
