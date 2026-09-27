package watchtogether

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/share"
)

const (
	driftMS     = 1000
	hostGrace   = 15 * time.Second
	ticketTTL   = 2 * time.Minute
	memberLease = 45 * time.Second
)

type QueueItem struct {
	ItemKind string `json:"item_kind"`
	ItemID   string `json:"item_id"`
	Title    string `json:"title,omitempty"`
}

type Member struct {
	ID             string
	Kind           string
	DisplayName    string
	GuestSessionID string
	Ready          bool
	LastSeen       time.Time
	Conn           conn
	DriftMS        int64
	Mode           string
	Rate           float64
	LastSeek       time.Time
	rep            report
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
	// OwnerID is the creator. The owner takes host back whenever they rejoin,
	// so a host who steps away (or only uses Discord) does not lose the room.
	OwnerID           string
	Playing           bool
	PositionMS        int64
	Clock             time.Time
	Seq               int64
	Members           map[string]*Member
	Chat              []ChatMsg
	Queue             []QueueItem
	IntermissionUntil time.Time
	Votes             map[string]map[string]bool
	SharedControl     bool
	// Panel is who sees the party panel: PanelEveryone, PanelHost or PanelHidden.
	Panel string
	// Banned lists principals an administrator removed; they cannot rejoin.
	Banned       map[string]bool
	EmptySince   time.Time
	Stats        SyncStats
	hostActionAt time.Time
	majDir       int
	majSince     time.Time
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
	Locator     library.MediaLocator
	Grants      library.LibraryGrants
	Gate        share.Gate
	AllowOrigin func(*http.Request) bool
	DB          *sql.DB
	Postgres    bool
	// Enabled gates creating and joining rooms; nil means enabled.
	Enabled func() bool
	// hardDriftMS overrides driftMS when positive.
	hardDriftMS atomic.Int64

	mu      sync.Mutex
	rooms   map[string]*Room
	invites map[string]string // code -> room id
	tickets map[string]ticket
	ticks   int64
}

func New(loc library.MediaLocator, grants library.LibraryGrants, gate share.Gate) *Hub {
	return NewWithOriginChecker(loc, grants, gate, nil)
}

func NewWithOriginChecker(loc library.MediaLocator, grants library.LibraryGrants, gate share.Gate, allowOrigin func(*http.Request) bool) *Hub {
	return newHub(loc, grants, gate, allowOrigin, nil, false)
}

func NewWithOriginCheckerAndDB(loc library.MediaLocator, grants library.LibraryGrants, gate share.Gate, allowOrigin func(*http.Request) bool, sqlDB *sql.DB, postgres bool) *Hub {
	return newHub(loc, grants, gate, allowOrigin, sqlDB, postgres)
}

func newHub(loc library.MediaLocator, grants library.LibraryGrants, gate share.Gate, allowOrigin func(*http.Request) bool, sqlDB *sql.DB, postgres bool) *Hub {
	if gate == nil {
		gate = share.NoopGate()
	}
	if allowOrigin == nil {
		allowOrigin = sameOrigin
	}
	h := &Hub{
		Locator: loc, Grants: grants, Gate: gate, AllowOrigin: allowOrigin,
		DB: sqlDB, Postgres: postgres,
		rooms: map[string]*Room{}, invites: map[string]string{}, tickets: map[string]ticket{},
	}
	h.restore(context.Background())
	go h.loop()
	return h
}

func (h *Hub) SetHardDriftMS(ms int) { h.hardDriftMS.Store(int64(ms)) }

func (h *Hub) driftLimit() int64 {
	if v := h.hardDriftMS.Load(); v > 0 {
		return v
	}
	return driftMS
}

func (h *Hub) enabled() bool { return h.Enabled == nil || h.Enabled() }

func writeDisabled(w http.ResponseWriter) {
	httpapi.WriteErr(w, http.StatusForbidden, "feature_disabled", "watch parties are turned off by the administrator")
}

func (h *Hub) Routes(r chi.Router) {
	r.Get("/watch-together/invites/{code}", h.handleInvite)
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireUserOrGuest)
		r.Post("/watch-together/rooms", h.handleCreate)
		r.Post("/watch-together/join", h.handleJoin)
		r.Post("/watch-together/rooms/{id}/ticket", h.handleTicket)
		r.Get("/watch-together/rooms/{id}/ws", h.handleWS)
		r.Post("/watch-together/rooms/{id}/queue", h.handleQueue)
		r.Post("/watch-together/rooms/{id}/vote", h.handleVote)
		r.Post("/watch-together/rooms/{id}/intermission", h.handleIntermission)
	})
}

