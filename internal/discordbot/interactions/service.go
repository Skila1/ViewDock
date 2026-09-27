// Package interactions serves the official Discord bot's HTTP interactions
// endpoint (slash commands, autocomplete and buttons) and its admin API.
// It never automates Discord user accounts.
package interactions

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/discordbot"
	"github.com/viewdock/viewdock/internal/httpapi"
)

// Path is where the public interactions endpoint must be mounted, outside
// session authentication, the setup gate and CSRF.
const Path = "/api/v1/integrations/discord/interactions"

// Party control actions accepted by Parties.Control.
const (
	ActionPause  = "pause"
	ActionResume = "resume"
)

var (
	// ErrNotLinked means the Discord user has no enabled ViewDock account.
	ErrNotLinked = auth.ErrDiscordNotLinked
	// ErrRoomNotFound means the watch party no longer exists.
	ErrRoomNotFound = errors.New("watch party not found")
	// ErrNotHost means a non-host attempted a host-only control.
	ErrNotHost = errors.New("only the party host can do that")
	// ErrForbidden means the account may not perform the action or open the title.
	ErrForbidden = errors.New("not allowed")
)

// Accounts maps a Discord user to the ViewDock account linked through
// Discord OAuth. *auth.Service implements it.
type Accounts interface {
	// PrincipalForDiscord returns ErrNotLinked when no enabled account is linked.
	PrincipalForDiscord(ctx context.Context, discordUserID string) (*auth.Principal, error)
}

// Parties is the narrow watch party contract. The adapter lives with the
// watch party coordinator.
type Parties interface {
	// Create opens a room hosted by p for a title p is authorised to play.
	Create(ctx context.Context, p *auth.Principal, kind, id string) (roomID, inviteCode string, err error)
	// State returns the room snapshot (keys: host, owner, playing,
	// position_ms, members, item_kind, item_id) or nil when the room does not
	// exist.
	State(roomID string) map[string]any
	// Control applies ActionPause or ActionResume on behalf of p,
	// broadcasting to connected members. The room owner regains host when
	// acting. It must return ErrNotHost for anyone but the host or owner and
	// ErrRoomNotFound for a missing room.
	Control(ctx context.Context, p *auth.Principal, roomID, action string) error
	// Resolve maps an invite code to its room.
	Resolve(inviteCode string) (roomID string, ok bool)
}

// Title is a catalogue search hit.
type Title struct {
	Kind string
	ID   string
	Name string
	Year string
}

// Catalog searches and names the titles a principal is allowed to see.
type Catalog interface {
	SearchTitles(ctx context.Context, p *auth.Principal, query string, limit int) ([]Title, error)
	// TitleOf returns the display title, or ErrForbidden when p may not see it.
	TitleOf(ctx context.Context, p *auth.Principal, kind, id string) (string, error)
}

// KV persists small admin records (server_settings). *settings.Store implements it.
type KV interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

// Auditor records admin and bot actions. *audit.Log implements it.
type Auditor interface {
	Event(ctx context.Context, actorID, action, target, ip, detail string)
}

type Deps struct {
	DB  *sql.DB
	Cfg config.Config
	// Settings resolves the public URL used in invite links.
	Settings httpapi.SettingsLookup
	KV       KV
	Audit    Auditor
	Log      *slog.Logger
	// PublicKey returns the runtime-configured application public key (hex).
	PublicKey func() string
	// Bot returns the runtime-configured bot client, nil when unset.
	Bot      func() *discordbot.Client
	Accounts Accounts
	Parties  Parties
	Catalog  Catalog
	// PartiesEnabled reports the watch party feature flag; nil means enabled.
	PartiesEnabled func() bool
	// SaveConfig stores runtime settings (used to save the application public
	// key fetched from Discord when none is configured). Optional.
	SaveConfig func(ctx context.Context, actorID, ip string, values map[string]string) error
	// PublicKeyConfigKey returns the runtime config key SaveConfig writes the
	// active bot application's public key to.
	PublicKeyConfigKey func() string
	// OAuth returns the Discord sign-in configuration, including the client
	// secret. Optional; diagnostics skip sign-in checks without it.
	OAuth func(ctx context.Context) auth.DiscordOAuthConfig
	// Setup reports which bot credentials are in use. Optional.
	Setup func() BotSetup
}

// BotSetup describes the bot credentials in use, never their values.
type BotSetup struct {
	// Separate is true when the bot uses its own application instead of the
	// sign-in application.
	Separate bool
	// TokenSource and PublicKeySource are "database" or "environment", or
	// empty when the active value is unset.
	TokenSource     string
	PublicKeySource string
}

type Service struct {
	Deps
	Links    *LinkStore
	verifier *discordbot.Verifier
	users    *limiter
	failures *limiter
	now      func() time.Time
	diag     diagnosticsCache
	// oauthBase is the Discord API root used for the client credentials check.
	oauthBase string
}

func New(d Deps) *Service {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.PublicKeyConfigKey == nil {
		d.PublicKeyConfigKey = func() string { return "discord.public_key" }
	}
	return &Service{
		Deps:      d,
		Links:     NewLinkStore(d.DB),
		verifier:  discordbot.NewVerifier(d.PublicKey),
		users:     newLimiter(),
		failures:  newLimiter(),
		now:       time.Now,
		oauthBase: "https://discord.com/api/v10",
	}
}

func (s *Service) setup() BotSetup {
	if s.Setup == nil {
		return BotSetup{}
	}
	return s.Setup()
}

func (s *Service) mode() string {
	if s.setup().Separate {
		return "separate"
	}
	return "shared"
}

func (s *Service) partiesEnabled() bool {
	return s.PartiesEnabled == nil || s.PartiesEnabled()
}

func (s *Service) bot() *discordbot.Client {
	if s.Bot == nil {
		return nil
	}
	return s.Bot()
}

func (s *Service) publicBase(ctx context.Context) string {
	return strings.TrimRight(httpapi.ResolvePublicURL(ctx, s.Cfg, s.Settings), "/")
}

func (s *Service) inviteURL(ctx context.Context, code string) string {
	base := s.publicBase(ctx)
	if base == "" || code == "" {
		return ""
	}
	return base + "/together/" + code
}

func (s *Service) audit(ctx context.Context, actorID, action, target, detail string) {
	if s.Audit != nil {
		s.Audit.Event(ctx, actorID, action, target, "", detail)
	}
}
