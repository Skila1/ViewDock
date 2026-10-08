CREATE TABLE IF NOT EXISTS discord_channel_links (
    channel_id TEXT PRIMARY KEY,
    guild_id TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL DEFAULT 'text',
    room_id TEXT NOT NULL,
    invite_code TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    linked_by TEXT NOT NULL,
    discord_user_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS discord_channel_links_room ON discord_channel_links(room_id);
