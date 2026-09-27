package interactions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/discordbot"
	"github.com/viewdock/viewdock/internal/httpapi"
)

// Check states. Info and skipped never affect the overall status.
const (
	CheckOK      = "ok"
	CheckWarn    = "warn"
	CheckError   = "error"
	CheckInfo    = "info"
	CheckSkipped = "skipped"
)

// Check groups, in display order.
var checkGroups = []string{"configuration", "oauth", "bot", "servers", "commands", "interactions"}

// Corrective actions the admin page knows how to perform.
const (
	ActionEditAuth     = "edit_auth"
	ActionEditBot      = "edit_bot"
	ActionEditRegister = "edit_registration"
	ActionSettings     = "open_settings"
	ActionRegister     = "register_commands"
	ActionSetEndpoint  = "set_endpoint"
	ActionInviteBot    = "invite_bot"
)

// Bot permissions requested by the invite link: View Channel and Send Messages.
const invitePermissions = discordbot.PermViewChannel | discordbot.PermSendMessages

const diagnosticsTimeout = 15 * time.Second

type CheckAction struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	URL   string `json:"url,omitempty"`
}

// Check is one diagnostic result. Detail never contains credentials.
type Check struct {
	ID     string       `json:"id"`
	Group  string       `json:"group"`
	Label  string       `json:"label"`
	Status string       `json:"status"`
	Detail string       `json:"detail"`
	Remote bool         `json:"remote"`
	Action *CheckAction `json:"action,omitempty"`
}

type DiagnosticsApp struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Diagnostics struct {
	Mode           string `json:"mode"`
	Status         string `json:"status"`
	SignInClientID string `json:"sign_in_client_id"`
	// CheckedAt is when Discord was last contacted; nil when never.
	CheckedAt *time.Time `json:"checked_at"`
	// Stale is true when the configuration changed after CheckedAt.
	Stale       bool            `json:"stale"`
	Application *DiagnosticsApp `json:"application,omitempty"`
	InviteURL   string          `json:"invite_url,omitempty"`
	Checks      []Check         `json:"checks"`
}

type diagnosticsCache struct {
	run sync.Mutex

	mu          sync.Mutex
	at          time.Time
	fingerprint string
	checks      []Check
	app         *DiagnosticsApp
	invite      string
}

type diagInput struct {
	setup      BotSetup
	oauth      auth.DiscordOAuthConfig
	oauthKnown bool
	publicKey  string
	bot        *discordbot.Client
	base       string
	endpoint   string
	reg        *Registration
	linkGuilds []string
	parties    bool
}

func (s *Service) diagInput(ctx context.Context) diagInput {
	in := diagInput{setup: s.setup(), bot: s.bot(), base: s.publicBase(ctx), endpoint: s.endpointURL(ctx), reg: s.registration(ctx), parties: s.partiesEnabled()}
	if s.OAuth != nil {
		in.oauth, in.oauthKnown = s.OAuth(ctx), true
	}
	if s.PublicKey != nil {
		in.publicKey = strings.TrimSpace(s.PublicKey())
	}
	if links, err := s.Links.List(ctx); err == nil {
		seen := map[string]bool{}
		for _, l := range links {
			if l.GuildID != "" && !seen[l.GuildID] {
				seen[l.GuildID] = true
				in.linkGuilds = append(in.linkGuilds, l.GuildID)
			}
		}
		sort.Strings(in.linkGuilds)
	}
	return in
}

// fingerprint identifies the configuration a remote run checked. It stays in
// memory and is never returned.
func (in diagInput) fingerprint() string {
	h := sha256.New()
	token := ""
	if in.bot != nil {
		token = in.bot.Token
	}
	reg, _ := json.Marshal(in.reg)
	for _, part := range []string{
		fmt.Sprint(in.setup.Separate), in.oauth.ClientID, in.oauth.Secret, fmt.Sprint(in.oauth.LoginEnabled),
		in.oauth.GuildID, fmt.Sprint(in.oauth.GuildEnabled || in.oauth.RoleEnabled),
		token, strings.ToLower(in.publicKey), in.endpoint, string(reg), strings.Join(in.linkGuilds, ","),
	} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Service) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	httpapi.WriteJSON(w, http.StatusOK, s.diagnostics(s.diagInput(r.Context())))
}

