package interactions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/viewdock/viewdock/internal/discordbot"
	"github.com/viewdock/viewdock/internal/httpapi"
)

// Interaction and response types from the Discord interactions API.
const (
	typePing         = 1
	typeCommand      = 2
	typeComponent    = 3
	typeAutocomplete = 4

	respPong         = 1
	respMessage      = 4
	respAutocomplete = 8

	flagEphemeral = 64
)

const (
	maxBody = 64 << 10
	// Discord drops responses slower than three seconds.
	handlerBudget = 2500 * time.Millisecond

	userCommands    = 8
	userCommandsPer = 20 * time.Second
	userComplete    = 40
	userCompletePer = 20 * time.Second
	failedVerify    = 30
	failedVerifyPer = time.Minute
)

type discordUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type option struct {
	Name    string          `json:"name"`
	Type    int             `json:"type"`
	Value   json.RawMessage `json:"value"`
	Focused bool            `json:"focused"`
	Options []option        `json:"options"`
}

func (o option) str() string {
	var s string
	if json.Unmarshal(o.Value, &s) == nil {
		return s
	}
	return ""
}

type interactionData struct {
	Name     string   `json:"name"`
	Options  []option `json:"options"`
	CustomID string   `json:"custom_id"`
}

type interaction struct {
	ID        string           `json:"id"`
	Type      int              `json:"type"`
	Data      *interactionData `json:"data"`
	GuildID   string           `json:"guild_id"`
	ChannelID string           `json:"channel_id"`
	Member    *struct {
		User *discordUser `json:"user"`
	} `json:"member"`
	User *discordUser `json:"user"`
}

func (in *interaction) userID() string {
	if in.Member != nil && in.Member.User != nil {
		return in.Member.User.ID
	}
	if in.User != nil {
		return in.User.ID
	}
	return ""
}

type button struct {
	Type     int    `json:"type"`
	Style    int    `json:"style"`
	Label    string `json:"label"`
	CustomID string `json:"custom_id,omitempty"`
	URL      string `json:"url,omitempty"`
}

type actionRow struct {
	Type       int      `json:"type"`
	Components []button `json:"components"`
}

type messageData struct {
	Content         string         `json:"content"`
	Flags           int            `json:"flags,omitempty"`
	AllowedMentions map[string]any `json:"allowed_mentions"`
	Components      []actionRow    `json:"components,omitempty"`
}

type choice struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type response struct {
	Type int `json:"type"`
	Data any `json:"data,omitempty"`
}

func message(content string, ephemeral bool, rows ...actionRow) response {
	d := messageData{Content: content, AllowedMentions: map[string]any{"parse": []string{}}, Components: rows}
	if ephemeral {
		d.Flags = flagEphemeral
	}
	return response{Type: respMessage, Data: d}
}

func private(content string) response { return message(content, true) }

func choices(list []choice) response {
	if list == nil {
		list = []choice{}
	}
	return response{Type: respAutocomplete, Data: map[string]any{"choices": list}}
}

// Handler serves Path. Mount it outside session auth, the setup gate and CSRF:
// Discord authenticates every request with an Ed25519 signature instead.
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			httpapi.WriteErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
			return
		}
		now := s.now()
		ip := httpapi.ClientIPString(r, s.Cfg)
		if s.failures.exceeded(ip, failedVerify, failedVerifyPer, now) {
			httpapi.WriteErr(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		if !s.verifier.Configured() {
			httpapi.WriteErr(w, http.StatusServiceUnavailable, "discord_disabled", "Discord interactions are not configured")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
		if err != nil {
			httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "could not read body")
			return
		}
		if len(body) > maxBody {
			httpapi.WriteErr(w, http.StatusRequestEntityTooLarge, "too_large", "request body too large")
			return
		}
		if err := s.verifier.Verify(r.Header.Get("X-Signature-Ed25519"), r.Header.Get("X-Signature-Timestamp"), body); err != nil {
			s.failures.allow(ip, failedVerify, failedVerifyPer, now)
			s.Log.Debug("discord interaction rejected", "category", "discord", "reason", err.Error(), "ip", ip)
			httpapi.WriteErr(w, http.StatusUnauthorized, "invalid_signature", "invalid request signature")
			return
		}
		var in interaction
		if err := json.Unmarshal(body, &in); err != nil {
			httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid interaction")
			return
		}
		if in.Type == typePing {
			httpapi.WriteJSON(w, http.StatusOK, response{Type: respPong})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), handlerBudget)
		defer cancel()
		resp, ok := s.dispatch(ctx, &in)
		if !ok {
			httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "unsupported interaction")
			return
		}
		httpapi.WriteJSON(w, http.StatusOK, resp)
	})
}

func (s *Service) dispatch(ctx context.Context, in *interaction) (response, bool) {
	if in.Data == nil {
		return response{}, false
	}
	userID := in.userID()
	if !discordbot.ValidSnowflake(userID) {
		return response{}, false
	}
	now := s.now()
	switch in.Type {
	case typeAutocomplete:
		if !s.users.allow("ac:"+userID, userComplete, userCompletePer, now) {
			return choices(nil), true
		}
		return s.autocomplete(ctx, in, userID), true
	case typeCommand, typeComponent:
		if !s.users.allow("cmd:"+userID, userCommands, userCommandsPer, now) {
			return private("You are sending commands too quickly. Wait a few seconds and try again."), true
		}
		if in.GuildID == "" || !discordbot.ValidSnowflake(in.ChannelID) {
			return private("Use ViewDock commands in a server channel."), true
		}
		if in.Type == typeComponent {
			return s.component(ctx, in, userID), true
		}
		return s.command(ctx, in, userID), true
	}
	return response{}, false
}

// errText maps internal failures to safe user-facing text.
func (s *Service) errText(ctx context.Context, action string, err error) string {
	switch {
	case errors.Is(err, ErrNotHost):
		return "Only the party host can do that."
	case errors.Is(err, ErrRoomNotFound), errors.Is(err, ErrNoLink):
		return "That watch party has ended."
	case errors.Is(err, ErrForbidden):
		return "Your ViewDock account is not allowed to do that."
	case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
		return "ViewDock took too long to respond. Try again."
	}
	s.Log.Warn("discord command failed", "category", "discord", "action", action, "err", err)
	return "ViewDock could not complete that request. Try again shortly."
}
