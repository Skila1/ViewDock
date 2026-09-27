package interactions

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/discordbot"
)

const (
	appID        = "400000000000000001"
	otherAppID   = "400000000000000002"
	botToken     = "bot-token-value-never-returned"
	clientSecret = "client-secret-value-never-returned"
)

// fakeDiscord answers the Discord API calls diagnostics make.
type fakeDiscord struct {
	mu       sync.Mutex
	requests []string
	app      discordbot.Application
	appCode  int
	guilds   []discordbot.Guild
	commands []discordbot.Command
	cmdCode  int
	oauth    int
}

func (f *fakeDiscord) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		reply := func(code int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(v)
		}
		switch {
		case r.URL.Path == "/oauth2/token":
			id, secret, ok := r.BasicAuth()
			if f.oauth != 0 {
				reply(f.oauth, map[string]string{"error": "invalid_client"})
				return
			}
			if !ok || id != appID || secret != clientSecret {
				reply(http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
				return
			}
			reply(http.StatusOK, map[string]string{"access_token": "x", "token_type": "Bearer"})
		case r.Header.Get("Authorization") != "Bot "+botToken:
			reply(http.StatusUnauthorized, map[string]any{"code": 0, "message": "401: Unauthorized"})
		case r.URL.Path == "/applications/@me":
			if f.appCode != 0 {
				reply(f.appCode, map[string]any{"message": "failed"})
				return
			}
			reply(http.StatusOK, f.app)
		case r.URL.Path == "/users/@me/guilds":
			reply(http.StatusOK, f.guilds)
		case strings.HasSuffix(r.URL.Path, "/commands"):
			if f.cmdCode != 0 {
				reply(f.cmdCode, map[string]any{"code": 50001, "message": "Missing Access"})
				return
			}
			reply(http.StatusOK, f.commands)
		default:
			t.Errorf("unexpected Discord request %s %s", r.Method, r.URL.Path)
			reply(http.StatusNotFound, map[string]any{})
		}
	})
}

func (f *fakeDiscord) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

type diagHarness struct {
	*harness
	discord *fakeDiscord
	oauth   auth.DiscordOAuthConfig
	setup   BotSetup
	key     string
}

func newDiagHarness(t *testing.T) *diagHarness {
	h := newHarness(t)
	pub := h.priv.Public().(ed25519.PublicKey)
	d := &diagHarness{harness: h, key: hex.EncodeToString(pub)}
	d.discord = &fakeDiscord{
		app:      discordbot.Application{ID: appID, Name: "ViewDock", VerifyKey: d.key, InteractionsEndpointURL: "https://vd.example" + Path},
		guilds:   []discordbot.Guild{{ID: guildID, Name: "Movie night", Permissions: "3072"}},
		commands: Commands(),
	}
	srv := httptest.NewServer(d.discord.handler(t))
	t.Cleanup(srv.Close)
	d.oauth = auth.DiscordOAuthConfig{LoginEnabled: true, ClientID: appID, Secret: clientSecret, ClientSecretSet: true, RegistrationEnabled: true}
	d.setup = BotSetup{TokenSource: "database", PublicKeySource: "database"}
	bot := discordbot.New(botToken)
	bot.BaseURL = srv.URL
	h.svc.Bot = func() *discordbot.Client { return bot }
	h.svc.PublicKey = func() string { return d.key }
	h.svc.OAuth = func(context.Context) auth.DiscordOAuthConfig { return d.oauth }
	h.svc.Setup = func() BotSetup { return d.setup }
	h.svc.oauthBase = srv.URL
	d.register(appID, "")
	return d
}

func (d *diagHarness) register(app, guild string) {
	raw, _ := json.Marshal(Registration{ApplicationID: app, GuildID: guild, Commands: 1, RegisteredAt: time.Now().UTC(), RegisteredBy: "u-admin"})
	_ = d.svc.KV.Set(context.Background(), registrationKey, string(raw))
}

func (d *diagHarness) request(method string) (Diagnostics, string) {
	d.t.Helper()
	req := httptest.NewRequest(method, "/admin/integrations/discord/diagnostics", nil)
	rec := httptest.NewRecorder()
	if method == http.MethodPost {
		d.svc.handleRunDiagnostics(rec, req)
	} else {
		d.svc.handleDiagnostics(rec, req)
	}
	if rec.Code != http.StatusOK {
		d.t.Fatalf("%s diagnostics = %d %s", method, rec.Code, rec.Body.String())
	}
	var out Diagnostics
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		d.t.Fatal(err)
	}
	return out, rec.Body.String()
}

