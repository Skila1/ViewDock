package interactions

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/discordbot"
)

const (
	activityApp      = "400000000000000001"
	activityInstance = "i-1234-gc-5678"
	adminDiscord     = "100000000000000009"
)

func TestWithEntryPointsKeepsOnlyEntryPoints(t *testing.T) {
	want := []discordbot.Command{{Name: "party"}}
	existing := []discordbot.Command{
		{ID: "1", Name: "party", Type: 1},
		{ID: "2", Name: "launch", Type: discordbot.CommandEntryPoint, Handler: 2, IntegrationTypes: []int{0, 1}},
	}
	got := withEntryPoints(want, existing, true)
	if len(got) != 2 || got[1].ID != "2" || got[1].Handler != 2 || len(want) != 1 {
		t.Fatalf("commands = %+v (want untouched: %+v)", got, want)
	}
}

type activityHarness struct {
	*harness
	enabled  bool
	separate bool
	calls    int
	// users is who Discord reports in the Activity instance.
	users []string
}

func newActivityHarness(t *testing.T) *activityHarness {
	h := &activityHarness{harness: newHarness(t), enabled: true, users: []string{hostDiscord, guestDiscord, partyOnlyID, adminDiscord}}
	discord := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.calls++
		if r.Header.Get("Authorization") != "Bot test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/applications/"+activityApp+"/activity-instances/"+activityInstance {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":10000,"message":"Unknown"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"instance_id": activityInstance,
			"location":    map[string]any{"kind": "gc", "channel_id": voiceChannel, "guild_id": guildID},
			"users":       h.users,
		})
	}))
	t.Cleanup(discord.Close)
	bot := discordbot.New("test-token")
	bot.BaseURL = discord.URL
	links := map[string]string{"u-host": hostDiscord, "u-guest": guestDiscord, "u-po": partyOnlyID, "u-stranger": strangerID, "u-admin": adminDiscord}
	h.svc.Bot = func() *discordbot.Client { return bot }
	h.svc.OAuth = func(context.Context) auth.DiscordOAuthConfig { return auth.DiscordOAuthConfig{ClientID: activityApp} }
	h.svc.ActivityEnabled = func() bool { return h.enabled }
	h.svc.Setup = func() BotSetup { return BotSetup{Separate: h.separate} }
	h.svc.DiscordUserID = func(_ context.Context, userID string) string { return links[userID] }
	return h
}

