package interactions

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/db"
)

const (
	hostDiscord  = "100000000000000001"
	guestDiscord = "100000000000000002"
	strangerID   = "100000000000000003"
	partyOnlyID  = "100000000000000004"
	guildID      = "200000000000000001"
	textChannel  = "300000000000000001"
	voiceChannel = "300000000000000002"
)

type fakeAccounts map[string]*auth.Principal

func (f fakeAccounts) PrincipalForDiscord(_ context.Context, id string) (*auth.Principal, error) {
	if p, ok := f[id]; ok {
		return p, nil
	}
	return nil, auth.ErrDiscordNotLinked
}

type fakeRoom struct {
	host, kind, id, code string
	playing              bool
	pos                  int64
	// empty marks a room whose members have all disconnected.
	empty bool
}

type fakeParties struct {
	rooms     map[string]*fakeRoom
	controls  []string
	handovers []string
	n         int
}

func (f *fakeParties) Create(_ context.Context, p *auth.Principal, kind, id string) (string, string, error) {
	if id == "forbidden" {
		return "", "", ErrForbidden
	}
	f.n++
	roomID, code := fmt.Sprintf("room-%d", f.n), fmt.Sprintf("CODE%04d", f.n)
	f.rooms[roomID] = &fakeRoom{host: p.ID(), kind: kind, id: id, code: code}
	return roomID, code, nil
}

func (f *fakeParties) State(roomID string) map[string]any {
	r := f.rooms[roomID]
	if r == nil {
		return nil
	}
	return map[string]any{
		"host": r.host, "playing": r.playing, "position_ms": r.pos, "item_kind": r.kind, "item_id": r.id,
		"members": []map[string]any{
			{"id": r.host, "display_name": "Hosty", "connected": !r.empty},
			{"id": "u-guest", "display_name": "Guesty", "connected": !r.empty},
		},
	}
}

func (f *fakeParties) Control(_ context.Context, p *auth.Principal, roomID, action string) error {
	r := f.rooms[roomID]
	if r == nil {
		return ErrRoomNotFound
	}
	if r.host != p.ID() {
		return ErrNotHost
	}
	f.controls = append(f.controls, action+":"+roomID)
	r.playing = action == ActionResume
	return nil
}

func (f *fakeParties) HandOver(_ context.Context, p *auth.Principal, roomID string) error {
	r := f.rooms[roomID]
	if r == nil {
		return ErrRoomNotFound
	}
	r.host = p.ID()
	f.handovers = append(f.handovers, p.ID()+":"+roomID)
	return nil
}

func (f *fakeParties) Resolve(code string) (string, bool) {
	for id, r := range f.rooms {
		if r.code == code {
			return id, true
		}
	}
	return "", false
}

type fakeCatalog struct{}

var titles = []Title{{Kind: "movie", ID: "m1", Name: "Big Buck Bunny", Year: "2008"}, {Kind: "series", ID: "s1", Name: "Bunny Tales"}}