func (s *Service) handleRunDiagnostics(w http.ResponseWriter, r *http.Request) {
	started := s.now()
	s.diag.run.Lock()
	defer s.diag.run.Unlock()
	in := s.diagInput(r.Context())
	s.diag.mu.Lock()
	fresh := !s.diag.at.Before(started) && s.diag.fingerprint == in.fingerprint()
	s.diag.mu.Unlock()
	// A run that finished while this request waited already answers it.
	if !fresh {
		ctx, cancel := context.WithTimeout(r.Context(), diagnosticsTimeout)
		checks, app, invite := s.remoteChecks(ctx, in)
		cancel()
		sortChecks(checks)
		s.diag.mu.Lock()
		s.diag.at, s.diag.fingerprint, s.diag.checks, s.diag.app, s.diag.invite = s.now().UTC(), in.fingerprint(), checks, app, invite
		s.diag.mu.Unlock()
	}
	httpapi.WriteJSON(w, http.StatusOK, s.diagnostics(in))
}

func (s *Service) diagnostics(in diagInput) Diagnostics {
	out := Diagnostics{Mode: s.mode(), SignInClientID: in.oauth.ClientID, Checks: localChecks(in)}
	s.diag.mu.Lock()
	if !s.diag.at.IsZero() {
		at := s.diag.at
		out.CheckedAt = &at
		out.Stale = s.diag.fingerprint != in.fingerprint()
		out.Checks = append(out.Checks, s.diag.checks...)
		out.Application, out.InviteURL = s.diag.app, s.diag.invite
	}
	s.diag.mu.Unlock()
	order := map[string]int{}
	for i, g := range checkGroups {
		order[g] = i
	}
	sort.SliceStable(out.Checks, func(i, j int) bool { return order[out.Checks[i].Group] < order[out.Checks[j].Group] })
	out.Status = overallStatus(out.Checks)
	return out
}

func overallStatus(checks []Check) string {
	status := CheckOK
	for _, c := range checks {
		switch c.Status {
		case CheckError:
			return CheckError
		case CheckWarn:
			status = CheckWarn
		}
	}
	return status
}

func sourceText(source string) string {
	if source == "environment" {
		return "Set from the environment."
	}
	return "Saved and encrypted."
}

func botCredentialOwner(separate bool) string {
	if separate {
		return "separate bot application"
	}
	return "sign-in application"
}

