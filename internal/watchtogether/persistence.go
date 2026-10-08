package watchtogether

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"sort"
	"time"

	"github.com/viewdock/viewdock/internal/db"
)

const persistTimeout = 3 * time.Second

func (h *Hub) restore(ctx context.Context) {
	if h.DB == nil {
		return
	}
	rows, err := h.queryContext(ctx, `SELECT id, invite_code, item_kind, item_id, share_path, host_id, owner_id, playing, position_ms, clock, sequence, queue_json, intermission_until, shared_control, panel, banned_json FROM watch_rooms`)
	if err != nil {
		slog.Warn("watch party restore", "err", err)
		return
	}
	var rooms []*Room
	for rows.Next() {
		room := &Room{}
		var playing, shared int
		var clock string
		var queueJSON, intermission, bannedJSON string
		if err := rows.Scan(&room.ID, &room.InviteCode, &room.ItemKind, &room.ItemID, &room.SharePath, &room.HostID, &room.OwnerID, &playing, &room.PositionMS, &clock, &room.Seq, &queueJSON, &intermission, &shared, &room.Panel, &bannedJSON); err != nil {
			slog.Warn("watch party restore row", "err", err)
			continue
		}
		if room.OwnerID == "" {
			room.OwnerID = room.HostID
		}
		room.Playing = playing == 1
		room.SharedControl = shared == 1
		room.Clock, _ = time.Parse(time.RFC3339Nano, clock)
		if room.Clock.IsZero() {
			room.Clock = time.Now()
		}
		room.Members = map[string]*Member{}
		room.Votes = map[string]map[string]bool{}
		room.Panel = normalPanel(room.Panel)
		var banned []string
		_ = json.Unmarshal([]byte(bannedJSON), &banned)
		for _, id := range banned {
			if room.Banned == nil {
				room.Banned = map[string]bool{}
			}
			room.Banned[id] = true
		}
		_ = json.Unmarshal([]byte(queueJSON), &room.Queue)
		room.IntermissionUntil, _ = time.Parse(time.RFC3339Nano, intermission)
		rooms = append(rooms, room)
	}
	_ = rows.Close()
	for _, room := range rooms {
		memberRows, err := h.queryContext(ctx, `SELECT member_id, kind, display_name, guest_session_id, ready, last_seen FROM watch_room_members WHERE room_id = ?`, room.ID)
		if err == nil {
			for memberRows.Next() {
				member := &Member{}
				var ready int
				var lastSeen string
				if memberRows.Scan(&member.ID, &member.Kind, &member.DisplayName, &member.GuestSessionID, &ready, &lastSeen) == nil {
					member.Ready = ready == 1
					member.LastSeen, _ = time.Parse(time.RFC3339Nano, lastSeen)
					if member.LastSeen.IsZero() {
						member.LastSeen = time.Now()
					}
					room.Members[member.ID] = member
				}
			}
			_ = memberRows.Close()
		}
		h.rooms[room.ID] = room
		h.invites[room.InviteCode] = room.ID
	}
}

type memberRow struct {
	ID, Kind, DisplayName, GuestSessionID string
	Ready                                 bool
	LastSeen                              time.Time
}

// roomSnapshot is a copy of the durable room fields so the database write can
// happen without holding the hub lock.
type roomSnapshot struct {
	ID, InviteCode, ItemKind, ItemID, SharePath, HostID, OwnerID, Panel string
	Playing, SharedControl                                              bool
	PositionMS, Seq                                                     int64
	Clock, IntermissionUntil                                            time.Time
	Queue                                                               []QueueItem
	Banned                                                              []string
	Members                                                             []memberRow
}