func (fakeCatalog) SearchTitles(_ context.Context, _ *auth.Principal, q string, limit int) ([]Title, error) {
	var out []Title
	for _, t := range titles {
		if strings.Contains(strings.ToLower(t.Name), strings.ToLower(q)) {
			out = append(out, t)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (fakeCatalog) TitleOf(_ context.Context, _ *auth.Principal, kind, id string) (string, error) {
	for _, t := range titles {
		if t.Kind == kind && t.ID == id {
			return t.Name, nil
		}
	}
	if id == "forbidden" {
		return "Forbidden", nil
	}
	return "", ErrForbidden
}

type fakeSettings map[string]string

func (f fakeSettings) Get(_ context.Context, k string) (string, error) { return f[k], nil }
func (f fakeSettings) Set(_ context.Context, k, v string) error        { f[k] = v; return nil }

type fakeAudit struct{ actions []string }

func (a *fakeAudit) Event(_ context.Context, _, action, _, _, _ string) {
	a.actions = append(a.actions, action)
}

type harness struct {
	t       *testing.T
	svc     *Service
	priv    ed25519.PrivateKey
	parties *fakeParties
	audit   *fakeAudit
	now     time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, priv: priv, parties: &fakeParties{rooms: map[string]*fakeRoom{}}, audit: &fakeAudit{}, now: time.Now()}
	kv := fakeSettings{"app.public_url": "https://vd.example"}
	h.svc = New(Deps{
		DB: sqlDB, Cfg: config.Config{}, Settings: kv, KV: kv, Audit: h.audit,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		PublicKey: func() string { return hex.EncodeToString(pub) },
		Accounts: fakeAccounts{
			hostDiscord:  {Kind: auth.KindUser, UserID: "u-host", DisplayName: "Hosty"},
			guestDiscord: {Kind: auth.KindUser, UserID: "u-guest", DisplayName: "Guesty"},
			partyOnlyID:  {Kind: auth.KindUser, UserID: "u-po", PartyOnly: true},
		},
		Parties: h.parties,
		Catalog: fakeCatalog{},
	})
	h.svc.now = func() time.Time { return h.now }
	return h
}

func (h *harness) raw(body []byte, ts string, sig string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, Path, bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.9:4444"
	req.Header.Set("X-Signature-Ed25519", sig)
	req.Header.Set("X-Signature-Timestamp", ts)
	rec := httptest.NewRecorder()
	h.svc.Handler().ServeHTTP(rec, req)
	return rec
}

func (h *harness) signed(v any) *httptest.ResponseRecorder {
	body, _ := json.Marshal(v)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	return h.raw(body, ts, hex.EncodeToString(ed25519.Sign(h.priv, append([]byte(ts), body...))))
}

type reply struct {
	Type int `json:"type"`
	Data struct {
		Content    string `json:"content"`
		Flags      int    `json:"flags"`
		Components []struct {
			Components []struct {
				Label    string `json:"label"`
				CustomID string `json:"custom_id"`
				URL      string `json:"url"`
			} `json:"components"`
		} `json:"components"`
		Choices *[]choice `json:"choices"`
	} `json:"data"`
}

func (h *harness) call(v any) reply {
	h.t.Helper()
	rec := h.signed(v)
	if rec.Code != http.StatusOK {
		h.t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var r reply
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		h.t.Fatal(err)
	}
	return r
}

var seq int

func cmd(user, channel, sub string, opts ...map[string]any) map[string]any {
	seq++
	sc := map[string]any{"type": 1, "name": sub}
	if len(opts) > 0 {
		sc["options"] = opts
	}
	return map[string]any{
		"id": strconv.Itoa(seq), "type": typeCommand, "guild_id": guildID, "channel_id": channel,
		"member": map[string]any{"user": map[string]any{"id": user}},
		"data":   map[string]any{"name": CommandName, "options": []any{sc}},
	}
}

func opt(name string, value any) map[string]any {
	return map[string]any{"name": name, "type": 3, "value": value}
}

func press(user, channel, customID string) map[string]any {
	seq++
	return map[string]any{
		"id": strconv.Itoa(seq), "type": typeComponent, "guild_id": guildID, "channel_id": channel,
		"member": map[string]any{"user": map[string]any{"id": user}},
		"data":   map[string]any{"custom_id": customID, "component_type": 2},
	}
}

func buttons(r reply) []string {
	var out []string
	for _, row := range r.Data.Components {
		for _, b := range row.Components {
			out = append(out, b.Label+"|"+b.CustomID+b.URL)
		}
	}
	return out
}

func TestSignatureVerificationAndPing(t *testing.T) {
	h := newHarness(t)
	body := []byte(`{"type":1}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := hex.EncodeToString(ed25519.Sign(h.priv, append([]byte(ts), body...)))

	rec := h.raw(body, ts, sig)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"type":1}` {
		t.Fatalf("ping = %d %s", rec.Code, rec.Body.String())
	}
	if rec := h.raw(body, ts, sig); rec.Code != http.StatusUnauthorized {
		t.Fatalf("replayed ping = %d", rec.Code)
	}
	bad := []byte(`{"type":1,"x":1}`)
	if rec := h.raw(bad, ts, sig); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature = %d", rec.Code)
	}
	old := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	if rec := h.raw(body, old, hex.EncodeToString(ed25519.Sign(h.priv, append([]byte(old), body...)))); rec.Code != http.StatusUnauthorized {
		t.Fatalf("stale timestamp = %d", rec.Code)
	}
	if rec := h.raw(body, "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing headers = %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, Path, nil)
	rr := httptest.NewRecorder()
	h.svc.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d", rr.Code)
	}

	h.svc.verifier.PublicKey = func() string { return "" }
	if rec := h.raw(body, ts, sig); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured = %d", rec.Code)
	}
}

func TestFailedVerificationIsRateLimited(t *testing.T) {
	h := newHarness(t)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	for i := 0; i < failedVerify; i++ {
		if rec := h.raw([]byte(`{}`), ts, strings.Repeat("00", 64)); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d", i, rec.Code)
		}
	}
	if rec := h.signed(map[string]any{"type": 1}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after failures = %d", rec.Code)
	}
	h.now = h.now.Add(failedVerifyPer)
	if rec := h.signed(map[string]any{"type": 1}); rec.Code != http.StatusOK {
		t.Fatalf("after window = %d", rec.Code)
	}
}

