package watchtogether

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/httpapi"
)

// Who sees the party panel in the player.
const (
	PanelEveryone = "everyone"
	PanelHost     = "host"
	PanelHidden   = "hidden"
)

var validPanels = map[string]bool{PanelEveryone: true, PanelHost: true, PanelHidden: true}

func normalPanel(p string) string {
	if validPanels[p] {
		return p
	}
	return PanelEveryone
}

// Kick codes sent to clients before their connection is closed.
const (
	KickRemoved = "removed_by_admin"
	KickEnded   = "party_ended"
)

// ErrNotFound means the room or member does not exist.
var ErrNotFound = errors.New("not found")

// AdminMember is a member as administrators see it.
type AdminMember struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Host        bool   `json:"host"`
	Owner       bool   `json:"owner"`
	Connected   bool   `json:"connected"`
	Ready       bool   `json:"ready"`
	Buffering   bool   `json:"buffering"`
	DriftMS     int64  `json:"drift_ms"`
	LastSeen    string `json:"last_seen"`
}

// AdminRoom is a room as administrators see it.
type AdminRoom struct {
	ID             string        `json:"id"`
	InviteCode     string        `json:"invite_code"`
	ItemKind       string        `json:"item_kind"`
	ItemID         string        `json:"item_id"`
	Title          string        `json:"title"`
	Playing        bool          `json:"playing"`
	PositionMS     int64         `json:"position_ms"`
	SharedControl  bool          `json:"shared_control"`
	Panel          string        `json:"panel"`
	Banned         int           `json:"banned"`
	Members        []AdminMember `json:"members"`
	DiscordChannel string        `json:"discord_channel_id,omitempty"`
	DiscordGuild   string        `json:"discord_guild_id,omitempty"`
}

// AdminRooms lists every room with its members, newest activity first.
func (h *Hub) AdminRooms() []AdminRoom {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]AdminRoom, 0, len(h.rooms))
	for _, room := range h.rooms {
		ar := AdminRoom{
			ID: room.ID, InviteCode: room.InviteCode, ItemKind: room.ItemKind, ItemID: room.ItemID,
			Playing: room.Playing, PositionMS: room.expectedAt(now), SharedControl: room.SharedControl,
			Panel: normalPanel(room.Panel), Banned: len(room.Banned), Members: []AdminMember{},
		}
		for _, m := range room.Members {
			ar.Members = append(ar.Members, AdminMember{
				ID: m.ID, Kind: m.Kind, DisplayName: m.DisplayName,
				Host: m.ID == room.HostID, Owner: m.ID == room.OwnerID, Connected: m.Conn != nil,
				Ready: m.Ready, Buffering: m.rep.Buffering, DriftMS: m.DriftMS,
				LastSeen: m.LastSeen.UTC().Format(time.RFC3339),
			})
		}
		sort.Slice(ar.Members, func(i, j int) bool { return ar.Members[i].DisplayName < ar.Members[j].DisplayName })
		out = append(out, ar)
	}
	sort.Slice(out, func(i, j int) bool {
		return len(out[i].Members) > len(out[j].Members) || (len(out[i].Members) == len(out[j].Members) && out[i].ID < out[j].ID)
	})
	return out
}

// Kick removes a member from a room and tells their client why. With ban
// the principal cannot rejoin that room.
func (h *Hub) Kick(roomID, memberID string, ban bool) error {
	now := time.Now()
	h.mu.Lock()
	room := h.rooms[roomID]
	if room == nil {
		h.mu.Unlock()
		return ErrNotFound
	}
	m := room.Members[memberID]
	if m == nil && !ban {
		h.mu.Unlock()
		return ErrNotFound
	}
	if ban {
		if room.Banned == nil {
			room.Banned = map[string]bool{}
		}
		room.Banned[memberID] = true
	}
	var s *sock
	if m != nil {
		s, _ = m.Conn.(*sock)
		h.dropLocked(room, memberID, false)
		room.Seq++
	}
	snap := snapshotLocked(room)
	st := h.stateLocked(room, now)
	h.mu.Unlock()
	if s != nil {
		s.send(map[string]string{"type": "kicked", "code": KickRemoved})
		_ = s.Close()
	}
	h.save(context.Background(), snap)
	h.broadcast(roomID, st)
	return nil
}

// EndRoom closes a room for everyone.
func (h *Hub) EndRoom(roomID string) error {
	h.mu.Lock()
	room := h.rooms[roomID]
	if room == nil {
		h.mu.Unlock()
		return ErrNotFound
	}
	var conns []*sock
	for _, m := range room.Members {
		if s, ok := m.Conn.(*sock); ok && s != nil {
			conns = append(conns, s)
		}
	}
	delete(h.rooms, roomID)
	delete(h.invites, room.InviteCode)
	h.mu.Unlock()
	for _, s := range conns {
		s.send(map[string]string{"type": "kicked", "code": KickEnded})
		_ = s.Close()
	}
	h.deleteRoom(context.Background(), roomID)
	return nil
}

