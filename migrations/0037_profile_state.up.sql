-- Hidden from Continue Watching without being marked watched; cleared when
-- the title is played again.
ALTER TABLE playback_progress ADD COLUMN dismissed INTEGER NOT NULL DEFAULT 0;

-- Per-profile playback defaults.
ALTER TABLE user_preferences ADD COLUMN playback_rate REAL NOT NULL DEFAULT 1;
ALTER TABLE user_preferences ADD COLUMN quality TEXT NOT NULL DEFAULT '';
ALTER TABLE user_preferences ADD COLUMN upnext_seconds INTEGER NOT NULL DEFAULT 10;
ALTER TABLE user_preferences ADD COLUMN source_pref TEXT NOT NULL DEFAULT '';
ALTER TABLE user_preferences ADD COLUMN home_rows TEXT NOT NULL DEFAULT '';

-- The audio and subtitle tracks a profile picked for a movie or a whole
-- series. audio_index -1 and subtitle_index -2 mean "not chosen"; subtitle
-- -1 means subtitles off.
CREATE TABLE title_track_prefs (
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope_kind     TEXT NOT NULL,
    scope_id       TEXT NOT NULL,
    audio_index    INTEGER NOT NULL DEFAULT -1,
    subtitle_index INTEGER NOT NULL DEFAULT -2,
    updated_at     TEXT NOT NULL,
    PRIMARY KEY (user_id, scope_kind, scope_id)
);

-- Profile-owned playlists, separate from the server's collections.
CREATE TABLE user_playlists (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX user_playlists_user ON user_playlists(user_id);

CREATE TABLE user_playlist_items (
    playlist_id TEXT NOT NULL REFERENCES user_playlists(id) ON DELETE CASCADE,
    item_kind   TEXT NOT NULL,
    item_id     TEXT NOT NULL,
    position    INTEGER NOT NULL DEFAULT 0,
    added_at    TEXT NOT NULL,
    PRIMARY KEY (playlist_id, item_kind, item_id)
);