func TestUnlinkedUserIsToldToLink(t *testing.T) {
	h := newHarness(t)
	r := h.call(cmd(strangerID, textChannel, "create", opt("title", "movie:m1")))
	if r.Data.Flags != flagEphemeral || !strings.Contains(r.Data.Content, "https://vd.example/settings/connected") {
		t.Fatalf("unlinked reply = %+v", r.Data)
	}
	if len(h.parties.rooms) != 0 {
		t.Fatal("unlinked user created a room")
	}
	ac := h.call(map[string]any{"type": typeAutocomplete, "guild_id": guildID, "channel_id": textChannel,
		"member": map[string]any{"user": map[string]any{"id": strangerID}},
		"data":   map[string]any{"name": CommandName, "options": []any{map[string]any{"type": 1, "name": "create", "options": []any{map[string]any{"name": "title", "type": 3, "value": "bunny", "focused": true}}}}}})
	if ac.Type != respAutocomplete || ac.Data.Choices == nil || len(*ac.Data.Choices) != 0 {
		t.Fatalf("unlinked autocomplete = %+v", ac)
	}
}

func TestPartyLifecycleAndHostOnlyControls(t *testing.T) {
	h := newHarness(t)

	ac := h.call(map[string]any{"type": typeAutocomplete, "guild_id": guildID, "channel_id": textChannel,
		"member": map[string]any{"user": map[string]any{"id": hostDiscord}},
		"data":   map[string]any{"name": CommandName, "options": []any{map[string]any{"type": 1, "name": "create", "options": []any{map[string]any{"name": "title", "type": 3, "value": "bunny", "focused": true}}}}}})
	if ac.Data.Choices == nil || len(*ac.Data.Choices) != 2 || (*ac.Data.Choices)[0].Value != "movie:m1" || (*ac.Data.Choices)[0].Name != "Big Buck Bunny (2008)" {
		t.Fatalf("autocomplete = %+v", ac.Data.Choices)
	}

	r := h.call(cmd(hostDiscord, textChannel, "create", opt("title", "movie:m1")))
	if r.Data.Flags != 0 || !strings.Contains(r.Data.Content, "https://vd.example/together/CODE0001") || !strings.Contains(r.Data.Content, "Big Buck Bunny") {
		t.Fatalf("create reply = %+v", r.Data)
	}
	if b := buttons(r); len(b) != 1 || b[0] != "Join party|https://vd.example/together/CODE0001" {
		t.Fatalf("create buttons = %v", b)
	}
	link, err := h.svc.Links.Get(context.Background(), textChannel)
	if err != nil || link.RoomID != "room-1" || link.LinkedBy != "u-host" {
		t.Fatalf("link = %+v, %v", link, err)
	}

	// Free text picks the exact title match.
	r = h.call(cmd(hostDiscord, "300000000000000009", "create", opt("title", "bunny tales")))
	if !strings.Contains(r.Data.Content, "Bunny Tales") {
		t.Fatalf("free text create = %q", r.Data.Content)
	}
	if r := h.call(cmd(hostDiscord, textChannel, "create", opt("title", "zzz"))); r.Data.Flags != flagEphemeral || !strings.Contains(r.Data.Content, "No title") {
		t.Fatalf("no match = %+v", r.Data)
	}
	if r := h.call(cmd(hostDiscord, textChannel, "create", opt("title", "movie:forbidden"))); !strings.Contains(r.Data.Content, "not allowed") {
		t.Fatalf("forbidden create = %q", r.Data.Content)
	}
	if r := h.call(cmd(partyOnlyID, textChannel, "create", opt("title", "movie:m1"))); !strings.Contains(r.Data.Content, "cannot start") {
		t.Fatalf("party-only create = %q", r.Data.Content)
	}

	if r := h.call(cmd(guestDiscord, textChannel, "invite")); !strings.Contains(r.Data.Content, "https://vd.example/together/CODE0001") || r.Data.Flags != 0 {
		t.Fatalf("invite = %+v", r.Data)
	}

	st := h.call(cmd(hostDiscord, textChannel, "status"))
	if st.Data.Flags != flagEphemeral || !strings.Contains(st.Data.Content, "Paused at 0:00, hosted by Hosty") || !strings.Contains(st.Data.Content, "2 watching") {
		t.Fatalf("status = %q", st.Data.Content)
	}
	if b := buttons(st); len(b) != 2 || b[0] != "Pause|party:pause:room-1" {
		t.Fatalf("host status buttons = %v", b)
	}
	if b := buttons(h.call(cmd(guestDiscord, textChannel, "status"))); len(b) != 0 {
		t.Fatalf("non-host sees controls: %v", b)
	}

	if r := h.call(cmd(guestDiscord, textChannel, "resume")); !strings.Contains(r.Data.Content, "Only the party host") {
		t.Fatalf("non-host resume = %q", r.Data.Content)
	}
	if r := h.call(press(guestDiscord, textChannel, "party:resume:room-1")); !strings.Contains(r.Data.Content, "Only the party host") {
		t.Fatalf("non-host button = %q", r.Data.Content)
	}
	if r := h.call(press(strangerID, textChannel, "party:resume:room-1")); !strings.Contains(r.Data.Content, "Link your Discord") {
		t.Fatalf("unlinked button = %q", r.Data.Content)
	}
	if len(h.parties.controls) != 0 {
		t.Fatalf("control reached parties: %v", h.parties.controls)
	}

	if r := h.call(cmd(hostDiscord, textChannel, "resume")); !strings.HasPrefix(r.Data.Content, "Resumed **Big Buck Bunny**") {
		t.Fatalf("host resume = %q", r.Data.Content)
	}
	if r := h.call(press(hostDiscord, textChannel, "party:pause:room-1")); !strings.HasPrefix(r.Data.Content, "Paused") {
		t.Fatalf("host pause button = %q", r.Data.Content)
	}
	if got := strings.Join(h.parties.controls, ","); got != "resume:room-1,pause:room-1" {
		t.Fatalf("controls = %s", got)
	}

	// A room that disappeared drops its links.
	h.now = h.now.Add(userCommandsPer)
	delete(h.parties.rooms, "room-1")
	if r := h.call(cmd(hostDiscord, textChannel, "status")); !strings.Contains(r.Data.Content, "ended") {
		t.Fatalf("ended status = %q", r.Data.Content)
	}
	if _, err := h.svc.Links.Get(context.Background(), textChannel); err != ErrNoLink {
		t.Fatalf("stale link kept: %v", err)
	}
	if r := h.call(cmd(hostDiscord, textChannel, "pause")); !strings.Contains(r.Data.Content, "No watch party") && !strings.Contains(r.Data.Content, "ended") {
		t.Fatalf("pause without link = %q", r.Data.Content)
	}
}

