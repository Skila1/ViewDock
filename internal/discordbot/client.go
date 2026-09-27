package discordbot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	Token   string
	BaseURL string
	HTTP    *http.Client
}

func New(token string) *Client {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	return &Client{Token: token, BaseURL: "https://discord.com/api/v10", HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// Discord snowflakes are 17 to 20 decimal digits.
var snowflake = regexp.MustCompile(`^[0-9]{17,20}$`)

func ValidSnowflake(id string) bool { return snowflake.MatchString(id) }

const maxTitle = 200

// ErrDisabled is returned when no bot token is configured.
var ErrDisabled = errors.New("discord bot is disabled")

// APIError is a non-2xx Discord REST response. Message is Discord's own
// error text, which never contains the bot token.
type APIError struct {
	Status     int
	Code       int
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("discord API returned %d %s", e.Status, http.StatusText(e.Status))
	if e.Message != "" {
		msg += ": " + e.Message
	}
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf(" (retry after %s)", e.RetryAfter.Round(time.Second))
	}
	return msg
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	if c == nil || c.Token == "" {
		return ErrDisabled
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+c.Token)
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/viewdock/viewdock, 2)")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &APIError{Status: resp.StatusCode}
		var payload struct {
			Code       int     `json:"code"`
			Message    string  `json:"message"`
			RetryAfter float64 `json:"retry_after"`
		}
		if json.Unmarshal(raw, &payload) == nil {
			apiErr.Code = payload.Code
			apiErr.Message = truncate(payload.Message, 200)
			apiErr.RetryAfter = time.Duration(payload.RetryAfter * float64(time.Second))
		}
		return apiErr
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (c *Client) SendRoomInvite(ctx context.Context, channelID, inviteURL, title string) error {
	if c == nil || c.Token == "" {
		return ErrDisabled
	}
	channelID = strings.TrimSpace(channelID)
	inviteURL = strings.TrimSpace(inviteURL)
	if channelID == "" || inviteURL == "" {
		return errors.New("channel_id and invite_url are required")
	}
	if !ValidSnowflake(channelID) {
		return errors.New("channel_id must be a Discord channel ID")
	}
	if u, err := url.Parse(inviteURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return errors.New("invite_url must be an absolute http(s) URL")
	}
	content := inviteURL
	if title = strings.TrimSpace(title); title != "" {
		content = fmt.Sprintf("**%s**\n%s", EscapeMarkdown(truncate(title, maxTitle)), inviteURL)
	}
	return c.do(ctx, http.MethodPost, "/channels/"+channelID+"/messages",
		map[string]any{"content": content, "allowed_mentions": map[string]any{"parse": []string{}}}, nil)
}

// EscapeMarkdown neutralises Discord markdown and mention syntax in
// user-controlled text.
func EscapeMarkdown(s string) string {
	return strings.NewReplacer("\\", "\\\\", "*", "\\*", "_", "\\_", "`", "\\`", "~", "\\~", "|", "\\|", ">", "\\>", "@", "@\u200b", "#", "\\#", "[", "\\[", "]", "\\]").Replace(s)
}

// Application is the subset of the bot's application object ViewDock uses.
// VerifyKey is the Ed25519 public key Discord signs interactions with.
type Application struct {
	ID                      string `json:"id"`
	Name                    string `json:"name"`
	VerifyKey               string `json:"verify_key"`
	InteractionsEndpointURL string `json:"interactions_endpoint_url"`
}

// CurrentApplication returns the application that owns the bot token.
func (c *Client) CurrentApplication(ctx context.Context) (Application, error) {
	var app Application
	if err := c.do(ctx, http.MethodGet, "/applications/@me", nil, &app); err != nil {
		return Application{}, err
	}
	if !ValidSnowflake(app.ID) {
		return Application{}, errors.New("discord returned an invalid application id")
	}
	return app, nil
}

// SetInteractionsEndpoint points the application's interactions endpoint at
// endpoint. Discord validates the URL synchronously by sending signed PINGs.
func (c *Client) SetInteractionsEndpoint(ctx context.Context, endpoint string) error {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return errors.New("the interactions endpoint must be an absolute https URL")
	}
	return c.do(ctx, http.MethodPatch, "/applications/@me", map[string]any{"interactions_endpoint_url": u.String()}, nil)
}

// Application command option types.
const (
	OptionSubCommand = 1
	OptionString     = 3
	OptionChannel    = 7
)

