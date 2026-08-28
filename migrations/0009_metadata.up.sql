CREATE TABLE tmdb_cache (
    cache_key    TEXT PRIMARY KEY,
    body         TEXT NOT NULL,
    fetched_at   TEXT NOT NULL,
    expires_at   TEXT NOT NULL
);

CREATE TABLE metadata_locks (
    item_kind  TEXT NOT NULL,
    item_id    TEXT NOT NULL,
    field      TEXT NOT NULL,
    PRIMARY KEY (item_kind, item_id, field)
);

CREATE TABLE artwork (
    id         TEXT PRIMARY KEY,
    item_kind  TEXT NOT NULL,
    item_id    TEXT NOT NULL,
    kind       TEXT NOT NULL, -- poster|backdrop|thumb
    path       TEXT NOT NULL,
    source     TEXT NOT NULL DEFAULT 'tmdb',
    locked     INTEGER NOT NULL DEFAULT 0,
    UNIQUE (item_kind, item_id, kind)
);

CREATE TABLE match_queue (
    id          TEXT PRIMARY KEY,
    item_kind   TEXT NOT NULL,
    item_id     TEXT NOT NULL,
    query       TEXT NOT NULL,
    year        INTEGER,
    attempts    INTEGER NOT NULL DEFAULT 0,
    last_error  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    UNIQUE (item_kind, item_id)
);