func TestVoiceChannelLinking(t *testing.T) {
	h := newHarness(t)
	h.call(cmd(hostDiscord, textChannel, "create", opt("title", "movie:m1")))

	ch := map[string]any{"name": "channel", "type": 7, "value": voiceChannel}
	if r := h.call(cmd(guestDiscord, textChannel, "link-voice", ch)); !strings.Contains(r.Data.Content, "Only the party host") {
		t.Fatalf("non-host link = %q", r.Data.Content)
	}
	if r := h.call(cmd(hostDiscord, "300000000000000008", "link-voice", ch, opt("invite", "https://vd.example/together/NOPE0000"))); !strings.Contains(r.Data.Content, "does not match") {
		t.Fatalf("unknown invite = %q", r.Data.Content)
	}
	r := h.call(cmd(hostDiscord, "300000000000000008", "link-voice", ch, opt("invite", "https://vd.example/together/CODE0001")))
	if !strings.Contains(r.Data.Content, "<#"+voiceChannel+"> is now linked") || !strings.Contains(r.Data.Content, "Big Buck Bunny") {
		t.Fatalf("link-voice = %q", r.Data.Content)
	}
	l, err := h.svc.Links.Get(context.Background(), voiceChannel)
	if err != nil || l.Kind != LinkVoice || l.RoomID != "room-1" {
		t.Fatalf("voice link = %+v, %v", l, err)
	}
	if r := h.call(cmd(hostDiscord, voiceChannel, "resume")); !strings.HasPrefix(r.Data.Content, "Resumed") {
		t.Fatalf("resume from voice chat = %q", r.Data.Content)
	}

	if r := h.call(cmd(guestDiscord, voiceChannel, "unlink")); !strings.Contains(r.Data.Content, "Only the party host") {
		t.Fatalf("non-host unlink = %q", r.Data.Content)
	}
	if r := h.call(cmd(hostDiscord, voiceChannel, "unlink")); !strings.Contains(r.Data.Content, "no longer linked") {
		t.Fatalf("unlink = %q", r.Data.Content)
	}
	for _, a := range []string{"discord.party_create", "discord.voice_link", "discord.party_resume", "discord.channel_unlink"} {
		if !strings.Contains(strings.Join(h.audit.actions, ","), a) {
			t.Fatalf("missing audit %s in %v", a, h.audit.actions)
		}
	}
}

