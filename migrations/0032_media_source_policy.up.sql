-- auth_mode is 'password' (a Jellyfin account) or 'api_key' (a Jellyfin API
-- key browsing as remote_user). policy holds the usage restrictions ViewDock
-- enforces on itself. api_key_enc is encrypted like the password.
ALTER TABLE media_sources ADD COLUMN auth_mode TEXT NOT NULL DEFAULT 'password';
ALTER TABLE media_sources ADD COLUMN api_key_enc TEXT NOT NULL DEFAULT '';
ALTER TABLE media_sources ADD COLUMN remote_user_name TEXT NOT NULL DEFAULT '';
ALTER TABLE media_sources ADD COLUMN policy TEXT NOT NULL DEFAULT '{}';

-- media_source_events records what ViewDock did with a source, without
-- credentials, for the admin usage log.
CREATE TABLE media_source_events (
    id         TEXT PRIMARY KEY,
    source_id  TEXT NOT NULL REFERENCES media_sources(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    ok         INTEGER NOT NULL DEFAULT 1,
    detail     TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX media_source_events_source ON media_source_events(source_id, created_at);
