package watchtogether

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	syncTargetMS = 250
	syncExitMS   = 120
	maxRateDelta = 0.05
	rateWindowMS = 6000
	seekCooldown = 3 * time.Second
	reportFresh  = 3 * time.Second
	quorumMin    = 3
	majorityNum  = 2
	majorityDen  = 3
	majorityHold = 3 * time.Second
	hostOverride = 5 * time.Second
	emptyRoomTTL = 2 * time.Hour
	chatLimit    = 100
	chatMaxRunes = 500
)

// report is the latest position a member sent, with At on the server clock.
type report struct {
	PosMS     int64
	At        time.Time
	Received  time.Time
	Playing   bool
	Buffering bool
}

func (r report) projected(t time.Time) int64 {
	if r.Playing && !r.Buffering {
		return r.PosMS + t.Sub(r.At).Milliseconds()
	}
	return r.PosMS
}

type SyncStats struct {
	Reports         int64 `json:"reports"`
	RateCorrections int64 `json:"rate_corrections"`
	Seeks           int64 `json:"seeks"`
	Realigns        int64 `json:"realigns"`
}

type clientMsg struct {
	Type          string  `json:"type"`
	PositionMS    int64   `json:"position_ms"`
	Text          string  `json:"text"`
	Emoji         string  `json:"emoji"`
	T0            int64   `json:"t0"`
	AtServerMS    int64   `json:"at_server_ms"`
	Playing       *bool   `json:"playing"`
	Buffering     bool    `json:"buffering"`
	SharedControl *bool   `json:"shared_control"`
	Panel         *string `json:"panel"`
}

type outbound struct {
	all   map[string]any
	reply map[string]any
}

func (room *Room) expectedAt(t time.Time) int64 {
	pos := room.PositionMS
	if room.Playing {
		pos += t.Sub(room.Clock).Milliseconds()
	}
	if pos < 0 {
		return 0
	}
	return pos
}

func (m *Member) eligible(now time.Time) bool {
	return m.Conn != nil && m.Ready && m.rep.Playing && !m.rep.Buffering && now.Sub(m.rep.Received) <= reportFresh
}

func (room *Room) canControl(principalID string) bool {
	return principalID == room.HostID || room.SharedControl
}

// Apply handles one client message and returns the payload to deliver: the
// room-wide broadcast when there is one, otherwise the reply to the sender.
func (h *Hub) Apply(roomID, principalID, typ string, positionMS int64, text, emoji string) (map[string]any, bool) {
	playing := true
	out, ok := h.handle(roomID, principalID, clientMsg{Type: typ, PositionMS: positionMS, Text: text, Emoji: emoji, Playing: &playing}, time.Now())
	if out.all != nil {
		return out.all, ok
	}
	return out.reply, ok
}