func checkByID(t *testing.T, d Diagnostics, id string) Check {
	t.Helper()
	for _, c := range d.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no %q check in %+v", id, d.Checks)
	return Check{}
}

func assertNoSecrets(t *testing.T, body string) {
	t.Helper()
	for _, s := range []string{botToken, clientSecret} {
		if strings.Contains(body, s) {
			t.Fatalf("diagnostics exposed a credential: %s", body)
		}
	}
}

func TestDiagnosticsLoadDoesNotContactDiscord(t *testing.T) {
	d := newDiagHarness(t)
	out, body := d.request(http.MethodGet)
	if d.discord.count() != 0 {
		t.Fatalf("loading diagnostics made %d Discord requests", d.discord.count())
	}
	if out.CheckedAt != nil || out.Mode != "shared" {
		t.Fatalf("checked_at = %v, mode = %s", out.CheckedAt, out.Mode)
	}
	for _, c := range out.Checks {
		if c.Remote {
			t.Fatalf("remote check %s reported before any run", c.ID)
		}
	}
	if c := checkByID(t, out, "client_secret"); c.Status != CheckOK {
		t.Fatalf("client secret check = %+v", c)
	}
	assertNoSecrets(t, body)
}

func TestDiagnosticsHealthySharedConfiguration(t *testing.T) {
	d := newDiagHarness(t)
	out, body := d.request(http.MethodPost)
	assertNoSecrets(t, body)
	if out.Status != CheckOK {
		for _, c := range out.Checks {
			if c.Status == CheckWarn || c.Status == CheckError {
				t.Errorf("%s: %s %s", c.ID, c.Status, c.Detail)
			}
		}
		t.Fatalf("status = %s", out.Status)
	}
	if out.CheckedAt == nil || out.Stale || out.Application == nil || out.Application.ID != appID {
		t.Fatalf("run metadata = %+v", out)
	}
	for _, id := range []string{"oauth_client", "bot_api", "bot_application", "bot_guilds", "commands_remote", "public_key_match", "endpoint_discord"} {
		if c := checkByID(t, out, id); c.Status != CheckOK {
			t.Errorf("%s = %s: %s", id, c.Status, c.Detail)
		}
	}
	if n := d.discord.count(); n > 4 {
		t.Fatalf("a diagnostics run made %d Discord requests, want at most 4", n)
	}

	again, _ := d.request(http.MethodGet)
	if again.Stale || again.CheckedAt == nil {
		t.Fatalf("unchanged configuration reported stale: %+v", again)
	}
	d.oauth.Secret = "rotated"
	if changed, _ := d.request(http.MethodGet); !changed.Stale {
		t.Fatal("a configuration change must mark the last run stale")
	}
}

func TestDiagnosticsExplainFailures(t *testing.T) {
	d := newDiagHarness(t)
	d.discord.oauth = http.StatusUnauthorized
	d.discord.app.ID = otherAppID
	d.discord.app.VerifyKey = strings.Repeat("ab", 32)
	d.discord.app.InteractionsEndpointURL = "https://elsewhere.example/hook"
	d.discord.guilds = []discordbot.Guild{{ID: guildID, Name: "Movie night", Permissions: "0"}}
	d.oauth.GuildEnabled, d.oauth.GuildID = true, "200000000000000009"

	out, body := d.request(http.MethodPost)
	assertNoSecrets(t, body)
	if out.Status != CheckError {
		t.Fatalf("status = %s", out.Status)
	}
	want := map[string]string{
		"oauth_client":             CheckError,
		"bot_application":          CheckWarn,
		"public_key_match":         CheckError,
		"endpoint_discord":         CheckWarn,
		"bot_guilds":               CheckWarn,
		"commands_remote":          CheckError,
		"guild:200000000000000009": CheckInfo,
	}
	for id, status := range want {
		c := checkByID(t, out, id)
		if c.Status != status {
			t.Errorf("%s = %s (%s), want %s", id, c.Status, c.Detail, status)
		}
		if status != CheckInfo && c.Action == nil {
			t.Errorf("%s has no corrective action", id)
		}
	}
	if c := checkByID(t, out, "bot_application"); !strings.Contains(c.Detail, "Use separate Discord bot configuration") {
		t.Errorf("mismatched application must point at the separate toggle: %s", c.Detail)
	}
	if c := checkByID(t, out, "endpoint_discord"); c.Action.ID != ActionSetEndpoint {
		t.Errorf("endpoint action = %+v", c.Action)
	}
	if c := checkByID(t, out, "guild:200000000000000009"); !strings.Contains(c.Action.URL, "guild_id=200000000000000009") {
		t.Errorf("invite action = %+v", c.Action)
	}
}