func (h *Hub) handleInternalAdminRooms(w http.ResponseWriter, _ *http.Request) {
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"items": h.AdminRooms()})
}

func (h *Hub) handleInternalKick(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeCoord(w, r)
	if !ok {
		return
	}
	if err := h.Kick(chi.URLParam(r, "id"), in.PrincipalID, in.Ban); err != nil {
		httpapi.WriteErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) handleInternalEnd(w http.ResponseWriter, r *http.Request) {
	if err := h.EndRoom(chi.URLParam(r, "id")); err != nil {
		httpapi.WriteErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AdminRooms returns nil when the coordinator is unreachable.
func (c *Remote) AdminRooms() []AdminRoom {
	var out struct {
		Items []AdminRoom `json:"items"`
	}
	st, err := c.call(context.Background(), http.MethodGet, "/admin-rooms", nil, &out)
	if err != nil || st != http.StatusOK {
		return nil
	}
	if out.Items == nil {
		out.Items = []AdminRoom{}
	}
	return out.Items
}

func (c *Remote) Kick(roomID, memberID string, ban bool) error {
	st, err := c.call(context.Background(), http.MethodPost, "/rooms/"+url.PathEscape(roomID)+"/kick",
		coordRequest{PrincipalID: memberID, Ban: ban}, nil)
	if err != nil {
		return err
	}
	if st != http.StatusNoContent {
		return ErrNotFound
	}
	return nil
}

func (c *Remote) EndRoom(roomID string) error {
	st, err := c.call(context.Background(), http.MethodDelete, "/rooms/"+url.PathEscape(roomID), nil, nil)
	if err != nil {
		return err
	}
	if st != http.StatusNoContent {
		return ErrNotFound
	}
	return nil
}

// DiscordLink is the voice channel a room is linked to.
type DiscordLink struct {
	ChannelID, GuildID, Title string
}

// AdminAPI serves the administrator view of watch parties, including the
// ones running in the Discord Activity.
type AdminAPI struct {
	Parties Coordinator
	Audit   *audit.Log
	Cfg     config.Config
	// Title names a room's current title; optional.
	Title func(ctx context.Context, kind, id string) string
	// DiscordLinks maps room IDs to their Discord voice channel; optional.
	DiscordLinks func(ctx context.Context) map[string]DiscordLink
}

func (a *AdminAPI) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequirePerm(auth.PermUsersManage))
		r.Get("/admin/watch-parties", a.handleList)
		r.Post("/admin/watch-parties/{id}/members/{member}/kick", a.handleKick)
		r.Delete("/admin/watch-parties/{id}", a.handleEnd)
	})
}

func (a *AdminAPI) handleList(w http.ResponseWriter, r *http.Request) {
	rooms := a.Parties.AdminRooms()
	if rooms == nil {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "coordinator_unavailable", ErrUnavailable.Error())
		return
	}
	var links map[string]DiscordLink
	if a.DiscordLinks != nil {
		links = a.DiscordLinks(r.Context())
	}
	for i := range rooms {
		if l, ok := links[rooms[i].ID]; ok {
			rooms[i].DiscordChannel, rooms[i].DiscordGuild, rooms[i].Title = l.ChannelID, l.GuildID, l.Title
		}
		if rooms[i].Title == "" && a.Title != nil {
			rooms[i].Title = a.Title(r.Context(), rooms[i].ItemKind, rooms[i].ItemID)
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"items": rooms})
}

func (a *AdminAPI) handleKick(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Ban bool `json:"ban"`
	}
	if r.ContentLength != 0 {
		if err := httpapi.ReadJSON(r, &body); err != nil {
			httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
			return
		}
	}
	roomID, member := chi.URLParam(r, "id"), strings.TrimSpace(chi.URLParam(r, "member"))
	if err := a.Parties.Kick(roomID, member, body.Ban); err != nil {
		a.writeErr(w, err)
		return
	}
	action := "watch_party.kick"
	if body.Ban {
		action = "watch_party.ban"
	}
	a.audit(r, action, roomID, "member="+member)
	w.WriteHeader(http.StatusNoContent)
}

func (a *AdminAPI) handleEnd(w http.ResponseWriter, r *http.Request) {
	roomID := chi.URLParam(r, "id")
	if err := a.Parties.EndRoom(roomID); err != nil {
		a.writeErr(w, err)
		return
	}
	a.audit(r, "watch_party.end", roomID, "")
	w.WriteHeader(http.StatusNoContent)
}

func (a *AdminAPI) writeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrUnavailable) {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "coordinator_unavailable", ErrUnavailable.Error())
		return
	}
	httpapi.WriteErr(w, http.StatusNotFound, "not_found", "that party or member no longer exists")
}

func (a *AdminAPI) audit(r *http.Request, action, target, detail string) {
	if a.Audit == nil {
		return
	}
	actor := ""
	if p := auth.FromRequest(r); p != nil {
		actor = p.UserID
	}
	a.Audit.Event(r.Context(), actor, action, target, httpapi.ClientIPString(r, a.Cfg), detail)
}