func (h *Hub) handle(roomID, principalID string, msg clientMsg, now time.Time) (outbound, bool) {
	var out outbound
	h.mu.Lock()
	room := h.rooms[roomID]
	if room == nil {
		h.mu.Unlock()
		return out, false
	}
	m := room.Members[principalID]
	if m == nil {
		h.mu.Unlock()
		return out, false
	}
	m.LastSeen = now
	var snap *roomSnapshot
	switch msg.Type {
	case "hello":
		out.reply = h.stateLocked(room, now)
	case "ping":
		out.reply = map[string]any{"type": "pong", "t0": msg.T0, "server_ms": now.UnixMilli()}
	case "ready", "unready":
		m.Ready = msg.Type == "ready"
		out.all = h.stateLocked(room, now)
	case "start", "play", "pause", "seek":
		if !room.canControl(principalID) {
			out.reply = h.stateLocked(room, now)
			break
		}
		pos := msg.PositionMS
		if pos < 0 {
			pos = 0
		}
		switch msg.Type {
		case "start", "play":
			room.Playing = true
			room.PositionMS = pos
		case "pause":
			if pos == 0 {
				pos = room.expectedAt(now)
			}
			room.Playing = false
			room.PositionMS = pos
		case "seek":
			room.PositionMS = pos
		}
		room.Clock = now
		room.Seq++
		room.hostActionAt = now
		room.majDir = 0
		for _, other := range room.Members {
			other.Mode = ""
		}
		snap = snapshotLocked(room)
		st := h.stateLocked(room, now)
		st["reason"] = msg.Type
		st["by"] = m.DisplayName
		out.all = st
	case "settings":
		validPanel := msg.Panel != nil && validPanels[*msg.Panel]
		if principalID != room.HostID || (msg.SharedControl == nil && !validPanel) {
			out.reply = h.stateLocked(room, now)
			break
		}
		if msg.SharedControl != nil {
			room.SharedControl = *msg.SharedControl
		}
		if validPanel {
			room.Panel = *msg.Panel
		}
		room.Seq++
		snap = snapshotLocked(room)
		out.all = h.stateLocked(room, now)
	case "position":
		room.Stats.Reports++
		m.rep = report{
			PosMS:     max(msg.PositionMS, 0),
			At:        reportTime(msg.AtServerMS, now),
			Received:  now,
			Playing:   msg.Playing == nil || *msg.Playing,
			Buffering: msg.Buffering,
		}
		out.reply = h.correctLocked(room, m, now)
	case "chat":
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			break
		}
		if utf8.RuneCountInString(text) > chatMaxRunes {
			text = string([]rune(text)[:chatMaxRunes])
		}
		room.Chat = append(room.Chat, ChatMsg{From: m.DisplayName, Text: text, At: now.UTC().Format(time.RFC3339)})
		if len(room.Chat) > chatLimit {
			room.Chat = append([]ChatMsg(nil), room.Chat[len(room.Chat)-chatLimit:]...)
		}
		out.all = map[string]any{"type": "chat", "from": m.DisplayName, "text": text}
	case "reaction":
		emoji := strings.TrimSpace(msg.Emoji)
		if emoji == "" || utf8.RuneCountInString(emoji) > 8 {
			break
		}
		out.all = map[string]any{"type": "reaction", "from": m.DisplayName, "emoji": emoji}
	default:
		out.reply = h.stateLocked(room, now)
	}
	h.mu.Unlock()
	if snap != nil {
		h.save(context.Background(), snap)
	}
	return out, true
}

// reportTime bounds the client's server-clock estimate so a skewed or
// malicious client cannot shift its own drift arbitrarily.
func reportTime(atServerMS int64, now time.Time) time.Time {
	if atServerMS <= 0 {
		return now
	}
	at := time.UnixMilli(atServerMS)
	if at.After(now) || now.Sub(at) > 5*time.Second {
		return now
	}
	return at
}

// correctLocked measures a member's drift against the room timeline and picks
// a correction: none inside the target, a bounded playback-rate change for
// minor drift (with hysteresis), or a seek beyond the hard threshold.
func (h *Hub) correctLocked(room *Room, m *Member, now time.Time) map[string]any {
	hard := h.driftLimit()
	expected := room.expectedAt(now)
	pos := m.rep.projected(now)
	drift := pos - expected
	m.DriftMS = drift
	if m.rep.Buffering {
		return nil
	}
	if !room.Playing {
		if abs64(drift) > hard && now.Sub(m.LastSeek) >= seekCooldown {
			m.LastSeek = now
			room.Stats.Seeks++
			return syncMsg(room, "seek", 1, expected, drift, now)
		}
		return nil
	}
	if !m.rep.Playing {
		return nil
	}
	ad := abs64(drift)
	switch {
	case ad > hard:
		if now.Sub(m.LastSeek) < seekCooldown {
			return nil
		}
		m.LastSeek = now
		m.Mode, m.Rate = "seek", 1
		room.Stats.Seeks++
		return syncMsg(room, "seek", 1, expected, drift, now)
	case ad > syncTargetMS || (m.Mode == "rate" && ad > syncExitMS):
		rate := 1 - float64(drift)/rateWindowMS
		rate = math.Max(1-maxRateDelta, math.Min(1+maxRateDelta, rate))
		rate = math.Round(rate*1000) / 1000
		changed := m.Mode != "rate" || math.Abs(rate-m.Rate) >= 0.005
		if m.Mode != "rate" {
			room.Stats.RateCorrections++
		}
		m.Mode, m.Rate = "rate", rate
		if !changed {
			return nil
		}
		return syncMsg(room, "rate", rate, expected, drift, now)
	default:
		if m.Mode == "ok" {
			return nil
		}
		m.Mode, m.Rate = "ok", 1
		return syncMsg(room, "ok", 1, expected, drift, now)
	}
}