func (h *activityHarness) room(p *auth.Principal, body map[string]any) (int, map[string]any) {
	h.t.Helper()
	r := chi.NewRouter()
	h.svc.ActivityRoutes(r)
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/discord/activity/room", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if p != nil {
		req = req.WithContext(auth.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func user(id string, partyOnly bool) *auth.Principal {
	return &auth.Principal{Kind: auth.KindUser, UserID: id, PartyOnly: partyOnly}
}

func TestActivityRoomCreatesAndSharesTheChannelParty(t *testing.T) {
	h := newActivityHarness(t)
	host, guest := user("u-host", false), user("u-guest", false)

	code, out := h.room(host, map[string]any{"instance_id": activityInstance})
	if code != http.StatusOK || out["room"] != nil || out["can_create"] != true {
		t.Fatalf("empty channel: %d %v", code, out)
	}
	code, out = h.room(host, map[string]any{"instance_id": activityInstance, "item_kind": "movie", "item_id": "m1"})
	room, _ := out["room"].(map[string]any)
	if code != http.StatusOK || room["created"] != true || room["title"] != "Big Buck Bunny" || room["invite_code"] == "" {
		t.Fatalf("create: %d %v", code, out)
	}
	link, err := h.svc.Links.Get(context.Background(), voiceChannel)
	if err != nil || link.Kind != LinkVoice || link.RoomID != room["room_id"] || link.GuildID != guildID {
		t.Fatalf("link = %+v err %v", link, err)
	}

	code, out = h.room(guest, map[string]any{"instance_id": activityInstance, "item_kind": "movie", "item_id": "m1"})
	joined, _ := out["room"].(map[string]any)
	if code != http.StatusOK || joined["invite_code"] != room["invite_code"] || joined["created"] != false {
		t.Fatalf("second participant must join the linked party: %d %v", code, out)
	}
	if len(h.parties.rooms) != 1 {
		t.Fatalf("rooms = %d, want 1", len(h.parties.rooms))
	}
	if h.audit.actions[len(h.audit.actions)-1] != "discord.activity_party_create" {
		t.Fatalf("audit = %v", h.audit.actions)
	}
}

func TestActivityRoomDoesNotResumeAnAbandonedParty(t *testing.T) {
	h := newActivityHarness(t)
	host := user("u-host", false)
	_, out := h.room(host, map[string]any{"instance_id": activityInstance, "item_kind": "movie", "item_id": "m1"})
	first, _ := out["room"].(map[string]any)
	h.parties.rooms[first["room_id"].(string)].empty = true

	code, out := h.room(host, map[string]any{"instance_id": activityInstance})
	if code != http.StatusOK || out["room"] != nil || out["can_create"] != true {
		t.Fatalf("abandoned party must not be resumed: %d %v", code, out)
	}
	_, out = h.room(host, map[string]any{"instance_id": activityInstance, "item_kind": "movie", "item_id": "m1"})
	second, _ := out["room"].(map[string]any)
	if second["created"] != true || second["room_id"] == first["room_id"] {
		t.Fatalf("a new party must replace the abandoned one: %v", out)
	}
	link, err := h.svc.Links.Get(context.Background(), voiceChannel)
	if err != nil || link.RoomID != second["room_id"] {
		t.Fatalf("link = %+v err %v", link, err)
	}
}

func TestActivityRoomRefusesOutsiders(t *testing.T) {
	h := newActivityHarness(t)
	cases := []struct {
		name string
		p    *auth.Principal
		body map[string]any
		want int
	}{
		{"signed out", nil, map[string]any{"instance_id": activityInstance}, http.StatusUnauthorized},
		{"not in the instance", user("u-stranger", false), map[string]any{"instance_id": activityInstance}, http.StatusForbidden},
		{"no linked Discord account", user("u-unlinked", false), map[string]any{"instance_id": activityInstance}, http.StatusForbidden},
		{"unknown instance", user("u-host", false), map[string]any{"instance_id": "i-9999"}, http.StatusNotFound},
		{"malformed instance", user("u-host", false), map[string]any{"instance_id": "../../users/@me"}, http.StatusBadRequest},
		{"party-only cannot start", user("u-po", true), map[string]any{"instance_id": activityInstance, "item_kind": "movie", "item_id": "m1"}, http.StatusForbidden},
		{"title not allowed", user("u-host", false), map[string]any{"instance_id": activityInstance, "item_kind": "movie", "item_id": "nope"}, http.StatusForbidden},
		{"series is not playable", user("u-host", false), map[string]any{"instance_id": activityInstance, "item_kind": "series", "item_id": "s1"}, http.StatusBadRequest},
	}
	for _, c := range cases {
		if code, out := h.room(c.p, c.body); code != c.want {
			t.Errorf("%s: status %d, want %d (%v)", c.name, code, c.want, out)
		}
	}
	if len(h.parties.rooms) != 0 {
		t.Fatalf("refused requests created rooms: %v", h.parties.rooms)
	}
}

func TestActivityRoomNeedsTheActivityAndSharedBot(t *testing.T) {
	h := newActivityHarness(t)
	h.enabled = false
	if code, _ := h.room(user("u-host", false), map[string]any{"instance_id": activityInstance}); code != http.StatusServiceUnavailable {
		t.Fatalf("disabled: %d", code)
	}
	h.enabled, h.separate = true, true
	if code, _ := h.room(user("u-host", false), map[string]any{"instance_id": activityInstance}); code != http.StatusServiceUnavailable {
		t.Fatalf("separate bot: %d", code)
	}
	if h.calls != 0 {
		t.Fatalf("Discord was called %d times while the Activity was unusable", h.calls)
	}
}

func TestActivityDiagnostics(t *testing.T) {
	in := diagInput{activity: true, parties: true, base: "https://vd.example", bot: discordbot.New("x"),
		oauth: auth.DiscordOAuthConfig{ClientID: activityApp, ClientSecretSet: true}}
	checks := activityChecks(in)
	if len(checks) != 2 || checks[0].Status != CheckOK || checks[1].Status != CheckInfo {
		t.Fatalf("ready = %+v", checks)
	}
	in.setup.Separate = true
	if c := activityChecks(in)[0]; c.Status != CheckError {
		t.Fatalf("separate = %+v", c)
	}
	in.setup.Separate, in.base = false, "http://vd.example"
	if c := activityChecks(in)[1]; c.Status != CheckError {
		t.Fatalf("http mapping = %+v", c)
	}
	if c := activityChecks(diagInput{})[0]; c.Status != CheckInfo || len(activityChecks(diagInput{})) != 1 {
		t.Fatalf("off = %+v", c)
	}
	if c := activityAppCheck(discordbot.Application{Name: "VD", Flags: discordbot.ApplicationFlagEmbedded}); c.Status != CheckOK {
		t.Fatalf("embedded = %+v", c)
	}
	if c := activityAppCheck(discordbot.Application{Name: "VD"}); c.Status != CheckWarn {
		t.Fatalf("not embedded = %+v", c)
	}
}

func named(id, name string, admin bool) *auth.Principal {
	return &auth.Principal{Kind: auth.KindUser, UserID: id, DisplayName: name, IsAdmin: admin}
}

func hostOf(out map[string]any) (string, bool) {
	h, _ := out["host"].(map[string]any)
	name, _ := h["name"].(string)
	you, _ := h["you"].(bool)
	return name, you
}

func TestActivityFirstLauncherHostsAndPicks(t *testing.T) {
	h := newActivityHarness(t)
	host, guest := named("u-host", "Hosty", false), named("u-guest", "Guesty", false)

	_, out := h.room(host, map[string]any{"instance_id": activityInstance})
	if name, you := hostOf(out); name != "Hosty" || !you || out["can_create"] != true {
		t.Fatalf("first launcher: %v", out)
	}
	_, out = h.room(guest, map[string]any{"instance_id": activityInstance})
	if name, you := hostOf(out); name != "Hosty" || you || out["can_create"] != false || out["room"] != nil {
		t.Fatalf("second launcher must wait for the host: %v", out)
	}
	if code, out := h.room(guest, map[string]any{"instance_id": activityInstance, "item_kind": "movie", "item_id": "m1"}); code != http.StatusForbidden || out["code"] != "not_host" {
		t.Fatalf("a guest must not start the channel's party: %d %v", code, out)
	}
	if len(h.parties.rooms) != 0 {
		t.Fatal("no party yet")
	}
	_, out = h.room(host, map[string]any{"instance_id": activityInstance, "item_kind": "movie", "item_id": "m1"})
	room, _ := out["room"].(map[string]any)
	_, out = h.room(guest, map[string]any{"instance_id": activityInstance})
	joined, _ := out["room"].(map[string]any)
	if joined["invite_code"] != room["invite_code"] {
		t.Fatalf("guest must join the host's party: %v", out)
	}
}

func TestActivityHostLeavingTheCallHandsHostingOn(t *testing.T) {
	h := newActivityHarness(t)
	h.room(named("u-host", "Hosty", false), map[string]any{"instance_id": activityInstance})
	h.users = []string{guestDiscord}
	_, out := h.room(named("u-guest", "Guesty", false), map[string]any{"instance_id": activityInstance})
	if name, you := hostOf(out); name != "Guesty" || !you || out["can_create"] != true {
		t.Fatalf("host left the call, the next launcher hosts: %v", out)
	}
}

func TestActivityPartyOnlyLauncherDoesNotHost(t *testing.T) {
	h := newActivityHarness(t)
	_, out := h.room(user("u-po", true), map[string]any{"instance_id": activityInstance})
	if out["host"] != nil || out["can_create"] != false {
		t.Fatalf("party-only launcher: %v", out)
	}
	_, out = h.room(named("u-guest", "Guesty", false), map[string]any{"instance_id": activityInstance})
	if _, you := hostOf(out); !you {
		t.Fatalf("the first launcher who can start a party hosts: %v", out)
	}
}

func TestActivityAdminTakesOverBeforeAPartyStarts(t *testing.T) {
	h := newActivityHarness(t)
	host, admin := named("u-host", "Hosty", false), named("u-admin", "Boss", true)
	h.room(host, map[string]any{"instance_id": activityInstance})
	_, out := h.room(admin, map[string]any{"instance_id": activityInstance})
	if name, you := hostOf(out); name != "Boss" || !you || out["can_create"] != true {
		t.Fatalf("admin joining later must host: %v", out)
	}
	_, out = h.room(host, map[string]any{"instance_id": activityInstance})
	if name, you := hostOf(out); name != "Boss" || you || out["can_create"] != false {
		t.Fatalf("the first launcher now waits for the admin: %v", out)
	}
	// A second administrator does not take over from the first.
	h.svc.DiscordUserID = func(_ context.Context, userID string) string {
		return map[string]string{"u-admin": adminDiscord, "u-admin2": guestDiscord}[userID]
	}
	_, out = h.room(named("u-admin2", "Other admin", true), map[string]any{"instance_id": activityInstance})
	if name, _ := hostOf(out); name != "Boss" {
		t.Fatalf("an administrator must not take over from another: %v", out)
	}
}

func TestActivityAdminTakesOverARunningParty(t *testing.T) {
	h := newActivityHarness(t)
	host, admin := named("u-host", "Hosty", false), named("u-admin", "Boss", true)
	_, out := h.room(host, map[string]any{"instance_id": activityInstance, "item_kind": "movie", "item_id": "m1"})
	room, _ := out["room"].(map[string]any)
	roomID := room["room_id"].(string)

	_, out = h.room(admin, map[string]any{"instance_id": activityInstance})
	joined, _ := out["room"].(map[string]any)
	if joined["room_id"] != roomID {
		t.Fatalf("admin joins the running party: %v", out)
	}
	if len(h.parties.handovers) != 1 || h.parties.handovers[0] != admin.ID()+":"+roomID || h.parties.rooms[roomID].host != admin.ID() {
		t.Fatalf("handovers = %v", h.parties.handovers)
	}
	if h.audit.actions[len(h.audit.actions)-1] != "discord.activity_party_takeover" {
		t.Fatalf("audit = %v", h.audit.actions)
	}
	// Opening the Activity again does not hand over again.
	h.room(admin, map[string]any{"instance_id": activityInstance})
	h.room(host, map[string]any{"instance_id": activityInstance})
	if len(h.parties.handovers) != 1 {
		t.Fatalf("handovers = %v", h.parties.handovers)
	}
}
