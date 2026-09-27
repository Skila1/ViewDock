package interactions

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/discordbot"
	"github.com/viewdock/viewdock/internal/httpapi"
)

// withEntryPoints appends the Activity entry point commands Discord already
// has, so a global overwrite keeps the Activity's launch button.
func withEntryPoints(want, existing []discordbot.Command) []discordbot.Command {
	out := append([]discordbot.Command(nil), want...)
	for _, c := range existing {
		if c.Type == discordbot.CommandEntryPoint {
			out = append(out, c)
		}
	}
	return out
}

// ActivityRoutes serves the watch party lookup used by the web app inside
// the Discord Activity. Callers must be signed in.
func (s *Service) ActivityRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireUser)
		r.Use(auth.RateLimit(s.Cfg, 30, time.Minute))
		r.Post("/discord/activity/room", s.handleActivityRoom)
	})
}

type activityRoom struct {
	RoomID     string `json:"room_id"`
	InviteCode string `json:"invite_code"`
	Title      string `json:"title"`
	Created    bool   `json:"created"`
}

// handleActivityRoom returns the watch party linked to the voice channel the
// Activity runs in, creating it when the caller picks a title and none is
// linked yet. Discord confirms that the caller is in the Activity instance
// and where it runs, so a caller cannot reach another channel's party.
func (s *Service) handleActivityRoom(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := auth.FromRequest(r)
	var body struct {
		InstanceID string `json:"instance_id"`
		ItemKind   string `json:"item_kind"`
		ItemID     string `json:"item_id"`
	}
	if err := httpapi.ReadJSON(r, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	body.InstanceID = strings.TrimSpace(body.InstanceID)
	if !discordbot.ValidActivityInstanceID(body.InstanceID) {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "an Activity instance ID is required")
		return
	}
	if s.ActivityEnabled == nil || !s.ActivityEnabled() || s.OAuth == nil {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "disabled", "the Discord Activity is off")
		return
	}
	if s.Parties == nil || s.Catalog == nil || !s.partiesEnabled() {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "parties_disabled", "watch parties are off on this server")
		return
	}
	if s.setup().Separate {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "separate_bot", "the Discord Activity needs the bot to use the sign-in application")
		return
	}
	bot := s.bot()
	appID := s.OAuth(ctx).ClientID
	if bot == nil || !discordbot.ValidSnowflake(appID) {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "discord_bot_disabled", "set up Discord sign-in and the bot first")
		return
	}
	discordID := ""
	if s.DiscordUserID != nil {
		discordID = s.DiscordUserID(ctx, p.UserID)
	}
	if discordID == "" {
		httpapi.WriteErr(w, http.StatusForbidden, "not_linked", "link Discord to your ViewDock account first")
		return
	}
	inst, err := bot.GetActivityInstance(ctx, appID, body.InstanceID)
	var apiErr *discordbot.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		httpapi.WriteErr(w, http.StatusNotFound, "instance_not_found", "Discord does not know this Activity instance; relaunch the Activity")
		return
	}
	if err != nil {
		s.discordFailure(w, "activity_instance", err)
		return
	}
	channelID := inst.Location.ChannelID
	if !inst.Has(discordID) || !discordbot.ValidSnowflake(channelID) {
		httpapi.WriteErr(w, http.StatusForbidden, "not_in_activity", "you are not in this Activity")
		return
	}

	if link, _, err := s.linkedRoom(ctx, channelID); err == nil {
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"room": activityRoom{RoomID: link.RoomID, InviteCode: link.InviteCode, Title: link.Title}})
		return
	}
	kind, id := strings.TrimSpace(body.ItemKind), strings.TrimSpace(body.ItemID)
	if kind == "" && id == "" {
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"room": nil, "can_create": !p.PartyOnly})
		return
	}
	if p.PartyOnly {
		httpapi.WriteErr(w, http.StatusForbidden, "party_only", "your account can join parties but cannot start them")
		return
	}
	if (kind != "movie" && kind != "episode") || id == "" || len(id) > 80 {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "pick a movie or an episode")
		return
	}
	title, err := s.Catalog.TitleOf(ctx, p, kind, id)
	if err != nil {
		httpapi.WriteErr(w, http.StatusForbidden, "forbidden", "you cannot watch that title")
		return
	}
	roomID, code, err := s.Parties.Create(ctx, p, kind, id)
	if err != nil {
		httpapi.WriteErr(w, http.StatusForbidden, "forbidden", "the watch party could not be started")
		return
	}
	link := Link{ChannelID: channelID, GuildID: inst.Location.GuildID, Kind: LinkVoice, RoomID: roomID, InviteCode: code, Title: title, LinkedBy: p.UserID, DiscordUserID: discordID}
	if err := s.Links.Put(ctx, link); err != nil {
		s.Log.Warn("discord activity link failed", "category", "discord", "err", err)
	}
	s.audit(ctx, p.UserID, "discord.activity_party_create", roomID, "channel="+channelID)
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"room": activityRoom{RoomID: roomID, InviteCode: code, Title: title, Created: true}})
}