// Channel types accepted by channel options.
const (
	ChannelGuildVoice = 2
	ChannelGuildStage = 13
)

// Interaction context types.
const ContextGuild = 0

type CommandOption struct {
	Type         int             `json:"type"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Required     bool            `json:"required,omitempty"`
	Autocomplete bool            `json:"autocomplete,omitempty"`
	MaxLength    int             `json:"max_length,omitempty"`
	ChannelTypes []int           `json:"channel_types,omitempty"`
	Options      []CommandOption `json:"options,omitempty"`
}

type Command struct {
	ID          string          `json:"id,omitempty"`
	Type        int             `json:"type,omitempty"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Options     []CommandOption `json:"options,omitempty"`
	Contexts    []int           `json:"contexts,omitempty"`
}

// OverwriteCommands replaces the application's commands, globally when
// guildID is empty or for one guild (applies immediately) otherwise.
func (c *Client) OverwriteCommands(ctx context.Context, appID, guildID string, cmds []Command) ([]Command, error) {
	if !ValidSnowflake(appID) {
		return nil, errors.New("application id must be a Discord ID")
	}
	path := "/applications/" + appID + "/commands"
	if guildID != "" {
		if !ValidSnowflake(guildID) {
			return nil, errors.New("guild id must be a Discord server ID")
		}
		path = "/applications/" + appID + "/guilds/" + guildID + "/commands"
	}
	var out []Command
	if err := c.do(ctx, http.MethodPut, path, cmds, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListCommands returns the application's registered commands, globally when
// guildID is empty or for one guild otherwise.
func (c *Client) ListCommands(ctx context.Context, appID, guildID string) ([]Command, error) {
	if !ValidSnowflake(appID) {
		return nil, errors.New("application id must be a Discord ID")
	}
	path := "/applications/" + appID + "/commands"
	if guildID != "" {
		if !ValidSnowflake(guildID) {
			return nil, errors.New("guild id must be a Discord server ID")
		}
		path = "/applications/" + appID + "/guilds/" + guildID + "/commands"
	}
	var out []Command
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Guild permission bits ViewDock checks.
const (
	PermAdministrator uint64 = 1 << 3
	PermViewChannel   uint64 = 1 << 10
	PermSendMessages  uint64 = 1 << 11
)

// Guild is a server the bot belongs to. Permissions is the bot's server-wide
// permission bitfield, before channel overwrites.
type Guild struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Permissions string `json:"permissions"`
}

// Has reports whether the bot holds every bit in perm, or Administrator.
func (g Guild) Has(perm uint64) bool {
	bits, err := strconv.ParseUint(strings.TrimSpace(g.Permissions), 10, 64)
	if err != nil {
		return false
	}
	return bits&PermAdministrator != 0 || bits&perm == perm
}

// CurrentGuilds returns up to the first 200 servers the bot belongs to.
func (c *Client) CurrentGuilds(ctx context.Context) ([]Guild, error) {
	var out []Guild
	if err := c.do(ctx, http.MethodGet, "/users/@me/guilds?limit=200", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ErrInvalidClient means Discord rejected an OAuth client ID and secret.
var ErrInvalidClient = errors.New("discord rejected the OAuth client ID or secret")

// VerifyClientCredentials checks an OAuth client ID and secret with a client
// credentials grant against baseURL (for example https://discord.com/api/v10).
// The issued token is discarded. Errors never contain the secret.
func VerifyClientCredentials(ctx context.Context, hc *http.Client, baseURL, clientID, secret string) error {
	form := url.Values{"grant_type": {"client_credentials"}, "scope": {"identify"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.SetBasicAuth(clientID, secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/viewdock/viewdock, 2)")
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	var payload struct {
		Error       string  `json:"error"`
		Description string  `json:"error_description"`
		Message     string  `json:"message"`
		RetryAfter  float64 `json:"retry_after"`
	}
	_ = json.Unmarshal(raw, &payload)
	if resp.StatusCode == http.StatusUnauthorized || payload.Error == "invalid_client" {
		return ErrInvalidClient
	}
	msg := payload.Description
	if msg == "" {
		msg = payload.Message
	}
	if msg == "" {
		msg = payload.Error
	}
	return &APIError{Status: resp.StatusCode, Message: truncate(msg, 200), RetryAfter: time.Duration(payload.RetryAfter * float64(time.Second))}
}
