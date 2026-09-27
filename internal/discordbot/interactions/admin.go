package interactions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/discordbot"
	"github.com/viewdock/viewdock/internal/httpapi"
)

const registrationKey = "discord.interactions.registration"

// Registration records the last successful command registration.
type Registration struct {
	ApplicationID string    `json:"application_id"`
	GuildID       string    `json:"guild_id,omitempty"`
	Commands      int       `json:"commands"`
	RegisteredAt  time.Time `json:"registered_at"`
	RegisteredBy  string    `json:"registered_by"`
}

// AdminRoutes mounts the admin API under the session-authenticated,
// CSRF-protected /api/v1 router.
func (s *Service) AdminRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequirePerm(auth.PermSettingsManage))
		r.Get("/admin/integrations/discord/interactions", s.handleStatus)
		r.Delete("/admin/integrations/discord/links/{channelID}", s.handleDeleteLink)
		r.Group(func(r chi.Router) {
			r.Use(auth.RateLimit(s.Cfg, 6, time.Minute))
			r.Post("/admin/integrations/discord/commands", s.handleRegister)
			r.Post("/admin/integrations/discord/endpoint", s.handleSetEndpoint)
		})
		r.Get("/admin/integrations/discord/diagnostics", s.handleDiagnostics)
		r.Group(func(r chi.Router) {
			r.Use(auth.RateLimit(s.Cfg, 10, time.Minute))
			r.Post("/admin/integrations/discord/diagnostics", s.handleRunDiagnostics)
		})
	})
}

type linkView struct {
	Link
	RoomActive bool `json:"room_active"`
}

func (s *Service) endpointURL(ctx context.Context) string {
	base := s.publicBase(ctx)
	if base == "" {
		return ""
	}
	return base + Path
}

func (s *Service) registration(ctx context.Context) *Registration {
	if s.KV == nil {
		return nil
	}
	raw, err := s.KV.Get(ctx, registrationKey)
	if err != nil || raw == "" {
		return nil
	}
	var reg Registration
	if json.Unmarshal([]byte(raw), &reg) != nil {
		return nil
	}
	return &reg
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	links, err := s.Links.List(ctx)
	if err != nil {
		s.Log.Error("list discord channel links", "category", "discord", "err", err)
		httpapi.WriteErr(w, http.StatusInternalServerError, "discord", "channel links could not be loaded")
		return
	}
	views := make([]linkView, 0, len(links))
	for _, l := range links {
		active := s.Parties != nil && s.Parties.State(l.RoomID) != nil
		views = append(views, linkView{Link: l, RoomActive: active})
	}
	key := ""
	if s.PublicKey != nil {
		key = strings.TrimSpace(s.PublicKey())
	}
	endpoint := s.endpointURL(ctx)
	setup := s.setup()
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"mode":              s.mode(),
		"bot_token_source":  setup.TokenSource,
		"public_key_source": setup.PublicKeySource,
		"endpoint_url":      endpoint,
		"endpoint_https":    strings.HasPrefix(endpoint, "https://"),
		"public_key_set":    key != "",
		"public_key_valid":  discordbot.ValidPublicKey(key),
		"bot_configured":    s.bot() != nil,
		"parties_enabled":   s.partiesEnabled(),
		"registration":      s.registration(ctx),
		"commands":          commandNames(),
		"links":             views,
	})
}

func commandNames() []string {
	var out []string
	for _, c := range Commands() {
		for _, o := range c.Options {
			out = append(out, "/"+c.Name+" "+o.Name)
		}
	}
	return out
}

// discordFailure maps a Discord REST error to a safe admin response.
func (s *Service) discordFailure(w http.ResponseWriter, action string, err error) {
	var apiErr *discordbot.APIError
	switch {
	case errors.Is(err, discordbot.ErrDisabled):
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "discord_bot_disabled", "set the Discord bot token first")
	case errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized:
		httpapi.WriteErr(w, http.StatusBadRequest, "discord_token", "Discord rejected the bot token")
	case errors.As(err, &apiErr) && apiErr.Status == http.StatusTooManyRequests:
		httpapi.WriteErr(w, http.StatusTooManyRequests, "discord_rate_limited", apiErr.Error())
	case errors.As(err, &apiErr):
		httpapi.WriteErr(w, http.StatusBadGateway, "discord", apiErr.Error())
	default:
		s.Log.Warn("discord request failed", "category", "discord", "action", action, "err", err)
		httpapi.WriteErr(w, http.StatusBadGateway, "discord", "Discord could not be reached")
	}
}

