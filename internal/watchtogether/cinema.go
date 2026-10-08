package watchtogether

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
)

func (h *Hub) hostRoom(r *http.Request) (*Room, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[chi.URLParam(r, "id")]
	p := auth.FromRequest(r)
	return room, room != nil && p != nil && p.ID() == room.HostID
}

func (h *Hub) memberRoom(r *http.Request, p *auth.Principal) (*Room, bool) {
	if p == nil {
		return nil, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[chi.URLParam(r, "id")]
	if room == nil {
		return nil, false
	}
	_, ok := room.Members[p.ID()]
	return room, ok
}

func (h *Hub) handleQueue(w http.ResponseWriter, r *http.Request) {
	room, ok := h.hostRoom(r)
	if !ok {
		writeDenied(w)
		return
	}
	var item QueueItem
	if json.NewDecoder(r.Body).Decode(&item) != nil || item.ItemKind == "" || item.ItemID == "" {
		httpapi.WriteErr(w, 400, "cinema", "item_kind and item_id required")
		return
	}
	if err := h.authorize(r.Context(), auth.FromRequest(r), item.ItemKind, item.ItemID); err != nil {
		writeDenied(w)
		return
	}
	h.mu.Lock()
	room.Queue = append(room.Queue, item)
	room.Seq++
	h.mu.Unlock()
	h.persistRoom(r.Context(), room)
	h.publish(w, room.ID)
}

func (h *Hub) handleVote(w http.ResponseWriter, r *http.Request) {
	p := auth.FromRequest(r)
	room, ok := h.memberRoom(r, p)
	if !ok {
		writeDenied(w)
		return
	}
	var body struct {
		ItemID string `json:"item_id"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.ItemID == "" {
		httpapi.WriteErr(w, 400, "cinema", "item_id is required")
		return
	}
	h.mu.Lock()
	if room.Votes == nil {
		room.Votes = map[string]map[string]bool{}
	}
	if room.Votes[body.ItemID] == nil {
		room.Votes[body.ItemID] = map[string]bool{}
	}
	room.Votes[body.ItemID][p.ID()] = true
	room.Seq++
	h.mu.Unlock()
	h.persistRoom(r.Context(), room)
	h.publish(w, room.ID)
}

func (h *Hub) handleIntermission(w http.ResponseWriter, r *http.Request) {
	room, ok := h.hostRoom(r)
	if !ok {
		writeDenied(w)
		return
	}
	var body struct {
		Seconds int `json:"seconds"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Seconds < 0 || body.Seconds > 3600 {
		httpapi.WriteErr(w, 400, "cinema", "seconds must be between 0 and 3600")
		return
	}
	h.mu.Lock()
	if body.Seconds == 0 {
		room.IntermissionUntil = time.Time{}
	} else {
		room.IntermissionUntil = time.Now().Add(time.Duration(body.Seconds) * time.Second)
	}
	room.Seq++
	h.mu.Unlock()
	h.persistRoom(r.Context(), room)
	h.publish(w, room.ID)
}

// publish answers the request with the new room state and pushes the same
// state to every connected member.
func (h *Hub) publish(w http.ResponseWriter, roomID string) {
	st := h.State(roomID)
	h.broadcast(roomID, st)
	httpapi.WriteJSON(w, 200, st)
}

func voteCounts(votes map[string]map[string]bool) map[string]int {
	out := map[string]int{}
	for item, members := range votes {
		out[item] = len(members)
	}
	return out
}
