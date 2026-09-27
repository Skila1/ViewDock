package watchtogether

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/share"
)

type fakeConn struct{}

func (fakeConn) Close() error { return nil }

// newTestHub builds a hub without the background ticker so tests control time.
func newTestHub() *Hub {
	return &Hub{
		Locator: loc{kind: "movie", id: "A"}, Grants: grants{}, Gate: share.NoopGate(), AllowOrigin: sameOrigin,
		rooms: map[string]*Room{}, invites: map[string]string{}, tickets: map[string]ticket{},
	}
}

type party struct {
	h    *Hub
	room *Room
	ids  []string
}

// newParty creates a room with n connected, ready members that started
// playing from 0 at start. ids[0] is the host.
func newParty(t *testing.T, n int, start time.Time) *party {
	t.Helper()
	h := newTestHub()
	host := &auth.Principal{Kind: auth.KindUser, UserID: "m0", DisplayName: "Host"}
	room, err := h.Create(context.Background(), host, "movie", "A")
	if err != nil {
		t.Fatal(err)
	}
	p := &party{h: h, room: room, ids: []string{host.ID()}}
	for i := 1; i < n; i++ {
		u := &auth.Principal{Kind: auth.KindUser, UserID: fmt.Sprintf("m%d", i), DisplayName: fmt.Sprintf("M%d", i)}
		if _, err := h.Join(context.Background(), u, room.InviteCode); err != nil {
			t.Fatal(err)
		}
		p.ids = append(p.ids, u.ID())
	}
	for _, id := range p.ids {
		room.Members[id].Conn = fakeConn{}
		p.send(t, id, clientMsg{Type: "ready"}, start)
	}
	p.send(t, host.ID(), clientMsg{Type: "play", PositionMS: 0}, start)
	return p
}

func (p *party) send(t *testing.T, id string, msg clientMsg, now time.Time) outbound {
	t.Helper()
	out, ok := p.h.handle(p.room.ID, id, msg, now)
	if !ok {
		t.Fatalf("message %s from %s rejected", msg.Type, id)
	}
	return out
}

// report sends a position that is driftMS away from the room timeline at now.
func (p *party) report(t *testing.T, id string, driftMS int64, buffering bool, now time.Time) map[string]any {
	t.Helper()
	playing := true
	pos := p.room.expectedAt(now) + driftMS
	return p.send(t, id, clientMsg{Type: "position", PositionMS: pos, AtServerMS: now.UnixMilli(), Playing: &playing, Buffering: buffering}, now).reply
}

