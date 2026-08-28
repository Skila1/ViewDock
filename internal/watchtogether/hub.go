package watchtogether

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/share"
)

const (
	driftMS      = 1500
	hostGrace    = 15 * time.Second
	ticketTTL    = 2 * time.Minute
	memberLease  = 45 * time.Second
)

type Member struct {
	ID             string
	Kind           string
	DisplayName    string
	GuestSessionID string
	Ready          bool
	LastSeen       time.Time
	Conn           conn
}

type conn interface {
	Close() error
}

type Room struct {
	ID         string
	InviteCode string
	ItemKind   string
	ItemID     string
	SharePath  string
	HostID     string
	Playing    bool
	PositionMS int64
	Clock      time.Time
	Seq        int64
	Members    map[string]*Member
	Chat       []ChatMsg
}

type ChatMsg struct {
	From string `json:"from"`
	Text string `json:"text"`
	At   string `json:"at"`
}

type ticket struct {
	RoomID         string
	PrincipalID    string
	GuestSessionID string
	Exp            time.Time
}

type Hub struct {
	Locator library.MediaLocator
	Grants  library.LibraryGrants
	Gate    share.Gate

	mu      sync.Mutex
	rooms   map[string]*Room
	invites map[string]string // code -> room id
	tickets map[string]ticket
}

func New(loc library.MediaLocator, grants library.LibraryGrants, gate share.Gate) *Hub {
	if gate == nil {
		gate = share.NoopGate()
	}
	h := &Hub{
		Locator: loc, Grants: grants, Gate: gate,
		rooms: map[string]*Room{}, invites: map[string]string{}, tickets: map[string]ticket{},
	}
	go h.loop()
	return h
}

func (h *Hub) Routes(r chi.Router) {
	r.Get("/watch-together/invites/{code}", h.handleInvite)
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireUserOrGuest)
		r.Post("/watch-together/rooms", h.handleCreate)
		r.Post("/watch-together/join", h.handleJoin)
		r.Post("/watch-together/rooms/{id}/ticket", h.handleTicket)
		r.Get("/watch-together/rooms/{id}/ws", h.handleWS)
	})
}

func (h *Hub) loop() {
	t := time.NewTicker(2 * time.Second)
	for range t.C {
		h.tick()
	}
}

func (h *Hub) tick() {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, room := range h.rooms {
		for id, m := range room.Members {
			if now.Sub(m.LastSeen) > memberLease {
				h.dropLocked(room, id, false)
			}
		}
		if room.HostID != "" {
			host := room.Members[room.HostID]
			if host == nil || now.Sub(host.LastSeen) > hostGrace {
				h.promoteLocked(room)
			}
		}
	}
}

func (h *Hub) authorize(ctx context.Context, p *auth.Principal, itemKind, itemID string) error {
	if p == nil {
		return errDenied
	}
	if p.IsUser() {
		if h.Locator == nil {
			return nil
		}
		loc, err := h.Locator.LocateItem(ctx, itemKind, itemID)
		if err != nil || loc == nil {
			return errDenied
		}
		if p.IsAdmin {
			return nil
		}
		if h.Grants != nil && !h.Grants.CanRead(ctx, p.UserID, loc.LibraryID) {
			return errDenied
		}
		return nil
	}
	if p.IsGuest() {
		if p.MediaKind != itemKind || p.MediaID != itemID {
			return errDenied
		}
		return h.Gate.CanStreamMedia(ctx, p.GuestSessionID, itemKind, itemID)
	}
	return errDenied
}

func displayName(p *auth.Principal) string {
	if p.IsGuest() {
		return "Guest"
	}
	if p.DisplayName != "" {
		return p.DisplayName
	}
	return p.Username
}