// localChecks read configuration only and never contact Discord.
func localChecks(in diagInput) []Check {
	var out []Check
	add := func(c Check) { out = append(out, c) }
	o := in.oauth
	owner := botCredentialOwner(in.setup.Separate)

	if in.setup.Separate {
		add(Check{ID: "mode", Group: "configuration", Label: "Configuration mode", Status: CheckInfo,
			Detail: "Separate bot. Sign-in and registration use the sign-in application; the bot uses its own application's token and public key."})
	} else {
		add(Check{ID: "mode", Group: "configuration", Label: "Configuration mode", Status: CheckInfo,
			Detail: "Shared application. Sign-in, registration, the bot and slash commands all use one Discord application."})
	}
	if in.oauthKnown {
		missing := CheckWarn
		if o.LoginEnabled {
			missing = CheckError
		}
		switch {
		case o.ClientID == "":
			add(Check{ID: "client_id", Group: "configuration", Label: "Client ID", Status: missing,
				Detail: "Not set. Copy the Application ID from General Information in the Discord Developer Portal.", Action: &CheckAction{ID: ActionEditAuth, Label: "Enter client ID"}})
		case !discordbot.ValidSnowflake(o.ClientID):
			add(Check{ID: "client_id", Group: "configuration", Label: "Client ID", Status: CheckError,
				Detail: "Not a Discord ID. Use the numeric Application ID from General Information.", Action: &CheckAction{ID: ActionEditAuth, Label: "Fix client ID"}})
		default:
			add(Check{ID: "client_id", Group: "configuration", Label: "Client ID", Status: CheckOK, Detail: o.ClientID})
		}
		if o.ClientSecretSet {
			add(Check{ID: "client_secret", Group: "configuration", Label: "Client secret", Status: CheckOK, Detail: "Saved and encrypted."})
		} else {
			add(Check{ID: "client_secret", Group: "configuration", Label: "Client secret", Status: missing,
				Detail: "Not set. Copy it from the OAuth2 page in the Developer Portal.", Action: &CheckAction{ID: ActionEditAuth, Label: "Enter client secret"}})
		}
	}
	if in.bot != nil {
		add(Check{ID: "bot_token", Group: "configuration", Label: "Bot token (" + owner + ")", Status: CheckOK, Detail: sourceText(in.setup.TokenSource)})
	} else {
		add(Check{ID: "bot_token", Group: "configuration", Label: "Bot token (" + owner + ")", Status: CheckWarn,
			Detail: "Not set. Party invites and slash commands are off until a bot token is saved.", Action: &CheckAction{ID: ActionEditBot, Label: "Add bot token"}})
	}
	switch {
	case in.publicKey == "":
		add(Check{ID: "public_key", Group: "configuration", Label: "Public key (" + owner + ")", Status: CheckWarn,
			Detail: "Not set. Discord interactions cannot be verified. Registering commands saves it automatically.", Action: &CheckAction{ID: ActionEditBot, Label: "Add public key"}})
	case !discordbot.ValidPublicKey(in.publicKey):
		add(Check{ID: "public_key", Group: "configuration", Label: "Public key (" + owner + ")", Status: CheckError,
			Detail: "Not a valid Ed25519 public key. It must be the 64 hexadecimal characters from General Information.", Action: &CheckAction{ID: ActionEditBot, Label: "Fix public key"}})
	default:
		add(Check{ID: "public_key", Group: "configuration", Label: "Public key (" + owner + ")", Status: CheckOK, Detail: sourceText(in.setup.PublicKeySource)})
	}

	if in.oauthKnown {
		switch {
		case o.LoginEnabled && o.Ready():
			add(Check{ID: "oauth_login", Group: "oauth", Label: "Discord sign-in", Status: CheckOK, Detail: "On. Local username and password sign-in is off."})
		case o.LoginEnabled:
			add(Check{ID: "oauth_login", Group: "oauth", Label: "Discord sign-in", Status: CheckError,
				Detail: "On, but the client ID or secret is missing, so every Discord sign-in is refused.", Action: &CheckAction{ID: ActionEditAuth, Label: "Complete credentials"}})
		default:
			add(Check{ID: "oauth_login", Group: "oauth", Label: "Discord sign-in", Status: CheckInfo, Detail: "Off. Accounts sign in with a username and password."})
		}
		switch {
		case in.base == "":
			add(Check{ID: "oauth_redirect", Group: "oauth", Label: "Redirect URL", Status: CheckWarn,
				Detail: "No public URL is set, so the redirect URL follows whichever host the browser used. Set the public URL so it stays stable.", Action: &CheckAction{ID: ActionSettings, Label: "Open settings"}})
		case !strings.HasPrefix(in.base, "https://"):
			add(Check{ID: "oauth_redirect", Group: "oauth", Label: "Redirect URL", Status: CheckWarn,
				Detail: "The public URL uses http. Use https so sign-in is protected in transit, and list " + in.base + "/api/v1/auth/discord/callback under OAuth2 Redirects.", Action: &CheckAction{ID: ActionSettings, Label: "Open settings"}})
		default:
			add(Check{ID: "oauth_redirect", Group: "oauth", Label: "Redirect URL", Status: CheckOK,
				Detail: "List " + in.base + "/api/v1/auth/discord/callback under OAuth2 Redirects in the Developer Portal."})
		}
		add(registrationCheck(o))
	}

	switch {
	case in.reg == nil && in.bot == nil:
		add(Check{ID: "commands_registered", Group: "commands", Label: "Command registration", Status: CheckSkipped, Detail: "Needs a bot token."})
	case in.reg == nil:
		add(Check{ID: "commands_registered", Group: "commands", Label: "Command registration", Status: CheckWarn,
			Detail: "Slash commands have not been registered with Discord yet.", Action: &CheckAction{ID: ActionRegister, Label: "Register commands"}})
	default:
		scope := "every server"
		if in.reg.GuildID != "" {
			scope = "server " + in.reg.GuildID
		}
		add(Check{ID: "commands_registered", Group: "commands", Label: "Command registration", Status: CheckOK,
			Detail: fmt.Sprintf("%d command(s) registered for %s on %s.", in.reg.Commands, scope, in.reg.RegisteredAt.UTC().Format("2 Jan 2006 15:04 MST"))})
	}

	switch {
	case in.endpoint == "":
		add(Check{ID: "endpoint_url", Group: "interactions", Label: "Interactions endpoint", Status: CheckError,
			Detail: "No public URL is set, so Discord has nowhere to send slash commands.", Action: &CheckAction{ID: ActionSettings, Label: "Set public URL"}})
	case !strings.HasPrefix(in.endpoint, "https://"):
		add(Check{ID: "endpoint_url", Group: "interactions", Label: "Interactions endpoint", Status: CheckError,
			Detail: "Discord requires an https interactions endpoint. Change the public URL to https.", Action: &CheckAction{ID: ActionSettings, Label: "Open settings"}})
	default:
		add(Check{ID: "endpoint_url", Group: "interactions", Label: "Interactions endpoint", Status: CheckOK, Detail: in.endpoint})
	}
	if in.parties {
		add(Check{ID: "parties", Group: "interactions", Label: "Watch parties", Status: CheckOK, Detail: "On. /party commands can create and control parties."})
	} else {
		add(Check{ID: "parties", Group: "interactions", Label: "Watch parties", Status: CheckWarn, Detail: "Off. /party commands reply that watch parties are turned off.", Action: &CheckAction{ID: ActionSettings, Label: "Open settings"}})
	}
	return out
}

