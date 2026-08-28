-- Auth: users, sessions, invites, audit, prefs, favourites. No progress tables.

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    display_name  TEXT NOT NULL DEFAULT '',
    email         TEXT NOT NULL DEFAULT '',
    is_admin      INTEGER NOT NULL DEFAULT 0,
    disabled      INTEGER NOT NULL DEFAULT 0,
    pin_hash      TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE TABLE sessions (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,
    expires_at   TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    ip           TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user_id ON sessions(user_id);
CREATE INDEX sessions_expires ON sessions(expires_at);

CREATE TABLE invites (
    id          TEXT PRIMARY KEY,
    token_hash  TEXT NOT NULL UNIQUE,
    created_by  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  TEXT NOT NULL,
    used_by     TEXT REFERENCES users(id) ON DELETE SET NULL,
    used_at     TEXT,
    is_admin    INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL
);

-- library_id has no FK until 0003/0004 exist.
CREATE TABLE invite_library_grants (
    invite_id    TEXT NOT NULL REFERENCES invites(id) ON DELETE CASCADE,
    library_id   TEXT NOT NULL,
    can_download INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (invite_id, library_id)
);

CREATE TABLE audit_events (
    id         TEXT PRIMARY KEY,
    at         TEXT NOT NULL,
    actor_id   TEXT NOT NULL DEFAULT '',
    action     TEXT NOT NULL,
    target     TEXT NOT NULL DEFAULT '',
    ip         TEXT NOT NULL DEFAULT '',
    detail     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_events_at ON audit_events(at);

CREATE TABLE user_preferences (
    user_id        TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    audio_lang     TEXT NOT NULL DEFAULT '',
    subtitle_lang  TEXT NOT NULL DEFAULT '',
    subtitle_mode  TEXT NOT NULL DEFAULT 'auto',
    autoplay       INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE favourites (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_kind  TEXT NOT NULL,
    item_id    TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (user_id, item_kind, item_id)
);