func TestCorrectionTiers(t *testing.T) {
	base := time.Now()
	p := newParty(t, 2, base)
	id := p.ids[1]
	now := base.Add(10 * time.Second)

	if out := p.report(t, id, 100, false, now); out == nil || out["action"] != "ok" {
		t.Fatalf("in-target first report = %v", out)
	}
	if out := p.report(t, id, 150, false, now.Add(time.Second)); out != nil {
		t.Fatalf("in-target repeat must be silent, got %v", out)
	}
	out := p.report(t, id, 600, false, now.Add(2*time.Second))
	if out == nil || out["action"] != "rate" || out["rate"].(float64) != 0.95 {
		t.Fatalf("600 ms ahead = %v, want rate 0.95", out)
	}
	out = p.report(t, id, -300, false, now.Add(3*time.Second))
	if out == nil || out["action"] != "rate" || out["rate"].(float64) != 1.05 {
		t.Fatalf("300 ms behind = %v, want rate 1.05", out)
	}
	out = p.report(t, id, 200, false, now.Add(4*time.Second))
	if out == nil || out["action"] != "rate" {
		t.Fatalf("hysteresis: 200 ms while correcting must stay in rate mode, got %v", out)
	}
	if out := p.report(t, id, 100, false, now.Add(5*time.Second)); out == nil || out["action"] != "ok" || out["rate"].(float64) != 1 {
		t.Fatalf("back inside exit threshold = %v", out)
	}
	out = p.report(t, id, 1500, false, now.Add(6*time.Second))
	if out == nil || out["action"] != "seek" {
		t.Fatalf("1500 ms = %v, want seek", out)
	}
	target := out["target_ms"].(int64)
	if want := p.room.expectedAt(now.Add(6 * time.Second)); target != want {
		t.Fatalf("seek target %d, want %d", target, want)
	}
	if out := p.report(t, id, 1500, false, now.Add(7*time.Second)); out != nil {
		t.Fatalf("second seek inside cooldown must be suppressed, got %v", out)
	}
	st := p.room.Stats
	if st.Seeks != 1 || st.RateCorrections != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestHardThresholdFollowsConfig(t *testing.T) {
	base := time.Now()
	p := newParty(t, 2, base)
	p.h.SetHardDriftMS(2000)
	if out := p.report(t, p.ids[1], 1500, false, base.Add(time.Second)); out == nil || out["action"] != "rate" {
		t.Fatalf("1500 ms under a 2000 ms threshold = %v, want rate", out)
	}
}

func TestBufferingMemberIsNotCorrectedOrCounted(t *testing.T) {
	base := time.Now()
	p := newParty(t, 2, base)
	now := base.Add(5 * time.Second)
	if out := p.report(t, p.ids[1], -5000, true, now); out != nil {
		t.Fatalf("buffering member corrected: %v", out)
	}
	if p.room.Members[p.ids[1]].eligible(now) {
		t.Fatal("buffering member must not be eligible")
	}
}

func TestMajorityRealignsTimelineAfterHold(t *testing.T) {
	base := time.Now()
	p := newParty(t, 5, base)
	start := base.Add(10 * time.Second)
	realigned := time.Time{}
	for i := 0; i <= 6; i++ {
		now := start.Add(time.Duration(i) * time.Second)
		for _, id := range p.ids[:3] {
			p.report(t, id, 800, false, now)
		}
		p.report(t, p.ids[3], 0, false, now)
		p.report(t, p.ids[4], -4000, true, now)
		before := p.room.Stats.Realigns
		p.h.tick(now)
		if p.room.Stats.Realigns > before {
			realigned = now
			break
		}
	}
	if realigned.IsZero() {
		t.Fatal("room never realigned to a stable majority")
	}
	if held := realigned.Sub(start); held < majorityHold {
		t.Fatalf("realigned after %v, before the %v hold", held, majorityHold)
	}
	if drift := p.room.expectedAt(realigned) - (realigned.Sub(base).Milliseconds() + 800); abs64(drift) > 5 {
		t.Fatalf("timeline not moved to the majority: off by %d ms", drift)
	}
	now := realigned.Add(500 * time.Millisecond)
	out := p.report(t, p.ids[3], -800, false, now)
	if out == nil || out["action"] != "rate" || out["rate"].(float64) <= 1 {
		t.Fatalf("minority behind the new timeline should speed up, got %v", out)
	}
}

func TestNoRealignWithoutSupermajority(t *testing.T) {
	base := time.Now()
	p := newParty(t, 4, base)
	start := base.Add(10 * time.Second)
	for i := 0; i <= 8; i++ {
		now := start.Add(time.Duration(i) * time.Second)
		p.report(t, p.ids[0], 800, false, now)
		p.report(t, p.ids[1], 800, false, now)
		p.report(t, p.ids[2], 0, false, now)
		p.report(t, p.ids[3], 0, false, now)
		p.h.tick(now)
	}
	if p.room.Stats.Realigns != 0 {
		t.Fatalf("a 50/50 split moved the room %d times", p.room.Stats.Realigns)
	}
}

func TestNoRealignBelowQuorumOrAfterHostAction(t *testing.T) {
	base := time.Now()
	p := newParty(t, 2, base)
	start := base.Add(10 * time.Second)
	for i := 0; i <= 6; i++ {
		now := start.Add(time.Duration(i) * time.Second)
		p.report(t, p.ids[0], 800, false, now)
		p.report(t, p.ids[1], 800, false, now)
		p.h.tick(now)
	}
	if p.room.Stats.Realigns != 0 {
		t.Fatal("two viewers must not form a quorum")
	}

	q := newParty(t, 3, base)
	for i := 0; i <= 4; i++ {
		now := base.Add(time.Duration(i) * time.Second)
		for _, id := range q.ids {
			q.report(t, id, 800, false, now)
		}
		q.h.tick(now)
	}
	if q.room.Stats.Realigns != 0 {
		t.Fatal("majority alignment must not override a recent host action")
	}
}

func TestSplitMajorityThatDisagreesIsIgnored(t *testing.T) {
	base := time.Now()
	p := newParty(t, 3, base)
	start := base.Add(10 * time.Second)
	for i := 0; i <= 6; i++ {
		now := start.Add(time.Duration(i) * time.Second)
		p.report(t, p.ids[0], 400, false, now)
		p.report(t, p.ids[1], 900, false, now)
		p.report(t, p.ids[2], 2600, false, now)
		p.h.tick(now)
	}
	if p.room.Stats.Realigns != 0 {
		t.Fatal("members ahead by widely different amounts are not a stable majority")
	}
}

func TestControlPermissions(t *testing.T) {
	base := time.Now()
	p := newParty(t, 2, base)
	guestID := p.ids[1]
	out := p.send(t, guestID, clientMsg{Type: "pause", PositionMS: 5000}, base.Add(time.Second))
	if out.all != nil || !p.room.Playing {
		t.Fatal("non-host pause must not change the room")
	}
	if out.reply == nil || out.reply["type"] != "state" {
		t.Fatalf("non-host should get the state back, got %v", out.reply)
	}
	on := true
	if out := p.send(t, guestID, clientMsg{Type: "settings", SharedControl: &on}, base.Add(time.Second)); out.all != nil || p.room.SharedControl {
		t.Fatal("only the host may enable shared control")
	}
	p.send(t, p.ids[0], clientMsg{Type: "settings", SharedControl: &on}, base.Add(time.Second))
	out = p.send(t, guestID, clientMsg{Type: "pause", PositionMS: 5000}, base.Add(2*time.Second))
	if out.all == nil || p.room.Playing || p.room.PositionMS != 5000 {
		t.Fatalf("shared control pause failed: %+v", out)
	}
	if err := p.h.Control(p.room.ID, "stranger", "play"); err == nil {
		t.Fatal("non-member control must fail")
	}
	if err := p.h.Control(p.room.ID, p.ids[0], "play"); err != nil || !p.room.Playing {
		t.Fatalf("host control: %v", err)
	}
}

func TestPausedRoomSeeksFarMember(t *testing.T) {
	base := time.Now()
	p := newParty(t, 2, base)
	p.send(t, p.ids[0], clientMsg{Type: "pause", PositionMS: 30_000}, base.Add(time.Second))
	paused := false
	out := p.send(t, p.ids[1], clientMsg{Type: "position", PositionMS: 10_000, Playing: &paused}, base.Add(2*time.Second)).reply
	if out == nil || out["action"] != "seek" || out["target_ms"].(int64) != 30_000 || out["playing"] != false {
		t.Fatalf("paused room correction = %v", out)
	}
}

func TestChatIsBoundedAndTrimmed(t *testing.T) {
	base := time.Now()
	p := newParty(t, 2, base)
	if out := p.send(t, p.ids[1], clientMsg{Type: "chat", Text: "   "}, base); out.all != nil {
		t.Fatal("blank chat must be dropped")
	}
	long := strings.Repeat("x", chatMaxRunes+50)
	out := p.send(t, p.ids[1], clientMsg{Type: "chat", Text: long}, base)
	if n := len([]rune(out.all["text"].(string))); n != chatMaxRunes {
		t.Fatalf("chat length %d", n)
	}
	for i := 0; i < chatLimit+20; i++ {
		p.send(t, p.ids[1], clientMsg{Type: "chat", Text: "hi"}, base)
	}
	if len(p.room.Chat) != chatLimit {
		t.Fatalf("chat history %d, want %d", len(p.room.Chat), chatLimit)
	}
}

func TestEmptyRoomsAreRemoved(t *testing.T) {
	base := time.Now()
	p := newParty(t, 1, base)
	later := base.Add(memberLease + time.Second)
	p.h.tick(later)
	if p.h.Room(p.room.ID) == nil {
		t.Fatal("room removed as soon as it emptied")
	}
	p.h.tick(later.Add(emptyRoomTTL + time.Second))
	if p.h.Room(p.room.ID) != nil || p.h.Invite(p.room.InviteCode) != nil {
		t.Fatal("empty room not removed after the TTL")
	}
}

func TestReportTimeIsBounded(t *testing.T) {
	now := time.Now()
	if got := reportTime(now.Add(time.Minute).UnixMilli(), now); !got.Equal(now) {
		t.Fatal("future timestamps must be ignored")
	}
	if got := reportTime(now.Add(-time.Minute).UnixMilli(), now); !got.Equal(now) {
		t.Fatal("stale timestamps must be ignored")
	}
	at := now.Add(-400 * time.Millisecond)
	if got := reportTime(at.UnixMilli(), now); got.UnixMilli() != at.UnixMilli() {
		t.Fatal("recent timestamps must be kept")
	}
}

func TestOwnerReclaimsHost(t *testing.T) {
	p := newParty(t, 2, time.Now())
	h, room := p.h, p.room
	owner := &auth.Principal{Kind: auth.KindUser, UserID: "m0", DisplayName: "Host"}
	other := &auth.Principal{Kind: auth.KindUser, UserID: "m1", DisplayName: "M1"}
	if room.OwnerID != owner.ID() {
		t.Fatalf("owner = %q", room.OwnerID)
	}

	h.mu.Lock()
	h.dropLocked(room, owner.ID(), false)
	h.mu.Unlock()
	if room.HostID != other.ID() {
		t.Fatalf("host after the owner lapsed = %q", room.HostID)
	}
	if err := h.Control(room.ID, owner.ID(), "pause"); err == nil {
		t.Fatal("a lapsed owner controlled the room without rejoining")
	}
	if h.ReclaimOwner(context.Background(), other, room.ID) {
		t.Fatal("a non-owner reclaimed the room")
	}
	if !h.ReclaimOwner(context.Background(), owner, room.ID) || room.HostID != owner.ID() {
		t.Fatalf("owner not restored as host: %q", room.HostID)
	}
	if err := h.Control(room.ID, owner.ID(), "pause"); err != nil || room.Playing {
		t.Fatalf("owner control after reclaim: %v playing=%v", err, room.Playing)
	}
	if err := h.Control(room.ID, other.ID(), "play"); err == nil {
		t.Fatal("the former stand-in host kept control")
	}

	h.mu.Lock()
	h.dropLocked(room, owner.ID(), false)
	h.mu.Unlock()
	if _, err := h.Join(context.Background(), owner, room.InviteCode); err != nil || room.HostID != owner.ID() {
		t.Fatalf("owner rejoin: host %q err %v", room.HostID, err)
	}
}