func registrationCheck(o auth.DiscordOAuthConfig) Check {
	c := Check{ID: "registration", Group: "oauth", Label: "Registration restrictions"}
	edit := &CheckAction{ID: ActionEditRegister, Label: "Edit restrictions"}
	switch {
	case !o.RegistrationEnabled:
		c.Status, c.Detail = CheckInfo, "New sign-ups with Discord are off. Linked accounts and listed administrators can still sign in."
	case (o.GuildEnabled || o.RoleEnabled) && !discordbot.ValidSnowflake(o.GuildID):
		c.Status, c.Detail, c.Action = CheckError, "A server is required, but its server ID is missing or invalid, so every new sign-up is refused.", edit
	case o.RoleEnabled && !discordbot.ValidSnowflake(o.RoleID):
		c.Status, c.Detail, c.Action = CheckError, "A role is required, but its role ID is missing or invalid, so every new sign-up is refused.", edit
	case o.RoleEnabled:
		c.Status, c.Detail = CheckOK, "New users must be in server "+o.GuildID+" with role "+o.RoleID+". Checked with the person's own Discord sign-in."
	case o.GuildEnabled:
		c.Status, c.Detail = CheckOK, "New users must be in server "+o.GuildID+". Checked with the person's own Discord sign-in."
	default:
		c.Status, c.Detail = CheckInfo, "Anyone with a Discord account can register."
	}
	return c
}