func (h *Hub) authorize(ctx context.Context, p *auth.Principal, itemKind, itemID string) error {
	if p == nil {
		return errDenied
	}
	if p.IsUser() && p.PartyOnly {
		if h.PartyAccess(p.ID(), itemKind, itemID) {
			return nil
		}
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
		if h.DB != nil {
			if ok, err := library.ItemPermitted(ctx, h.DB, p.UserID, itemKind, itemID); err != nil || !ok {
				return errDenied
			}
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

// PartyAccess reports whether a member of some room may stream the item
// because it is that room's current title or is queued in it.
func (h *Hub) PartyAccess(principalID, itemKind, itemID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, room := range h.rooms {
		if room.Members[principalID] == nil {
			continue
		}
		if room.ItemKind == itemKind && room.ItemID == itemID {
			return true
		}
		for _, q := range room.Queue {
			if q.ItemKind == itemKind && q.ItemID == itemID {
				return true
			}
		}
	}
	return false
}

func (h *Hub) Create(ctx context.Context, p *auth.Principal, itemKind, itemID string) (*Room, error) {
	if p != nil && p.PartyOnly {
		return nil, errDenied
	}
	if err := h.authorize(ctx, p, itemKind, itemID); err != nil {
		return nil, err
	}
	room := &Room{
		ID: uuid.NewString(), InviteCode: randomCode(8),
		ItemKind: itemKind, ItemID: itemID,
		HostID: p.ID(), OwnerID: p.ID(), Members: map[string]*Member{}, Clock: time.Now(),
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
	h.persistRoom(ctx, room)
	return room, nil
}

func (h *Hub) addMember(room *Room, p *auth.Principal) *Member {
	room.EmptySince = time.Time{}
	m := room.Members[p.ID()]
	if m != nil {
		m.LastSeen = time.Now()
	} else {
		m = &Member{
			ID: p.ID(), Kind: p.Kind, DisplayName: displayName(p),
			GuestSessionID: p.GuestSessionID, LastSeen: time.Now(),
		}
		room.Members[m.ID] = m
	}
	if room.HostID == "" || (room.OwnerID != "" && m.ID == room.OwnerID) {
		room.HostID = m.ID
	}
	return m
}

// ReclaimOwner re-admits the room owner as a member (and so as host) when
// their membership lapsed, for example a host who created the party from
// Discord and never kept a browser open. It reports whether p is the owner
// and is still authorized for the room's title.
func (h *Hub) ReclaimOwner(ctx context.Context, p *auth.Principal, roomID string) bool {
	if p == nil || !p.IsUser() || p.PartyOnly {
		return false
	}
	h.mu.Lock()
	room := h.rooms[roomID]
	owner := room != nil && room.OwnerID != "" && room.OwnerID == p.ID() && !room.Banned[p.ID()]
	var kind, id string
	if owner {
		kind, id = room.ItemKind, room.ItemID
	}
	h.mu.Unlock()
	if !owner || h.authorize(ctx, p, kind, id) != nil {
		return false
	}
	h.mu.Lock()
	if h.rooms[roomID] != room {
		h.mu.Unlock()
		return false
	}
	h.addMember(room, p)
	h.mu.Unlock()
	h.persistRoom(ctx, room)
	return true
}

func (h *Hub) Join(ctx context.Context, p *auth.Principal, code string) (*Room, error) {
	h.mu.Lock()
	id := h.invites[code]
	room := h.rooms[id]
	banned := room != nil && p != nil && room.Banned[p.ID()]
	h.mu.Unlock()
	if room == nil || banned {
		return nil, errDenied
	}
	// For party-only accounts the invite code is the capability.
	if p == nil || !(p.IsUser() && p.PartyOnly) {
		if err := h.authorize(ctx, p, room.ItemKind, room.ItemID); err != nil {
			return nil, errDenied
		}
	}
	h.mu.Lock()
	h.addMember(room, p)
	h.mu.Unlock()
	h.persistRoom(ctx, room)
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
	banned := room != nil && p != nil && room.Banned[p.ID()]
	h.mu.Unlock()
	if room == nil || banned {
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
	return h.stateLocked(room, time.Now())
}

// stateLocked reports position_ms as of server_ms so clients can project the
// timeline with their estimated clock offset.
func (h *Hub) stateLocked(room *Room, now time.Time) map[string]any {
	return map[string]any{
		"type": "state", "room_id": room.ID, "host": room.HostID, "owner": room.OwnerID,
		"playing": room.Playing, "position_ms": room.expectedAt(now), "server_ms": now.UnixMilli(), "seq": room.Seq,
		"members": h.membersLocked(room, now), "item_kind": room.ItemKind, "item_id": room.ItemID,
		"queue": room.Queue, "intermission_until": room.IntermissionUntil,
		"votes": voteCounts(room.Votes), "shared_control": room.SharedControl,
		"panel": normalPanel(room.Panel), "sync": h.syncInfoLocked(room, now),
	}
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
	if !h.enabled() {
		writeDisabled(w)
		return
	}
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
	out := map[string]any{"needs_auth": false, "needs_share": false, "code": room.InviteCode}
	if p != nil && (p.IsUser() || p.IsGuest()) {
		out["room_id"] = room.ID
		out["item_kind"] = room.ItemKind
		out["item_id"] = room.ItemID
	}
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
	if !h.enabled() {
		writeDisabled(w)
		return
	}
	p := auth.FromRequest(r)
	var body struct {
		InviteCode string `json:"invite_code"`
		Code       string `json:"code"`
	}
	_ = readJSON(r, &body)
	code := strings.TrimSpace(body.InviteCode)
	if code == "" {
		code = strings.TrimSpace(body.Code)
	}
	room, err := h.Join(r.Context(), p, code)
	if err != nil {
		writeDenied(w)
		return
	}
	httpapi.WriteJSON(w, 200, map[string]any{"room_id": room.ID, "item_kind": room.ItemKind, "item_id": room.ItemID})
}

func (h *Hub) handleTicket(w http.ResponseWriter, r *http.Request) {
	p := auth.FromRequest(r)
	id := chi.URLParam(r, "id")
	raw, err := h.MintTicket(p, id)
	if err != nil {
		writeDenied(w)
		return
	}
	httpapi.WriteJSON(w, 200, map[string]string{
		"ticket":    raw,
		"member_id": p.ID(),
		"ws_url":    "/api/v1/watch-together/rooms/" + url.PathEscape(id) + "/ws?ticket=" + url.QueryEscape(raw),
	})
}
