package watchtogether

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/viewdock/viewdock/internal/auth"
)

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

func (h *Hub) handleWS(w http.ResponseWriter, r *http.Request) {
	if h.AllowOrigin == nil || !h.AllowOrigin(r) {
		writeDenied(w)
		return
	}
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
	upgrader := websocket.Upgrader{CheckOrigin: h.AllowOrigin}
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

	if out, ok := h.handle(roomID, p.ID(), clientMsg{Type: "hello"}, time.Now()); ok {
		s.send(out.reply)
		h.broadcast(roomID, h.Presence(roomID))
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
			h.broadcast(roomID, h.Presence(roomID))
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
		out, ok := h.handle(roomID, p.ID(), msg, time.Now())
		if !ok {
			s.send(map[string]string{"type": "kicked", "code": "not_member"})
			return
		}
		if out.reply != nil {
			s.send(out.reply)
		}
		if out.all != nil {
			h.broadcast(roomID, out.all)
		}
	}
}

func sameOrigin(r *http.Request) bool {
	origin := strings.TrimRight(strings.TrimSpace(r.Header.Get("Origin")), "/")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	expected := scheme + "://" + r.Host
	return strings.EqualFold(origin, expected)
}

func (h *Hub) Presence(roomID string) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[roomID]
	if room == nil {
		return nil
	}
	return h.presenceLocked(room, time.Now())
}

func (h *Hub) broadcast(roomID string, payload any) {
	if payload == nil {
		return
	}
	if m, ok := payload.(map[string]any); ok && m == nil {
		return
	}
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