func inviteURL(appID, guildID string) string {
	q := url.Values{"client_id": {appID}, "scope": {"bot applications.commands"}, "permissions": {fmt.Sprint(invitePermissions)}}
	if guildID != "" {
		q.Set("guild_id", guildID)
		q.Set("disable_guild_select", "true")
	}
	return "https://discord.com/oauth2/authorize?" + q.Encode()
}

// discordProblem turns a Discord request failure into a check status and an
// explanation that contains no credentials.
func discordProblem(err error, rejected string) (string, string) {
	var apiErr *discordbot.APIError
	switch {
	case errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized:
		return CheckError, rejected
	case errors.As(err, &apiErr) && apiErr.Status == http.StatusTooManyRequests:
		return CheckWarn, "Discord is rate limiting requests. Try again in a minute."
	case errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden:
		return CheckError, "Discord refused access (" + apiErr.Error() + ")."
	case errors.As(err, &apiErr):
		return CheckWarn, "Discord returned an error: " + apiErr.Error() + "."
	case errors.Is(err, context.DeadlineExceeded):
		return CheckWarn, "Discord did not answer in time. Check this server's internet access and try again."
	default:
		return CheckWarn, "Discord could not be reached. Check this server's internet access and DNS."
	}
}

// remoteChecks contact Discord: at most one OAuth token request and three
// bot API requests.
func (s *Service) remoteChecks(ctx context.Context, in diagInput) ([]Check, *DiagnosticsApp, string) {
	var (
		mu  sync.Mutex
		out []Check
		wg  sync.WaitGroup
	)
	add := func(c Check) {
		c.Remote = true
		mu.Lock()
		out = append(out, c)
		mu.Unlock()
	}
	o := in.oauth
	if in.oauthKnown {
		if discordbot.ValidSnowflake(o.ClientID) && o.ClientSecretSet {
			wg.Add(1)
			go func() {
				defer wg.Done()
				add(s.oauthCheck(ctx, o))
			}()
		} else {
			add(Check{ID: "oauth_client", Group: "oauth", Label: "Client credentials", Status: CheckSkipped, Detail: "Needs a valid client ID and secret."})
		}
	}

	needsBot := func(id, group, label string) {
		add(Check{ID: id, Group: group, Label: label, Status: CheckSkipped, Detail: "Needs a working bot token."})
	}
	skipBotChecks := func() {
		needsBot("bot_application", "bot", "Application")
		needsBot("bot_guilds", "servers", "Server membership")
		needsBot("commands_remote", "commands", "Commands in Discord")
		needsBot("public_key_match", "interactions", "Public key")
		needsBot("endpoint_discord", "interactions", "Endpoint in Discord")
	}
	if in.bot == nil {
		add(Check{ID: "bot_api", Group: "bot", Label: "Bot connection", Status: CheckSkipped, Detail: "No bot token is set."})
		skipBotChecks()
		wg.Wait()
		return out, nil, ""
	}
	app, err := in.bot.CurrentApplication(ctx)
	if err != nil {
		status, detail := discordProblem(err, "Discord rejected the bot token. It may have been reset or revoked; copy a new token from the Bot page of the "+botCredentialOwner(in.setup.Separate)+" and save it.")
		s.Log.Warn("discord diagnostics", "category", "discord", "check", "bot_api", "err", err)
		add(Check{ID: "bot_api", Group: "bot", Label: "Bot connection", Status: status, Detail: detail, Action: &CheckAction{ID: ActionEditBot, Label: "Replace bot token"}})
		skipBotChecks()
		wg.Wait()
		return out, nil, ""
	}
	info := &DiagnosticsApp{ID: app.ID, Name: app.Name}
	invite := inviteURL(app.ID, "")
	add(Check{ID: "bot_api", Group: "bot", Label: "Bot connection", Status: CheckOK, Detail: fmt.Sprintf("Connected to application %s (%s).", app.Name, app.ID)})
	add(applicationCheck(in, app))
	add(publicKeyCheck(in, app))
	add(endpointCheck(in, app))

	wg.Add(2)
	go func() {
		defer wg.Done()
		guilds, err := in.bot.CurrentGuilds(ctx)
		if err != nil {
			status, detail := discordProblem(err, "Discord rejected the bot token.")
			s.Log.Warn("discord diagnostics", "category", "discord", "check", "bot_guilds", "err", err)
			add(Check{ID: "bot_guilds", Group: "servers", Label: "Server membership", Status: status, Detail: detail})
			return
		}
		for _, c := range guildChecks(in, app.ID, guilds) {
			add(c)
		}
	}()
	go func() {
		defer wg.Done()
		add(s.commandsCheck(ctx, in, app.ID))
	}()
	wg.Wait()
	return out, info, invite
}

