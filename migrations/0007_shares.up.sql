CREATE TABLE shares (
    id              TEXT PRIMARY KEY,
    token_hash      TEXT NOT NULL UNIQUE,
    item_kind       TEXT NOT NULL,
    item_id         TEXT NOT NULL,
    created_by      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    password_hash   TEXT NOT NULL DEFAULT '',
    expires_at      TEXT,
    revoked_at      TEXT,
    max_concurrent  INTEGER NOT NULL DEFAULT 0,
    allowed_quality TEXT NOT NULL DEFAULT '',
    allow_download  INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL
);
CREATE INDEX shares_item ON shares(item_kind, item_id);

CREATE TABLE guest_sessions (
    id           TEXT PRIMARY KEY,
    share_id     TEXT NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,
    created_at   TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    expires_at   TEXT NOT NULL,
    ip           TEXT NOT NULL DEFAULT ''
);
CREATE INDEX guest_sessions_share ON guest_sessions(share_id);
