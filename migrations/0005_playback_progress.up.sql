CREATE TABLE playback_progress (
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_kind    TEXT NOT NULL,
    item_id      TEXT NOT NULL,
    media_file_id TEXT NOT NULL DEFAULT '',
    position_ms  INTEGER NOT NULL DEFAULT 0,
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    completed    INTEGER NOT NULL DEFAULT 0,
    updated_at   TEXT NOT NULL,
    PRIMARY KEY (user_id, item_kind, item_id)
);
CREATE INDEX playback_progress_user ON playback_progress(user_id, updated_at);

CREATE TABLE watch_history (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_kind    TEXT NOT NULL,
    item_id      TEXT NOT NULL,
    watched_at   TEXT NOT NULL,
    position_ms  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX watch_history_user ON watch_history(user_id, watched_at);
