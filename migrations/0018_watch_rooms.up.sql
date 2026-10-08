CREATE TABLE IF NOT EXISTS watch_rooms (
    id TEXT PRIMARY KEY,
    invite_code TEXT NOT NULL UNIQUE,
    item_kind TEXT NOT NULL,
    item_id TEXT NOT NULL,
    share_path TEXT NOT NULL DEFAULT '',
    host_id TEXT NOT NULL DEFAULT '',
    playing INTEGER NOT NULL DEFAULT 0,
    position_ms BIGINT NOT NULL DEFAULT 0,
    clock TEXT NOT NULL,
    sequence BIGINT NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS watch_room_members (
    room_id TEXT NOT NULL REFERENCES watch_rooms(id) ON DELETE CASCADE,
    member_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    guest_session_id TEXT NOT NULL DEFAULT '',
    ready INTEGER NOT NULL DEFAULT 0,
    last_seen TEXT NOT NULL,
    PRIMARY KEY (room_id, member_id)
);
CREATE INDEX IF NOT EXISTS watch_rooms_updated ON watch_rooms(updated_at);