func TestPerUserCommandRateLimit(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < userCommands; i++ {
		if r := h.call(cmd(guestDiscord, textChannel, "invite")); strings.Contains(r.Data.Content, "too quickly") {
			t.Fatalf("limited early at %d", i)
		}
	}
	if r := h.call(cmd(guestDiscord, textChannel, "invite")); !strings.Contains(r.Data.Content, "too quickly") {
		t.Fatalf("not limited: %q", r.Data.Content)
	}
	if r := h.call(cmd(hostDiscord, textChannel, "invite")); strings.Contains(r.Data.Content, "too quickly") {
		t.Fatal("limit shared across users")
	}
}

func TestDirectMessagesAndDisabledParties(t *testing.T) {
	h := newHarness(t)
	dm := cmd(hostDiscord, textChannel, "invite")
	delete(dm, "guild_id")
	delete(dm, "member")
	dm["user"] = map[string]any{"id": hostDiscord}
	if r := h.call(dm); !strings.Contains(r.Data.Content, "server channel") {
		t.Fatalf("dm = %q", r.Data.Content)
	}
	h.svc.PartiesEnabled = func() bool { return false }
	if r := h.call(cmd(hostDiscord, textChannel, "create", opt("title", "movie:m1"))); !strings.Contains(r.Data.Content, "turned off") {
		t.Fatalf("disabled = %q", r.Data.Content)
	}
}

func TestParseInvite(t *testing.T) {
	cases := map[string]string{
		"CODE0001":                               "CODE0001",
		"https://vd.example/together/CODE0001":   "CODE0001",
		"https://vd.example/together/CODE0001/":  "CODE0001",
		"https://vd.example/s/tok/together/ABCD": "ABCD",
		"https://vd.example/other/CODE0001":      "",
		"../../etc":                              "",
		"":                                       "",
	}
	for in, want := range cases {
		if got := parseInvite(in); got != want {
			t.Errorf("parseInvite(%q) = %q, want %q", in, got, want)
		}
	}
}