// sortChecks orders checks by group, then by ID, so concurrent results are stable.
func sortChecks(checks []Check) {
	order := map[string]int{}
	for i, g := range checkGroups {
		order[g] = i
	}
	sort.SliceStable(checks, func(i, j int) bool {
		if order[checks[i].Group] != order[checks[j].Group] {
			return order[checks[i].Group] < order[checks[j].Group]
		}
		return checks[i].ID < checks[j].ID
	})
}

func (s *Service) oauthCheck(ctx context.Context, o auth.DiscordOAuthConfig) Check {
	c := Check{ID: "oauth_client", Group: "oauth", Label: "Client credentials"}
	err := discordbot.VerifyClientCredentials(ctx, nil, s.oauthBase, o.ClientID, o.Secret)
	switch {
	case err == nil:
		c.Status, c.Detail = CheckOK, "Discord accepted the client ID and secret."
	case errors.Is(err, discordbot.ErrInvalidClient):
		c.Status, c.Detail = CheckError, "Discord rejected the client ID or secret. The secret may have been reset in the Developer Portal; save the current one."
		c.Action = &CheckAction{ID: ActionEditAuth, Label: "Replace client secret"}
	default:
		s.Log.Warn("discord diagnostics", "category", "discord", "check", "oauth_client", "err", err)
		c.Status, c.Detail = discordProblem(err, "Discord rejected the client ID or secret.")
	}
	return c
}

func applicationCheck(in diagInput, app discordbot.Application) Check {
	c := Check{ID: "bot_application", Group: "bot", Label: "Application"}
	clientID := in.oauth.ClientID
	switch {
	case in.setup.Separate && clientID != "" && app.ID == clientID:
		c.Status, c.Detail = CheckInfo, "The separate bot token belongs to the sign-in application, so the separate configuration is not needed. You can turn it off."
	case in.setup.Separate:
		c.Status, c.Detail = CheckOK, fmt.Sprintf("The bot uses its own application %s (%s), separate from sign-in.", app.Name, app.ID)
	case !in.oauthKnown || clientID == "":
		c.Status, c.Detail = CheckInfo, fmt.Sprintf("The bot uses application %s (%s). Set the client ID of the same application for sign-in.", app.Name, app.ID)
	case app.ID != clientID:
		c.Status = CheckWarn
		c.Detail = fmt.Sprintf("The bot token belongs to application %s (%s), but sign-in uses client ID %s. Save the bot token of the sign-in application, or turn on Use separate Discord bot configuration.", app.Name, app.ID, clientID)
		c.Action = &CheckAction{ID: ActionEditBot, Label: "Review bot settings"}
	default:
		c.Status, c.Detail = CheckOK, "The bot and sign-in use the same application."
	}
	return c
}

func publicKeyCheck(in diagInput, app discordbot.Application) Check {
	c := Check{ID: "public_key_match", Group: "interactions", Label: "Public key"}
	switch {
	case in.publicKey == "":
		c.Status, c.Detail = CheckWarn, "No public key is saved, so Discord's requests are rejected. Registering commands saves it automatically."
		c.Action = &CheckAction{ID: ActionRegister, Label: "Register commands"}
	case app.VerifyKey == "":
		c.Status, c.Detail = CheckInfo, "Discord did not report the application's public key; compare it manually with General Information."
	case !strings.EqualFold(in.publicKey, app.VerifyKey):
		c.Status = CheckError
		c.Detail = fmt.Sprintf("The saved public key does not belong to application %s, so Discord's requests are rejected. Copy the public key from that application's General Information page.", app.Name)
		c.Action = &CheckAction{ID: ActionEditBot, Label: "Replace public key"}
	default:
		c.Status, c.Detail = CheckOK, "Matches the bot's application."
	}
	return c
}