func syncMsg(room *Room, action string, rate float64, targetMS, driftMS int64, now time.Time) map[string]any {
	return map[string]any{
		"type": "sync", "action": action, "rate": rate, "target_ms": targetMS,
		"server_ms": now.UnixMilli(), "drift_ms": driftMS, "seq": room.Seq, "playing": room.Playing,
	}
}

// realignLocked moves the room timeline to a stable majority of eligible
// viewers that agree with each other but not with the timeline. Buffering,
// paused, disconnected and unready members do not vote.
func (h *Hub) realignLocked(room *Room, now time.Time) bool {
	if !room.Playing || now.Sub(room.hostActionAt) < hostOverride {
		room.majDir = 0
		return false
	}
	expected := room.expectedAt(now)
	var ahead, behind []int64
	n := 0
	for _, m := range room.Members {
		if !m.eligible(now) {
			continue
		}
		n++
		d := m.rep.projected(now) - expected
		if d > syncTargetMS {
			ahead = append(ahead, d)
		} else if d < -syncTargetMS {
			behind = append(behind, d)
		}
	}
	if n < quorumMin {
		room.majDir = 0
		return false
	}
	var group []int64
	dir := 0
	if len(ahead)*majorityDen >= majorityNum*n {
		group, dir = ahead, 1
	} else if len(behind)*majorityDen >= majorityNum*n {
		group, dir = behind, -1
	}
	if dir == 0 || spread(group) > h.driftLimit() {
		room.majDir = 0
		return false
	}
	if room.majDir != dir {
		room.majDir, room.majSince = dir, now
		return false
	}
	if now.Sub(room.majSince) < majorityHold {
		return false
	}
	room.PositionMS = expected + median(group)
	room.Clock = now
	room.Seq++
	room.Stats.Realigns++
	room.majDir = 0
	room.hostActionAt = now
	for _, m := range room.Members {
		m.Mode = ""
	}
	return true
}

func spread(v []int64) int64 {
	if len(v) == 0 {
		return 0
	}
	lo, hi := v[0], v[0]
	for _, x := range v[1:] {
		lo, hi = min(lo, x), max(hi, x)
	}
	return hi - lo
}