func (h *Hub) Create(ctx context.Context, p *auth.Principal, itemKind, itemID string) (*Room, error) {
	if err := h.authorize(ctx, p, itemKind, itemID); err != nil {
		return nil, err
	}
	room := &Room{
		ID: uuid.NewString(), InviteCode: randomCode(8),
		ItemKind: itemKind, ItemID: itemID,
		HostID: p.ID(), Members: map[string]*Member{}, Clock: time.Now(),
	}
	if p.IsGuest() {
		if tok := h.Gate.ShareTokenForGuest(ctx, p.GuestSessionID); tok != "" {
			room.SharePath = "/s/" + tok + "/together/" + room.InviteCode
		} else {
			room.SharePath = "/s/"
		}
	}
	h.addMember(room, p)
	h.mu.Lock()
	h.rooms[room.ID] = room
	h.invites[room.InviteCode] = room.ID
	h.mu.Unlock()
	return room, nil
}

func (h *Hub) addMember(room *Room, p *auth.Principal) *Member {
	m := &Member{
		ID: p.ID(), Kind: p.Kind, DisplayName: displayName(p),
		GuestSessionID: p.GuestSessionID, LastSeen: time.Now(),
	}
	room.Members[m.ID] = m
	if room.HostID == "" {
		room.HostID = m.ID
	}
	return m
}

func (h *Hub) Join(ctx context.Context, p *auth.Principal, code string) (*Room, error) {
	h.mu.Lock()
	id := h.invites[code]
	room := h.rooms[id]
	h.mu.Unlock()
	if room == nil {
		return nil, errDenied
	}
	if err := h.authorize(ctx, p, room.ItemKind, room.ItemID); err != nil {
		return nil, errDenied
	}
	h.mu.Lock()
	h.addMember(room, p)
	h.mu.Unlock()
	return room, nil
}

func (h *Hub) Invite(code string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.rooms[h.invites[code]]
}

func (h *Hub) Room(id string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.rooms[id]
}

func (h *Hub) MintTicket(p *auth.Principal, roomID string) (string, error) {
	h.mu.Lock()
	room := h.rooms[roomID]
	h.mu.Unlock()
	if room == nil {
		return "", errDenied
	}
	if err := h.authorize(context.Background(), p, room.ItemKind, room.ItemID); err != nil {
		return "", errDenied
	}
	raw, err := randomHex(24)
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	h.tickets[raw] = ticket{
		RoomID: roomID, PrincipalID: p.ID(), GuestSessionID: p.GuestSessionID,
		Exp: time.Now().Add(ticketTTL),
	}
	h.mu.Unlock()
	return raw, nil
}

func (h *Hub) State(roomID string) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[roomID]
	if room == nil {
		return nil
	}
	return h.stateLocked(room)
}

func (h *Hub) stateLocked(room *Room) map[string]any {
	pos := room.PositionMS
	if room.Playing {
		pos += time.Since(room.Clock).Milliseconds()
	}
	var members []map[string]any
	for _, m := range room.Members {
		members = append(members, map[string]any{
			"id": m.ID, "display_name": m.DisplayName, "ready": m.Ready, "kind": m.Kind,
		})
	}
	return map[string]any{
		"type": "state", "room_id": room.ID, "host": room.HostID,
		"playing": room.Playing, "position_ms": pos, "seq": room.Seq,
		"members": members, "item_kind": room.ItemKind, "item_id": room.ItemID,
	}
}

func (h *Hub) Apply(roomID, principalID, typ string, positionMS int64, text, emoji string) (map[string]any, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[roomID]
	if room == nil {
		return nil, false
	}
	m := room.Members[principalID]
	if m == nil {
		return nil, false
	}
	m.LastSeen = time.Now()
	switch typ {
	case "hello":
		return h.stateLocked(room), true
	case "ready":
		m.Ready = true
		return h.stateLocked(room), true
	case "start", "play":
		if principalID != room.HostID {
			return h.stateLocked(room), true
		}
		room.Playing = true
		room.PositionMS = positionMS
		room.Clock = time.Now()
		room.Seq++
		return h.stateLocked(room), true
	case "pause":
		if principalID != room.HostID {
			return h.stateLocked(room), true
		}
		if room.Playing {
			room.PositionMS += time.Since(room.Clock).Milliseconds()
		}
		room.Playing = false
		room.Clock = time.Now()
		if positionMS > 0 {
			room.PositionMS = positionMS
		}
		room.Seq++
		return h.stateLocked(room), true
	case "seek":
		if principalID != room.HostID {
			return h.stateLocked(room), true
		}
		room.PositionMS = positionMS
		room.Clock = time.Now()
		room.Seq++
		return h.stateLocked(room), true
	case "position":
		expected := room.PositionMS
		if room.Playing {
			expected += time.Since(room.Clock).Milliseconds()
		}
		if abs64(expected-positionMS) > driftMS {
			return map[string]any{"type": "resync", "position_ms": expected, "playing": room.Playing, "seq": room.Seq}, true
		}
		return nil, true
	case "chat":
		room.Chat = append(room.Chat, ChatMsg{From: m.DisplayName, Text: text, At: time.Now().UTC().Format(time.RFC3339)})
		return map[string]any{"type": "chat", "from": m.DisplayName, "text": text}, true
	case "reaction":
		return map[string]any{"type": "reaction", "from": m.DisplayName, "emoji": emoji}, true
	}
	return h.stateLocked(room), true
}

