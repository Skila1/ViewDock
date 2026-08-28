package watchtogether

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/viewdock/viewdock/internal/auth"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type sock struct {
	c  *websocket.Conn
	mu sync.Mutex
}

func (s *sock) Close() error {
	if s == nil || s.c == nil {
		return nil
	}
	return s.c.Close()
}

func (s *sock) send(v any) {
	if s == nil || s.c == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = s.c.WriteJSON(v)
}

type clientMsg struct {
	Type       string `json:"type"`
	PositionMS int64  `json:"position_ms"`
	Text       string `json:"text"`
	Emoji      string `json:"emoji"`
}

func (h *Hub) handleWS(w http.ResponseWriter, r *http.Request) {
	p := auth.FromRequest(r)
	roomID := chi.URLParam(r, "id")
	tok := r.URL.Query().Get("ticket")
	h.mu.Lock()
	t, ok := h.tickets[tok]
	room := h.rooms[roomID]
	h.mu.Unlock()
	if !ok || time.Now().After(t.Exp) || t.RoomID != roomID || p == nil || t.PrincipalID != p.ID() || room == nil {
		writeDenied(w)
		return
	}
	if err := h.CheckGate(r.Context(), room, p); err != nil {
		writeDenied(w)
		return
	}
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s := &sock{c: c}
	defer s.Close()

	h.mu.Lock()
	if m := room.Members[p.ID()]; m != nil {
		m.Conn = s
		m.LastSeen = time.Now()
	}
	h.mu.Unlock()

	if st, _ := h.Apply(roomID, p.ID(), "hello", 0, "", ""); st != nil {
		s.send(st)
	}

	c.SetReadLimit(64 << 10)
	_ = c.SetReadDeadline(time.Now().Add(memberLease))
	c.SetPongHandler(func(string) error {
		_ = c.SetReadDeadline(time.Now().Add(memberLease))
		return nil
	})

	for {
		_, data, err := c.ReadMessage()
		if err != nil {
			h.mu.Lock()
			if m := room.Members[p.ID()]; m != nil && m.Conn == s {
				m.Conn = nil
			}
			h.mu.Unlock()
			return
		}
		_ = c.SetReadDeadline(time.Now().Add(memberLease))
		if p.IsGuest() {
			if err := h.CheckGate(r.Context(), room, p); err != nil {
				s.send(map[string]string{"type": "kicked", "code": "share_revoked"})
				h.KickGuest(p.GuestSessionID)
				return
			}
		}
		var msg clientMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		out, ok := h.Apply(roomID, p.ID(), msg.Type, msg.PositionMS, msg.Text, msg.Emoji)
		if !ok {
			return
		}
		if out != nil {
			h.broadcast(roomID, out)
		}
	}
}

func (h *Hub) broadcast(roomID string, payload any) {
	h.mu.Lock()
	room := h.rooms[roomID]
	var conns []*sock
	if room != nil {
		for _, m := range room.Members {
			if s, ok := m.Conn.(*sock); ok && s != nil {
				conns = append(conns, s)
			}
		}
	}
	h.mu.Unlock()
	for _, s := range conns {
		s.send(payload)
	}
}