func TestDiagnosticsRevokedBotTokenSkipsDependentChecks(t *testing.T) {
	d := newDiagHarness(t)
	bot := discordbot.New("revoked-token")
	bot.BaseURL = d.svc.Bot().BaseURL
	d.svc.Bot = func() *discordbot.Client { return bot }

	out, body := d.request(http.MethodPost)
	if strings.Contains(body, "revoked-token") {
		t.Fatal("diagnostics exposed the bot token")
	}
	c := checkByID(t, out, "bot_api")
	if c.Status != CheckError || !strings.Contains(c.Detail, "reset or revoked") || c.Action == nil || c.Action.ID != ActionEditBot {
		t.Fatalf("bot_api = %+v", c)
	}
	for _, id := range []string{"bot_guilds", "commands_remote", "public_key_match", "endpoint_discord"} {
		if got := checkByID(t, out, id).Status; got != CheckSkipped {
			t.Errorf("%s = %s, want skipped", id, got)
		}
	}
}

func TestDiagnosticsMissingGuildAccessForCommands(t *testing.T) {
	d := newDiagHarness(t)
	d.register(appID, guildID)
	d.discord.guilds = nil
	d.discord.cmdCode = http.StatusForbidden
	out, _ := d.request(http.MethodPost)
	if c := checkByID(t, out, "commands_remote"); c.Status != CheckError || c.Action == nil || c.Action.ID != ActionInviteBot {
		t.Fatalf("commands_remote = %+v", c)
	}
	if c := checkByID(t, out, "guild:"+guildID); c.Status != CheckError {
		t.Fatalf("command server membership = %+v", c)
	}
	if c := checkByID(t, out, "bot_guilds"); c.Status != CheckWarn {
		t.Fatalf("bot_guilds = %+v", c)
	}
}

func TestDiagnosticsSeparateMode(t *testing.T) {
	d := newDiagHarness(t)
	d.setup.Separate = true
	out, _ := d.request(http.MethodPost)
	if out.Mode != "separate" {
		t.Fatalf("mode = %s", out.Mode)
	}
	if c := checkByID(t, out, "bot_token"); !strings.Contains(c.Label, "separate bot application") {
		t.Fatalf("bot token label = %q", c.Label)
	}
	if c := checkByID(t, out, "bot_application"); c.Status != CheckInfo {
		t.Fatalf("separate bot on the sign-in application = %+v", c)
	}
	d.discord.app.ID = otherAppID
	d.register(otherAppID, "")
	out, _ = d.request(http.MethodPost)
	if c := checkByID(t, out, "bot_application"); c.Status != CheckOK {
		t.Fatalf("separate bot application = %+v", c)
	}
}

func TestDiagnosticsIncompleteCredentials(t *testing.T) {
	d := newDiagHarness(t)
	d.oauth = auth.DiscordOAuthConfig{LoginEnabled: true}
	d.svc.Bot = func() *discordbot.Client { return nil }
	d.key = ""
	_ = d.svc.KV.Set(context.Background(), registrationKey, "")

	out, _ := d.request(http.MethodPost)
	if d.discord.count() != 0 {
		t.Fatalf("incomplete credentials made %d Discord requests", d.discord.count())
	}
	for id, status := range map[string]string{
		"client_id": CheckError, "client_secret": CheckError, "oauth_login": CheckError, "bot_token": CheckWarn,
		"public_key": CheckWarn, "oauth_client": CheckSkipped, "bot_api": CheckSkipped, "commands_registered": CheckSkipped,
	} {
		if got := checkByID(t, out, id).Status; got != status {
			t.Errorf("%s = %s, want %s", id, got, status)
		}
	}
}

func TestRegisterSavesPublicKeyToActiveKey(t *testing.T) {
	d := newDiagHarness(t)
	d.key = ""
	var saved map[string]string
	d.svc.SaveConfig = func(_ context.Context, _, _ string, values map[string]string) error {
		saved = values
		return nil
	}
	d.svc.PublicKeyConfigKey = func() string { return "discord.bot.separate_public_key" }
	req := httptest.NewRequest(http.MethodPost, "/admin/integrations/discord/commands", strings.NewReader(`{}`))
	req = req.WithContext(auth.WithPrincipal(req.Context(), &auth.Principal{Kind: auth.KindUser, UserID: "u-admin"}))
	rec := httptest.NewRecorder()
	d.svc.handleRegister(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("register = %d %s", rec.Code, rec.Body.String())
	}
	if saved["discord.bot.separate_public_key"] != d.discord.app.VerifyKey || len(saved) != 1 {
		t.Fatalf("saved = %v", saved)
	}
}
