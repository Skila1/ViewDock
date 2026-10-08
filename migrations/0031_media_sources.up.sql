-- External Jellyfin servers. Each source owns one read-only library row whose
-- titles are shared with every user. The password and access token are
-- encrypted with the server master key and never returned by the API.
CREATE TABLE media_sources (
    id            TEXT PRIMARY KEY,
    library_id    TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    url           TEXT NOT NULL,
    username      TEXT NOT NULL,
    password_enc  TEXT NOT NULL DEFAULT '',
    token_enc     TEXT NOT NULL DEFAULT '',
    remote_user   TEXT NOT NULL DEFAULT '',
    views         TEXT NOT NULL DEFAULT '[]',
    enabled       INTEGER NOT NULL DEFAULT 1,
    status        TEXT NOT NULL DEFAULT 'pending',
    last_error    TEXT NOT NULL DEFAULT '',
    last_sync_at  TEXT NOT NULL DEFAULT '',
    item_count    INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

-- remote_items maps catalogue rows to the Jellyfin item they came from.
CREATE TABLE remote_items (
    source_id   TEXT NOT NULL REFERENCES media_sources(id) ON DELETE CASCADE,
    remote_id   TEXT NOT NULL,
    item_kind   TEXT NOT NULL,
    item_id     TEXT NOT NULL,
    image_tag   TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (source_id, remote_id)
);
CREATE INDEX remote_items_item ON remote_items(item_kind, item_id);