func endpointCheck(in diagInput, app discordbot.Application) Check {
	c := Check{ID: "endpoint_discord", Group: "interactions", Label: "Endpoint in Discord"}
	current := strings.TrimRight(strings.TrimSpace(app.InteractionsEndpointURL), "/")
	var fix *CheckAction
	if strings.HasPrefix(in.endpoint, "https://") && discordbot.ValidPublicKey(in.publicKey) {
		fix = &CheckAction{ID: ActionSetEndpoint, Label: "Set endpoint in Discord"}
	}
	switch {
	case current == "":
		c.Status, c.Detail, c.Action = CheckWarn, "No interactions endpoint is set in Discord, so slash commands get no reply.", fix
	case in.endpoint != "" && strings.EqualFold(current, in.endpoint):
		c.Status, c.Detail = CheckOK, "Discord sends interactions to this server."
	default:
		c.Status, c.Detail, c.Action = CheckWarn, "Discord sends interactions to "+current+", not this server.", fix
	}
	return c
}

type guildRole struct {
	id    string
	roles []string
	// need is the status when the bot is missing from the server.
	need string
}

func relevantGuilds(in diagInput) []guildRole {
	var out []guildRole
	idx := map[string]int{}
	note := func(id, role, need string) {
		if !discordbot.ValidSnowflake(id) {
			return
		}
		if i, ok := idx[id]; ok {
			out[i].roles = append(out[i].roles, role)
			if rank(need) > rank(out[i].need) {
				out[i].need = need
			}
			return
		}
		idx[id] = len(out)
		out = append(out, guildRole{id: id, roles: []string{role}, need: need})
	}
	if in.reg != nil && in.reg.GuildID != "" {
		note(in.reg.GuildID, "commands registered here", CheckError)
	}
	if in.oauth.RegistrationEnabled && (in.oauth.GuildEnabled || in.oauth.RoleEnabled) {
		note(in.oauth.GuildID, "registration server", CheckInfo)
	}
	for _, id := range in.linkGuilds {
		if len(out) >= 10 {
			break
		}
		note(id, "linked channels", CheckWarn)
	}
	return out
}

func rank(status string) int {
	switch status {
	case CheckError:
		return 3
	case CheckWarn:
		return 2
	case CheckInfo:
		return 1
	}
	return 0
}