func (s *Service) handleRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		GuildID string `json:"guild_id"`
	}
	if err := httpapi.ReadJSON(r, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	body.GuildID = strings.TrimSpace(body.GuildID)
	if body.GuildID != "" && !discordbot.ValidSnowflake(body.GuildID) {
		httpapi.WriteErr(w, http.StatusBadRequest, "discord", "guild_id must be a Discord server ID")
		return
	}
	bot := s.bot()
	if bot == nil {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "discord_bot_disabled", "set the Discord bot token first")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	p := auth.FromRequest(r)
	ip := httpapi.ClientIPString(r, s.Cfg)
	app, err := bot.CurrentApplication(ctx)
	if err != nil {
		s.discordFailure(w, "application", err)
		return
	}
	keyStatus := "ok"
	current := ""
	if s.PublicKey != nil {
		current = strings.TrimSpace(s.PublicKey())
	}
	switch {
	case app.VerifyKey == "":
		keyStatus = "unknown"
	case current == "" && s.SaveConfig != nil && discordbot.ValidPublicKey(app.VerifyKey):
		if err := s.SaveConfig(ctx, p.UserID, ip, map[string]string{s.PublicKeyConfigKey(): app.VerifyKey}); err != nil {
			s.Log.Warn("save discord public key", "category", "discord", "err", err)
			keyStatus = "unset"
		} else {
			keyStatus = "saved"
		}
	case current == "":
		keyStatus = "unset"
	case !strings.EqualFold(current, app.VerifyKey):
		keyStatus = "mismatch"
	}
	cmds, err := bot.OverwriteCommands(ctx, app.ID, body.GuildID, Commands())
	if err != nil {
		s.discordFailure(w, "commands", err)
		return
	}
	reg := Registration{ApplicationID: app.ID, GuildID: body.GuildID, Commands: len(cmds), RegisteredAt: time.Now().UTC(), RegisteredBy: p.UserID}
	if s.KV != nil {
		if raw, err := json.Marshal(reg); err == nil {
			if err := s.KV.Set(ctx, registrationKey, string(raw)); err != nil {
				s.Log.Warn("store discord command registration", "category", "discord", "err", err)
			}
		}
	}
	scope := "global"
	if body.GuildID != "" {
		scope = "guild=" + body.GuildID
	}
	if s.Audit != nil {
		s.Audit.Event(ctx, p.UserID, "discord.commands_register", app.ID, ip, scope)
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"registration":      reg,
		"public_key_status": keyStatus,
		"endpoint_url":      s.endpointURL(ctx),
	})
}

func (s *Service) handleSetEndpoint(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if !s.verifier.Configured() {
		httpapi.WriteErr(w, http.StatusBadRequest, "discord", "set the application public key first")
		return
	}
	endpoint := s.endpointURL(ctx)
	if !strings.HasPrefix(endpoint, "https://") {
		httpapi.WriteErr(w, http.StatusBadRequest, "discord", "Discord requires an https public URL; set it under Admin, Settings")
		return
	}
	bot := s.bot()
	if bot == nil {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "discord_bot_disabled", "set the Discord bot token first")
		return
	}
	if err := bot.SetInteractionsEndpoint(ctx, endpoint); err != nil {
		var apiErr *discordbot.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest {
			httpapi.WriteErr(w, http.StatusBadRequest, "discord_endpoint", "Discord could not verify "+endpoint+". Check that it is reachable from the internet and that the public key matches the application.")
			return
		}
		s.discordFailure(w, "endpoint", err)
		return
	}
	p := auth.FromRequest(r)
	if s.Audit != nil {
		s.Audit.Event(ctx, p.UserID, "discord.endpoint_set", endpoint, httpapi.ClientIPString(r, s.Cfg), "")
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "endpoint_url": endpoint})
}

func (s *Service) handleDeleteLink(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "channelID")
	if !discordbot.ValidSnowflake(id) {
		httpapi.WriteErr(w, http.StatusBadRequest, "discord", "channel id must be a Discord ID")
		return
	}
	ok, err := s.Links.Delete(r.Context(), id)
	if err != nil {
		s.Log.Error("delete discord channel link", "category", "discord", "err", err)
		httpapi.WriteErr(w, http.StatusInternalServerError, "discord", "the link could not be removed")
		return
	}
	if !ok {
		httpapi.WriteErr(w, http.StatusNotFound, "not_found", "no link for that channel")
		return
	}
	p := auth.FromRequest(r)
	if s.Audit != nil {
		s.Audit.Event(r.Context(), p.UserID, "discord.channel_unlink", id, httpapi.ClientIPString(r, s.Cfg), "admin")
	}
	httpapi.WriteOK(w)
}