func (h *Hub) KickGuest(guestSessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, room := range h.rooms {
		for id, m := range room.Members {
			if m.GuestSessionID == guestSessionID {
				h.dropLocked(room, id, true)
			}
		}
	}
}

func (h *Hub) CheckGate(ctx context.Context, room *Room, p *auth.Principal) error {
	if p == nil || !p.IsGuest() {
		return nil
	}
	return h.Gate.CanStreamMedia(ctx, p.GuestSessionID, room.ItemKind, room.ItemID)
}

func (h *Hub) dropLocked(room *Room, id string, closeConn bool) {
	m := room.Members[id]
	if m == nil {
		return
	}
	if closeConn && m.Conn != nil {
		_ = m.Conn.Close()
	}
	delete(room.Members, id)
	if room.HostID == id {
		h.promoteLocked(room)
	}
}

func (h *Hub) promoteLocked(room *Room) {
	var best *Member
	for _, m := range room.Members {
		if best == nil || m.LastSeen.After(best.LastSeen) {
			best = m
		}
	}
	if best == nil {
		room.HostID = ""
		return
	}
	room.HostID = best.ID
}

func randomCode(n int) string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func writeDenied(w http.ResponseWriter) {
	httpapi.WriteErr(w, http.StatusNotFound, "not_found", "not found")
}

func (h *Hub) handleCreate(w http.ResponseWriter, r *http.Request) {
	p := auth.FromRequest(r)
	var body struct {
		ItemKind string `json:"item_kind"`
		ItemID   string `json:"item_id"`
	}
	_ = readJSON(r, &body)
	room, err := h.Create(r.Context(), p, body.ItemKind, body.ItemID)
	if err != nil {
		writeDenied(w)
		return
	}
	httpapi.WriteJSON(w, 200, map[string]string{"room_id": room.ID, "invite_code": room.InviteCode})
}

func (h *Hub) handleInvite(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	room := h.Invite(code)
	if room == nil {
		writeDenied(w)
		return
	}
	p := auth.FromRequest(r)
	out := map[string]any{"needs_auth": false, "needs_share": false}
	if room.SharePath != "" {
		out["needs_share"] = p == nil || !p.IsGuest()
		if room.SharePath != "/s/" {
			out["share_path"] = room.SharePath
		} else {
			out["share_path"] = "/s/"
		}
	} else if p == nil || !p.IsUser() {
		out["needs_auth"] = true
	}
	httpapi.WriteJSON(w, 200, out)
}

func (h *Hub) handleJoin(w http.ResponseWriter, r *http.Request) {
	p := auth.FromRequest(r)
	var body struct {
		InviteCode string `json:"invite_code"`
	}
	_ = readJSON(r, &body)
	room, err := h.Join(r.Context(), p, strings.TrimSpace(body.InviteCode))
	if err != nil {
		writeDenied(w)
		return
	}
	httpapi.WriteJSON(w, 200, map[string]any{"room_id": room.ID, "item_kind": room.ItemKind})
}

func (h *Hub) handleTicket(w http.ResponseWriter, r *http.Request) {
	p := auth.FromRequest(r)
	id := chi.URLParam(r, "id")
	raw, err := h.MintTicket(p, id)
	if err != nil {
		writeDenied(w)
		return
	}
	httpapi.WriteJSON(w, 200, map[string]string{"ticket": raw})
}