func guildChecks(in diagInput, appID string, guilds []discordbot.Guild) []Check {
	var out []Check
	truncated := ""
	if len(guilds) >= 200 {
		truncated = " Only the first 200 servers were checked."
	}
	if len(guilds) == 0 {
		out = append(out, Check{ID: "bot_guilds", Group: "servers", Label: "Server membership", Status: CheckWarn,
			Detail: "The bot is not in any Discord server. Invite it so members can use slash commands and receive party invites.",
			Action: &CheckAction{ID: ActionInviteBot, Label: "Invite the bot", URL: inviteURL(appID, "")}})
	} else {
		var lacking []string
		for _, g := range guilds {
			if !g.Has(invitePermissions) {
				lacking = append(lacking, g.Name)
			}
		}
		c := Check{ID: "bot_guilds", Group: "servers", Label: "Server membership", Status: CheckOK,
			Detail: fmt.Sprintf("The bot is in %d server(s) and can view channels and send messages in all of them.%s", len(guilds), truncated)}
		if len(lacking) > 0 {
			shown := lacking
			if len(shown) > 5 {
				shown = shown[:5]
			}
			c.Status = CheckWarn
			c.Detail = fmt.Sprintf("The bot is in %d server(s), but lacks View Channel or Send Messages in %d: %s. Party invites cannot be posted there.%s",
				len(guilds), len(lacking), strings.Join(shown, ", "), truncated)
			c.Action = &CheckAction{ID: ActionInviteBot, Label: "Grant permissions", URL: inviteURL(appID, "")}
		}
		out = append(out, c)
	}
	byID := map[string]discordbot.Guild{}
	for _, g := range guilds {
		byID[g.ID] = g
	}
	for _, rg := range relevantGuilds(in) {
		c := Check{ID: "guild:" + rg.id, Group: "servers", Label: "Server " + rg.id + " (" + strings.Join(rg.roles, ", ") + ")"}
		g, ok := byID[rg.id]
		switch {
		case !ok:
			c.Status = rg.need
			c.Detail = "The bot is not a member of this server." + truncated
			switch rg.need {
			case CheckError:
				c.Detail += " The commands registered here cannot be used until it is invited."
			case CheckWarn:
				c.Detail += " Invites to its linked channels cannot be posted."
			default:
				c.Detail += " Registration checks do not need the bot, but slash commands are not available there."
			}
			c.Action = &CheckAction{ID: ActionInviteBot, Label: "Invite to this server", URL: inviteURL(appID, rg.id)}
		case !g.Has(invitePermissions):
			c.Label = "Server " + g.Name + " (" + strings.Join(rg.roles, ", ") + ")"
			c.Status = CheckWarn
			c.Detail = "The bot is a member, but lacks View Channel or Send Messages, so party invites cannot be posted. Channel permission overrides can also block it."
			c.Action = &CheckAction{ID: ActionInviteBot, Label: "Grant permissions", URL: inviteURL(appID, rg.id)}
		default:
			c.Label = "Server " + g.Name + " (" + strings.Join(rg.roles, ", ") + ")"
			c.Status, c.Detail = CheckOK, "The bot is a member and can view channels and send messages. Channel permission overrides can still block individual channels."
		}
		out = append(out, c)
	}
	return out
}

func (s *Service) commandsCheck(ctx context.Context, in diagInput, appID string) Check {
	c := Check{ID: "commands_remote", Group: "commands", Label: "Commands in Discord"}
	register := &CheckAction{ID: ActionRegister, Label: "Register commands"}
	if in.reg == nil {
		c.Status, c.Detail, c.Action = CheckSkipped, "Register the commands first.", register
		return c
	}
	if in.reg.ApplicationID != appID {
		c.Status, c.Action = CheckError, register
		c.Detail = fmt.Sprintf("The commands were registered for application %s, but the bot now uses %s. Register them again.", in.reg.ApplicationID, appID)
		return c
	}
	cmds, err := in.bot.ListCommands(ctx, appID, in.reg.GuildID)
	if err != nil {
		var apiErr *discordbot.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden {
			c.Status = CheckError
			c.Detail = "Discord refused access to the server's commands. The bot may have been removed from server " + in.reg.GuildID + ", or was invited without the applications.commands scope."
			c.Action = &CheckAction{ID: ActionInviteBot, Label: "Invite to this server", URL: inviteURL(appID, in.reg.GuildID)}
			return c
		}
		s.Log.Warn("discord diagnostics", "category", "discord", "check", "commands_remote", "err", err)
		c.Status, c.Detail = discordProblem(err, "Discord rejected the bot token.")
		return c
	}
	have := map[string]bool{}
	for _, cmd := range cmds {
		have[cmd.Name] = true
	}
	var missing []string
	for _, want := range Commands() {
		if !have[want.Name] {
			missing = append(missing, "/"+want.Name)
		}
	}
	if len(missing) > 0 {
		c.Status, c.Action = CheckWarn, register
		c.Detail = "Discord does not list " + strings.Join(missing, ", ") + ". Register the commands again."
		return c
	}
	c.Status, c.Detail = CheckOK, fmt.Sprintf("Discord lists %d command(s) for this application.", len(cmds))
	return c
}