func median(v []int64) int64 {
	s := append([]int64(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

func (h *Hub) loop() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for now := range t.C {
		h.tick(now)
	}
}

type delivery struct {
	roomID  string
	payload map[string]any
}

// tick expires members and tickets, rotates hosts, evaluates majority
// alignment, publishes presence every other second and removes rooms that
// have been empty for emptyRoomTTL.
func (h *Hub) tick(now time.Time) {
	var sends []delivery
	var snaps []*roomSnapshot
	var gone []string
	h.mu.Lock()
	h.ticks++
	for code, t := range h.tickets {
		if now.After(t.Exp) {
			delete(h.tickets, code)
		}
	}
	for id, room := range h.rooms {
		for mid, m := range room.Members {
			if now.Sub(m.LastSeen) > memberLease {
				h.dropLocked(room, mid, false)
			}
		}
		if room.HostID != "" {
			host := room.Members[room.HostID]
			if host == nil || now.Sub(host.LastSeen) > hostGrace {
				h.promoteLocked(room)
			}
		}
		if len(room.Members) == 0 {
			if room.EmptySince.IsZero() {
				room.EmptySince = now
			} else if now.Sub(room.EmptySince) > emptyRoomTTL {
				delete(h.rooms, id)
				delete(h.invites, room.InviteCode)
				gone = append(gone, id)
			}
			continue
		}
		room.EmptySince = time.Time{}
		if h.realignLocked(room, now) {
			st := h.stateLocked(room, now)
			st["reason"] = "majority"
			sends = append(sends, delivery{id, st})
			snaps = append(snaps, snapshotLocked(room))
			continue
		}
		if h.ticks%2 == 0 && connected(room) {
			sends = append(sends, delivery{id, h.presenceLocked(room, now)})
		}
	}
	h.mu.Unlock()
	for _, s := range snaps {
		h.save(context.Background(), s)
	}
	for _, id := range gone {
		h.deleteRoom(context.Background(), id)
	}
	for _, d := range sends {
		h.broadcast(d.roomID, d.payload)
	}
}

func connected(room *Room) bool {
	for _, m := range room.Members {
		if m.Conn != nil {
			return true
		}
	}
	return false
}

func (h *Hub) membersLocked(room *Room, now time.Time) []map[string]any {
	members := make([]map[string]any, 0, len(room.Members))
	for _, m := range room.Members {
		members = append(members, map[string]any{
			"id": m.ID, "display_name": m.DisplayName, "ready": m.Ready, "kind": m.Kind,
			"host": m.ID == room.HostID, "connected": m.Conn != nil,
			"buffering": m.rep.Buffering, "eligible": m.eligible(now), "drift_ms": m.DriftMS,
		})
	}
	sort.Slice(members, func(i, j int) bool { return members[i]["id"].(string) < members[j]["id"].(string) })
	return members
}

func (h *Hub) syncInfoLocked(room *Room, now time.Time) map[string]any {
	eligible := 0
	var worst int64
	for _, m := range room.Members {
		if m.eligible(now) {
			eligible++
			worst = max(worst, abs64(m.DriftMS))
		}
	}
	return map[string]any{
		"target_ms": syncTargetMS, "hard_ms": h.driftLimit(), "eligible": eligible,
		"max_drift_ms": worst, "stats": room.Stats,
	}
}

func (h *Hub) presenceLocked(room *Room, now time.Time) map[string]any {
	return map[string]any{
		"type": "presence", "room_id": room.ID, "host": room.HostID,
		"members": h.membersLocked(room, now), "sync": h.syncInfoLocked(room, now),
	}
}

// Control applies a play or pause on behalf of a member who holds control of
// the room (the host, or anyone when shared control is on) and pushes the new
// state to connected members.
func (h *Hub) Control(roomID, principalID, action string) error {
	if action != "play" && action != "pause" {
		return errDenied
	}
	now := time.Now()
	h.mu.Lock()
	room := h.rooms[roomID]
	allowed := room != nil && room.Members[principalID] != nil && room.canControl(principalID)
	var pos int64
	if allowed {
		pos = room.expectedAt(now)
	}
	h.mu.Unlock()
	if !allowed {
		return errDenied
	}
	out, ok := h.handle(roomID, principalID, clientMsg{Type: action, PositionMS: pos}, now)
	if !ok {
		return errDenied
	}
	h.broadcast(roomID, out.all)
	return nil
}

// RoomSummary is a non-sensitive listing entry for administrators.
type RoomSummary struct {
	ID       string
	ItemKind string
	ItemID   string
	Members  int
	Playing  bool
}

// Rooms lists rooms that currently have members.
func (h *Hub) Rooms() []RoomSummary {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]RoomSummary, 0, len(h.rooms))
	for _, room := range h.rooms {
		if len(room.Members) == 0 {
			continue
		}
		out = append(out, RoomSummary{ID: room.ID, ItemKind: room.ItemKind, ItemID: room.ItemID, Members: len(room.Members), Playing: room.Playing})
	}
	return out
}

// SyncSnapshot summarises coordinator health for diagnostics.
func (h *Hub) SyncSnapshot() map[string]any {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	rooms, members, conns := 0, 0, 0
	var worst int64
	var stats SyncStats
	for _, room := range h.rooms {
		rooms++
		for _, m := range room.Members {
			members++
			if m.Conn != nil {
				conns++
			}
			if m.eligible(now) {
				worst = max(worst, abs64(m.DriftMS))
			}
		}
		stats.Reports += room.Stats.Reports
		stats.RateCorrections += room.Stats.RateCorrections
		stats.Seeks += room.Stats.Seeks
		stats.Realigns += room.Stats.Realigns
	}
	return map[string]any{
		"rooms": rooms, "members": members, "connected": conns, "max_drift_ms": worst,
		"target_ms": syncTargetMS, "hard_ms": h.driftLimit(), "stats": stats, "server_ms": now.UnixMilli(),
	}
}
