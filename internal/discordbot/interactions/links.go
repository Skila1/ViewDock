package interactions

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Link kinds.
const (
	LinkText  = "text"
	LinkVoice = "voice"
)

// Link ties a Discord channel (the text channel a party was created in, or a
// voice channel) to a ViewDock watch room.
type Link struct {
	ChannelID     string    `json:"channel_id"`
	GuildID       string    `json:"guild_id"`
	Kind          string    `json:"kind"`
	RoomID        string    `json:"room_id"`
	InviteCode    string    `json:"invite_code"`
	Title         string    `json:"title"`
	LinkedBy      string    `json:"linked_by"`
	DiscordUserID string    `json:"discord_user_id"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// LinkStore persists channel links (migration 0027).
type LinkStore struct{ DB *sql.DB }

func NewLinkStore(db *sql.DB) *LinkStore { return &LinkStore{DB: db} }

var ErrNoLink = errors.New("no watch party is linked to this channel")

func (s *LinkStore) Put(ctx context.Context, l Link) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO discord_channel_links(channel_id, guild_id, kind, room_id, invite_code, title, linked_by, discord_user_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(channel_id) DO UPDATE SET
			guild_id = excluded.guild_id,
			kind = excluded.kind,
			room_id = excluded.room_id,
			invite_code = excluded.invite_code,
			title = excluded.title,
			linked_by = excluded.linked_by,
			discord_user_id = excluded.discord_user_id,
			updated_at = excluded.updated_at
	`, l.ChannelID, l.GuildID, l.Kind, l.RoomID, l.InviteCode, l.Title, l.LinkedBy, l.DiscordUserID, now, now)
	return err
}

const linkColumns = `channel_id, guild_id, kind, room_id, invite_code, title, linked_by, discord_user_id, created_at, updated_at`

func scanLink(scan func(...any) error) (Link, error) {
	var l Link
	var created, updated string
	if err := scan(&l.ChannelID, &l.GuildID, &l.Kind, &l.RoomID, &l.InviteCode, &l.Title, &l.LinkedBy, &l.DiscordUserID, &created, &updated); err != nil {
		return Link{}, err
	}
	l.CreatedAt, _ = time.Parse(time.RFC3339, created)
	l.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return l, nil
}

func (s *LinkStore) Get(ctx context.Context, channelID string) (Link, error) {
	l, err := scanLink(s.DB.QueryRowContext(ctx, `SELECT `+linkColumns+` FROM discord_channel_links WHERE channel_id = ?`, channelID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, ErrNoLink
	}
	return l, err
}

func (s *LinkStore) List(ctx context.Context) ([]Link, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+linkColumns+` FROM discord_channel_links ORDER BY updated_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Link{}
	for rows.Next() {
		l, err := scanLink(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *LinkStore) Delete(ctx context.Context, channelID string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM discord_channel_links WHERE channel_id = ?`, channelID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteRoom removes every link to a room that no longer exists.
func (s *LinkStore) DeleteRoom(ctx context.Context, roomID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM discord_channel_links WHERE room_id = ?`, roomID)
	return err
}