func snapshotLocked(room *Room) *roomSnapshot {
	s := &roomSnapshot{
		ID: room.ID, InviteCode: room.InviteCode, ItemKind: room.ItemKind, ItemID: room.ItemID,
		SharePath: room.SharePath, HostID: room.HostID, OwnerID: room.OwnerID, Playing: room.Playing, SharedControl: room.SharedControl,
		Panel:      normalPanel(room.Panel),
		PositionMS: room.PositionMS, Seq: room.Seq, Clock: room.Clock, IntermissionUntil: room.IntermissionUntil,
		Queue:  append([]QueueItem(nil), room.Queue...),
		Banned: []string{},
	}
	for id := range room.Banned {
		s.Banned = append(s.Banned, id)
	}
	sort.Strings(s.Banned)
	for _, m := range room.Members {
		s.Members = append(s.Members, memberRow{m.ID, m.Kind, m.DisplayName, m.GuestSessionID, m.Ready, m.LastSeen})
	}
	return s
}

// persistRoom must be called without h.mu held.
func (h *Hub) persistRoom(ctx context.Context, room *Room) {
	if h.DB == nil || room == nil {
		return
	}
	h.mu.Lock()
	snap := snapshotLocked(room)
	h.mu.Unlock()
	h.save(ctx, snap)
}

func (h *Hub) save(ctx context.Context, s *roomSnapshot) {
	if h.DB == nil || s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), persistTimeout)
	defer cancel()
	if err := h.saveTx(ctx, s); err != nil {
		slog.Warn("watch party save", "room", s.ID, "err", err)
	}
}

func (h *Hub) saveTx(ctx context.Context, s *roomSnapshot) error {
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	queueJSON, _ := json.Marshal(s.Queue)
	bannedJSON, _ := json.Marshal(s.Banned)
	if _, err := tx.ExecContext(ctx, h.query(`
		INSERT INTO watch_rooms(id, invite_code, item_kind, item_id, share_path, host_id, owner_id, playing, position_ms, clock, sequence, created_at, updated_at, queue_json, intermission_until, shared_control, panel, banned_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET invite_code = excluded.invite_code, share_path = excluded.share_path,
			host_id = excluded.host_id, owner_id = excluded.owner_id, playing = excluded.playing, position_ms = excluded.position_ms,
			clock = excluded.clock, sequence = excluded.sequence, updated_at = excluded.updated_at,
			queue_json = excluded.queue_json, intermission_until = excluded.intermission_until,
			shared_control = excluded.shared_control, panel = excluded.panel, banned_json = excluded.banned_json
	`), s.ID, s.InviteCode, s.ItemKind, s.ItemID, s.SharePath, s.HostID, s.OwnerID, boolInt(s.Playing), s.PositionMS,
		s.Clock.UTC().Format(time.RFC3339Nano), s.Seq, now, now, string(queueJSON),
		s.IntermissionUntil.UTC().Format(time.RFC3339Nano), boolInt(s.SharedControl), s.Panel, string(bannedJSON)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, h.query(`DELETE FROM watch_room_members WHERE room_id = ?`), s.ID); err != nil {
		return err
	}
	for _, m := range s.Members {
		if _, err := tx.ExecContext(ctx, h.query(`
			INSERT INTO watch_room_members(room_id, member_id, kind, display_name, guest_session_id, ready, last_seen)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`), s.ID, m.ID, m.Kind, m.DisplayName, m.GuestSessionID, boolInt(m.Ready), m.LastSeen.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (h *Hub) deleteRoom(ctx context.Context, id string) {
	if h.DB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), persistTimeout)
	defer cancel()
	if _, err := h.execContext(ctx, `DELETE FROM watch_room_members WHERE room_id = ?`, id); err != nil {
		slog.Warn("watch party delete members", "room", id, "err", err)
		return
	}
	if _, err := h.execContext(ctx, `DELETE FROM watch_rooms WHERE id = ?`, id); err != nil {
		slog.Warn("watch party delete", "room", id, "err", err)
	}
}

func (h *Hub) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return h.DB.QueryContext(ctx, h.query(query), args...)
}

func (h *Hub) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return h.DB.ExecContext(ctx, h.query(query), args...)
}

func (h *Hub) query(query string) string {
	if !h.Postgres {
		return query
	}
	return db.RewritePlaceholders(query)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
